package observability_test

import (
	"bytes"
	"embed"
	"fmt"
	"math/rand/v2"
	"regexp"
	"testing"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
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
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

var (
	//go:embed testdata
	s3EndpointTestdata embed.FS

	s3EndpointResourceName string = resourceName(observability.NewForwardingEndpointS3Resource())
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

func renderS3EndpointResource(resourceName string, m *model.ForwardingEndpointS3) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{s3EndpointResourceName, resourceName})
	resourceBody := resource.Body()

	setCommonEndpointAttributes(resourceBody, m.ForwardingEndpointCore)
	resourceBody.SetAttributeValue("uri", cty.StringVal(m.URI.ValueString()))
	resourceBody.SetAttributeValue("region", cty.StringVal(m.Region.ValueString()))

	if !m.CredentialsVersion.IsNull() {
		resourceBody.SetAttributeValue("credentials_version", cty.NumberIntVal(m.CredentialsVersion.ValueInt64()))
	}

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

type s3EndpointTestStep struct {
	TestName         string
	ResourceName     string
	Model            *model.ForwardingEndpointS3
	PreConfig        func()
	ConfigPlanChecks resource.ConfigPlanChecks
	ExtraChecks      []statecheck.StateCheck
	Options          []testStepOption
}

func createS3EndpointTestStep(t *testing.T, opts s3EndpointTestStep) resource.TestStep {
	t.Helper()

	fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, opts.ResourceName)

	stateChecks := make([]statecheck.StateCheck, 0, 9+len(opts.ExtraChecks))
	stateChecks = append(stateChecks,
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("slug"), knownvalue.StringExact(opts.Model.Slug.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("display_name"), knownvalue.StringExact(opts.Model.DisplayName.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("uri"), knownvalue.StringExact(opts.Model.URI.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("region"), knownvalue.StringExact(opts.Model.Region.ValueString())),

		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("updated_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String())),

		// S3 credentials are mandatory, so this is always true for a resource
		// that applied successfully.
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials_configured"), knownvalue.Bool(true)),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials_updated_at"), knownvalue.NotNull()),
	)

	stateChecks = append(stateChecks, opts.ExtraChecks...)

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning %s test: %s", s3EndpointResourceName, opts.TestName)
			if opts.PreConfig != nil {
				opts.PreConfig()
			}
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

func fixtureS3Credentials() *model.S3Credentials {
	return &model.S3Credentials{
		AccessKeyID:     types.StringValue("AKIAIOSFODNN7EXAMPLE"),
		SecretAccessKey: types.StringValue("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
	}
}

// TestForwardingEndpointS3 is the umbrella for every S3 forwarding endpoint
// test. Like HTTPS, S3 endpoints are database-only records server-side, so
// these are safe against any org.
//
// Only this function and the pure-unit subtests call t.Parallel();
// resource.ParallelTest calls it internally.
func TestForwardingEndpointS3(t *testing.T) {
	t.Parallel()

	t.Run("schema", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		schemaResponse := &fwresource.SchemaResponse{}

		observability.NewForwardingEndpointS3Resource().Schema(ctx, fwresource.SchemaRequest{}, schemaResponse)
		assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

		diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
		assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
	})

	t.Run("render", func(t *testing.T) {
		t.Parallel()

		endpoint := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue("test-s3-endpoint"),
				DisplayName: types.StringValue("Test S3 Endpoint"),
			},
			URI:    types.StringValue("s3://my-telemetry-bucket"),
			Region: types.StringValue("us-east-1"),
		}

		expectedHCL, err := s3EndpointTestdata.ReadFile("testdata/hcl_endpoint_s3_basic.tf")
		require.NoError(t, err)

		hcl := renderS3EndpointResource("test", endpoint)
		assert.Equal(t, string(expectedHCL), hcl)
	})

	t.Run("invalid_config", func(t *testing.T) {
		t.Parallel()

		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_s3_invalid_%d", randomInt)

		valid := func() *model.ForwardingEndpointS3 {
			return &model.ForwardingEndpointS3{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue(slugify("s3-invalid", randomInt)),
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
				expectError: regexp.MustCompile(`(?s)(?i)credentials.*required`),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resource.ParallelTest(t, resource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
					PreCheck:                 func() { testutil.SetEnvDefaults() },
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
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_s3_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("s3-fe", randomInt)),
				DisplayName: types.StringValue("Test S3 Endpoint"),
			},
			URI:         types.StringValue("s3://tf-acc-telemetry-bucket"),
			Region:      types.StringValue("us-east-1"),
			Credentials: fixtureS3Credentials(),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
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
					TestName:     "update display name only, credentials untouched",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3) {
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
				{
					PreConfig: func() {
						t.Log("import")
					},
					ResourceName:            fullResourceName,
					ImportState:             true,
					ImportStateId:           baseModel.Slug.ValueString(),
					ImportStateVerify:       true,
					ImportStateVerifyIgnore: []string{"credentials", "credentials_version"},
				},
				createS3EndpointTestStep(t, s3EndpointTestStep{
					TestName:     "update slug (requires replacement, plan only)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3) {
						m.Slug = types.StringValue(slugify("s3-fe2", randomInt))
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
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_s3_creds_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:               types.StringValue(slugify("s3-creds", randomInt)),
				DisplayName:        types.StringValue("Test S3 Endpoint Credentials"),
				CredentialsVersion: types.Int64Value(1),
			},
			URI:         types.StringValue("s3://tf-acc-telemetry-creds"),
			Region:      types.StringValue("us-east-1"),
			Credentials: fixtureS3Credentials(),
		}

		var afterCreate, afterNoopUpdate, afterRotation string

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
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
					TestName:     "display_name-only update preserves credentials (PRESERVE)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3) {
						m.DisplayName = types.StringValue("Renamed, credentials untouched")
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
					TestName:     "bumping credentials_version rotates the key (REPLACE)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointS3) {
						m.CredentialsVersion = types.Int64Value(2)
						m.Credentials = with(fixtureS3Credentials(), func(c *model.S3Credentials) {
							c.AccessKeyID = types.StringValue("AKIAI44QH8DHBEXAMPLE")
							c.SecretAccessKey = types.StringValue("je7MtGbClwBF/2Zp9Utk/h3yCo8nvbEXAMPLEKEY")
						})
					}),
					ExtraChecks: []statecheck.StateCheck{
						extractCredentialsUpdatedAt(fullResourceName, &afterRotation),
						expectChanged(credentialsUpdatedAtAttribute, &afterNoopUpdate, &afterRotation),
					},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
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
			},
		})
	})

	t.Run("session_token", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_s3_session_token_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("s3-session-token", randomInt)),
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
			PreCheck:                 func() { testutil.SetEnvDefaults() },
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
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_s3_disappears_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("s3-gone", randomInt)),
				DisplayName: types.StringValue("Test S3 Endpoint Disappears"),
			},
			URI:         types.StringValue("s3://tf-acc-telemetry-gone"),
			Region:      types.StringValue("us-east-1"),
			Credentials: fixtureS3Credentials(),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
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
