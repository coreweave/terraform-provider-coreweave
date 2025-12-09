package telecaster_test

import (
	"bytes"
	"embed"
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
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

var (
	//go:embed testdata
	prometheusEndpointTestdata embed.FS

	prometheusEndpointResourceName string = resourceName(telecaster.NewForwardingEndpointPrometheusResource())
)

func init() {
	resource.AddTestSweepers(prometheusEndpointResourceName, &resource.Sweeper{
		Name:         prometheusEndpointResourceName,
		Dependencies: []string{pipelineResourceName},
		F: func(r string) error {
			testutil.SetEnvDefaults()
			return typedEndpointSweeper(typesv1beta1.ForwardingEndpointSpec_Prometheus_case.String())(r)
		},
	})
}

func renderPrometheusEndpointResource(resourceName string, m *model.ForwardingEndpointPrometheusModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{prometheusEndpointResourceName, resourceName})
	resourceBody := resource.Body()

	setCommonEndpointAttributes(resourceBody, m.ForwardingEndpointModelCore)
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

func TestPrometheusForwardingEndpointSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaRequest := fwresource.SchemaRequest{}
	schemaResponse := &fwresource.SchemaResponse{}

	telecaster.NewForwardingEndpointPrometheusResource().Schema(ctx, schemaRequest, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

type prometheusEndpointTestStep struct {
	TestName         string
	ResourceName     string
	Model            *model.ForwardingEndpointPrometheusModel
	ConfigPlanChecks resource.ConfigPlanChecks
	Options          []testStepOption
}

func createPrometheusEndpointTestStep(t *testing.T, opts prometheusEndpointTestStep) resource.TestStep {
	t.Helper()

	fullResourceName := fmt.Sprintf("%s.%s", prometheusEndpointResourceName, opts.ResourceName)

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
			t.Logf("Beginning %s test: %s", prometheusEndpointResourceName, opts.TestName)
		},
		Config:            renderPrometheusEndpointResource(opts.ResourceName, opts.Model),
		ConfigPlanChecks:  opts.ConfigPlanChecks,
		ConfigStateChecks: stateChecks,
	}

	for _, option := range opts.Options {
		option(&testStep)
	}

	return testStep
}

func TestPrometheusForwardingEndpointResource(t *testing.T) {
	t.Run("core lifecycle", func(t *testing.T) {
		randomInt := rand.IntN(100)
		resourceName := fmt.Sprintf("test_acc_prometheus_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", prometheusEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointPrometheusModel{
			ForwardingEndpointModelCore: model.ForwardingEndpointModelCore{
				Slug:        types.StringValue(slugify("prom-fe", randomInt)),
				DisplayName: types.StringValue("Test Prometheus Endpoint"),
			},
			Endpoint: types.StringValue("http://prometheus.us-east-03-core-services.int.coreweave.com:9090/api/v1/write"),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
			Steps: []resource.TestStep{
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
					TestName:     "initial Prometheus forwarding endpoint",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
					TestName:     "no-op (noop)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
				}),
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
					TestName:     "update display name (update)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointPrometheusModel) {
						m.DisplayName = types.StringValue("Updated Prometheus Endpoint")
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
					TestName:     "revert display name (update)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
					TestName:     "update slug (requires replacement)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointPrometheusModel) {
						m.Slug = types.StringValue(slugify("prometheus-fe2", randomInt))
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
					},
				}),
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
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
		resourceName := fmt.Sprintf("test_acc_prometheus_tls_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", prometheusEndpointResourceName, resourceName)

		testCAData := "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJURENDQWZPZ0F3SUJBZ0lVZmRLdDdHWU9hRDZuL2pvb3A3OEVoT3Y3YkFvd0NnWUlLb1pJemowRUF3SXcKSERFYU1CZ0dBMVVFQXd3UlkyOXlaWGRsWVhabFkyRXRjbTl2ZEMwd0hoY05NalF4TVRFek1EQTBNVEExV2hjTgpNalV4TVRFek1EQTBNVEExV2pBY01Sb3dHQVlEVlFRRERCRmpiM0psZDJWaGRtVmpZUzF5YjI5ME1Ea1ZNQk1HCkJ5cUdTTTQ5QWdFR0NDcUdTTTQ5QXdFSEEwSUFCUElIdUMyQklIdlFyUlV0bjdodFFnY1NGRDlDbEs0U3BLN0sKaEhWaS9RQm9naVREMC9yMWRqRkViYmZHOW9DTzFodHpXWjd4aE1CRUY4NFJ2TlhtdWNlamdZWXdnWU13RGdZRApWUjBQQVFIL0JBUURBZ0VHTUJJR0ExVWRFd0VCL3dRSU1BWUJBZjhDQVFBd0hRWURWUjBPQkJZRUZOckZjS1dJClVOcWdXcWNxWk5FSVRzOVJuZGh4TUI4R0ExVWRJd1FZTUJhQUZOckZjS1dJVU5xZ1dxY3FaTkVJVHM5Um5kaHgKTUJrR0ExVWRFUVFTTUJDQ0RuZGxkR2h2YjJ0ekxuTjJZekFLQmdncWhrak9QUVFEQWdOSEFEQkVBaUJlM3NsYQpTWjc5bmxQeWJlYVY4NXp5VW9VQ1hVWjNvTnhjN1lZc3N0WDFuZ0lnSUhYQ0xEZUZWKzF2Mlk1RzdwN3N0VTRCClA0VTlScHlyVzhMWnhRdWhFYjQ9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K"

		baseModel := &model.ForwardingEndpointPrometheusModel{
			ForwardingEndpointModelCore: model.ForwardingEndpointModelCore{
				Slug:        types.StringValue(slugify("prom-fe-tls", randomInt)),
				DisplayName: types.StringValue("Test Prometheus Endpoint with TLS"),
			},
			Endpoint: types.StringValue("https://secure-prometheus.example.com/api/v1/write"),
			TLS: &model.TLSConfigModel{
				CertificateAuthorityData: types.StringValue(testCAData),
			},
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
			Steps: []resource.TestStep{
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
					TestName:     "initial Prometheus endpoint with TLS",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				}),
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
					TestName:     "no-op (noop)",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
				}),
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
					TestName:     "remove TLS (update)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointPrometheusModel) {
						m.TLS = nil
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
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
		resourceName := fmt.Sprintf("test_acc_prometheus_credentials_%d", randomInt)
		fullResourceName := fmt.Sprintf("%s.%s", prometheusEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointPrometheusModel{
			ForwardingEndpointModelCore: model.ForwardingEndpointModelCore{
				Slug:        types.StringValue(slugify("prom-fe-creds", randomInt)),
				DisplayName: types.StringValue("Test Prometheus Endpoint with Credentials"),
			},
			Endpoint: types.StringValue("https://secure-prometheus.example.com/api/v1/write"),
			Credentials: &model.PrometheusCredentialsModel{
				BasicAuth: &model.BasicAuthCredentialsModel{
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
				createPrometheusEndpointTestStep(t, prometheusEndpointTestStep{
					TestName:     "initial Prometheus endpoint with credentials",
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

// TestPrometheusForwardingEndpointResource_RenderFunction validates HCL rendering against testdata
func TestPrometheusForwardingEndpointResource_RenderFunction(t *testing.T) {
	t.Parallel()

	endpoint := &model.ForwardingEndpointPrometheusModel{
		ForwardingEndpointModelCore: model.ForwardingEndpointModelCore{
			Slug:        types.StringValue("test-prometheus-endpoint"),
			DisplayName: types.StringValue("Test Prometheus Endpoint"),
		},
		Endpoint: types.StringValue("https://prometheus.example.com/api/v1/write"),
	}

	expectedHCL, err := prometheusEndpointTestdata.ReadFile("testdata/hcl_endpoint_prometheus_basic.tf")
	require.NoError(t, err)

	hcl := renderPrometheusEndpointResource("test", endpoint)
	assert.Equal(t, string(expectedHCL), hcl)
}
