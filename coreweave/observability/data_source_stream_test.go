package observability_test

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
	"testing"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
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
	fmt.Fprintf(&buf, "data %q %q {\n", telemetryStreamDataSourceName, resourceName)
	fmt.Fprintf(&buf, "  slug = %q\n", stream.Slug.ValueString())
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
		require.False(t, m.Set(t.Context(), stream).HasError())

		state := tfsdk.State{
			Schema: schemaResponse.Schema,
			Raw:    tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil),
		}
		assert.False(t, state.Set(ctx, &m).HasError())
	})
}
