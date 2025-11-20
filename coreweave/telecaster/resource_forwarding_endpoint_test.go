package telecaster_test

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
)

const (
	AcceptanceTestPrefix = "tf-acc-tc-"
)

func init() {
	resource.AddTestSweepers("coreweave_telecaster_forwarding_endpoint", &resource.Sweeper{
		Name:         "coreweave_telecaster_forwarding_endpoint",
		Dependencies: []string{"coreweave_telecaster_forwarding_pipeline"},
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.ListEndpoints(ctx, connect.NewRequest(&clusterv1beta1.ListEndpointsRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list forwarding endpoints: %w", err)
			}

			for _, endpoint := range listResp.Msg.GetEndpoints() {
				if !strings.HasPrefix(endpoint.Ref.Slug, AcceptanceTestPrefix) {
					log.Printf("skipping forwarding endpoint %s because it does not have prefix %s", endpoint.Ref.Slug, AcceptanceTestPrefix)
					continue
				}

				log.Printf("sweeping forwarding endpoint %s", endpoint.Ref.Slug)
				if testutil.SweepDryRun() {
					log.Printf("skipping forwarding endpoint %s because of dry-run mode", endpoint.Ref.Slug)
					continue
				}

				deleteCtx, deleteCancel := context.WithTimeout(ctx, 5*time.Minute)
				defer deleteCancel()

				if _, err := client.DeleteEndpoint(deleteCtx, connect.NewRequest(&clusterv1beta1.DeleteEndpointRequest{
					Ref: endpoint.Ref,
				})); err != nil {
					if connect.CodeOf(err) == connect.CodeNotFound {
						log.Printf("forwarding endpoint %s already deleted", endpoint.Ref.Slug)
						continue
					}
					return fmt.Errorf("failed to delete forwarding endpoint %s: %w", endpoint.Ref.Slug, err)
				}

				waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Minute)
				defer waitCancel()

				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()

				for {
					select {
					case <-waitCtx.Done():
						return fmt.Errorf("timeout waiting for forwarding endpoint %s to be deleted: %w", endpoint.Ref.Slug, waitCtx.Err())
					case <-ticker.C:
						_, err := client.GetEndpoint(waitCtx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
							Ref: endpoint.Ref,
						}))
						if connect.CodeOf(err) == connect.CodeNotFound {
							log.Printf("forwarding endpoint %s successfully deleted", endpoint.Ref.Slug)
							goto nextEndpoint
						}
						if err != nil {
							return fmt.Errorf("error checking forwarding endpoint %s deletion status: %w", endpoint.Ref.Slug, err)
						}
					}
				}
			nextEndpoint:
			}

			return nil
		},
	})
}

func mustForwardingEndpointResourceModel(t *testing.T, ref model.ForwardingEndpointRefModel, spec model.ForwardingEndpointSpecModel, status model.ForwardingEndpointStatusModel) *telecaster.ForwardingEndpointResourceModel {
	t.Helper()

	ctx := t.Context()

	schemaResp := new(fwresource.SchemaResponse)
	telecaster.NewForwardingEndpointResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("failed to get schema: %v", schemaResp.Diagnostics)
	}

	model := telecaster.ForwardingEndpointResourceModel{}
	dieIfDiagnostics(t, tfsdk.ValueFrom(ctx, ref, schemaResp.Schema.Attributes["ref"].GetType(), &model.Ref))
	dieIfDiagnostics(t, tfsdk.ValueFrom(ctx, spec, schemaResp.Schema.Attributes["spec"].GetType(), &model.Spec))
	dieIfDiagnostics(t, tfsdk.ValueFrom(ctx, status, schemaResp.Schema.Attributes["status"].GetType(), &model.Status))
	return &model
}

func TestTFLogInterceptor(t *testing.T) {
	t.Parallel()

	interceptor := coreweave.TFLogInterceptor()
	assert.NotNil(t, interceptor, "TFLogInterceptor should not return nil")
}

func TestForwardingEndpointSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaRequest := fwresource.SchemaRequest{}
	schemaResponse := &fwresource.SchemaResponse{}

	telecaster.NewForwardingEndpointResource().Schema(ctx, schemaRequest, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

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

type forwardingEndpointTestStep struct {
	TestName         string
	ResourceName     string
	Endpoint         *telecaster.ForwardingEndpointResourceModel
	ConfigPlanChecks resource.ConfigPlanChecks
	// Options are applied to the test step in the order they are provided. Use this to add extra configuration not directly supported.
	Options []testStepOption
}

type testStepOption func(*resource.TestStep)

func testStepOptionPlanOnly(v bool) testStepOption {
	return func(ts *resource.TestStep) {
		ts.PlanOnly = v
	}
}

func createForwardingEndpointTestStep(ctx context.Context, t *testing.T, opts forwardingEndpointTestStep) resource.TestStep {
	t.Helper()

	metadataResp := new(fwresource.MetadataResponse)
	telecaster.NewForwardingEndpointResource().Metadata(ctx, fwresource.MetadataRequest{ProviderTypeName: "coreweave"}, metadataResp)

	fullResourceName := strings.Join([]string{metadataResp.TypeName, opts.ResourceName}, ".")

	var ref model.ForwardingEndpointRefModel
	if err := opts.Endpoint.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true}); err != nil {
		t.Fatalf("failed to convert ref to %T model: %v", ref, err)
	}

	var spec model.ForwardingEndpointSpecModel
	if err := opts.Endpoint.Spec.As(ctx, &spec, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true}); err != nil {
		t.Fatalf("failed to convert spec to %T model: %v", spec, err)
	}

	stateChecks := make([]statecheck.StateCheck, 0)
	stateChecks = append(stateChecks,
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("ref"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("ref").AtMapKey("slug"), knownvalue.StringExact(ref.Slug.ValueString())),

		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("display_name"), knownvalue.StringExact(spec.DisplayName.ValueString())),

		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status").AtMapKey("created_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status").AtMapKey("updated_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status").AtMapKey("state_code"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status").AtMapKey("state"), knownvalue.NotNull()),
	)

	specPath := tfjsonpath.New("spec")

	if spec.Kafka != nil {
		kafkaPath := specPath.AtMapKey("kafka")
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, kafkaPath, knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullResourceName, kafkaPath.AtMapKey("bootstrap_endpoints"), knownvalue.StringExact(spec.Kafka.BootstrapEndpoints.ValueString())),
			statecheck.ExpectKnownValue(fullResourceName, kafkaPath.AtMapKey("topic"), knownvalue.StringExact(spec.Kafka.Topic.ValueString())),
		)

		if spec.Kafka.TLS != nil {
			stateChecks = append(stateChecks,
				statecheck.ExpectKnownValue(fullResourceName, kafkaPath.AtMapKey("tls"), knownvalue.NotNull()),
			)
		}

		if spec.Kafka.ScramAuth != nil {
			scramAuthPath := kafkaPath.AtMapKey("scram_auth")
			stateChecks = append(stateChecks,
				statecheck.ExpectKnownValue(fullResourceName, scramAuthPath, knownvalue.NotNull()),
			)
			if !spec.Kafka.ScramAuth.Mechanism.IsNull() {
				stateChecks = append(stateChecks,
					statecheck.ExpectKnownValue(fullResourceName, scramAuthPath.AtMapKey("mechanism"), knownvalue.StringExact(spec.Kafka.ScramAuth.Mechanism.ValueString())),
				)
			}
		}
	}

	if spec.Prometheus != nil {
		promPath := specPath.AtMapKey("prometheus")
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, promPath, knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullResourceName, promPath.AtMapKey("endpoint"),
				knownvalue.StringExact(spec.Prometheus.Endpoint.ValueString())),
		)

		if spec.Prometheus.TLS != nil {
			stateChecks = append(stateChecks,
				statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("prometheus").AtMapKey("tls"), knownvalue.NotNull()),
			)
		}
	}

	if spec.S3 != nil {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("s3"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("s3").AtMapKey("uri"),
				knownvalue.StringExact(spec.S3.URI.ValueString())),
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("s3").AtMapKey("region"),
				knownvalue.StringExact(spec.S3.Region.ValueString())),
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("s3").AtMapKey("requires_credentials"),
				knownvalue.Bool(spec.S3.RequiresCredentials.ValueBool())),
		)
	}

	if spec.HTTPS != nil {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("https"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("https").AtMapKey("endpoint"),
				knownvalue.StringExact(spec.HTTPS.Endpoint.ValueString())),
		)

		// Check TLS config if present
		if spec.HTTPS.TLS != nil {
			stateChecks = append(stateChecks,
				statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("spec").AtMapKey("https").AtMapKey("tls"), knownvalue.NotNull()),
			)
		}
	}

	return resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning coreweave_telecaster_forwarding_endpoint test: %s", opts.TestName)
		},
		Config:            telecaster.MustRenderForwardingEndpointResource(ctx, opts.ResourceName, opts.Endpoint),
		ConfigPlanChecks:  opts.ConfigPlanChecks,
		ConfigStateChecks: stateChecks,
	}
}

func dieIfDiagnostics(t *testing.T, diagnostics diag.Diagnostics) {
	t.Helper()
	if diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", diagnostics)
	}
}

func transform[T any](t *testing.T, base T, f func(*T)) T {
	t.Helper()
	transformed := base
	f(&transformed)
	return transformed
}

// TestForwardingEndpointResource_HTTPS tests the full lifecycle of an HTTPS forwarding endpoint
func TestForwardingEndpointResource_HTTPS(t *testing.T) {
	randomInt := rand.IntN(100)
	resourceName := fmt.Sprintf("test_acc_https_endpoint_%d", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_endpoint.%s", resourceName)
	ctx := t.Context()

	slugify := func(s string) string {
		return fmt.Sprintf("%s%s-%d", AcceptanceTestPrefix, s, randomInt)
	}

	schemaResp := &fwresource.SchemaResponse{}
	telecaster.NewForwardingEndpointResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("failed to get schema: %v", schemaResp.Diagnostics)
	}

	// Base models, using data types, to be used as a basis for the test steps
	refModel := model.ForwardingEndpointRefModel{
		Slug: types.StringValue(slugify("https-fe")),
	}
	specModel := model.ForwardingEndpointSpecModel{
		DisplayName: types.StringValue("Test HTTPS Endpoint"),
		HTTPS: &model.ForwardingEndpointHTTPSModel{
			Endpoint: types.StringValue("http://telecaster-console.us-east-03-core-services.int.coreweave.com:9000/"),
		},
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "initial HTTPS forwarding endpoint",
				ResourceName: resourceName,
				Endpoint:     mustForwardingEndpointResourceModel(t, refModel, specModel, model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "no-op (noop)",
				ResourceName: resourceName,
				Endpoint:     mustForwardingEndpointResourceModel(t, refModel, specModel, model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "update HTTPS endpoint display name (update)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.DisplayName = types.StringValue("Updated HTTPS Endpoint")
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "revert HTTPS endpoint display name (update)",
				ResourceName: resourceName,
				Endpoint:     mustForwardingEndpointResourceModel(t, refModel, specModel, model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "update ref slug (requires replacement)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, transform(t, refModel, func(ref *model.ForwardingEndpointRefModel) {
					ref.Slug = types.StringValue(slugify("https-fe2"))
				}), specModel, model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "revert ref slug (plan only) (requires replacement)",
				ResourceName: resourceName,
				Endpoint:     mustForwardingEndpointResourceModel(t, refModel, specModel, model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
				},
				Options: []testStepOption{testStepOptionPlanOnly(true)},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "update spec kind to s3 (plan only) (requires replacement)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.HTTPS = nil
					spec.S3 = &model.ForwardingEndpointS3Model{
						URI:    types.StringValue("s3://bucket/key"),
						Region: types.StringValue("US-EAST-04A"),
					}
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
				},
				Options: []testStepOption{testStepOptionPlanOnly(true)},
			}),
		},
	})
}

// TestForwardingEndpointResource_S3 tests the full lifecycle of an S3 forwarding endpoint
func TestForwardingEndpointResource_S3(t *testing.T) {
	// TODO: Enable once API is available
	t.Skip("Skipping acceptance test until Telecaster API is available")

	// TODO: Uncomment and implement when ready
	// randomInt := rand.IntN(10000)
	// endpointSlug := fmt.Sprintf("%ss3-endpoint-%d", AcceptanceTestPrefix, randomInt)
	// resourceName := fmt.Sprintf("test_acc_s3_endpoint_%d", randomInt)
	// ctx := context.Background()

	// TODO: Implement S3 endpoint test with requires_credentials flag
	// resource.ParallelTest(t, resource.TestCase{
	// 	ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
	// 	PreCheck: func() {
	// 		testutil.SetEnvDefaults()
	// 	},
	// 	Steps: []resource.TestStep{
	// 		// TODO: Create step with requires_credentials = false
	// 		// TODO: Update step with requires_credentials = true
	// 		// TODO: Import test
	// 	},
	// })
}

// TestForwardingEndpointResource_Kafka tests the full lifecycle of a Kafka forwarding endpoint
func TestForwardingEndpointResource_Kafka(t *testing.T) {
	t.Skip("Skipping acceptance test until Telecaster API is available")
}

// TestForwardingEndpointResource_Prometheus tests the full lifecycle of a Prometheus forwarding endpoint
func TestForwardingEndpointResource_Prometheus(t *testing.T) {
	// TODO: Enable once API is available
	t.Skip("Skipping acceptance test until Telecaster API is available")

	// TODO: Uncomment and implement when ready
	// randomInt := rand.IntN(10000)
	// endpointSlug := fmt.Sprintf("%sprometheus-endpoint-%d", AcceptanceTestPrefix, randomInt)
	// resourceName := fmt.Sprintf("test_acc_prometheus_endpoint_%d", randomInt)
	// ctx := context.Background()

	// TODO: Implement Prometheus endpoint test with basic auth
	// resource.ParallelTest(t, resource.TestCase{
	// 	ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
	// 	PreCheck: func() {
	// 		testutil.SetEnvDefaults()
	// 	},
	// 	Steps: []resource.TestStep{
	// 		// TODO: Create step with prometheus config and basic auth
	// 		// TODO: Update step with different endpoint or auth
	// 		// TODO: Import test
	// 	},
	// })
}

// TestForwardingEndpointResource_RequiresReplace tests attributes that require replacement
func TestForwardingEndpointResource_RequiresReplace(t *testing.T) {
	// TODO: Enable once API is available and determine which fields require replacement
	t.Skip("Skipping acceptance test until Telecaster API is available")

	// TODO: Uncomment and implement when ready
	// randomInt := rand.IntN(10000)
	// resourceName := fmt.Sprintf("test_acc_replace_endpoint_%d", randomInt)
	// fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_endpoint.%s", resourceName)
	// ctx := context.Background()

	// resource.ParallelTest(t, resource.TestCase{
	// 	ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
	// 	PreCheck: func() {
	// 		testutil.SetEnvDefaults()
	// 	},
	// 	Steps: []resource.TestStep{
	// 		// TODO: Create initial endpoint
	// 		// TODO: Change slug (should require replacement)
	// 		// createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
	// 		// 	TestName:     "requires replace - change slug",
	// 		// 	ResourceName: resourceName,
	// 		// 	// TODO: Endpoint with different slug
	// 		// 	ConfigPlanChecks: resource.ConfigPlanChecks{
	// 		// 		PreApply: []plancheck.PlanCheck{
	// 		// 			plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionDestroyBeforeCreate),
	// 		// 		},
	// 		// 	},
	// 		// }),
	// 	},
	// })
}

// TestTelemetryStreamDataSource tests reading stream data
func TestTelemetryStreamDataSource(t *testing.T) {
	// TODO: Enable once API is available
	t.Skip("Skipping acceptance test until Telecaster API is available")

	// TODO: Uncomment and implement when ready
	// ctx := context.Background()
	// randomInt := rand.IntN(10000)
	// resourceName := fmt.Sprintf("test_acc_stream_%d", randomInt)
	// fullDataSourceName := fmt.Sprintf("data.coreweave_telecaster_stream.%s", resourceName)
	// streamSlug := fmt.Sprintf("%sstream-%d", AcceptanceTestPrefix, randomInt)

	// TODO: Determine how streams are created - are they managed resources or pre-existing?
	// resource.ParallelTest(t, resource.TestCase{
	// 	ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
	// 	PreCheck: func() {
	// 		testutil.SetEnvDefaults()
	// 	},
	// 	Steps: []resource.TestStep{
	// 		{
	// 			PreConfig: func() {
	// 				t.Log("Beginning coreweave_telecaster_stream data source test")
	// 			},
	// 			// TODO: Create the stream first if it's a managed resource, or reference an existing one
	// 			Config: telecaster.MustRenderTelemetryStreamDataSource(ctx, resourceName, &telecaster.TelemetryStreamDataSourceModel{
	// 				Ref: types.ObjectValueMust(
	// 					map[string]attr.Type{
	// 						"slug": types.StringType,
	// 					},
	// 					map[string]attr.Value{
	// 						"slug": types.StringValue(streamSlug),
	// 					},
	// 				),
	// 			}),
	// 			ConfigStateChecks: []statecheck.StateCheck{
	// 				// TODO: Add proper state checks once we understand the stream structure
	// 				statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("ref"), knownvalue.NotNull()),
	// 				statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("spec"), knownvalue.NotNull()),
	// 				statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("status"), knownvalue.NotNull()),
	// 			},
	// 		},
	// 	},
	// })
}

// TestTelemetryStreamDataSource_NotFound tests behavior when stream doesn't exist
func TestTelemetryStreamDataSource_NotFound(t *testing.T) {
	// TODO: Enable once API is available
	t.Skip("Skipping acceptance test until Telecaster API is available")

	// TODO: Uncomment and implement when ready
	// ctx := context.Background()
	// randomInt := rand.IntN(10000)
	// resourceName := fmt.Sprintf("test_acc_stream_notfound_%d", randomInt)
	// nonExistentSlug := "nonexistent-stream-00000000-0000-0000-0000-000000000000"

	// resource.ParallelTest(t, resource.TestCase{
	// 	ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
	// 	PreCheck: func() {
	// 		testutil.SetEnvDefaults()
	// 	},
	// 	Steps: []resource.TestStep{
	// 		{
	// 			PreConfig: func() {
	// 				t.Log("Beginning coreweave_telecaster_stream not found test")
	// 			},
	// 			Config: telecaster.MustRenderTelemetryStreamDataSource(ctx, resourceName, &telecaster.TelemetryStreamDataSourceModel{
	// 				Ref: types.ObjectValueMust(
	// 					map[string]attr.Type{
	// 						"slug": types.StringType,
	// 					},
	// 					map[string]attr.Value{
	// 						"slug": types.StringValue(nonExistentSlug),
	// 					},
	// 				),
	// 			}),
	// 			// TODO: Determine if this should be an error or a warning
	// 			// ExpectError: regexp.MustCompile(`(?i)stream.*not found`),
	// 		},
	// 	},
	// })
}
