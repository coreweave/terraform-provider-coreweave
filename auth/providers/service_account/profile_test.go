//nolint:goconst // Repeated values make independent test cases easier to read.
package serviceaccount

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/auth/cache"
	"github.com/coreweave/terraform-provider-coreweave/auth/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfileRoundTrip(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	input := Profile{Source: OIDCSource{Command: &OIDCCommand{Args: []string{"./oidc-helper", "argument with spaces"}}}}
	environment := map[string]string{
		envServiceAccountID:        " account ",
		config.EnvOrganizationID:   " org ",
		config.EnvAPIEndpoint:      " https://example.com ",
		envTokenDuration:           "1200s",
		envServiceAccountTokenFile: "ignored-token",
		envExchangeTimeout:         "invalid",
	}
	profile, err := ResolveProfile(input, func(name string) string { return environment[name] })
	require.NoError(t, err)
	assert.Equal(t, "account", profile.ServiceAccountID)
	assert.Equal(t, "org", profile.OrganizationID)
	assert.Equal(t, "https://example.com", profile.Endpoint)
	assert.Equal(t, "20m0s", profile.RequestedLifetime)
	require.NotNil(t, profile.Source.Command)
	assert.Equal(t, cwd, profile.Source.Command.Dir)
	input.Source.Command.Args[1] = "changed"
	assert.Equal(t, "argument with spaces", profile.Source.Command.Args[1])

	data, err := json.Marshal(profile)
	require.NoError(t, err)
	var saved Profile
	require.NoError(t, json.Unmarshal(data, &saved))
	store := cache.NewMemory()
	provider, err := saved.Open(RuntimeOptions{
		Cache: store,
		Getenv: func(name string) string {
			assert.Equal(t, envExchangeTimeout, name)
			return " 1.5s "
		},
	})
	require.NoError(t, err)
	t.Cleanup(provider.Close)
	assert.Equal(t, 1500*time.Millisecond, provider.options.ExchangeTimeout)
	assert.Equal(t, "https://example.com", provider.options.Endpoint)
	_, err = saved.Open(RuntimeOptions{Getenv: func(name string) string { return environment[name] }})
	require.ErrorContains(t, err, "COREWEAVE_AUTH_EXCHANGE_TIMEOUT must be a positive duration")
	key, err := saved.CacheKey()
	require.NoError(t, err)
	providerKey, ok := provider.CacheKey()
	require.True(t, ok)
	assert.Equal(t, providerKey, key)

	require.NoError(t, store.WithEntry(t.Context(), key, func(entry cache.Entry) error {
		return entry.Save([]byte("cached record"))
	}))
	require.NoError(t, saved.ClearCache(t.Context(), store))
	require.NoError(t, store.WithEntry(t.Context(), key, func(entry cache.Entry) error {
		data, err := entry.Load()
		assert.Empty(t, data)
		return err
	}))
}

func TestDetectProfileWithoutRuntimeSettings(t *testing.T) {
	t.Parallel()

	environment := map[string]string{
		envServiceAccountID:        "account",
		config.EnvOrganizationID:   "org",
		envServiceAccountTokenFile: "token",
	}
	profile, err := DetectProfile(func(name string) string {
		assert.NotEqual(t, envExchangeTimeout, name)
		return environment[name]
	})
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, "account", profile.ServiceAccountID)
	assert.Equal(t, "org", profile.OrganizationID)
	assert.Equal(t, "15m0s", profile.RequestedLifetime)
}

func TestResolveProfileRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		profile Profile
		want    string
	}{
		"zero lifetime": {
			profile: Profile{RequestedLifetime: "0s", Source: OIDCSource{File: "token"}},
			want:    "RequestedLifetime must be a whole-second duration of at least 5m",
		},
		"command without executable": {
			profile: Profile{Source: OIDCSource{Command: &OIDCCommand{}}},
			want:    "OIDC token command requires an executable",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			profile := tt.profile
			profile.ServiceAccountID = "account"
			profile.OrganizationID = "org"
			_, err := ResolveProfile(profile, nil)
			require.ErrorContains(t, err, tt.want)
		})
	}
}
