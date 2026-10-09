package observability_test

import (
	"embed"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	//go:embed testdata
	streamTestdata embed.FS

	telemetryStreamDataSourceName string = datasourceName(observability.NewTelemetryStreamDataSource())
)

func mustRenderTelemetryStreamDataSource(resourceName string, stream *model.TelemetryStreamDataSource) string {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("data %q %q {\n", telemetryStreamDataSourceName, resourceName))
	buf.WriteString(fmt.Sprintf("  slug = %q\n", stream.Slug.ValueString()))
	buf.WriteString("}\n")

	return buf.String()
}

type streamDataSourceTestStep struct {
	TestName       string
	DataSourceName string
	Slug           string
	StateChecks    []statecheck.StateCheck
	ExpectError    *regexp.Regexp
}

func createStreamDataSourceTestStep(t *testing.T, opts streamDataSourceTestStep) resource.TestStep {
	t.Helper()

	fullDataSourceName := fmt.Sprintf("data.%s.%s", telemetryStreamDataSourceName, opts.DataSourceName)
	m := &model.TelemetryStreamDataSource{
		Slug: types.StringValue(opts.Slug),
	}

	var stateChecks []statecheck.StateCheck

	// Only add state checks if we're not expecting an error
	if opts.ExpectError == nil {
		stateChecks = make([]statecheck.StateCheck, 0, 6+len(opts.StateChecks))
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("slug"), knownvalue.StringExact(opts.Slug)),
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("display_name"), knownvalue.NotNull()),

			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("updated_at"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.String())),
			// zones_active is populated for streams (unlike pipelines), but it
			// depends on the org having registered clusters, so only its presence
			// is asserted here.
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("zones_active"), knownvalue.NotNull()),
		)

		stateChecks = append(stateChecks, opts.StateChecks...)
	}

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning %s test: %s", telemetryStreamDataSourceName, opts.TestName)
		},
		Config:            mustRenderTelemetryStreamDataSource(opts.DataSourceName, m),
		ConfigStateChecks: stateChecks,
		ExpectError:       opts.ExpectError,
	}

	return testStep
}

// TestTelemetryStream is the umbrella for the stream data source.
//
// There is no stream resource: the customer-facing service exposes only
// GetStream and ListStreams. Streams are platform-defined, so the acceptance
// subtests read fixtures that already exist in the org rather than creating
// anything, and need no sweeper.
func TestTelemetryStream(t *testing.T) {
	t.Parallel()

	t.Run("schema", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		schemaResponse := &datasource.SchemaResponse{}

		observability.NewTelemetryStreamDataSource().Schema(ctx, datasource.SchemaRequest{}, schemaResponse)
		assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

		diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
		assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
	})

	t.Run("render", func(t *testing.T) {
		t.Parallel()

		streamModel := &model.TelemetryStreamDataSource{
			Slug: types.StringValue("test-render-stream"),
		}

		expectedHCL, err := streamTestdata.ReadFile("testdata/hcl_stream_datasource.tf")
		require.NoError(t, err)

		hcl := mustRenderTelemetryStreamDataSource("test_stream", streamModel)

		assert.Equal(t, string(expectedHCL), hcl)
	})

	// filter_round_trip is the test the schema/model mismatch needed. The
	// schema declared filter.include as map(map(list(string))) while the model
	// was map(object({values = list(string)})), so State.Set failed on any
	// stream with a non-nil LabelSelector. It stayed latent because no fixture
	// had a filter and the assertion was a TODO.
	//
	// Writing the model through the real schema reproduces that failure without
	// needing the API, so it holds whether or not the test org has a filtered
	// stream.
	t.Run("filter_round_trip", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		schemaResponse := &datasource.SchemaResponse{}
		observability.NewTelemetryStreamDataSource().Schema(ctx, datasource.SchemaRequest{}, schemaResponse)
		require.False(t, schemaResponse.Diagnostics.HasError())

		stream := &typesv1beta1.TelemetryStream{
			Ref:  &typesv1beta1.TelemetryStreamRef{Slug: "logs-filtered"},
			Spec: &typesv1beta1.TelemetryStreamSpec{DisplayName: "Filtered Logs"},
			Status: &typesv1beta1.TelemetryStreamStatus{
				CreatedAt: timestamppb.Now(),
				UpdatedAt: timestamppb.Now(),
				State:     typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE,
			},
		}
		stream.Spec.SetKind(typesv1beta1.StreamKind_STREAM_KIND_LOGS)
		stream.Spec.SetFilter(&typesv1beta1.LabelSelector{
			Include: map[string]*typesv1beta1.StringList{
				"namespace": {Values: []string{"kube-system", "default"}},
			},
			Exclude: map[string]*typesv1beta1.StringList{
				"container": {Values: []string{"istio-proxy"}},
			},
		})

		var m model.TelemetryStreamDataSource
		require.False(t, m.Set(stream).HasError())

		// The StringList wrapper must not reach HCL: users write
		// filter.include["namespace"], not filter.include["namespace"].values.
		assert.Equal(t, map[string][]string{"namespace": {"kube-system", "default"}}, m.Filter.Include)
		assert.Equal(t, map[string][]string{"container": {"istio-proxy"}}, m.Filter.Exclude)

		state := tfsdk.State{
			Schema: schemaResponse.Schema,
			Raw:    tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil),
		}
		diags := state.Set(ctx, &m)
		assert.False(t, diags.HasError(), "writing a filtered stream to state failed: %v", diags)
	})

	t.Run("unfiltered_round_trip", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		schemaResponse := &datasource.SchemaResponse{}
		observability.NewTelemetryStreamDataSource().Schema(ctx, datasource.SchemaRequest{}, schemaResponse)
		require.False(t, schemaResponse.Diagnostics.HasError())

		stream := &typesv1beta1.TelemetryStream{
			Ref:  &typesv1beta1.TelemetryStreamRef{Slug: "logs-plain"},
			Spec: &typesv1beta1.TelemetryStreamSpec{DisplayName: "Plain Logs"},
			Status: &typesv1beta1.TelemetryStreamStatus{
				CreatedAt: timestamppb.Now(),
				UpdatedAt: timestamppb.Now(),
				State:     typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE,
			},
		}
		stream.Spec.SetKind(typesv1beta1.StreamKind_STREAM_KIND_LOGS)

		var m model.TelemetryStreamDataSource
		require.False(t, m.Set(stream).HasError())
		assert.Nil(t, m.Filter, "an absent LabelSelector must read as null, not as an empty filter")

		state := tfsdk.State{
			Schema: schemaResponse.Schema,
			Raw:    tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil),
		}
		assert.False(t, state.Set(ctx, &m).HasError())
	})

	t.Run("lifecycle", func(t *testing.T) {
		streamTests := []struct {
			streamType   string
			expectedKind string
			slugs        []string
		}{
			{
				streamType:   "metrics",
				expectedKind: typesv1beta1.StreamKind_STREAM_KIND_METRICS.String(),
				slugs:        metricsStreams,
			},
			{
				streamType:   "logs",
				expectedKind: typesv1beta1.StreamKind_STREAM_KIND_LOGS.String(),
				slugs:        logsStreams,
			},
		}

		for _, tt := range streamTests {
			for _, slug := range tt.slugs {
				t.Run(fmt.Sprintf("%s/%s", tt.streamType, slug), func(t *testing.T) {
					dataSourceName := fmt.Sprintf("test_acc_%s_stream", tt.streamType)
					fullDataSourceName := fmt.Sprintf("data.%s.%s", telemetryStreamDataSourceName, dataSourceName)

					resource.ParallelTest(t, resource.TestCase{
						ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
						PreCheck:                 func() { testutil.SetEnvDefaults() },
						Steps: []resource.TestStep{
							createStreamDataSourceTestStep(t, streamDataSourceTestStep{
								TestName:       fmt.Sprintf("%s stream: %s", tt.streamType, slug),
								DataSourceName: dataSourceName,
								Slug:           slug,
								StateChecks: []statecheck.StateCheck{
									statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("kind"), knownvalue.StringExact(tt.expectedKind)),
								},
							}),
						},
					})
				})
			}
		}
	})

	// filtered_stream reads whichever stream in the org actually carries a
	// LabelSelector. Streams are platform-defined and cannot be created through
	// the customer-facing API, so there is nothing to set up; if the org has no
	// filtered stream, the unit round-trip subtests above still cover the type.
	t.Run("filtered_stream", func(t *testing.T) {
		if os.Getenv(resource.EnvTfAcc) == "" {
			t.Skipf("%s not set; filter type agreement is covered by filter_round_trip", resource.EnvTfAcc)
		}

		slug, ok := findFilteredStream(t)
		if !ok {
			t.Skip("no stream in this organization carries a LabelSelector; filter type agreement is covered by filter_round_trip")
		}

		dataSourceName := "test_acc_filtered_stream"
		fullDataSourceName := fmt.Sprintf("data.%s.%s", telemetryStreamDataSourceName, dataSourceName)

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				createStreamDataSourceTestStep(t, streamDataSourceTestStep{
					TestName:       fmt.Sprintf("filtered stream: %s", slug),
					DataSourceName: dataSourceName,
					Slug:           slug,
					StateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("filter"), knownvalue.NotNull()),
					},
				}),
			},
		})
	})

	t.Run("not_found", func(t *testing.T) {
		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				createStreamDataSourceTestStep(t, streamDataSourceTestStep{
					TestName:       "stream not found",
					DataSourceName: "test_acc_stream_notfound",
					Slug:           "nonexistent-stream",
					ExpectError:    regexp.MustCompile(`(?i)(not found)`),
				}),
			},
		})
	})

	t.Run("invalid_slug", func(t *testing.T) {
		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				createStreamDataSourceTestStep(t, streamDataSourceTestStep{
					TestName:       "invalid slug validation",
					DataSourceName: "test_acc_stream_invalid",
					Slug:           "invalid-slug-way-too-long-to-be-valid",
					ExpectError:    regexp.MustCompile(`(?i)(validation|invalid|required|empty)`),
				}),
			},
		})
	})
}

// findFilteredStream returns the slug of a stream in the test organization
// that carries a non-nil LabelSelector, if there is one.
func findFilteredStream(t *testing.T) (string, bool) {
	t.Helper()

	client := testClient(t)
	listResp, err := client.ListStreams(t.Context(), connect.NewRequest(&clusterv1beta1.ListStreamsRequest{}))
	require.NoError(t, err, "failed to list streams")

	for _, stream := range listResp.Msg.GetStreams() {
		if stream.GetSpec().GetFilter() != nil {
			return stream.GetRef().GetSlug(), true
		}
	}

	return "", false
}
