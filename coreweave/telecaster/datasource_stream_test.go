package telecaster_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
)

// TestTelemetryStreamDataSourceSchema validates the datasource schema implementation
func TestTelemetryStreamDataSourceSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaRequest := datasource.SchemaRequest{}
	schemaResponse := &datasource.SchemaResponse{}

	telecaster.NewTelemetryStreamDataSource().Schema(ctx, schemaRequest, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

// mustTelemetryStreamDataSourceModel creates a TelemetryStreamDataSourceModel from a ref
func mustTelemetryStreamDataSourceModel(t *testing.T, ref model.TelemetryStreamRefModel) *telecaster.TelemetryStreamDataSourceModel {
	t.Helper()

	ctx := t.Context()

	schemaResp := new(datasource.SchemaResponse)
	telecaster.NewTelemetryStreamDataSource().Schema(ctx, datasource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("failed to get schema: %v", schemaResp.Diagnostics)
	}

	dsModel := telecaster.TelemetryStreamDataSourceModel{}

	refAttrTypes := schemaResp.Schema.Attributes["ref"].GetType().(basetypes.ObjectType).AttrTypes
	refObj, diags := types.ObjectValueFrom(ctx, refAttrTypes, ref)
	if diags.HasError() {
		t.Fatalf("failed to create ref object: %v", diags)
	}
	dsModel.Ref = refObj

	specAttrTypes := schemaResp.Schema.Attributes["spec"].GetType().(basetypes.ObjectType).AttrTypes
	statusAttrTypes := schemaResp.Schema.Attributes["status"].GetType().(basetypes.ObjectType).AttrTypes
	dsModel.Spec = types.ObjectUnknown(specAttrTypes)
	dsModel.Status = types.ObjectUnknown(statusAttrTypes)

	return &dsModel
}

func mustRenderTelemetryStreamDataSource(ctx context.Context, resourceName string, stream *telecaster.TelemetryStreamDataSourceModel) string {
	var ref model.TelemetryStreamRefModel
	if err := stream.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true}); err != nil {
		panic(fmt.Sprintf("failed to extract ref: %v", err))
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("data \"coreweave_telecaster_stream\" %q {\n", resourceName))
	buf.WriteString("  ref = {\n")
	buf.WriteString(fmt.Sprintf("    slug = %q\n", ref.Slug.ValueString()))
	buf.WriteString("  }\n")
	buf.WriteString("}\n")

	return buf.String()
}

// TestTelemetryStreamDataSource_MetricsStream tests reading a metrics stream specifically
func TestTelemetryStreamDataSource_MetricsStream(t *testing.T) {
	t.Parallel() // this is ok because we nest the parallels in sub-tests
	randomInt := rand.IntN(100)
	dataSourceName := fmt.Sprintf("test_acc_metrics_stream_%d", randomInt)
	fullDataSourceName := fmt.Sprintf("data.coreweave_telecaster_stream.%s", dataSourceName)
	ctx := t.Context()

	type StreamConfig struct {
		Slug string
		Kind string
	}

	streams := []string{
		"metrics-customer-cluster",
		"metrics-platform",
	}

	for _, stream := range streams {
		refModel := model.TelemetryStreamRefModel{
			Slug: types.StringValue(stream),
		}

		t.Run(stream, func(t *testing.T) {
			resource.ParallelTest(t, resource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				PreCheck: func() {
					testutil.SetEnvDefaults()
				},
				Steps: []resource.TestStep{
					{
						PreConfig: func() {
							t.Logf("Beginning metrics stream data source test: %s", stream)
						},
						Config: mustRenderTelemetryStreamDataSource(ctx, dataSourceName, mustTelemetryStreamDataSourceModel(t, refModel)),
						ConfigStateChecks: []statecheck.StateCheck{
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("ref"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("ref").AtMapKey("slug"), knownvalue.StringExact(stream)),

							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec").AtMapKey("display_name"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec").AtMapKey("kind"), knownvalue.StringExact("metrics")),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec").AtMapKey("metrics"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec").AtMapKey("logs"), knownvalue.Null()),

							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("status"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("status").AtMapKey("created_at"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("status").AtMapKey("updated_at"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("status").AtMapKey("state"), knownvalue.StringExact(typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.String())),
						},
					},
				},
			})
		})
	}
}

// TestTelemetryStreamDataSource_LogsStream tests reading a logs stream specifically
func TestTelemetryStreamDataSource_LogsStream(t *testing.T) {
	t.Parallel() // this is ok because we nest the parallels in sub-tests
	dataSourceName := "test_acc_logs_stream"
	fullDataSourceName := fmt.Sprintf("data.coreweave_telecaster_stream.%s", dataSourceName)
	ctx := t.Context()

	slugs := []string{
		"logs-audit-caios",
		"logs-audit-console",
		"logs-audit-kube-api",
		"logs-customer-cluster",
		"logs-events",
		"logs-journald",
	}

	for _, slug := range slugs {
		refModel := model.TelemetryStreamRefModel{
			Slug: types.StringValue(slug),
		}

		t.Run(slug, func(t *testing.T) {
			resource.ParallelTest(t, resource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				PreCheck: func() {
					testutil.SetEnvDefaults()
				},
				Steps: []resource.TestStep{
					{
						PreConfig: func() {
							t.Logf("Beginning logs stream data source test: %s", slug)
						},
						Config: mustRenderTelemetryStreamDataSource(ctx, dataSourceName, mustTelemetryStreamDataSourceModel(t, refModel)),
						ConfigStateChecks: []statecheck.StateCheck{
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("ref"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("ref").AtMapKey("slug"), knownvalue.StringExact(slug)),

							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec").AtMapKey("display_name"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec").AtMapKey("kind"), knownvalue.StringExact("logs")),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec").AtMapKey("logs"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec").AtMapKey("metrics"), knownvalue.Null()),

							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("status"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("status").AtMapKey("created_at"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("status").AtMapKey("updated_at"), knownvalue.NotNull()),
							statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("status").AtMapKey("state"), knownvalue.StringExact(typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.String())),
						},
					},
				},
			})
		})
	}
}

// TestTelemetryStreamDataSource_NotFound tests behavior when stream doesn't exist
func TestTelemetryStreamDataSource_NotFound(t *testing.T) {
	ctx := t.Context()
	dataSourceName := "test_acc_stream_notfound"

	nonExistentSlug := "nonexistent-stream"

	refModel := model.TelemetryStreamRefModel{
		Slug: types.StringValue(nonExistentSlug),
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					t.Logf("Beginning stream not found test with slug: %s", nonExistentSlug)
				},
				Config: mustRenderTelemetryStreamDataSource(ctx, dataSourceName, mustTelemetryStreamDataSourceModel(t, refModel)),
			},
		},
	})
}

// TestTelemetryStreamDataSource_InvalidSlug tests validation with an invalid slug
func TestTelemetryStreamDataSource_InvalidSlug(t *testing.T) {
	dataSourceName := "test_acc_stream_invalid"
	ctx := t.Context()

	refModel := model.TelemetryStreamRefModel{
		Slug: types.StringValue("invalid-slug-way-too-long-to-be-valid"),
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					t.Logf("Beginning invalid slug test")
				},
				Config:      mustRenderTelemetryStreamDataSource(ctx, dataSourceName, mustTelemetryStreamDataSourceModel(t, refModel)),
				ExpectError: regexp.MustCompile(`(?i)(validation|invalid|required|empty)`),
			},
		},
	})
}

// TestTelemetryStreamDataSourceModel_Set validates the Set method works correctly
func TestTelemetryStreamDataSourceModel_Set(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	schemaResp := &datasource.SchemaResponse{}
	telecaster.NewTelemetryStreamDataSource().Schema(ctx, datasource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("failed to get schema: %v", schemaResp.Diagnostics)
	}

	// Create a model
	dsModel := telecaster.TelemetryStreamDataSourceModel{}
	refModel := model.TelemetryStreamRefModel{
		Slug: types.StringValue("test-stream"),
	}

	refAttrTypes := schemaResp.Schema.Attributes["ref"].GetType().(basetypes.ObjectType).AttrTypes
	refObj, diags := types.ObjectValueFrom(ctx, refAttrTypes, refModel)
	if diags.HasError() {
		t.Fatalf("failed to create ref object: %v", diags)
	}
	dsModel.Ref = refObj

	// Verify ref can be extracted
	var extractedRef model.TelemetryStreamRefModel
	diags = dsModel.Ref.As(ctx, &extractedRef, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		t.Fatalf("failed to extract ref: %v", diags)
	}

	assert.Equal(t, "test-stream", extractedRef.Slug.ValueString(), "slug should match")
}

// TestTelemetryStreamDataSource_RenderFunction validates the HCL rendering function
func TestTelemetryStreamDataSource_RenderFunction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	refModel := model.TelemetryStreamRefModel{
		Slug: types.StringValue("test-render-stream"),
	}

	streamModel := mustTelemetryStreamDataSourceModel(t, refModel)

	// Render the HCL
	hcl := mustRenderTelemetryStreamDataSource(ctx, "test_stream", streamModel)

	// Verify HCL contains expected elements
	assert.Contains(t, hcl, `data "coreweave_telecaster_stream"`, "HCL should contain data block")
	assert.Contains(t, hcl, `"test_stream"`, "HCL should contain resource name")
	assert.Contains(t, hcl, "ref", "HCL should contain ref block")
	assert.Contains(t, hcl, `slug = "test-render-stream"`, "HCL should contain slug")
}
