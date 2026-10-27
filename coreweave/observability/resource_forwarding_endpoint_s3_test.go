package observability_test

import (
	"bytes"
	"embed"
	"fmt"
	"math/rand/v2"
	"regexp"
	"testing"
	"time"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
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
	"google.golang.org/protobuf/types/known/timestamppb"
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
			setAccEnvDefaults()
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
	// ExpectError asserts the apply is refused. State checks are skipped when
	// it is set, since nothing was applied to check.
	ExpectError *regexp.Regexp
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
		Config:           renderS3EndpointResource(opts.ResourceName, opts.Model),
		ConfigPlanChecks: opts.ConfigPlanChecks,
		ExpectError:      opts.ExpectError,
	}

	if opts.ExpectError == nil {
		testStep.ConfigStateChecks = stateChecks
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

	// round_trip pins the Set/ToMsg mapping. The shared status fields are
	// covered by the HTTPS suite; this asserts the S3 config and the guards
	// that keep the two endpoint types from reading each other's records.
	t.Run("round_trip", func(t *testing.T) {
		t.Parallel()

		created := timestamppb.New(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

		s3Msg := func() *typesv1beta1.ForwardingEndpoint {
			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test S3 Endpoint"}
			spec.SetS3(&typesv1beta1.S3Config{
				Uri:    "s3://my-telemetry-bucket/prefix",
				Region: "us-east-1",
			})

			return &typesv1beta1.ForwardingEndpoint{
				Ref:  &typesv1beta1.ForwardingEndpointRef{Slug: "test-s3-endpoint"},
				Spec: spec,
				Status: &typesv1beta1.ForwardingEndpointStatus{
					CreatedAt: created,
					State:     typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED,
				},
			}
		}

		t.Run("spec and status populate the model", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointS3
			require.False(t, endpoint.Set(s3Msg()).HasError())

			assert.Equal(t, "test-s3-endpoint", endpoint.Slug.ValueString())
			assert.Equal(t, "Test S3 Endpoint", endpoint.DisplayName.ValueString())
			assert.Equal(t, "s3://my-telemetry-bucket/prefix", endpoint.URI.ValueString())
			assert.Equal(t, "us-east-1", endpoint.Region.ValueString())
			assert.Equal(t, "2026-10-01T12:00:00Z", endpoint.CreatedAt.ValueString())
			assert.True(t, endpoint.UpdatedAt.IsNull(), "an endpoint never updated has no updated_at")
		})

		t.Run("uri and region survive the round trip", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointS3
			require.False(t, endpoint.Set(s3Msg()).HasError())

			out, diags := endpoint.ToMsg()
			require.False(t, diags.HasError())

			s3 := out.GetSpec().GetS3()
			assert.Equal(t, "s3://my-telemetry-bucket/prefix", s3.GetUri())
			assert.Equal(t, "us-east-1", s3.GetRegion())
		})

		t.Run("an endpoint of the wrong config type is refused", func(t *testing.T) {
			t.Parallel()

			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test HTTPS Endpoint"}
			spec.SetHttps(&typesv1beta1.HTTPSConfig{Endpoint: "https://example.com/telemetry"})

			var endpoint model.ForwardingEndpointS3
			diags := endpoint.Set(&typesv1beta1.ForwardingEndpoint{
				Ref:    &typesv1beta1.ForwardingEndpointRef{Slug: "test-https-endpoint"},
				Spec:   spec,
				Status: &typesv1beta1.ForwardingEndpointStatus{},
			})

			assert.True(t, diags.HasError())
		})

		// The session token is optional, and an empty one has to read back as
		// null so that omitting it produces no diff.
		t.Run("an empty session token reads as null", func(t *testing.T) {
			t.Parallel()

			var credentials model.S3Credentials
			credentials.Set(t.Context(), &typesv1beta1.S3Credentials{
				AccessKeyId:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "secret",
			})

			assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", credentials.AccessKeyID.ValueString())
			assert.True(t, credentials.SessionToken.IsNull())
		})

		t.Run("a set session token is preserved", func(t *testing.T) {
			t.Parallel()

			var credentials model.S3Credentials
			credentials.Set(t.Context(), &typesv1beta1.S3Credentials{
				AccessKeyId:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "secret",
				SessionToken:    "token",
			})

			assert.Equal(t, "token", credentials.SessionToken.ValueString())
		})

		t.Run("a nil model converts to a nil message", func(t *testing.T) {
			t.Parallel()

			var endpoint *model.ForwardingEndpointS3
			msg, diags := endpoint.ToMsg()

			assert.False(t, diags.HasError())
			assert.Nil(t, msg)
		})
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
