package observability_test

import (
	"bytes"
	"embed"
	"fmt"
	"math/rand/v2"
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
		Dependencies: []string{resourceName(observability.NewForwardingPipelineResource())},
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
		if m.Credentials.BearerToken != nil {
			credsObj["bearer_token"] = cty.ObjectVal(map[string]cty.Value{
				"token": cty.StringVal(m.Credentials.BearerToken.Token.ValueString()),
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

func TestHTTPSForwardingEndpointSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaRequest := fwresource.SchemaRequest{}
	schemaResponse := &fwresource.SchemaResponse{}

	observability.NewForwardingEndpointHTTPSResource().Schema(ctx, schemaRequest, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

type httpsEndpointTestStep struct {
	TestName         string
	ResourceName     string
	Model            *model.ForwardingEndpointHTTPS
	ConfigPlanChecks resource.ConfigPlanChecks
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
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state_code"), knownvalue.Int32Exact(int32(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED))),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String())),
	}

	if opts.Model.TLS != nil {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tls"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tls").AtMapKey("certificate_authority_data"), knownvalue.StringExact(opts.Model.TLS.CertificateAuthorityData.ValueString())),
		)
	}

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning %s test: %s", httpsEndpointResourceName, opts.TestName)
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

func TestHTTPSForwardingEndpointResource(t *testing.T) {
	t.Run("core lifecycle", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_https_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-fe", randomInt)),
				DisplayName: types.StringValue("Test HTTPS Endpoint"),
			},
			Endpoint: types.StringValue("http://telecaster-console.us-east-03-core-services.int.coreweave.com:9000/"),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
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
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "update slug (requires replacement)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.Slug = types.StringValue(slugify("https-fe2", randomInt))
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
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

	t.Run("with TLS", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_https_tls_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		testCAData := "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJURENDQWZPZ0F3SUJBZ0lVZmRLdDdHWU9hRDZuL2pvb3A3OEVoT3Y3YkFvd0NnWUlLb1pJemowRUF3SXcKSERFYU1CZ0dBMVVFQXd3UlkyOXlaWGRsWVhabFkyRXRjbTl2ZEMwd0hoY05NalF4TVRFek1EQTBNVEExV2hjTgpNalV4TVRFek1EQTBNVEExV2pBY01Sb3dHQVlEVlFRRERCRmpiM0psZDJWaGRtVmpZUzF5YjI5ME1Ea1ZNQk1HCkJ5cUdTTTQ5QWdFR0NDcUdTTTQ5QXdFSEEwSUFCUElIdUMyQklIdlFyUlV0bjdodFFnY1NGRDlDbEs0U3BLN0sKaEhWaS9RQm9naVREMC9yMWRqRkViYmZHOW9DTzFodHpXWjd4aE1CRUY4NFJ2TlhtdWNlamdZWXdnWU13RGdZRApWUjBQQVFIL0JBUURBZ0VHTUJJR0ExVWRFd0VCL3dRSU1BWUJBZjhDQVFBd0hRWURWUjBPQkJZRUZOckZjS1dJClVOcWdXcWNxWk5FSVRzOVJuZGh4TUI4R0ExVWRJd1FZTUJhQUZOckZjS1dJVU5xZ1dxY3FaTkVJVHM5Um5kaHgKTUJrR0ExVWRFUVFTTUJDQ0RuZGxkR2h2YjJ0ekxuTjJZekFLQmdncWhrak9QUVFEQWdOSEFEQkVBaUJlM3NsYQpTWjc5bmxQeWJlYVY4NXp5VW9VQ1hVWjNvTnhjN1lZc3N0WDFuZ0lnSUhYQ0xEZUZWKzF2Mlk1RzdwN3N0VTRCClA0VTlScHlyVzhMWnhRdWhFYjQ9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K"

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-tls", randomInt)),
				DisplayName: types.StringValue("Test HTTPS Endpoint with TLS"),
			},
			Endpoint: types.StringValue("https://secure-endpoint.example.com/"),
			TLS: &model.TLSConfig{
				CertificateAuthorityData: types.StringValue(testCAData),
			},
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
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

	t.Run("with credentials", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_https_credentials_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-credentials", randomInt)),
				DisplayName: types.StringValue("Test HTTPS Endpoint with Credentials"),
			},
			Endpoint: types.StringValue("https://secure-endpoint.example.com/"),
			Credentials: &model.HTTPSCredentials{
				BasicAuth: &model.BasicAuthCredentials{
					Username: types.StringValue("testuser"),
					Password: types.StringValue("testpassword"),
				},
			},
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
			Steps: []resource.TestStep{
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "initial HTTPS endpoint with credentials",
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

// TestHTTPSForwardingEndpointResource_RenderFunction validates HCL rendering against testdata
func TestHTTPSForwardingEndpointResource_RenderFunction(t *testing.T) {
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
}
