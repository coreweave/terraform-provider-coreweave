package telecaster_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/hcl/v2/hclwrite"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/zclconf/go-cty/cty"
)

var (
	s3EndpointResourceName string = resourceName(telecaster.NewForwardingEndpointS3Resource())
)

func init() {
	resource.AddTestSweepers(s3EndpointResourceName, &resource.Sweeper{
		Name:         s3EndpointResourceName,
		Dependencies: []string{pipelineResourceName},
		F: func(r string) error {
			testutil.SetEnvDefaults()
			return typedEndpointSweeper(typesv1beta1.ForwardingEndpointSpec_S3_case.String())(r)
		},
	})
}

func renderS3EndpointResource(resourceName string, m *model.ForwardingEndpointS3Model) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{s3EndpointResourceName, resourceName})
	resourceBody := resource.Body()

	resourceBody.SetAttributeValue("slug", cty.StringVal(m.Slug.ValueString()))
	resourceBody.SetAttributeValue("display_name", cty.StringVal(m.DisplayName.ValueString()))
	resourceBody.SetAttributeValue("bucket", cty.StringVal(m.Bucket.ValueString()))
	resourceBody.SetAttributeValue("region", cty.StringVal(m.Region.ValueString()))

	if m.Credentials != nil {
		credsObj := map[string]cty.Value{
			"access_key_id":     cty.StringVal(m.Credentials.AccessKeyID.ValueString()),
			"secret_access_key": cty.StringVal(m.Credentials.SecretAccessKey.ValueString()),
		}
		if !m.Credentials.SessionToken.IsNull() {
			credsObj["session_token"] = cty.StringVal(m.Credentials.SessionToken.ValueString())
		}
		resourceBody.SetAttributeValue("credentials", cty.ObjectVal(credsObj))
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(fmt.Sprintf("failed to write HCL: %v", err))
	}
	return buf.String()
}

func TestS3ForwardingEndpointSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaRequest := fwresource.SchemaRequest{}
	schemaResponse := &fwresource.SchemaResponse{}

	telecaster.NewForwardingEndpointS3Resource().Schema(ctx, schemaRequest, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

type s3EndpointTestStep struct {
	TestName         string
	ResourceName     string
	Model            *model.ForwardingEndpointS3Model
	ConfigPlanChecks resource.ConfigPlanChecks
	Options          []testStepOption
}

func createS3EndpointTestStep(t *testing.T, opts s3EndpointTestStep) resource.TestStep {
	t.Helper()

	fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, opts.ResourceName)

	stateChecks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("slug"), knownvalue.StringExact(opts.Model.Slug.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("display_name"), knownvalue.StringExact(opts.Model.DisplayName.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("bucket"), knownvalue.StringExact(opts.Model.Bucket.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("region"), knownvalue.StringExact(opts.Model.Region.ValueString())),

		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("updated_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state_code"), knownvalue.Int32Exact(int32(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED))),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String())),
	}

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning %s test: %s", s3EndpointResourceName, opts.TestName)
		},
		Config:            renderS3EndpointResource(opts.ResourceName, opts.Model),
		ConfigPlanChecks:  opts.ConfigPlanChecks,
		ConfigStateChecks: stateChecks,
	}

	for _, option := range opts.Options {
		option(&testStep)
	}

	return testStep
}

func TestS3ForwardingEndpointResource(t *testing.T) {
	t.Run("core lifecycle", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_s3_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3Model{
			ForwardingEndpointModelCore: model.ForwardingEndpointModelCore{
				Slug:        types.StringValue(slugify("s3-fe", randomInt)),
				DisplayName: types.StringValue("Test S3 Endpoint"),
			},
			Bucket: types.StringValue("s3://test-bucket"),
			Region: types.StringValue("us-east-1"),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
			Steps: []resource.TestStep{
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "initial S3 forwarding endpoint",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "no-op (noop)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "update display name (update)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3Model) {
						m.DisplayName = types.StringValue("Updated S3 Endpoint")
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "revert display name (update)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "update slug (requires replacement)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3Model) {
						m.Slug = types.StringValue(slugify("s3-fe2", randomInt))
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "revert slug (plan only) (requires replacement)",
					ResourceName: resourceName,
					Model:        baseModel,
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
	})

	t.Run("with credentials", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_s3_credentials_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3Model{
			ForwardingEndpointModelCore: model.ForwardingEndpointModelCore{
				Slug:        types.StringValue(slugify("s3-credentials", randomInt)),
				DisplayName: types.StringValue("Test S3 Endpoint with Credentials"),
			},
			Bucket: types.StringValue("s3://secure-bucket"),
			Region: types.StringValue("us-west-2"),
			Credentials: &model.S3CredentialsModel{
				AccessKeyID:     types.StringValue("AKIAIOSFODNN7EXAMPLE"),
				SecretAccessKey: types.StringValue("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
			},
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
			Steps: []resource.TestStep{
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "initial S3 endpoint with credentials",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "no-op (noop)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
				}),
			},
		})
	})

	t.Run("with session token", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_s3_session_token_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3Model{
			ForwardingEndpointModelCore: model.ForwardingEndpointModelCore{
				Slug:        types.StringValue(slugify("s3-session-token", randomInt)),
				DisplayName: types.StringValue("Test S3 Endpoint with Session Token"),
			},
			Bucket: types.StringValue("s3://secure-bucket-with-token"),
			Region: types.StringValue("us-west-2"),
			Credentials: &model.S3CredentialsModel{
				AccessKeyID:     types.StringValue("AKIAIOSFODNN7EXAMPLE"),
				SecretAccessKey: types.StringValue("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
				SessionToken:    types.StringValue("FwoGZXIvYXdzEBYaDGV4YW1wbGVzZXNzaW9u"),
			},
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
			Steps: []resource.TestStep{
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "initial S3 endpoint with session token",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
			},
		})
	})
}
