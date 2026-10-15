package provider_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestBuildClientRequiresTokenSource(t *testing.T) {
	clearProviderEnv(t)

	client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{}, "", "")

	require.ErrorContains(t, err, "token is required")
	assert.Nil(t, client)
}

func TestBuildClientUsesDefaultEndpoint(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "CW-SECRET-token")

	client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{}, "", "")

	require.NoError(t, err)
	require.NotNil(t, client)
}
