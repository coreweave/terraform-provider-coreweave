package observability_test

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
	"testing"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	//go:embed testdata
	streamTestdata embed.FS

	telemetryStreamDataSourceName string = datasourceName(observability.NewTelemetryStreamDataSource())
)

// TestTelemetryStreamDataSourceSchema validates the datasource schema implementation
func TestTelemetryStreamDataSourceSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaRequest := datasource.SchemaRequest{}
	schemaResponse := &datasource.SchemaResponse{}

	observability.NewTelemetryStreamDataSource().Schema(ctx, schemaRequest, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

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
	model := &model.TelemetryStreamDataSource{
		Slug: types.StringValue(opts.Slug),
	}

	var stateChecks []statecheck.StateCheck

	// Only add state checks if we're not expecting an error
	if opts.ExpectError == nil {
		stateChecks = []statecheck.StateCheck{
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("slug"), knownvalue.StringExact(opts.Slug)),

			// statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("filter"), knownvalue.Null()), <- TODO: not yet clear what desired states should be, if any.
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("display_name"), knownvalue.NotNull()),

			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("updated_at"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.String())),
			statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("state_code"), knownvalue.Int32Exact(int32(typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.Number()))),
			// statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("zones_active"), knownvalue.NotNull()), <- TODO: this needs to be integrated with a cluster resource, with a subscription, for full quality testing.
			// statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("zones_active").AtMapKey("us-lab-01a").AtMapKey("clusters"), knownvalue.NotNull()),
		}

		stateChecks = append(stateChecks, opts.StateChecks...)
	}

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning %s test: %s", telemetryStreamDataSourceName, opts.TestName)
		},
		Config:            mustRenderTelemetryStreamDataSource(opts.DataSourceName, model),
		ConfigStateChecks: stateChecks,
		ExpectError:       opts.ExpectError,
	}

	return testStep
}

// TestTelemetryStreamDataSource consolidates all acceptance tests
func TestTelemetryStreamDataSource(t *testing.T) {
	t.Parallel()

	streamTests := []struct {
		streamType   string
		expectedKind string
		slugs        []string
	}{
		{
			streamType:   "metrics",
			expectedKind: "STREAM_KIND_METRICS",
			slugs: []string{
				"metrics-customer-cluster",
				"metrics-platform",
			},
		},
		{
			streamType:   "logs",
			expectedKind: "STREAM_KIND_LOGS",
			slugs: []string{
				"logs-audit-caios",
				"logs-audit-console",
				"logs-audit-kube-api",
				"logs-customer-cluster",
				"logs-events",
				"logs-journald",
			},
		},
	}

	for _, tt := range streamTests {
		for _, slug := range tt.slugs {
			t.Run(fmt.Sprintf("%s/%s", tt.streamType, slug), func(t *testing.T) {
				dataSourceName := fmt.Sprintf("test_acc_%s_stream", tt.streamType)
				fullDataSourceName := fmt.Sprintf("data.%s.%s", telemetryStreamDataSourceName, dataSourceName)

				resource.ParallelTest(t, resource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
					PreCheck: func() {
						testutil.SetEnvDefaults()
					},
					Steps: []resource.TestStep{
						createStreamDataSourceTestStep(t, streamDataSourceTestStep{
							TestName:       fmt.Sprintf("%s stream: %s", tt.streamType, slug),
							DataSourceName: dataSourceName,
							Slug:           slug,
							StateChecks: []statecheck.StateCheck{
								statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("kind"), knownvalue.StringExact(tt.expectedKind)),
								statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("filter"), knownvalue.Null()),
								statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
								statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("updated_at"), knownvalue.NotNull()),
								statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.String())),
							},
						}),
					},
				})
			})
		}
	}

	t.Run("not_found", func(t *testing.T) {
		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
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
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
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

// TestTelemetryStreamDataSource_RenderFunction validates the HCL rendering function
func TestTelemetryStreamDataSource_RenderFunction(t *testing.T) {
	t.Parallel()

	streamModel := &model.TelemetryStreamDataSource{
		Slug: types.StringValue("test-render-stream"),
	}

	expectedHCL, err := streamTestdata.ReadFile("testdata/hcl_stream_datasource.tf")
	require.NoError(t, err)

	hcl := mustRenderTelemetryStreamDataSource("test_stream", streamModel)

	assert.Equal(t, string(expectedHCL), hcl)
}
