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

// Every subtest here drives a real API through resource.ParallelTest, so the
// whole function is skipped without TF_ACC. The offline schema, render and
// round_trip coverage lives in resource_forwarding_endpoint_https_test.go.

func TestForwardingEndpointHTTPSAcceptance(t *testing.T) {
	t.Parallel()

	// invalid_config asserts the plan-time validators reject what buf.validate
	// would otherwise only reject at apply. Every step here fails before any
	// resource is created, so nothing needs sweeping.
	t.Run("invalid_config", func(t *testing.T) {
		t.Parallel()

		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_https_invalid_%s", token)

		valid := func() *model.ForwardingEndpointHTTPS {
			return &model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue(slugify("https-invalid", token)),
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
					PreCheck:                 func() { setAccEnvDefaults() },
					CheckDestroy:             checkDestroyed(t),
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
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_https_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-fe", token)),
				DisplayName: types.StringValue("Test HTTPS Endpoint"),
			},
			Endpoint: types.StringValue(testHTTPSEndpointURL),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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
				// Updates the endpoint URL rather than display_name. UpdateEndpoint
				// rejects any changed display_name as immutable, though the proto
				// marks it REQUIRED and reserves "cannot be changed after creation"
				// for the config oneof. Restore display_name here once the server
				// stops rejecting it; it is the more natural thing to assert.
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "update endpoint url (update)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.Endpoint = types.StringValue("https://192.0.2.1/ingest-v2")
					}),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "revert endpoint url (update)",
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
						m.Slug = types.StringValue(slugify("https-fe2", token))
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
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_https_tls_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-tls", token)),
				DisplayName: types.StringValue("Test HTTPS Endpoint with TLS"),
			},
			Endpoint: types.StringValue(testHTTPSEndpointURL),
			TLS: &model.TLSConfig{
				CertificateAuthorityData: types.StringValue(testCACertificateData),
			},
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_https_compression_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-compress", token)),
				DisplayName: types.StringValue("Test HTTPS Endpoint Compression"),
			},
			Endpoint:    types.StringValue(testHTTPSEndpointURL),
			Compression: types.StringValue(typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_GZIP.String()),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_https_creds_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:               types.StringValue(slugify("https-creds", token)),
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
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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
					// Carries a spec change alongside the secret so the step proves a
					// spec update does not rewrite credentials. That was display_name
					// until the server began rejecting it as immutable; compression is
					// equivalent here and, unlike the endpoint URL, is not an input to
					// the credential AAD.
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.Compression = types.StringValue("COMPRESSION_TYPE_GZIP")
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
					// The credential type is fixed for the life of an endpoint's
					// credentials: UpdateEndpoint refuses to move between basic_auth
					// and header_auth, naming both in the violation. The provider
					// cannot catch this at plan time — credentials are write-only and
					// state records only credentials_configured, never which type is
					// stored — so the server's refusal is the only possible signal.
					// Switching method means clearing first, as the next two steps do.
					TestName:     "switching auth method in place is refused",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.CredentialsVersion = types.Int64Value(3)
						m.Credentials = &model.HTTPSCredentials{
							AuthHeaders: &model.AuthHeadersCredentials{
								Headers: map[string]string{"X-API-Key": "header-secret"},
							},
						}
					}),
					ExpectError: regexp.MustCompile(`cannot change credential_type`),
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
					// Adds back with the *other* auth method, which the clear above
					// makes legal: the stored credential type is gone, so there is
					// nothing for the server to refuse a change against. This is the
					// supported route to switching method, and the pair of steps is
					// what proves the earlier refusal is about changing in place
					// rather than about header auth being unusable.
					TestName:     "adding credentials back with the other method (REPLACE)",
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
			},
		})
	})

	t.Run("header_credentials", func(t *testing.T) {
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_https_hdrcreds_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		// The credentials subtest above reaches header auth only through
		// basic_auth -> CLEAR -> REPLACE, and only ever with one header. This
		// one creates with header auth outright and with several. One value
		// carries colons, a semicolon and internal spaces — legal where ',' is
		// not — so HCL rendering or the write-only path mangling a realistic
		// value shows up here rather than at a customer's endpoint.
		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:               types.StringValue(slugify("https-hdrcreds", token)),
				DisplayName:        types.StringValue("Test HTTPS Endpoint Header Credentials"),
				CredentialsVersion: types.Int64Value(1),
			},
			Endpoint: types.StringValue(testHTTPSEndpointURL),
			Credentials: &model.HTTPSCredentials{
				AuthHeaders: &model.AuthHeadersCredentials{
					Headers: map[string]string{
						"X-API-Key":           "first-key",
						"X-Tenant-Id":         "acc-tenant",
						"Authorization":       "Bearer first-token",
						"X-Forwarded-Context": "tenant:acc-tenant; region:us-lab-01a",
					},
				},
			},
		}

		var afterCreate, afterRotation string

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
			Steps: []resource.TestStep{
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					TestName:     "create with header auth credentials",
					ResourceName: resourceName,
					Model:        baseModel,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
					ExtraChecks: []statecheck.StateCheck{
						// headers is WriteOnly, so the whole map stays out of state.
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials").AtMapKey("auth_headers").AtMapKey("headers"), knownvalue.Null()),
						extractCredentialsUpdatedAt(fullResourceName, &afterCreate),
					},
				}),
				createHTTPSEndpointTestStep(t, httpsEndpointTestStep{
					// Rotation is only proven for basic_auth above. The version
					// counter is credential-type agnostic, so this asserts the
					// header path reaches the same REPLACE.
					TestName:     "bumping credentials_version rotates the headers (REPLACE)",
					ResourceName: resourceName,
					Model: with(baseModel, func(m *model.ForwardingEndpointHTTPS) {
						m.CredentialsVersion = types.Int64Value(2)
						m.Credentials = &model.HTTPSCredentials{
							AuthHeaders: &model.AuthHeadersCredentials{
								Headers: map[string]string{
									"X-API-Key":           "second-key",
									"X-Tenant-Id":         "acc-tenant",
									"Authorization":       "Bearer second-token",
									"X-Forwarded-Context": "tenant:acc-tenant; region:us-east-04a",
								},
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
						expectChanged(credentialsUpdatedAtAttribute, &afterCreate, &afterRotation),
					},
				}),
			},
		})
	})

	t.Run("invalid_credentials", func(t *testing.T) {
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_https_badcreds_%s", token)

		valid := func() *model.ForwardingEndpointHTTPS {
			return &model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue(slugify("https-badcreds", token)),
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
				expectError: regexp.MustCompile(`(?s)(?i)cannot\s+be\s+configured\s+together`),
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
				// \s+ rather than literal spaces: terraform wraps diagnostic text, so
				// a pattern spanning the wrap point never matches.
				expectError: regexp.MustCompile(`(?s)(?i)unique\s+ignoring\s+case`),
			},
			{
				name: "empty credentials block",
				model: with(valid(), func(m *model.ForwardingEndpointHTTPS) {
					m.Credentials = &model.HTTPSCredentials{}
				}),
				// Matches the diagnostic summary plus one discriminating word, not
				// the detail prose: rewording the detail should not fail a test.
				// The diagnostic summary only. The detail names the auth methods, and
				// a third one would make any list of them wrong.
				expectError: regexp.MustCompile(`(?i)credentials`),
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
					PreCheck:                 func() { setAccEnvDefaults() },
					CheckDestroy:             checkDestroyed(t),
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
		token := testToken(t)
		resourceName := fmt.Sprintf("test_acc_https_disappears_%s", token)
		fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, resourceName)

		baseModel := &model.ForwardingEndpointHTTPS{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue(slugify("https-gone", token)),
				DisplayName: types.StringValue("Test HTTPS Endpoint Disappears"),
			},
			Endpoint: types.StringValue(testHTTPSEndpointURL),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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
