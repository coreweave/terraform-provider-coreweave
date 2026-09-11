package provider_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A structurally valid JWT; only its shape is checked before the exchange.
const testWorkloadIdentityJWT = "e30.e30.c2lnbmF0dXJl"

// clearProviderEnv neutralizes every environment variable BuildClient reads, so
// that an ambient value (routine in this repo when running acceptance tests)
// cannot change which code path a unit test exercises. BuildClient treats an
// empty value the same as an unset one and falls back to its default.
func clearProviderEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		provider.CoreweaveApiTokenEnvVar,
		provider.CoreweaveApiEndpointEnvVar,
		provider.CoreWeaveS3EndpointEnvVar,
		provider.CoreweaveHTTPTimeoutEnvVar,
		auth.TerraformCloudWorkloadIdentityTokenEnvVar,
	} {
		t.Setenv(name, "")
	}
}

// The endpoint attribute validator only runs against the provider config, so an
// endpoint supplied through the environment reaches BuildClient unvalidated.
func TestBuildClientRejectsUnusableEndpointFromEnv(t *testing.T) {
	tests := map[string]struct {
		endpoint string
		wantErr  string
	}{
		"unparseable": {
			endpoint: "https://api.coreweave.com:port",
			wantErr:  "parsing endpoint",
		},
		"scheme-relative": {
			endpoint: "//api.coreweave.com",
			wantErr:  "must be an absolute http or https URL",
		},
		"non-http scheme": {
			endpoint: "ftp://api.coreweave.com",
			wantErr:  "must be an absolute http or https URL",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			clearProviderEnv(t)
			t.Setenv(provider.CoreweaveApiTokenEnvVar, "CW-SECRET-token")
			t.Setenv(provider.CoreweaveApiEndpointEnvVar, test.endpoint)

			client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{}, "", "")

			require.ErrorContains(t, err, test.wantErr)
			assert.Nil(t, client)
		})
	}
}

func TestBuildClientUsesWorkloadIdentity(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv(auth.TerraformCloudWorkloadIdentityTokenEnvVar, testWorkloadIdentityJWT)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "ambient-static-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/coreweave.directory.v1alpha.InternalDirectoryService/ServiceAccountOidcAuth":
			_, _ = fmt.Fprintf(w, `{"bearerToken":"exchanged-token","expireTime":"%s"}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		case "/v1beta1/auth/whoami":
			assert.Equal(t, "Bearer exchanged-token", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"principal":{"uid":"principal-uid","orgUid":"organization-uid"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)

	client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{
		Authentication: &provider.AuthenticationProviderModel{
			WorkloadIdentity: &provider.WorkloadIdentityProviderModel{
				ServiceAccountUID: types.StringValue("service-account-uid"),
			},
		},
	}, "", "")

	require.NoError(t, err)
	require.NotNil(t, client)
	identity, err := client.GetCallerIdentity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "principal-uid", identity.PrincipalID)
	assert.Equal(t, "organization-uid", identity.OrganizationID)
}

// BuildClient resolves which credential to use from the model plus the
// environment, and every rejection it can make is a property of that pairing.
func TestBuildClientResolvesCredentials(t *testing.T) {
	workloadIdentity := func(uid types.String) *provider.AuthenticationProviderModel {
		return &provider.AuthenticationProviderModel{
			WorkloadIdentity: &provider.WorkloadIdentityProviderModel{ServiceAccountUID: uid},
		}
	}

	tests := map[string]struct {
		env     map[string]string
		model   provider.CoreweaveProviderModel
		wantErr string
	}{
		"no credential at all": {
			wantErr: "token is required",
		},
		"token from the environment": {
			env: map[string]string{provider.CoreweaveApiTokenEnvVar: "CW-SECRET-token"},
		},
		"token and workload identity together": {
			model: provider.CoreweaveProviderModel{
				Token:          types.StringValue("CW-SECRET-token"),
				Authentication: workloadIdentity(types.StringValue("service-account-uid")),
			},
			wantErr: "conflicts with the configured token attribute",
		},
		"unknown service account UID": {
			model:   provider.CoreweaveProviderModel{Authentication: workloadIdentity(types.StringUnknown())},
			wantErr: "service_account_uid is unknown",
		},
		// An empty token is what token = var.x yields when x is left unset, so
		// it is no credential rather than one that conflicts.
		"empty token with workload identity": {
			env: map[string]string{auth.TerraformCloudWorkloadIdentityTokenEnvVar: testWorkloadIdentityJWT},
			model: provider.CoreweaveProviderModel{
				Token:          types.StringValue(""),
				Authentication: workloadIdentity(types.StringValue("service-account-uid")),
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			clearProviderEnv(t)
			for key, value := range test.env {
				t.Setenv(key, value)
			}

			client, err := provider.BuildClient(t.Context(), test.model, "", "")

			if test.wantErr == "" {
				require.NoError(t, err)
				assert.NotNil(t, client)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
			assert.Nil(t, client)
		})
	}
}

// The authentication attributes are modeled as Go structs, which cannot hold an
// unknown value, and the client is built during Configure, so a value Terraform
// cannot resolve yet has to be rejected by name. Without these checks the
// framework reports a conversion error blaming the provider itself.
func TestProviderConfigureRejectsUnknownValues(t *testing.T) {
	tests := map[string]struct {
		config      func(*testing.T, tftypes.Object) tfprotov6.DynamicValue
		wantSummary string
	}{
		"authentication object": {
			config: func(t *testing.T, providerType tftypes.Object) tfprotov6.DynamicValue {
				t.Helper()
				return providerConfig(t, providerType, map[string]tftypes.Value{
					"authentication": tftypes.NewValue(authenticationType(providerType), tftypes.UnknownValue),
				})
			},
			wantSummary: "Unknown authentication configuration",
		},
		"workload_identity object": {
			config: func(t *testing.T, providerType tftypes.Object) tfprotov6.DynamicValue {
				t.Helper()
				authType := authenticationType(providerType)
				return providerConfig(t, providerType, map[string]tftypes.Value{
					"authentication": tftypes.NewValue(authType, map[string]tftypes.Value{
						"workload_identity": tftypes.NewValue(workloadIdentityType(providerType), tftypes.UnknownValue),
					}),
				})
			},
			wantSummary: "Unknown workload identity configuration",
		},
		"service_account_uid": {
			config: func(t *testing.T, providerType tftypes.Object) tfprotov6.DynamicValue {
				t.Helper()
				return workloadIdentityProviderConfig(t, providerType, nil, tftypes.UnknownValue)
			},
			wantSummary: "Unknown workload identity service account UID",
		},
		"token": {
			config: func(t *testing.T, providerType tftypes.Object) tfprotov6.DynamicValue {
				t.Helper()
				return workloadIdentityProviderConfig(t, providerType, tftypes.UnknownValue, "service-account-uid")
			},
			wantSummary: "Unknown API token",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			clearProviderEnv(t)
			server, providerType := newProviderServer(t)
			config := test.config(t, providerType)

			response, err := server.ConfigureProvider(t.Context(), &tfprotov6.ConfigureProviderRequest{Config: &config})
			require.NoError(t, err)

			require.NotEmpty(t, response.Diagnostics)
			assert.Equal(t, test.wantSummary, response.Diagnostics[0].Summary)
			assert.Contains(t, response.Diagnostics[0].Detail, "must be known while configuring the provider")
			assert.NotContains(t, response.Diagnostics[0].Detail, "always an error in the provider")
		})
	}
}

// The config validator and BuildClient must agree on what counts as a
// configured token. Asserting the pair together is the point: they diverged
// once, and a table over either one alone would not have caught it.
func TestTokenConflictIsConsistentAcrossLayers(t *testing.T) {
	clearProviderEnv(t)
	server, providerType := newProviderServer(t)

	validate := func(t *testing.T, token any) []*tfprotov6.Diagnostic {
		t.Helper()

		config := workloadIdentityProviderConfig(t, providerType, token, "service-account-uid")
		response, err := server.ValidateProviderConfig(t.Context(), &tfprotov6.ValidateProviderConfigRequest{Config: &config})
		require.NoError(t, err)
		return response.Diagnostics
	}

	t.Run("a real token conflicts", func(t *testing.T) {
		diagnostics := validate(t, "CW-SECRET-token")
		require.NotEmpty(t, diagnostics)
		assert.Contains(t, diagnostics[0].Detail, "cannot be configured together")
	})

	t.Run("an empty token does not", func(t *testing.T) {
		assert.Empty(t, validate(t, ""), "an unset variable supplies no credential to conflict with")
	})

	t.Run("an unknown token defers to Configure", func(t *testing.T) {
		// The validator skips unknown values, so it must stay silent and leave
		// the rejection to Configure rather than reporting a false conflict.
		assert.Empty(t, validate(t, tftypes.UnknownValue))
	})
}

func newProviderServer(t *testing.T) (tfprotov6.ProviderServer, tftypes.Object) {
	t.Helper()

	server, err := provider.TestProtoV6ProviderFactories["coreweave"]()
	require.NoError(t, err)

	schemaResponse, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	return server, schemaResponse.Provider.ValueType().(tftypes.Object)
}

func authenticationType(providerType tftypes.Object) tftypes.Object {
	return providerType.AttributeTypes["authentication"].(tftypes.Object)
}

func workloadIdentityType(providerType tftypes.Object) tftypes.Object {
	return authenticationType(providerType).AttributeTypes["workload_identity"].(tftypes.Object)
}

// providerConfig builds a provider config in which every attribute is null
// except the overrides supplied.
func providerConfig(t *testing.T, providerType tftypes.Object, overrides map[string]tftypes.Value) tfprotov6.DynamicValue {
	t.Helper()

	attributes := make(map[string]tftypes.Value, len(providerType.AttributeTypes))
	for name, attributeType := range providerType.AttributeTypes {
		attributes[name] = tftypes.NewValue(attributeType, nil)
	}
	for name, value := range overrides {
		attributes[name] = value
	}

	config, err := tfprotov6.NewDynamicValue(providerType, tftypes.NewValue(providerType, attributes))
	require.NoError(t, err)
	return config
}

func workloadIdentityProviderConfig(t *testing.T, providerType tftypes.Object, token, serviceAccountUID any) tfprotov6.DynamicValue {
	t.Helper()

	workloadIdentity := tftypes.NewValue(workloadIdentityType(providerType), map[string]tftypes.Value{
		"service_account_uid": tftypes.NewValue(tftypes.String, serviceAccountUID),
	})
	return providerConfig(t, providerType, map[string]tftypes.Value{
		"token": tftypes.NewValue(tftypes.String, token),
		"authentication": tftypes.NewValue(authenticationType(providerType), map[string]tftypes.Value{
			"workload_identity": workloadIdentity,
		}),
	})
}
