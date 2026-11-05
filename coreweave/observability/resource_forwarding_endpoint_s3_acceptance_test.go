package observability_test

import (
	"fmt"
	"regexp"
	"testing"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
)

// Every subtest here drives a real API, so the whole function is skipped
// without TF_ACC. The offline schema, render and round_trip coverage lives in
// resource_forwarding_endpoint_s3_test.go.
func TestForwardingEndpointS3Acceptance(t *testing.T) {
	t.Parallel()

	t.Run("invalid_config", func(t *testing.T) {
		t.Parallel()

		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_s3_invalid_%s", token)

		valid := func() *model.ForwardingEndpointS3 {
			return &model.ForwardingEndpointS3{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue(slugify("s3-invalid", token)),
					DisplayName: types.StringValue("Invalid Config Probe"),
				},
				URI:         types.StringValue("s3://tf-acc-telemetry/prefix/"),
				Region:      types.StringValue("us-east-1"),
				Credentials: fixtureS3Credentials(),
			}
		}

		cases := []struct {
			name        string
			model       *model.ForwardingEndpointS3
			expectError *regexp.Regexp
		}{
			{
				name: "uri is not an s3 scheme",
				model: with(valid(), func(m *model.ForwardingEndpointS3) {
					m.URI = types.StringValue("https://not-s3.example.com/bucket")
				}),
				expectError: regexp.MustCompile(`(?s)uri.*s3://`),
			},
			{
				name: "uri has an uppercase bucket name",
				model: with(valid(), func(m *model.ForwardingEndpointS3) {
					m.URI = types.StringValue("s3://My-Bucket/prefix/")
				}),
				expectError: regexp.MustCompile(`(?s)uri.*s3://`),
			},
			{
				name: "region is empty",
				model: with(valid(), func(m *model.ForwardingEndpointS3) {
					m.Region = types.StringValue("")
				}),
				expectError: regexp.MustCompile(`(?s)region.*at least 1`),
			},
			{
				// CreateEndpointRequest carries a message-level CEL rule:
				// "!has(this.spec.s3) || has(this.s3)". Credentials are mandatory.
				name: "credentials omitted",
				model: with(valid(), func(m *model.ForwardingEndpointS3) {
					m.Credentials = nil
				}),
				expectError: regexp.MustCompile(`(?s)(?i)require\w*\s+credentials`),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resource.ParallelTest(t, resource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
					PreCheck:                 func() { setAccEnvDefaults() },
					CheckDestroy:             checkDestroyed(t),
					Steps: []resource.TestStep{
						{
							Config:      renderS3EndpointResource(resourceName, tc.model),
							PlanOnly:    true,
							ExpectError: tc.expectError,
						},
					},
				})
			})
		}
	})

	t.Run("lifecycle", func(t *testing.T) {
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_s3_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("s3-fe", token)),
				DisplayName: types.StringValue("Test S3 Endpoint"),
			},
			URI:         types.StringValue("s3://tf-acc-telemetry-bucket"),
			Region:      types.StringValue("us-east-1"),
			Credentials: fixtureS3Credentials(),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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
					// The case that sent empty credentials before Update learned to read
					// req.Config: nothing about the credentials changed, so the request
					// must carry PRESERVE and no credential payload.
					//
					// Changes region rather than display_name: UpdateEndpoint rejects
					// any changed display_name as immutable. Region is confirmed
					// mutable and, unlike uri, is not an input to the credential AAD,
					// so the stored credentials stay decryptable.
					TestName:     "update region only, credentials untouched",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3) {
						m.Region = types.StringValue("us-west-2")
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "revert region (update)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				{
					PreConfig: func() {
						t.Log("import")
					},
					ResourceName:      fullResourceName,
					ImportState:       true,
					ImportStateId:     baseModel.Slug.ValueString(),
					ImportStateVerify: true,
					// The resource has no id attribute; slug is the identity the
					// import passthrough reads, so verification has to match on it.
					ImportStateVerifyIdentifierAttribute: "slug",
					ImportStateVerifyIgnore:              []string{"credentials", "credentials_version"},
				},
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "update slug (requires replacement, plan only)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3) {
						m.Slug = types.StringValue(slugify("s3-fe2", token))
					}),
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

	// S3 is the type where CLEAR is illegal: CreateEndpointRequest requires
	// credentials and UpdateEndpointRequest carries
	// "credentials cannot be cleared for S3 endpoints". Rotation still works.
	t.Run("credentials", func(t *testing.T) {
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_s3_creds_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:               types.StringValue(slugify("s3-creds", token)),
				DisplayName:        types.StringValue("Test S3 Endpoint Credentials"),
				CredentialsVersion: types.Int64Value(1),
			},
			URI:         types.StringValue("s3://tf-acc-telemetry-creds"),
			Region:      types.StringValue("us-east-1"),
			Credentials: fixtureS3Credentials(),
		}

		var afterCreate, afterNoopUpdate string

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
			Steps: []resource.TestStep{
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "create with credentials",
					ResourceName: resourceName,
					Model:        baseModel,
					ExtraChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials").AtMapKey("secret_access_key"), knownvalue.Null()),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials").AtMapKey("access_key_id"), knownvalue.Null()),
						extractCredentialsUpdatedAt(fullResourceName, &afterCreate),
					},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					// Was a display_name-only update until the server began rejecting
					// that as immutable; region is the equivalent spec-only change.
					TestName:     "spec-only update preserves credentials (PRESERVE)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3) {
						m.Region = types.StringValue("us-west-2")
					}),
					ExtraChecks: []statecheck.StateCheck{
						extractCredentialsUpdatedAt(fullResourceName, &afterNoopUpdate),
						expectUnchanged(credentialsUpdatedAtAttribute, &afterCreate, &afterNoopUpdate),
					},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					// REPLACE on an S3 endpoint runs a live bucket probe before the
					// credentials are stored, so a rotation only succeeds with keys
					// that genuinely reach the bucket. Fixture keys cannot, and this
					// asserts the refusal rather than pretending otherwise.
					//
					// The consequence is that successful S3 rotation is not covered
					// here; exercising it needs a real bucket and working keys for
					// the test organization. The HTTPS suite covers the full
					// PRESERVE/REPLACE/CLEAR matrix, which has no such probe.
					TestName:     "rotating S3 credentials probes the bucket first (REPLACE)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3) {
						m.CredentialsVersion = types.Int64Value(2)
						m.Credentials = with(fixtureS3Credentials(), func(c *model.S3Credentials) {
							c.AccessKeyID = types.StringValue("AKIAI44QH8DHBEXAMPLE")
							c.SecretAccessKey = types.StringValue("je7MtGbClwBF/2Zp9Utk/h3yCo8nvbEXAMPLEKEY")
						})
					}),
					ExpectError: regexp.MustCompile(`ACCESS_DENIED`),
				}),
				{
					// Clearing is rejected at plan, with the attribute named, rather
					// than at apply with the server's CEL message.
					PreConfig: func() { t.Log("removing credentials from an S3 endpoint is rejected") },
					Config: renderS3EndpointResource(resourceName, with(baseModel, func(m *model.ForwardingEndpointS3) {
						m.Credentials = nil
						m.CredentialsVersion = types.Int64Null()
					})),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(`(?s)(?i)credentials.*cannot be removed`),
				},
				// Leaves a valid config behind. The framework destroys using the
				// last step's config, and the rejection above is deliberately
				// unappliable, so ending there fails the destroy rather than the
				// assertion.
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "restore a destroyable config",
					ResourceName: resourceName,
					Model:        baseModel,
				}),
			},
		})
	})

	t.Run("session_token", func(t *testing.T) {
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_s3_session_token_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("s3-session", token)),
				DisplayName: types.StringValue("Test S3 Endpoint with Session Token"),
			},
			URI:    types.StringValue("s3://tf-acc-telemetry-token"),
			Region: types.StringValue("us-west-2"),
			Credentials: with(fixtureS3Credentials(), func(c *model.S3Credentials) {
				c.SessionToken = types.StringValue("FwoGZXIvYXdzEBYaDGV4YW1wbGVzZXNzaW9u")
			}),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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

	t.Run("disappears", func(t *testing.T) {
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_s3_disappears_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("s3-gone", token)),
				DisplayName: types.StringValue("Test S3 Endpoint Disappears"),
			},
			URI:         types.StringValue("s3://tf-acc-telemetry-gone"),
			Region:      types.StringValue("us-east-1"),
			Credentials: fixtureS3Credentials(),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
			Steps: []resource.TestStep{
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "create the endpoint to be deleted out of band",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "out-of-band delete plans a re-create, not a panic",
					ResourceName: resourceName,
					Model:        baseModel,
					PreConfig: func() {
						client := testClient(t)
						require.NoError(t, observability.DeleteEndpointAndWait(
							t.Context(), client,
							&typesv1beta1.ForwardingEndpointRef{Slug: baseModel.Slug.ValueString()},
						))
					},
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
