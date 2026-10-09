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
	httpsEndpointTestdata embed.FS

	httpsEndpointResourceName string = resourceName(observability.NewForwardingEndpointHTTPSResource())
)

func init() {
	resource.AddTestSweepers(httpsEndpointResourceName, &resource.Sweeper{
		Name:         httpsEndpointResourceName,
		Dependencies: []string{pipelineResourceName},
		F: func(r string) error {
			testutil.SetEnvDefaults()
			return typedEndpointSweeper(typesv1beta1.ForwardingEndpointSpec_Https_case.String())(r)
		},
	})
}

func renderHTTPSEndpointResource(resourceName string, m *model.ForwardingEndpointHTTPS) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{httpsEndpointResourceName, resourceName})
	resourceBody := resource.Body()

	setCommonEndpointAttributes(resourceBody, m.ForwardingEndpointCore)
	resourceBody.SetAttributeValue("endpoint", cty.StringVal(m.Endpoint.ValueString()))

	if !m.Compression.IsNull() {
		resourceBody.SetAttributeValue("compression", cty.StringVal(m.Compression.ValueString()))
	}

	if !m.CredentialsVersion.IsNull() {
		resourceBody.SetAttributeValue("credentials_version", cty.NumberIntVal(m.CredentialsVersion.ValueInt64()))
	}

	if m.TLS != nil {
		resourceBody.SetAttributeValue("tls", cty.ObjectVal(map[string]cty.Value{
			"certificate_authority_data": cty.StringVal(m.TLS.CertificateAuthorityData.ValueString()),
		}))
	}

	if m.Credentials != nil {
		credsObj := make(map[string]cty.Value)
		if m.Credentials.BasicAuth != nil {
			credsObj["basic_auth"] = cty.ObjectVal(map[string]cty.Value{
				"username": cty.StringVal(m.Credentials.BasicAuth.Username.ValueString()),
				"password": cty.StringVal(m.Credentials.BasicAuth.Password.ValueString()),
			})
		}
		if m.Credentials.AuthHeaders != nil {
			headersMap := make(map[string]cty.Value)
			for key, value := range m.Credentials.AuthHeaders.Headers {
				headersMap[key] = cty.StringVal(value)
			}
			credsObj["auth_headers"] = cty.ObjectVal(map[string]cty.Value{
				"headers": cty.MapVal(headersMap),
			})
		}
		resourceBody.SetAttributeValue("credentials", cty.ObjectVal(credsObj))
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(fmt.Sprintf("failed to write HCL: %v", err))
	}
	return buf.String()
}

type httpsEndpointTestStep struct {
	TestName         string
	ResourceName     string
	Model            *model.ForwardingEndpointHTTPS
	PreConfig        func()
	ConfigPlanChecks resource.ConfigPlanChecks
	ExtraChecks      []statecheck.StateCheck
	Options          []testStepOption
}

func createHTTPSEndpointTestStep(t *testing.T, opts httpsEndpointTestStep) resource.TestStep {
	t.Helper()

	fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, opts.ResourceName)

	stateChecks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("slug"), knownvalue.StringExact(opts.Model.Slug.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("display_name"), knownvalue.StringExact(opts.Model.DisplayName.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("endpoint"), knownvalue.StringExact(opts.Model.Endpoint.ValueString())),

		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("updated_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String())),
	}

	// compression round-trips exactly: null stays null (never configured), a set
	// value reads back as itself. This is the assertion that would catch an
	// Optional+Computed regression.
	if opts.Model.Compression.IsNull() {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("compression"), knownvalue.Null()))
	} else {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("compression"), knownvalue.StringExact(opts.Model.Compression.ValueString())))
	}

	// Credentials are write-only: the server never returns them, so this
	// computed pair is the only observable record that any were set.
	stateChecks = append(stateChecks,
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials_configured"), knownvalue.Bool(opts.Model.Credentials != nil)))
	if opts.Model.Credentials != nil {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials_updated_at"), knownvalue.NotNull()))
	}

	if opts.Model.TLS != nil {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tls"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tls").AtMapKey("certificate_authority_data"), knownvalue.StringExact(opts.Model.TLS.CertificateAuthorityData.ValueString())),
		)
	}

	stateChecks = append(stateChecks, opts.ExtraChecks...)

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning %s test: %s", httpsEndpointResourceName, opts.TestName)
			if opts.PreConfig != nil {
				opts.PreConfig()
			}
		},
		Config:            renderHTTPSEndpointResource(opts.ResourceName, opts.Model),
		ConfigPlanChecks:  opts.ConfigPlanChecks,
		ConfigStateChecks: stateChecks,
	}

	for _, option := range opts.Options {
		option(&testStep)
	}

	return testStep
}

// TestForwardingEndpointHTTPS is the umbrella for every HTTPS forwarding
// endpoint test, so `go test -run TestForwardingEndpointHTTPS
// ./coreweave/observability` reaches all of them. Unit subtests run anywhere;
// the rest need TF_ACC=1 plus COREWEAVE_API_ENDPOINT and COREWEAVE_API_TOKEN.
//
// HTTPS endpoints are database-only server-side — no connector is provisioned
// and CheckEndpoint performs no connectivity test — so these are safe to run
// against any org, including one with a production cluster footprint.
//
// Only this function and the pure-unit subtests call t.Parallel().
// resource.ParallelTest calls it internally, so a subtest that calls both
// panics with "t.Parallel called multiple times".
func TestForwardingEndpointHTTPS(t *testing.T) {
	t.Parallel()

	t.Run("schema", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		schemaResponse := &fwresource.SchemaResponse{}

		observability.NewForwardingEndpointHTTPSResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResponse)
		assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

		diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
		assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
	})

	t.Run("render", func(t *testing.T) {
		t.Parallel()

		t.Run("basic endpoint", func(t *testing.T) {
			t.Parallel()

			endpoint := &model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue("test-https-endpoint"),
					DisplayName: types.StringValue("Test HTTPS Endpoint"),
				},
				Endpoint: types.StringValue("https://example.com/telemetry"),
			}

			expectedHCL, err := httpsEndpointTestdata.ReadFile("testdata/hcl_endpoint_https_basic.tf")
			require.NoError(t, err)

			hcl := renderHTTPSEndpointResource("test", endpoint)
			assert.Equal(t, string(expectedHCL), hcl)
		})

		t.Run("with TLS", func(t *testing.T) {
			t.Parallel()

			endpoint := &model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue("test-https-tls"),
					DisplayName: types.StringValue("Test HTTPS with TLS"),
				},
				Endpoint: types.StringValue("https://example.com/telemetry"),
				TLS: &model.TLSConfig{
					CertificateAuthorityData: types.StringValue("LS0tLS1CRUdJTi=="),
				},
			}

			expectedHCL, err := httpsEndpointTestdata.ReadFile("testdata/hcl_endpoint_https_with_tls.tf")
			require.NoError(t, err)

			hcl := renderHTTPSEndpointResource("test_tls", endpoint)
			assert.Equal(t, string(expectedHCL), hcl)
		})
	})

	// invalid_config asserts the plan-time validators reject what buf.validate
	// would otherwise only reject at apply. Every step here fails before any
	// resource is created, so nothing needs sweeping.
	t.Run("invalid_config", func(t *testing.T) {
		t.Parallel()

		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_https_invalid_%d", randomInt)

		valid := func() *model.ForwardingEndpointHTTPS {
			return &model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue(slugify("https-invalid", randomInt)),
					DisplayName: types.StringValue("Invalid Config Probe"),
				},
				Endpoint: types.StringValue(testHTTPSEndpointURL),
			}
		}

		cases := []struct {
			name        string
			model       *model.ForwardingEndpointHTTPS
			expectError *regexp.Regexp
		}{
			{
				name: "slug too short",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Slug = types.StringValue("ab")
				}),
				expectError: regexp.MustCompile(`(?s)slug.*at least 3`),
			},
			{
				name: "slug has an illegal leading hyphen",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Slug = types.StringValue("-leading-hyphen")
				}),
				expectError: regexp.MustCompile(`(?s)slug.*alphanumeric`),
			},
			{
				name: "display_name is empty",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.DisplayName = types.StringValue("")
				}),
				expectError: regexp.MustCompile(`(?s)display_name.*at least 1`),
			},
			{
				name: "endpoint is plain http",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Endpoint = types.StringValue("http://insecure.example.com/ingest")
				}),
				expectError: regexp.MustCompile(`(?s)endpoint.*https://`),
			},
			{
				name: "compression is not a member of the enum",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Compression = types.StringValue("COMPRESSION_TYPE_BROTLI")
				}),
				expectError: regexp.MustCompile(`(?s)compression.*COMPRESSION_TYPE_GZIP`),
			},
			{
				// COMPRESSION_TYPE_UNSPECIFIED is the enum's zero value, which means
				// "never configured". Publishing it as a legal configurable value
				// would be permanent, so the OneOf list drops it.
				name: "compression is the UNSPECIFIED zero value",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Compression = types.StringValue("COMPRESSION_TYPE_UNSPECIFIED")
				}),
				expectError: regexp.MustCompile(`(?s)compression.*COMPRESSION_TYPE_NONE`),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resource.ParallelTest(t, resource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
					PreCheck:                 func() { testutil.SetEnvDefaults() },
					Steps: []resource.TestStep{
						{
							Config:      renderHTTPSEndpointResource(resourceName, tc.model),
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
		resourceName := fmt.Sprintf("test_acc_https_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-fe", randomInt)),
				DisplayName: types.StringValue("Test HTTPS Endpoint"),
			},
			Endpoint: types.StringValue(testHTTPSEndpointURL),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "initial HTTPS forwarding endpoint",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "no-op (noop)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "update display name (update)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.DisplayName = types.StringValue("Updated HTTPS Endpoint")
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
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
					ResourceName:      fullResourceName,
					ImportState:       true,
					ImportStateId:     baseModel.Slug.ValueString(),
					ImportStateVerify: true,
					// Write-only credential inputs and the user-owned rotation counter
					// are not recoverable from the API, so import cannot reproduce them.
					ImportStateVerifyIgnore: []string{"credentials", "credentials_version"},
				},
				// slug is the Ref: changing it is a different object, so the plan must
				// be a replace rather than an in-place update the API cannot honor.
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "update slug (requires replacement, plan only)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.Slug = types.StringValue(slugify("https-fe2", randomInt))
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

	t.Run("tls", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_https_tls_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-tls", randomInt)),
				DisplayName: types.StringValue("Test HTTPS Endpoint with TLS"),
			},
			Endpoint: types.StringValue(testHTTPSEndpointURL),
			TLS: &model.TLSConfig{
				CertificateAuthorityData: types.StringValue(testCACertificateData),
			},
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "initial HTTPS endpoint with TLS",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "no-op (noop)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "remove TLS (update)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.TLS = nil
					}),
					ExtraChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tls"), knownvalue.Null()),
					},
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "add TLS back (update)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
			},
		})
	})

	// compression is Optional and never Optional+Computed, which is what makes
	// removal clear the value instead of silently preserving it. The third step
	// is the one that proves it.
	t.Run("compression", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_https_compression_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-compress", randomInt)),
				DisplayName: types.StringValue("Test HTTPS Endpoint Compression"),
			},
			Endpoint:    types.StringValue(testHTTPSEndpointURL),
			Compression: types.StringValue(typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_GZIP.String()),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "create with gzip compression",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "change compression to explicit NONE",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.Compression = types.StringValue(typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_NONE.String())
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					// Removing the attribute must read back as null (UNSPECIFIED, "never
					// configured"), not as the previous value. Optional+Computed would
					// fail here.
					TestName:     "remove compression clears it",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.Compression = types.StringNull()
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "no-op after removal",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.Compression = types.StringNull()
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
				}),
			},
		})
	})

	// credentials walks the whole PRESERVE / REPLACE / CLEAR table. Every step
	// here would have failed before Update learned to read req.Config: the
	// plan's write-only values are null, so the request carried
	// {username: "", password: ""} and the API's min_len rule rejected it.
	t.Run("credentials", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_https_creds_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:               types.StringValue(slugify("https-creds", randomInt)),
				DisplayName:        types.StringValue("Test HTTPS Endpoint Credentials"),
				CredentialsVersion: types.Int64Value(1),
			},
			Endpoint: types.StringValue(testHTTPSEndpointURL),
			Credentials: &model.HTTPSCredentials{
				BasicAuth: &model.BasicAuthCredentials{
					Username: types.StringValue("telemetry"),
					Password: types.StringValue("first-password"),
				},
			},
		}

		// credentials_updated_at is the only signal a rotation happened, so each
		// step captures it and the next asserts whether it moved.
		var afterCreate, afterNoopUpdate, afterRotation string

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "create with basic auth credentials",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
					ExtraChecks: []statecheck.StateCheck{
						// Write-only values must never reach state.
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials").AtMapKey("basic_auth").AtMapKey("password"), knownvalue.Null()),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials").AtMapKey("basic_auth").AtMapKey("username"), knownvalue.Null()),
						extractCredentialsUpdatedAt(fullResourceName, &afterCreate),
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					// The secret changes but the version does not. This is the documented
					// cost of the version counter: PRESERVE is sent, the stored
					// credentials are untouched, and credentials_updated_at must not move.
					TestName:     "changed secret without a version bump is a no-op (PRESERVE)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.DisplayName = types.StringValue("Renamed, credentials untouched")
						m.Credentials = &model.HTTPSCredentials{
							BasicAuth: &model.BasicAuthCredentials{
								Username: types.StringValue("telemetry"),
								Password: types.StringValue("ignored-without-a-version-bump"),
							},
						}
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
					ExtraChecks: []statecheck.StateCheck{
						extractCredentialsUpdatedAt(fullResourceName, &afterNoopUpdate),
						expectUnchanged(credentialsUpdatedAtAttribute, &afterCreate, &afterNoopUpdate),
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "bumping credentials_version rotates the secret (REPLACE)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.CredentialsVersion = types.Int64Value(2)
						m.Credentials = &model.HTTPSCredentials{
							BasicAuth: &model.BasicAuthCredentials{
								Username: types.StringValue("telemetry"),
								Password: types.StringValue("second-password"),
							},
						}
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
					ExtraChecks: []statecheck.StateCheck{
						extractCredentialsUpdatedAt(fullResourceName, &afterRotation),
						expectChanged(credentialsUpdatedAtAttribute, &afterNoopUpdate, &afterRotation),
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					// Switching auth method is also a rotation, so it needs a bump too.
					TestName:     "switch from basic auth to headers",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.CredentialsVersion = types.Int64Value(3)
						m.Credentials = &model.HTTPSCredentials{
							AuthHeaders: &model.AuthHeadersCredentials{
								Headers: map[string]string{"X-API-Key": "header-secret"},
							},
						}
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					// Removing the block sends CLEAR, which is legal for HTTPS (unlike
					// S3). credentials_configured flips to false.
					TestName:     "removing the credentials block clears them (CLEAR)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.Credentials = nil
						m.CredentialsVersion = types.Int64Null()
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "adding credentials back on an endpoint that has none (REPLACE)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
			},
		})
	})

	t.Run("invalid_credentials", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_https_badcreds_%d", randomInt)

		valid := func() *model.ForwardingEndpointHTTPS {
			return &model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue(slugify("https-badcreds", randomInt)),
					DisplayName: types.StringValue("Bad Credentials Probe"),
				},
				Endpoint: types.StringValue(testHTTPSEndpointURL),
			}
		}

		cases := []struct {
			name        string
			model       *model.ForwardingEndpointHTTPS
			expectError *regexp.Regexp
		}{
			{
				// ConfigValidators catches this at the attribute; protovalidate would
				// only see it once the whole request was assembled.
				name: "two auth methods at once",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Credentials = &model.HTTPSCredentials{
						BasicAuth: &model.BasicAuthCredentials{
							Username: types.StringValue("telemetry"),
							Password: types.StringValue("secret"),
						},
						AuthHeaders: &model.AuthHeadersCredentials{
							Headers: map[string]string{"X-API-Key": "secret"},
						},
					}
				}),
				expectError: regexp.MustCompile(`(?s)(?i)(cannot be specified when|conflict)`),
			},
			{
				// Content-Type is set by the connector. This rule lives only in the
				// proto's CEL; before ValidateConfig validated the request rather than
				// the bare endpoint message, it could not fire until apply.
				name: "reserved header name",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Credentials = &model.HTTPSCredentials{
						AuthHeaders: &model.AuthHeadersCredentials{
							Headers: map[string]string{"Content-Type": "application/json"},
						},
					}
				}),
				expectError: regexp.MustCompile(`(?s)(?i)cannot be overridden`),
			},
			{
				name: "header names differing only by case",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Credentials = &model.HTTPSCredentials{
						AuthHeaders: &model.AuthHeadersCredentials{
							Headers: map[string]string{"X-Api-Key": "a", "x-api-key": "b"},
						},
					}
				}),
				expectError: regexp.MustCompile(`(?s)(?i)unique ignoring case`),
			},
			{
				name: "empty credentials block",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Credentials = &model.HTTPSCredentials{}
				}),
				expectError: regexp.MustCompile(`(?s)(?i)(exactly one|must be set)`),
			},
			{
				name: "negative credentials_version",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.CredentialsVersion = types.Int64Value(-1)
				}),
				expectError: regexp.MustCompile(`(?s)credentials_version`),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resource.ParallelTest(t, resource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
					PreCheck:                 func() { testutil.SetEnvDefaults() },
					Steps: []resource.TestStep{
						{
							Config:      renderHTTPSEndpointResource(resourceName, tc.model),
							PlanOnly:    true,
							ExpectError: tc.expectError,
						},
					},
				})
			})
		}
	})

	// disappears covers Read's NotFound path: delete the endpoint behind
	// Terraform's back, then plan. Before RemoveResource was added, refresh
	// dereferenced a nil endpoint and the provider panicked.
	t.Run("disappears", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_https_disappears_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-gone", randomInt)),
				DisplayName: types.StringValue("Test HTTPS Endpoint Disappears"),
			},
			Endpoint: types.StringValue(testHTTPSEndpointURL),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "create the endpoint to be deleted out of band",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
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
