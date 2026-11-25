package provider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	authv1alphaconnect "buf.build/gen/go/coreweave/directory/connectrpc/go/coreweave/auth/v1alpha/authv1alphaconnect"
	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	connectv1 "connectrpc.com/connect"
	providerauth "github.com/coreweave/terraform-provider-coreweave/internal/auth"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testOIDCAssertion              = "e30.e30.c2lnbmF0dXJl"
	authenticationAttributeName    = "authentication"
	serviceAccountUIDAttributeName = "service_account_uid"
)

type tokenExchangeServer struct {
	authv1alphaconnect.UnimplementedTokenServiceHandler

	mu           sync.Mutex
	requests     []*directoryv1alpha.ServiceAccountOidcAuthRequest
	apiBearer    string
	wantAPIToken string
}

func (s *tokenExchangeServer) ServiceAccountOidcAuth(
	_ context.Context,
	req *connectv1.Request[directoryv1alpha.ServiceAccountOidcAuthRequest],
) (*connectv1.Response[directoryv1alpha.ServiceAccountOidcAuthResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req.Msg)
	return connectv1.NewResponse(&directoryv1alpha.ServiceAccountOidcAuthResponse{
		BearerToken: &s.wantAPIToken,
		ExpireTime:  timestamppb.New(time.Now().Add(time.Hour)),
	}), nil
}

func (s *tokenExchangeServer) handler() http.Handler {
	mux := http.NewServeMux()
	path, handler := authv1alphaconnect.NewTokenServiceHandler(s)
	mux.Handle(path, handler)
	mux.HandleFunc("/v1beta1/auth/whoami", func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		s.apiBearer = req.Header.Get("Authorization")
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"principal":{"uid":"service-account-principal","orgUid":"principal-org"}}`))
	})
	return mux
}

func (s *tokenExchangeServer) request(t *testing.T) *directoryv1alpha.ServiceAccountOidcAuthRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.requests, 1)
	return s.requests[0]
}

func (s *tokenExchangeServer) bearer() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apiBearer
}

func TestBuildClientUsesExplicitServiceAccountAuthentication(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "ambient-static-token")
	t.Setenv(providerauth.TerraformCloudWorkloadIdentityTokenEnvVar, testOIDCAssertion)

	service := &tokenExchangeServer{wantAPIToken: "exchanged-token"}
	server := httptest.NewServer(service.handler())
	t.Cleanup(server.Close)
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)

	client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{
		OrgID:       types.StringValue("resource-org"),
		Endpoint:    types.StringValue(server.URL),
		S3Endpoint:  types.StringValue("https://objects.example.test"),
		HTTPTimeout: types.StringValue("30s"),
		Authentication: &provider.AuthenticationProviderModel{
			ServiceAccountUID: &provider.ServiceAccountAuthenticationProviderModel{
				ID:    types.StringValue("service-account-id"),
				OrgID: types.StringValue("principal-org"),
			},
		},
	}, "1.14.0", "test")
	require.NoError(t, err)

	identity, err := client.GetCallerIdentity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "service-account-principal", identity.PrincipalID)
	assert.Equal(t, "resource-org", client.ResourceOrganizationID())

	request := service.request(t)
	assert.Equal(t, "service-account-id", request.GetServiceAccountUid())
	assert.Equal(t, "principal-org", request.GetOrgUid())
	assert.Equal(t, testOIDCAssertion, request.GetOidcToken())
	assert.Equal(t, int64(3600), request.GetDurationSeconds())
	assert.Equal(t, "Bearer exchanged-token", service.bearer())
}

func TestBuildClientUsesEnvironmentServiceAccountAuthenticationAndTokenFile(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv(provider.CoreweaveOrgIDEnvVar, "resource-org-env")
	t.Setenv(provider.CoreweaveServiceAccountIDEnvVar, "service-account-env")
	t.Setenv(provider.CoreweaveServiceAccountOrgIDEnvVar, "principal-org-env")
	tokenFile := filepath.Join(t.TempDir(), "assertion.jwt")
	require.NoError(t, os.WriteFile(tokenFile, []byte(testOIDCAssertion+"\n"), 0o600))
	t.Setenv(provider.CoreweaveServiceAccountTokenFileEnvVar, tokenFile)

	service := &tokenExchangeServer{wantAPIToken: "environment-exchanged-token"}
	server := httptest.NewServer(service.handler())
	t.Cleanup(server.Close)
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)

	client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{
		Endpoint:    types.StringValue(server.URL),
		S3Endpoint:  types.StringValue("https://objects.example.test"),
		HTTPTimeout: types.StringValue("30s"),
	}, "1.14.0", "test")
	require.NoError(t, err)

	_, err = client.GetCallerIdentity(t.Context())
	require.NoError(t, err)
	request := service.request(t)
	assert.Equal(t, "service-account-env", request.GetServiceAccountUid())
	assert.Equal(t, "principal-org-env", request.GetOrgUid())
	assert.Equal(t, testOIDCAssertion, request.GetOidcToken())
	assert.Equal(t, "resource-org-env", client.ResourceOrganizationID())
}

func TestBuildClientUsesEnvironmentServiceAccountAuthenticationAndTokenCommand(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv(provider.CoreweaveServiceAccountIDEnvVar, "service-account-command")
	t.Setenv(provider.CoreweaveServiceAccountOrgIDEnvVar, "principal-org-command")
	command := filepath.Join(t.TempDir(), "print-token")
	require.NoError(t, os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s\\n' '"+testOIDCAssertion+"'\n"), 0o700))
	t.Setenv(provider.CoreweaveServiceAccountTokenCmdEnvVar, command)

	service := &tokenExchangeServer{wantAPIToken: "command-exchanged-token"}
	server := httptest.NewServer(service.handler())
	t.Cleanup(server.Close)
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)

	client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{
		Endpoint:    types.StringValue(server.URL),
		S3Endpoint:  types.StringValue("https://objects.example.test"),
		HTTPTimeout: types.StringValue("30s"),
	}, "1.14.0", "test")
	require.NoError(t, err)

	_, err = client.GetCallerIdentity(t.Context())
	require.NoError(t, err)
	request := service.request(t)
	assert.Equal(t, "service-account-command", request.GetServiceAccountUid())
	assert.Equal(t, "principal-org-command", request.GetOrgUid())
	assert.Equal(t, testOIDCAssertion, request.GetOidcToken())
}

func TestBuildClientRejectsInvalidServiceAccountAuthentication(t *testing.T) {
	clearProviderEnv(t)

	tests := map[string]struct {
		model   provider.CoreweaveProviderModel
		env     map[string]string
		wantErr []string
	}{
		"static token conflict": {
			model: provider.CoreweaveProviderModel{
				Token: types.StringValue("static-token"),
				Authentication: &provider.AuthenticationProviderModel{
					ServiceAccountUID: &provider.ServiceAccountAuthenticationProviderModel{},
				},
			},
			wantErr: []string{"conflicts with the configured token attribute"},
		},
		"missing service account fields": {
			model: provider.CoreweaveProviderModel{Authentication: &provider.AuthenticationProviderModel{
				ServiceAccountUID: &provider.ServiceAccountAuthenticationProviderModel{},
			}},
			wantErr: []string{
				provider.CoreweaveServiceAccountIDEnvVar,
				provider.CoreweaveServiceAccountOrgIDEnvVar,
				providerauth.TerraformCloudWorkloadIdentityTokenEnvVar,
			},
		},
		"conflicting environment token sources": {
			model: provider.CoreweaveProviderModel{},
			env: map[string]string{
				provider.CoreweaveServiceAccountIDEnvVar:        "service-account-id",
				provider.CoreweaveServiceAccountOrgIDEnvVar:     "principal-org",
				provider.CoreweaveServiceAccountTokenFileEnvVar: "/token/file",
				provider.CoreweaveServiceAccountTokenCmdEnvVar:  "/token/command",
			},
			wantErr: []string{
				provider.CoreweaveServiceAccountTokenFileEnvVar,
				provider.CoreweaveServiceAccountTokenCmdEnvVar,
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			client, err := provider.BuildClient(t.Context(), test.model, "", "")
			require.Error(t, err)
			assert.Nil(t, client)
			for _, expected := range test.wantErr {
				assert.ErrorContains(t, err, expected)
			}
		})
	}
}

func TestProviderRejectsStaticTokenWithServiceAccountAuthentication(t *testing.T) {
	clearProviderEnv(t)
	server, providerType := newProviderProtocolServer(t)
	serviceAccountType := serviceAccountAuthenticationType(providerType)
	authenticationType := providerAuthenticationType(providerType)
	config := providerDynamicConfig(t, providerType, map[string]tftypes.Value{
		"token": tftypes.NewValue(tftypes.String, "static-token"),
		authenticationAttributeName: tftypes.NewValue(authenticationType, map[string]tftypes.Value{
			serviceAccountUIDAttributeName: tftypes.NewValue(serviceAccountType, map[string]tftypes.Value{
				"id":         tftypes.NewValue(tftypes.String, "service-account-id"),
				"org_id":     tftypes.NewValue(tftypes.String, "principal-org"),
				"token_file": tftypes.NewValue(tftypes.String, nil),
				"token_cmd":  tftypes.NewValue(tftypes.String, nil),
			}),
		}),
	})

	response, err := server.ValidateProviderConfig(t.Context(), &tfprotov6.ValidateProviderConfigRequest{Config: &config})
	require.NoError(t, err)
	require.NotEmpty(t, response.Diagnostics)
	assert.Contains(t, response.Diagnostics[0].Detail, "cannot be configured together")
}

func TestProviderRejectsTokenFileWithTokenCommand(t *testing.T) {
	clearProviderEnv(t)
	server, providerType := newProviderProtocolServer(t)
	serviceAccountType := serviceAccountAuthenticationType(providerType)
	authenticationType := providerAuthenticationType(providerType)
	config := providerDynamicConfig(t, providerType, map[string]tftypes.Value{
		authenticationAttributeName: tftypes.NewValue(authenticationType, map[string]tftypes.Value{
			serviceAccountUIDAttributeName: tftypes.NewValue(serviceAccountType, map[string]tftypes.Value{
				"id":         tftypes.NewValue(tftypes.String, "service-account-id"),
				"org_id":     tftypes.NewValue(tftypes.String, "principal-org"),
				"token_file": tftypes.NewValue(tftypes.String, "/token/file"),
				"token_cmd":  tftypes.NewValue(tftypes.String, "/token/command"),
			}),
		}),
	})

	response, err := server.ValidateProviderConfig(t.Context(), &tfprotov6.ValidateProviderConfigRequest{Config: &config})
	require.NoError(t, err)
	require.Len(t, response.Diagnostics, 1)
	assert.Contains(t, response.Diagnostics[0].Detail, "cannot be configured together")
}

func TestProviderConfigureRejectsUnknownServiceAccountAuthentication(t *testing.T) {
	clearProviderEnv(t)
	server, providerType := newProviderProtocolServer(t)
	authenticationType := providerAuthenticationType(providerType)
	config := providerDynamicConfig(t, providerType, map[string]tftypes.Value{
		authenticationAttributeName: tftypes.NewValue(authenticationType, map[string]tftypes.Value{
			serviceAccountUIDAttributeName: tftypes.NewValue(serviceAccountAuthenticationType(providerType), tftypes.UnknownValue),
		}),
	})

	response, err := server.ConfigureProvider(t.Context(), &tfprotov6.ConfigureProviderRequest{Config: &config})
	require.NoError(t, err)
	require.NotEmpty(t, response.Diagnostics)
	assert.Equal(t, "Unknown service-account authentication configuration", response.Diagnostics[0].Summary)
	assert.Contains(t, response.Diagnostics[0].Detail, "must be known while configuring the provider")
}

func newProviderProtocolServer(t *testing.T) (tfprotov6.ProviderServer, tftypes.Object) {
	t.Helper()
	server, err := provider.TestProtoV6ProviderFactories["coreweave"]()
	require.NoError(t, err)
	schemaResponse, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	return server, schemaResponse.Provider.ValueType().(tftypes.Object)
}

func providerAuthenticationType(providerType tftypes.Object) tftypes.Object {
	return providerType.AttributeTypes[authenticationAttributeName].(tftypes.Object)
}

func serviceAccountAuthenticationType(providerType tftypes.Object) tftypes.Object {
	return providerAuthenticationType(providerType).AttributeTypes[serviceAccountUIDAttributeName].(tftypes.Object)
}

func providerDynamicConfig(t *testing.T, providerType tftypes.Object, overrides map[string]tftypes.Value) tfprotov6.DynamicValue {
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
