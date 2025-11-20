package telecaster_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/hcl/v2/hclwrite"
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
	"github.com/zclconf/go-cty/cty"
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

func mustRenderForwardingEndpointResource(ctx context.Context, resourceName string, endpoint *telecaster.ForwardingEndpointResourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{"coreweave_telecaster_forwarding_endpoint", resourceName})
	resourceBody := resource.Body()

	// Extract refModel and spec from the model
	var refModel model.ForwardingEndpointRefModel
	if err := endpoint.Ref.As(ctx, &refModel, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true}); err != nil {
		panic(fmt.Sprintf("failed to extract ref: %v", err))
	}

	var specModel model.ForwardingEndpointSpecModel
	if err := endpoint.Spec.As(ctx, &specModel, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true}); err != nil {
		panic(fmt.Sprintf("failed to extract spec: %v", err))
	}

	// Set refMap nested object
	refMap := make(map[string]cty.Value)
	refMap["slug"] = cty.StringVal(refModel.Slug.ValueString())

	specMap := make(map[string]cty.Value)
	specMap["display_name"] = cty.StringVal(specModel.DisplayName.ValueString())

	if specModel.S3 != nil {
		specMap["s3"] = cty.ObjectVal(map[string]cty.Value{
			"uri":                  cty.StringVal(specModel.S3.URI.ValueString()),
			"region":               cty.StringVal(specModel.S3.Region.ValueString()),
			"requires_credentials": cty.BoolVal(specModel.S3.RequiresCredentials.ValueBool()),
		})
	}

	if specModel.Prometheus != nil {
		prometheusMap := make(map[string]cty.Value)
		prometheusMap["endpoint"] = cty.StringVal(specModel.Prometheus.Endpoint.ValueString())
		if specModel.Prometheus.TLS != nil {
			prometheusMap["tls"] = cty.ObjectVal(map[string]cty.Value{
				"certificate_authority_data": cty.StringVal(specModel.Prometheus.TLS.CertificateAuthorityData.ValueString()),
			})
		}
		specMap["prometheus"] = cty.ObjectVal(prometheusMap)
	}

	if specModel.HTTPS != nil {
		httpsMap := make(map[string]cty.Value)
		httpsMap["endpoint"] = cty.StringVal(specModel.HTTPS.Endpoint.ValueString())
		if specModel.HTTPS.TLS != nil {
			httpsMap["tls"] = cty.ObjectVal(map[string]cty.Value{
				"certificate_authority_data": cty.StringVal(specModel.HTTPS.TLS.CertificateAuthorityData.ValueString()),
			})
		}
		specMap["https"] = cty.ObjectVal(httpsMap)
	}

	resourceBody.SetAttributeValue("ref", cty.ObjectVal(refMap))
	resourceBody.SetAttributeValue("spec", cty.ObjectVal(specMap))

	// Write to buffer and return
	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(fmt.Sprintf("failed to write HCL: %v", err))
	}
	return buf.String()
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

func testStepOptionExpectNonEmptyPlan(v bool) testStepOption {
	return func(ts *resource.TestStep) {
		ts.ExpectNonEmptyPlan = v
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

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning coreweave_telecaster_forwarding_endpoint test: %s", opts.TestName)
		},
		Config:            mustRenderForwardingEndpointResource(ctx, opts.ResourceName, opts.Endpoint),
		ConfigPlanChecks:  opts.ConfigPlanChecks,
		ConfigStateChecks: stateChecks,
	}

	// Apply any custom options
	for _, option := range opts.Options {
		option(&testStep)
	}

	return testStep
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

func TestForwardingEndpointResource_HTTPS(t *testing.T) {
	randomInt := rand.IntN(100)
	resourceName := fmt.Sprintf("test_acc_https_endpoint_%d", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_endpoint.%s", resourceName)
	ctx := t.Context()

	schemaResp := &fwresource.SchemaResponse{}
	telecaster.NewForwardingEndpointResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("failed to get schema: %v", schemaResp.Diagnostics)
	}

	// Base models, using data types, to be used as a basis for the test steps
	refModel := model.ForwardingEndpointRefModel{
		Slug: types.StringValue(slugify("https-fe", randomInt)),
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
					ref.Slug = types.StringValue(slugify("https-fe2", randomInt))
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
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
				},
				Options: []testStepOption{testStepOptionPlanOnly(true), testStepOptionExpectNonEmptyPlan(true)},
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
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
				},
				Options: []testStepOption{testStepOptionPlanOnly(true), testStepOptionExpectNonEmptyPlan(true)},
			}),
		},
	})
}

func TestForwardingEndpointResource_S3(t *testing.T) {
	randomInt := rand.IntN(100)
	resourceName := fmt.Sprintf("test_acc_s3_endpoint_%d", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_endpoint.%s", resourceName)
	ctx := t.Context()

	schemaResp := &fwresource.SchemaResponse{}
	telecaster.NewForwardingEndpointResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("failed to get schema: %v", schemaResp.Diagnostics)
	}

	// Base models for S3 endpoint
	refModel := model.ForwardingEndpointRefModel{
		Slug: types.StringValue(slugify("s3-fe", randomInt)),
	}
	specModel := model.ForwardingEndpointSpecModel{
		DisplayName: types.StringValue("Test S3 Endpoint"),
		S3: &model.ForwardingEndpointS3Model{
			URI:                 types.StringValue("s3://test-bucket/telemetry"),
			Region:              types.StringValue("us-east-1"),
			RequiresCredentials: types.BoolValue(false),
		},
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "initial S3 forwarding endpoint",
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
				TestName:     "update S3 endpoint display name (update)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.DisplayName = types.StringValue("Updated S3 Endpoint")
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "update S3 URI (update)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.DisplayName = types.StringValue("Updated S3 Endpoint")
					spec.S3.URI = types.StringValue("s3://test-bucket/telemetry/v2")
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "update S3 region (update)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.DisplayName = types.StringValue("Updated S3 Endpoint")
					spec.S3.URI = types.StringValue("s3://test-bucket/telemetry/v2")
					spec.S3.Region = types.StringValue("us-west-2")
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "update S3 requires_credentials (update)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.DisplayName = types.StringValue("Updated S3 Endpoint")
					spec.S3.URI = types.StringValue("s3://test-bucket/telemetry/v2")
					spec.S3.Region = types.StringValue("us-west-2")
					spec.S3.RequiresCredentials = types.BoolValue(true)
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "revert all S3 changes (update)",
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
					ref.Slug = types.StringValue(slugify("s3-fe2", randomInt))
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
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "update spec kind to https (plan only) (requires replacement)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.S3 = nil
					spec.HTTPS = &model.ForwardingEndpointHTTPSModel{
						Endpoint: types.StringValue("http://telecaster-console.us-east-03-core-services.int.coreweave.com:9000/"),
					}
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
				},
				Options: []testStepOption{testStepOptionPlanOnly(true), testStepOptionExpectNonEmptyPlan(true)},
			}),
		},
	})
}

// TestForwardingEndpointResource_Kafka tests the full lifecycle of a Kafka forwarding endpoint
func TestForwardingEndpointResource_Kafka(t *testing.T) {
	t.Skip("Skipping acceptance test until Kafka endpoint is available")
}

// TestForwardingEndpointResource_Prometheus tests the full lifecycle of a Prometheus forwarding endpoint
func TestForwardingEndpointResource_Prometheus(t *testing.T) {
	t.Skip("Skipping acceptance test until a Prometheus endpoint is available")
	randomInt := rand.IntN(100)
	resourceName := fmt.Sprintf("test_acc_prometheus_endpoint_%d", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_endpoint.%s", resourceName)
	ctx := t.Context()

	schemaResp := &fwresource.SchemaResponse{}
	telecaster.NewForwardingEndpointResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("failed to get schema: %v", schemaResp.Diagnostics)
	}

	refModel := model.ForwardingEndpointRefModel{
		Slug: types.StringValue(slugify("prometheus-fe", randomInt)),
	}
	specModel := model.ForwardingEndpointSpecModel{
		DisplayName: types.StringValue("Test Prometheus Endpoint"),
		Prometheus: &model.ForwardingEndpointPrometheusModel{
			Endpoint: types.StringValue("http://prometheus.example.com:9090/api/v1/write"),
		},
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "initial Prometheus forwarding endpoint",
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
				TestName:     "update Prometheus endpoint display name (update)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.DisplayName = types.StringValue("Updated Prometheus Endpoint")
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "update Prometheus endpoint URL (update)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.DisplayName = types.StringValue("Updated Prometheus Endpoint")
					spec.Prometheus.Endpoint = types.StringValue("http://prometheus-v2.example.com:9090/api/v1/write")
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "add TLS configuration (update)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.DisplayName = types.StringValue("Updated Prometheus Endpoint")
					spec.Prometheus.Endpoint = types.StringValue("http://prometheus-v2.example.com:9090/api/v1/write")
					spec.Prometheus.TLS = &model.TLSConfigModel{
						CertificateAuthorityData: types.StringValue("LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJURENDQWZPZ0F3SUJBZ0lVZmRLdDdHWU9hRDZuL2pvb3A3OEVoT3Y3YkFvd0NnWUlLb1pJemowRUF3SXcKSERFYU1CZ0dBMVVFQXd3UlkyOXlaWGRsWVhabFkyRXRjbTl2ZEMwd0hoY05NalF4TVRFek1EQTBNVEExV2hjTgpNalV4TVRFek1EQTBNVEExV2pBY01Sb3dHQVlEVlFRRERCRmpiM0psZDJWaGRtVmpZUzF5YjI5ME1Ea1ZNQk1HCkJ5cUdTTTQ5QWdFR0NDcUdTTTQ5QXdFSEEwSUFCUElIdUMyQklIdlFyUlV0bjdodFFnY1NGRDlDbEs0U3BLN0sKaEhWaS9RQm9naVREMC9yMWRqRkViYmZHOW9DTzFodHpXWjd4aE1CRUY4NFJ2TlhtdWNlamdZWXdnWU13RGdZRApWUjBQQVFIL0JBUURBZ0VHTUJJR0ExVWRFd0VCL3dRSU1BWUJBZjhDQVFBd0hRWURWUjBPQkJZRUZOckZjS1dJClVOcWdXcWNxWk5FSVRzOVJuZGh4TUI4R0ExVWRJd1FZTUJhQUZOckZjS1dJVU5xZ1dxY3FaTkVJVHM5Um5kaHgKTUJrR0ExVWRFUVFTTUJDQ0RuZGxkR2h2YjJ0ekxuTjJZekFLQmdncWhrak9QUVFEQWdOSEFEQkVBaUJlM3NsYQpTWjc5bmxQeWJlYVY4NXp5VW9VQ1hVWjNvTnhjN1lZc3N0WDFuZ0lnSUhYQ0xEZUZWKzF2Mlk1RzdwN3N0VTRCClA0VTlScHlyVzhMWnhRdWhFYjQ9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K"),
					}
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "remove TLS configuration (update)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.DisplayName = types.StringValue("Updated Prometheus Endpoint")
					spec.Prometheus.Endpoint = types.StringValue("http://prometheus-v2.example.com:9090/api/v1/write")
					spec.Prometheus.TLS = nil
				}), model.ForwardingEndpointStatusModel{}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createForwardingEndpointTestStep(ctx, t, forwardingEndpointTestStep{
				TestName:     "revert all Prometheus changes (update)",
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
					ref.Slug = types.StringValue(slugify("prometheus-fe2", randomInt))
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
				TestName:     "update spec kind to https (plan only) (requires replacement)",
				ResourceName: resourceName,
				Endpoint: mustForwardingEndpointResourceModel(t, refModel, transform(t, specModel, func(spec *model.ForwardingEndpointSpecModel) {
					spec.Prometheus = nil
					spec.HTTPS = &model.ForwardingEndpointHTTPSModel{
						Endpoint: types.StringValue("http://telecaster-console.us-east-03-core-services.int.coreweave.com:9000/"),
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
