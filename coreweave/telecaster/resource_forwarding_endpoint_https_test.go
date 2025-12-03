package telecaster_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/hcl/v2/hclwrite"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/zclconf/go-cty/cty"
)

func init() {
	resource.AddTestSweepers("coreweave_telecaster_forwarding_endpoint_https", &resource.Sweeper{
		Name:         "coreweave_telecaster_forwarding_endpoint_https",
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

				// Only sweep HTTPS endpoints
				if endpoint.Spec.GetHttps() == nil {
					continue
				}

				log.Printf("sweeping HTTPS forwarding endpoint %s", endpoint.Ref.Slug)
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
					var connectErr *connect.Error
					if errors.As(err, &connectErr) {
						detailStrings := make([]string, len(connectErr.Details()))
						for i, detail := range connectErr.Details() {
							val, valueErr := detail.Value()
							if valueErr != nil {
								detailStrings[i] = fmt.Sprintf("unknown detail type: %s: %s", detail.Type(), valueErr.Error())
							} else {
								detailStrings[i] = fmt.Sprintf("detail %s: %s", detail.Type(), val)
							}
						}
						return fmt.Errorf("failed to delete forwarding endpoint %s: %s: %s", endpoint.Ref.Slug, connectErr.Message(), strings.Join(detailStrings, ", "))
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

type HTTPSEndpointTestModel struct {
	Slug        string
	DisplayName string
	Endpoint    string
	TLS         *TLSConfigTestModel
}

type TLSConfigTestModel struct {
	CertificateAuthorityData string
}

func renderHTTPSEndpointResource(resourceName string, model *HTTPSEndpointTestModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{"coreweave_telecaster_forwarding_endpoint_https", resourceName})
	resourceBody := resource.Body()

	resourceBody.SetAttributeValue("slug", cty.StringVal(model.Slug))
	resourceBody.SetAttributeValue("display_name", cty.StringVal(model.DisplayName))
	resourceBody.SetAttributeValue("endpoint", cty.StringVal(model.Endpoint))

	if model.TLS != nil {
		resourceBody.SetAttributeValue("tls", cty.ObjectVal(map[string]cty.Value{
			"certificate_authority_data": cty.StringVal(model.TLS.CertificateAuthorityData),
		}))
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

	telecaster.NewForwardingEndpointHTTPSResource().Schema(ctx, schemaRequest, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

type httpsEndpointTestStep struct {
	TestName         string
	ResourceName     string
	Model            *HTTPSEndpointTestModel
	ConfigPlanChecks resource.ConfigPlanChecks
	Options          []testStepOption
}

func createHTTPSEndpointTestStep(ctx context.Context, t *testing.T, opts httpsEndpointTestStep) resource.TestStep {
	t.Helper()

	metadataResp := new(fwresource.MetadataResponse)
	telecaster.NewForwardingEndpointHTTPSResource().Metadata(ctx, fwresource.MetadataRequest{ProviderTypeName: "coreweave"}, metadataResp)

	fullResourceName := strings.Join([]string{metadataResp.TypeName, opts.ResourceName}, ".")

	stateChecks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("slug"), knownvalue.StringExact(opts.Model.Slug)),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("display_name"), knownvalue.StringExact(opts.Model.DisplayName)),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("endpoint"), knownvalue.StringExact(opts.Model.Endpoint)),

		// Status fields should be set
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("updated_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state_code"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state"), knownvalue.NotNull()),
	}

	if opts.Model.TLS != nil {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tls"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tls").AtMapKey("certificate_authority_data"),
				knownvalue.StringExact(opts.Model.TLS.CertificateAuthorityData)),
		)
	}

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning coreweave_telecaster_forwarding_endpoint_https test: %s", opts.TestName)
		},
		Config:            renderHTTPSEndpointResource(opts.ResourceName, opts.Model),
		ConfigPlanChecks:  opts.ConfigPlanChecks,
		ConfigStateChecks: stateChecks,
	}

	// Apply any custom options
	for _, option := range opts.Options {
		option(&testStep)
	}

	return testStep
}

func TestHTTPSForwardingEndpointResource(t *testing.T) {
	randomInt := rand.IntN(100)
	resourceName := fmt.Sprintf("test_acc_https_%d", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_endpoint_https.%s", resourceName)
	ctx := t.Context()

	baseModel := &HTTPSEndpointTestModel{
		Slug:        slugify("https-fe", randomInt),
		DisplayName: "Test HTTPS Endpoint",
		Endpoint:    "http://telecaster-console.us-east-03-core-services.int.coreweave.com:9000/",
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			// Create initial HTTPS endpoint
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
				TestName:     "initial HTTPS forwarding endpoint",
				ResourceName: resourceName,
				Model:        baseModel,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
					},
				},
			}),
			// No-op (noop)
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
				TestName:     "no-op (noop)",
				ResourceName: resourceName,
				Model:        baseModel,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
					},
				},
			}),
			// Update display name
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
				TestName:     "update display name (update)",
				ResourceName: resourceName,
				Model: &HTTPSEndpointTestModel{
					Slug:        baseModel.Slug,
					DisplayName: "Updated HTTPS Endpoint",
					Endpoint:    baseModel.Endpoint,
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			// Revert display name
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
				TestName:     "revert display name (update)",
				ResourceName: resourceName,
				Model:        baseModel,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			// Update slug (requires replacement)
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
				TestName:     "update slug (requires replacement)",
				ResourceName: resourceName,
				Model: &HTTPSEndpointTestModel{
					Slug:        slugify("https-fe2", randomInt),
					DisplayName: baseModel.DisplayName,
					Endpoint:    baseModel.Endpoint,
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
					},
				},
			}),
			// Revert slug (plan only)
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
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
}

func TestHTTPSForwardingEndpointResource_WithTLS(t *testing.T) {
	randomInt := rand.IntN(100)
	resourceName := fmt.Sprintf("test_acc_https_tls_%d", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_telecaster_forwarding_endpoint_https.%s", resourceName)
	ctx := t.Context()

	// Sample base64-encoded CA certificate for testing
	testCAData := "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJURENDQWZPZ0F3SUJBZ0lVZmRLdDdHWU9hRDZuL2pvb3A3OEVoT3Y3YkFvd0NnWUlLb1pJemowRUF3SXcKSERFYU1CZ0dBMVVFQXd3UlkyOXlaWGRsWVhabFkyRXRjbTl2ZEMwd0hoY05NalF4TVRFek1EQTBNVEExV2hjTgpNalV4TVRFek1EQTBNVEExV2pBY01Sb3dHQVlEVlFRRERCRmpiM0psZDJWaGRtVmpZUzF5YjI5ME1Ea1ZNQk1HCkJ5cUdTTTQ5QWdFR0NDcUdTTTQ5QXdFSEEwSUFCUElIdUMyQklIdlFyUlV0bjdodFFnY1NGRDlDbEs0U3BLN0sKaEhWaS9RQm9naVREMC9yMWRqRkViYmZHOW9DTzFodHpXWjd4aE1CRUY4NFJ2TlhtdWNlamdZWXdnWU13RGdZRApWUjBQQVFIL0JBUURBZ0VHTUJJR0ExVWRFd0VCL3dRSU1BWUJBZjhDQVFBd0hRWURWUjBPQkJZRUZOckZjS1dJClVOcWdXcWNxWk5FSVRzOVJuZGh4TUI4R0ExVWRJd1FZTUJhQUZOckZjS1dJVU5xZ1dxY3FaTkVJVHM5Um5kaHgKTUJrR0ExVWRFUVFTTUJDQ0RuZGxkR2h2YjJ0ekxuTjJZekFLQmdncWhrak9QUVFEQWdOSEFEQkVBaUJlM3NsYQpTWjc5bmxQeWJlYVY4NXp5VW9VQ1hVWjNvTnhjN1lZc3N0WDFuZ0lnSUhYQ0xEZUZWKzF2Mlk1RzdwN3N0VTRCClA0VTlScHlyVzhMWnhRdWhFYjQ9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K"

	baseModel := &HTTPSEndpointTestModel{
		Slug:        slugify("https-tls", randomInt),
		DisplayName: "Test HTTPS Endpoint with TLS",
		Endpoint:    "https://secure-endpoint.example.com/",
		TLS: &TLSConfigTestModel{
			CertificateAuthorityData: testCAData,
		},
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
				TestName:     "initial HTTPS endpoint with TLS",
				ResourceName: resourceName,
				Model:        baseModel,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
					},
				},
			}),
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
				TestName:     "no-op (noop)",
				ResourceName: resourceName,
				Model:        baseModel,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
					},
				},
			}),
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
				TestName:     "remove TLS (update)",
				ResourceName: resourceName,
				Model: &HTTPSEndpointTestModel{
					Slug:        baseModel.Slug,
					DisplayName: baseModel.DisplayName,
					Endpoint:    baseModel.Endpoint,
					TLS:         nil,
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
			}),
			createHTTPSEndpointTestStep(ctx, t, httpsEndpointTestStep{
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
}
