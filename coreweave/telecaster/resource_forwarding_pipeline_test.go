package telecaster_test

import (
	"context"
	"embed"
	"fmt"
	"log"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPipelineResourceName = "test_pipeline"
)

var (
	//go:embed testdata
	testdata embed.FS
)

func init() {
	resource.AddTestSweepers("coreweave_telecaster_forwarding_pipeline", &resource.Sweeper{
		Name:         "coreweave_telecaster_forwarding_pipeline",
		Dependencies: []string{},
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.ListPipelines(ctx, connect.NewRequest(&clusterv1beta1.ListPipelinesRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list forwarding pipelines: %w", err)
			}

			streamPrefixes := make([]string, len(metricsStreams)+len(logsStreams))
			copy(streamPrefixes, metricsStreams)
			copy(streamPrefixes[len(metricsStreams):], logsStreams)

			prefixPattern := fmt.Sprintf(`^(%s)-%s`, strings.Join(streamPrefixes, "|"), AcceptanceTestPrefix)
			prefixMatcher := regexp.MustCompile(prefixPattern)

			for _, pipeline := range listResp.Msg.GetPipelines() {
				if !prefixMatcher.MatchString(pipeline.Ref.Slug) {
					log.Printf("skipping forwarding pipeline %q because it does not match regular expression %q", pipeline.Ref.Slug, prefixMatcher)
					continue
				}

				log.Printf("sweeping forwarding pipeline %q", pipeline.Ref.Slug)
				if testutil.SweepDryRun() {
					log.Printf("skipping forwarding pipeline %q because of dry-run mode", pipeline.Ref.Slug)
					continue
				}

				deleteCtx, deleteCancel := context.WithTimeout(ctx, 5*time.Minute)
				defer deleteCancel()

				if _, err := client.DeletePipeline(deleteCtx, connect.NewRequest(&clusterv1beta1.DeletePipelineRequest{
					Ref: pipeline.Ref,
				})); err != nil {
					if connect.CodeOf(err) == connect.CodeNotFound {
						log.Printf("forwarding pipeline %q already deleted", pipeline.Ref.Slug)
						continue
					}
					return fmt.Errorf("failed to delete forwarding pipeline %q: %w", pipeline.Ref.Slug, err)
				}

				// Wait for deletion to complete
				waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Minute)
				defer waitCancel()

				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()

				for {
					select {
					case <-waitCtx.Done():
						return fmt.Errorf("timeout waiting for forwarding pipeline %q to be deleted: %w", pipeline.Ref.Slug, waitCtx.Err())
					case <-ticker.C:
						_, err := client.GetPipeline(waitCtx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
							Ref: pipeline.Ref,
						}))
						if connect.CodeOf(err) == connect.CodeNotFound {
							log.Printf("forwarding pipeline %q successfully deleted", pipeline.Ref.Slug)
							goto nextPipeline
						}
						if err != nil {
							return fmt.Errorf("error checking forwarding pipeline %q deletion status: %w", pipeline.Ref.Slug, err)
						}
					}
				}
			nextPipeline:
			}

			return nil
		},
	})
}

// Real stream slugs for testing - these exist in the test environment
var (
	metricsStreams = []string{
		"metrics-customer-cluster",
		"metrics-platform",
	}

	logsStreams = []string{
		"logs-audit-caios",
		"logs-audit-console",
		"logs-audit-kube-api",
		"logs-customer-cluster",
		"logs-events",
		"logs-journald",
	}
)

// EndpointType represents the type of forwarding endpoint
type EndpointType string

const (
	EndpointTypeHTTPS      EndpointType = "https"
	EndpointTypeS3         EndpointType = "s3"
	EndpointTypePrometheus EndpointType = "prometheus"
)

// StreamType represents the type of telemetry stream
type StreamType string

const (
	StreamTypeMetrics StreamType = "metrics"
	StreamTypeLogs    StreamType = "logs"
)

// CompatibilityRule defines which endpoint types are compatible with which stream types
type CompatibilityRule struct {
	StreamType   StreamType
	EndpointType EndpointType
	Compatible   bool
	Skip         bool
}

var streamEndpointCompatibility = []CompatibilityRule{
	{StreamType: StreamTypeMetrics, EndpointType: EndpointTypeHTTPS, Compatible: false},
	{StreamType: StreamTypeMetrics, EndpointType: EndpointTypePrometheus, Compatible: true},
	{StreamType: StreamTypeMetrics, EndpointType: EndpointTypeS3, Compatible: false},

	{StreamType: StreamTypeLogs, EndpointType: EndpointTypeHTTPS, Compatible: true},
	{StreamType: StreamTypeLogs, EndpointType: EndpointTypePrometheus, Compatible: false},
	{StreamType: StreamTypeLogs, EndpointType: EndpointTypeS3, Compatible: true},
}

// getCompatibleEndpointTypes returns the list of compatible endpoint types for a given stream type
func getCompatibleEndpointTypes(streamType StreamType) []EndpointType {
	var compatible []EndpointType
	for _, rule := range streamEndpointCompatibility {
		if rule.StreamType == streamType && rule.Compatible {
			compatible = append(compatible, rule.EndpointType)
		}
	}
	return compatible
}

// getIncompatibleEndpointTypes returns the list of incompatible endpoint types for a given stream type
func getIncompatibleEndpointTypes(streamType StreamType) []EndpointType {
	var incompatible []EndpointType
	for _, rule := range streamEndpointCompatibility {
		if rule.StreamType == streamType && !rule.Compatible {
			incompatible = append(incompatible, rule.EndpointType)
		}
	}
	return incompatible
}

func shorten(name string) string {
	if len(name) < 4 {
		return name
	}
	truncated := len(name) - 2
	return fmt.Sprintf("%s%d%s", name[:1], truncated, name[len(name)-1:])
}

func createHTTPSEndpoint(t *testing.T, slug string) *HTTPSEndpointTestModel {
	t.Helper()
	return &HTTPSEndpointTestModel{
		Slug:        slug,
		DisplayName: fmt.Sprintf("Test HTTPS Endpoint - %s", slug),
		Endpoint:    "http://telecaster-console.us-east-03-core-services.int.coreweave.com:9000/",
	}
}

// TODO: Implement createS3Endpoint when S3 endpoint resource is available
// func createS3Endpoint(t *testing.T, slug string) *S3EndpointTestModel

// TODO: Implement createPrometheusEndpoint when Prometheus endpoint resource is available
// func createPrometheusEndpoint(t *testing.T, slug string) *PrometheusEndpointTestModel

func skipUnimplementedEndpointTypes(t *testing.T, endpointType EndpointType) {
	t.Helper()
	switch endpointType {
	case EndpointTypeHTTPS:
		// HTTPS is implemented, continue
	case EndpointTypePrometheus:
		t.Skip("Skipping Prometheus endpoint until it is available")
	case EndpointTypeS3:
		t.Skip("Skipping S3 endpoint until it is supported")
	default:
		t.Skipf("Unknown endpoint type: %s", endpointType)
	}
}

func mustForwardingPipelineResourceModel(t *testing.T, spec model.ForwardingPipelineSpecModel) *telecaster.ResourceForwardingPipelineModel {
	t.Helper()

	ctx := t.Context()

	schemaResp := new(fwresource.SchemaResponse)
	telecaster.NewForwardingPipelineResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("failed to get schema: %v", schemaResp.Diagnostics)
	}

	pipelineModel := telecaster.ResourceForwardingPipelineModel{}
	dieIfDiagnostics(t, tfsdk.ValueFrom(ctx, model.ForwardingPipelineRefModel{}, schemaResp.Schema.Attributes["ref"].GetType(), &pipelineModel.Ref))
	dieIfDiagnostics(t, tfsdk.ValueFrom(ctx, spec, schemaResp.Schema.Attributes["spec"].GetType(), &pipelineModel.Spec))
	dieIfDiagnostics(t, tfsdk.ValueFrom(ctx, model.ForwardingPipelineStatusModel{}, schemaResp.Schema.Attributes["status"].GetType(), &pipelineModel.Status))

	return &pipelineModel
}

// mustRenderForwardingPipelineResource renders HCL for a forwarding pipeline with dependencies
func mustRenderForwardingPipelineResource(ctx context.Context, resourceName string, pipeline *telecaster.ResourceForwardingPipelineModel, endpoint *HTTPSEndpointTestModel) string {
	var parts []string

	if endpoint != nil {
		parts = append(parts, renderHTTPSEndpointResource(resourceName, endpoint))
	}

	var specModel model.ForwardingPipelineSpecModel
	if err := pipeline.Spec.As(ctx, &specModel, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true}); err != nil {
		panic(fmt.Sprintf("failed to extract spec: %v", err))
	}

	// Build the HCL as a simple string
	var hcl strings.Builder
	hcl.WriteString(fmt.Sprintf("resource %q %q {\n", "coreweave_telecaster_forwarding_pipeline", resourceName))
	hcl.WriteString("  spec = {\n")
	hcl.WriteString("    source = {\n")
	hcl.WriteString(fmt.Sprintf("      slug = %q\n", specModel.Source.Slug.ValueString()))
	hcl.WriteString("    }\n")
	hcl.WriteString("    destination = {\n")

	if endpoint != nil {
		// Use the flattened slug attribute from the new HTTPS endpoint resource
		hcl.WriteString(fmt.Sprintf("      slug = coreweave_telecaster_forwarding_endpoint_https.%s.slug\n", resourceName))
	} else {
		hcl.WriteString(fmt.Sprintf("      slug = %q\n", specModel.Destination.Slug.ValueString()))
	}

	hcl.WriteString("    }\n")
	hcl.WriteString(fmt.Sprintf("    enabled = %t\n", specModel.Enabled.ValueBool()))
	hcl.WriteString("  }\n")
	hcl.WriteString("}\n")

	parts = append(parts, hcl.String())

	// Join endpoint and pipeline with newline (like CKS VPC + Cluster)
	return strings.Join(parts, "\n")
}

// TestForwardingPipelineResourceSchema validates the resource schema
func TestForwardingPipelineResourceSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	schemaResponse := new(fwresource.SchemaResponse)
	telecaster.NewForwardingPipelineResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

// TestMustRenderForwardingPipelineResource tests the surprising HCL rendering behavior:
// when an endpoint is provided, destination uses a resource reference (traversal),
// otherwise it uses a literal slug string.
func TestMustRenderForwardingPipelineResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("without_endpoint_uses_literal_slug", func(t *testing.T) {
		t.Parallel()

		pipeline := mustForwardingPipelineResourceModel(t,
			model.ForwardingPipelineSpecModel{
				Source:      model.TelemetryStreamRefModel{Slug: types.StringValue("test-stream")},
				Destination: model.ForwardingEndpointRefModel{Slug: types.StringValue("test-endpoint")},
				Enabled:     types.BoolValue(true),
			},
		)

		exampleHCLPipelineUsesLiterals, err := testdata.ReadFile("testdata/hcl_pipeline_uses_literals.tf")
		require.NoError(t, err)

		hcl := mustRenderForwardingPipelineResource(ctx, "test", pipeline, nil)
		assert.Equal(t, string(exampleHCLPipelineUsesLiterals), hcl)
	})

	t.Run("with_endpoint_uses_resource_reference", func(t *testing.T) {
		t.Parallel()

		pipeline := mustForwardingPipelineResourceModel(t,
			model.ForwardingPipelineSpecModel{
				Source:      model.TelemetryStreamRefModel{Slug: types.StringValue("test-stream")},
				Destination: model.ForwardingEndpointRefModel{Slug: types.StringValue("${coreweave_telecaster_forwarding_endpoint_https.endpoint.slug}")},
				Enabled:     types.BoolValue(true),
			},
		)

		exampleHCLEndpointUsesReferences, err := testdata.ReadFile("testdata/hcl_pipeline_uses_references.tf")
		require.NoError(t, err)

		endpoint := createHTTPSEndpoint(t, "test-endpoint")
		hcl := mustRenderForwardingPipelineResource(ctx, "test", pipeline, endpoint)
		assert.Equal(t, string(exampleHCLEndpointUsesReferences), hcl)
	})
}

// TestForwardingPipelineResourceModelRef_ToMsg validates the ref model conversion
func TestForwardingPipelineResourceModelRef_ToMsg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *model.ForwardingPipelineRefModel
		expected *typesv1beta1.ForwardingPipelineRef
		wantErr  bool
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name: "valid input converts correctly",
			input: &model.ForwardingPipelineRefModel{
				Slug: types.StringValue("example-pipeline"),
			},
			expected: &typesv1beta1.ForwardingPipelineRef{
				Slug: "example-pipeline",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := tt.input.ToMsg()
			assert.Equal(t, tt.expected, msg)
		})
	}
}

// TestForwardingPipelineResourceSpecModel_ToMsg validates the spec model conversion
func TestForwardingPipelineResourceSpecModel_ToMsg(t *testing.T) {
	t.Parallel()

	specBase := func() *model.ForwardingPipelineSpecModel {
		return &model.ForwardingPipelineSpecModel{
			Enabled: types.BoolValue(true),
			Source: model.TelemetryStreamRefModel{
				Slug: types.StringValue("example-stream"),
			},
			Destination: model.ForwardingEndpointRefModel{
				Slug: types.StringValue("example-destination"),
			},
		}
	}
	outputBase := func() *typesv1beta1.ForwardingPipelineSpec {
		return &typesv1beta1.ForwardingPipelineSpec{
			Enabled: true,
			Source: &typesv1beta1.TelemetryStreamRef{
				Slug: "example-stream",
			},
			Destination: &typesv1beta1.ForwardingEndpointRef{
				Slug: "example-destination",
			},
		}
	}

	tests := []struct {
		name     string
		input    *model.ForwardingPipelineSpecModel
		expected *typesv1beta1.ForwardingPipelineSpec
		wantErr  bool
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "valid input converts correctly",
			input:    specBase(),
			expected: outputBase(),
		},
		{
			name: "enabled false converts correctly",
			input: with(specBase(), func(s *model.ForwardingPipelineSpecModel) {
				s.Enabled = types.BoolValue(false)
			}),
			expected: with(outputBase(), func(s *typesv1beta1.ForwardingPipelineSpec) {
				s.Enabled = false
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := tt.input.ToMsg()
			assert.Equal(t, tt.expected, msg)
		})
	}
}

func TestForwardingPipelineResource_CompatibleCombinations(t *testing.T) {
	t.Skip()
	t.Parallel()

	for _, streamSlug := range metricsStreams {
		for _, endpointType := range getCompatibleEndpointTypes(StreamTypeMetrics) {
			testName := fmt.Sprintf("%s_to_%s", streamSlug, endpointType)

			t.Run(testName, func(t *testing.T) {
				randomInt := rand.IntN(100)
				resourceName := fmt.Sprintf("%s_to_%s", streamSlug, endpointType)
				fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_pipeline.%s", resourceName)
				ctx := t.Context()

				skipUnimplementedEndpointTypes(t, endpointType)

				// We end up creating many slugs that are illegally long, so we need to shorten them
				endpointSlug := slugify(fmt.Sprintf("p-%s-%s", streamSlug, endpointType), randomInt)
				if len(endpointSlug) > 32 {
					endpointSlug = slugify(fmt.Sprintf("p-%s-%s", shorten(streamSlug), string(endpointType)), randomInt)
				}
				if len(endpointSlug) > 32 {
					endpointSlug = slugify(fmt.Sprintf("p-%s-%s", shorten(streamSlug), shorten(string(endpointType))), randomInt)
				}
				endpoint := createHTTPSEndpoint(t, endpointSlug)

				pipelineSpec := model.ForwardingPipelineSpecModel{
					Source: model.TelemetryStreamRefModel{
						Slug: types.StringValue(streamSlug),
					},
					Destination: model.ForwardingEndpointRefModel{
						Slug: types.StringValue(endpoint.Slug),
					},
					Enabled: types.BoolValue(true),
				}
				pipeline := mustForwardingPipelineResourceModel(t, pipelineSpec)

				resource.ParallelTest(t, resource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
					PreCheck: func() {
						testutil.SetEnvDefaults()
					},
					Steps: []resource.TestStep{
						{
							PreConfig: func() {
								t.Logf("Creating pipeline: %s -> %s, endpoint: %s", streamSlug, endpointType, endpointSlug)
							},
							Config: mustRenderForwardingPipelineResource(ctx, resourceName, pipeline, endpoint),
							ConfigPlanChecks: resource.ConfigPlanChecks{
								PreApply: []plancheck.PlanCheck{
									plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
								},
							},
							ConfigStateChecks: []statecheck.StateCheck{
								statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("ref").AtMapKey("slug"), knownvalue.StringExact(endpointSlug)),
								statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("source").AtMapKey("slug"), knownvalue.StringExact(streamSlug)),
								statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("enabled"), knownvalue.Bool(true)),
								statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status").AtMapKey("state"), knownvalue.StringExact(typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE.String())),
							},
						},
					},
				})
			})
		}
	}

	for _, streamSlug := range logsStreams {
		for _, endpointType := range getCompatibleEndpointTypes(StreamTypeLogs) {
			testName := fmt.Sprintf("%s_to_%s", streamSlug, endpointType)

			t.Run(testName, func(t *testing.T) {
				randomInt := rand.IntN(100)
				resourceName := testName
				fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_pipeline.%s", resourceName)
				ctx := t.Context()

				slug := slugify(fmt.Sprintf("pipe-%s-%s", streamSlug, endpointType), randomInt)

				// We end up creating many slugs that are illegally long, so we need to shorten them
				endpointSlug := slugify(fmt.Sprintf("p-%s-%s", streamSlug, endpointType), randomInt)
				if len(endpointSlug) > 32 {
					endpointSlug = slugify(fmt.Sprintf("p-%s-%s", shorten(streamSlug), string(endpointType)), randomInt)
				}
				if len(endpointSlug) > 32 {
					endpointSlug = slugify(fmt.Sprintf("p-%s-%s", shorten(streamSlug), shorten(string(endpointType))), randomInt)
				}

				endpoint := createHTTPSEndpoint(t, endpointSlug)

				pipelineSpec := model.ForwardingPipelineSpecModel{
					Source: model.TelemetryStreamRefModel{
						Slug: types.StringValue(streamSlug),
					},
					Destination: model.ForwardingEndpointRefModel{
						Slug: types.StringValue(endpoint.Slug),
					},
					Enabled: types.BoolValue(true),
				}
				pipeline := mustForwardingPipelineResourceModel(t, pipelineSpec)

				resource.ParallelTest(t, resource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
					PreCheck: func() {
						testutil.SetEnvDefaults()
					},
					Steps: []resource.TestStep{
						{
							PreConfig: func() {
								t.Logf("Creating pipeline: %s -> %s, endpoint: %s", streamSlug, endpointType, endpoint.Slug)
							},
							Config: mustRenderForwardingPipelineResource(ctx, resourceName, pipeline, endpoint),
							ConfigPlanChecks: resource.ConfigPlanChecks{
								PreApply: []plancheck.PlanCheck{
									plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
								},
							},
							ConfigStateChecks: []statecheck.StateCheck{
								statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("ref").AtMapKey("slug"), knownvalue.StringExact(slug)),
								statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("source").AtMapKey("slug"), knownvalue.StringExact(streamSlug)),
								statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("enabled"), knownvalue.Bool(true)),
								statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status").AtMapKey("state"), knownvalue.StringExact(typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE.String())),
							},
						},
					},
				})
			})
		}
	}
}

func TestForwardingPipelineResource_IncompatibleCombinations(t *testing.T) {
	t.Skip("Skipping incompatible combinations until status fails quickly")
	t.Parallel()

	for _, streamSlug := range metricsStreams {
		for _, endpointType := range getIncompatibleEndpointTypes(StreamTypeMetrics) {
			t.Run(fmt.Sprintf("%s_to_%s_fails", streamSlug, endpointType), func(t *testing.T) {
				randomInt := rand.IntN(100)
				resourceName := "incompat"
				ctx := t.Context()

				slug := slugify(fmt.Sprintf("incompat-%s", endpointType), randomInt)

				// TODO: When S3/Prometheus endpoints are implemented, dispatch based on endpointType
				endpoint := createHTTPSEndpoint(t, slug)

				if endpoint.Slug == "" {
					t.Fatalf("endpoint slug is empty: %+v", endpoint)
				}

				pipelineSpec := model.ForwardingPipelineSpecModel{
					Source: model.TelemetryStreamRefModel{
						Slug: types.StringValue(streamSlug),
					},
					Destination: model.ForwardingEndpointRefModel{
						Slug: types.StringValue(endpoint.Slug),
					},
					Enabled: types.BoolValue(true),
				}
				pipeline := mustForwardingPipelineResourceModel(t, pipelineSpec)

				resource.ParallelTest(t, resource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
					PreCheck: func() {
						testutil.SetEnvDefaults()
					},
					Steps: []resource.TestStep{
						{
							PreConfig: func() {
								t.Logf("Testing incompatible combination: %s -> %s endpoint (should fail)", streamSlug, endpointType)
							},
							Config:      mustRenderForwardingPipelineResource(ctx, resourceName, pipeline, endpoint),
							ExpectError: regexp.MustCompile(`(?i)(incompatible|invalid|not supported|failed)`),
						},
					},
				})
			})
		}
	}

	for _, streamSlug := range logsStreams {
		for _, endpointType := range getIncompatibleEndpointTypes(StreamTypeLogs) {
			testName := fmt.Sprintf("%s_to_%s_fails", streamSlug, endpointType)

		t.Run(testName, func(t *testing.T) {
			randomInt := rand.IntN(100)
			resourceName := testPipelineResourceName
			ctx := t.Context()

				// TODO: When S3/Prometheus endpoints are implemented, dispatch based on endpointType
				endpoint := createHTTPSEndpoint(t, slugify(fmt.Sprintf("incompat-%s", endpointType), randomInt))

				pipelineSpec := model.ForwardingPipelineSpecModel{
					Source: model.TelemetryStreamRefModel{
						Slug: types.StringValue(streamSlug),
					},
					Destination: model.ForwardingEndpointRefModel{
						Slug: types.StringValue(endpoint.Slug),
					},
					Enabled: types.BoolValue(true),
				}
				pipeline := mustForwardingPipelineResourceModel(t, pipelineSpec)

				resource.ParallelTest(t, resource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
					PreCheck: func() {
						testutil.SetEnvDefaults()
					},
					Steps: []resource.TestStep{
						{
							PreConfig: func() {
								t.Logf("Testing incompatible combination: %s -> %s endpoint (should fail)", streamSlug, endpointType)
							},
							Config:      mustRenderForwardingPipelineResource(ctx, resourceName, pipeline, endpoint),
							ExpectError: regexp.MustCompile(`(?i)(incompatible|invalid|not supported|failed)`),
						},
					},
				})
			})
		}
	}
}

// TestForwardingPipelineResource_Lifecycle tests the full lifecycle of a pipeline
func TestForwardingPipelineResource_Lifecycle(t *testing.T) {
	t.Skip()
	randomInt := rand.IntN(100)
	resourceName := "test_pipeline"
	fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_pipeline.%s", resourceName)
	ctx := t.Context()

	streamSlug := logsStreams[0]

	slug := slugify("lifecycle", randomInt)
	endpoint := createHTTPSEndpoint(t, slug)

	pipelineSpec := model.ForwardingPipelineSpecModel{
		Source: model.TelemetryStreamRefModel{
			Slug: types.StringValue(streamSlug),
		},
		Destination: model.ForwardingEndpointRefModel{
			Slug: types.StringValue(endpoint.Slug),
		},
		Enabled: types.BoolValue(true),
	}
	pipeline := mustForwardingPipelineResourceModel(t, pipelineSpec)

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					t.Log("Step 1: Create pipeline (enabled)")
				},
				Config: mustRenderForwardingPipelineResource(ctx, resourceName, pipeline, endpoint),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status").AtMapKey("state"), knownvalue.StringExact(typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE.String())),
				},
			},
			{
				PreConfig: func() {
					t.Log("Step 2: No-op (verify idempotency)")
				},
				Config: mustRenderForwardingPipelineResource(ctx, resourceName, pipeline, endpoint),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
					},
				},
			},
			{
				PreConfig: func() {
					t.Log("Step 3: Disable pipeline")
				},
				Config: mustRenderForwardingPipelineResource(ctx, resourceName, mustForwardingPipelineResourceModel(t, model.ForwardingPipelineSpecModel{
					Source: model.TelemetryStreamRefModel{
						Slug: types.StringValue(streamSlug),
					},
					Destination: model.ForwardingEndpointRefModel{
						Slug: types.StringValue(endpoint.Slug),
					},
					Enabled: types.BoolValue(false),
				}), endpoint),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("enabled"), knownvalue.Bool(false)),
				},
			},
			{
				PreConfig: func() {
					t.Log("Step 4: Re-enable pipeline")
				},
				Config: mustRenderForwardingPipelineResource(ctx, resourceName, pipeline, endpoint),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("enabled"), knownvalue.Bool(true)),
				},
			},
			{
				PreConfig: func() {
					t.Log("Step 5: Change slug (requires replacement)")
				},
				Config: mustRenderForwardingPipelineResource(ctx, resourceName, mustForwardingPipelineResourceModel(t, pipelineSpec), endpoint),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
		},
	})
}

// TestForwardingPipelineResource_InvalidStream tests error handling with invalid stream
func TestForwardingPipelineResource_InvalidStream(t *testing.T) {
	t.Skip()
	randomInt := rand.IntN(100)
	resourceName := "test_pipeline"
	ctx := t.Context()

	slug := slugify("invalid-stream", randomInt)
	endpoint := createHTTPSEndpoint(t, slug)

	pipelineSpec := model.ForwardingPipelineSpecModel{
		Source: model.TelemetryStreamRefModel{
			Slug: types.StringValue("nonexistent-stream"),
		},
		Destination: model.ForwardingEndpointRefModel{
			Slug: types.StringValue(endpoint.Slug),
		},
		Enabled: types.BoolValue(true),
	}
	pipeline := mustForwardingPipelineResourceModel(t, pipelineSpec)

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					t.Log("Testing pipeline creation with invalid stream")
				},
				Config:      mustRenderForwardingPipelineResource(ctx, resourceName, pipeline, endpoint),
				ExpectError: regexp.MustCompile(`(?i)(not found|invalid|failed)`),
			},
		},
	})
}
