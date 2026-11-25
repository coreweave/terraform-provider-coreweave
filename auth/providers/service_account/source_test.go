//nolint:goconst // Repeated values make independent test cases easier to read.
package serviceaccount

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheKey(t *testing.T) {
	t.Parallel()
	base := Options{OrganizationID: "org", ServiceAccountID: "account", Source: &OIDCSource{File: "token"}}
	original, err := New(base)
	require.NoError(t, err)
	t.Cleanup(original.Close)
	originalKey, ok := original.CacheKey()
	require.True(t, ok)
	tests := map[string]struct {
		change func(*Options)
		same   bool
	}{
		"same configuration": {same: true},
		"organization":       {change: func(o *Options) { o.OrganizationID = "other" }},
		"account":            {change: func(o *Options) { o.ServiceAccountID = "other" }},
		"endpoint":           {change: func(o *Options) { o.Endpoint = "https://example.com" }},
		"lifetime":           {change: func(o *Options) { o.RequestedLifetime = 20 * time.Minute }},
		"file":               {change: func(o *Options) { o.Source = &OIDCSource{File: "other-token"} }},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opts := base
			if tt.change != nil {
				tt.change(&opts)
			}
			provider, err := New(opts)
			require.NoError(t, err)
			t.Cleanup(provider.Close)
			key, ok := provider.CacheKey()
			require.True(t, ok)
			assert.Equal(t, tt.same, key == originalKey)
		})
	}
}

func TestCallbackCacheKeyRequiresExplicitIdentity(t *testing.T) {
	t.Parallel()
	for name, id := range map[string]string{"anonymous": "", "identified": "issuer/subject/audience"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			provider, err := New(Options{
				OrganizationID: "org", ServiceAccountID: "account", TokenSourceID: id,
				TokenSource: func(context.Context) (string, error) { panic("must not acquire token") },
			})
			require.NoError(t, err)
			t.Cleanup(provider.Close)
			key, ok := provider.CacheKey()
			assert.Equal(t, id != "", ok)
			assert.Equal(t, id != "", key != "")
		})
	}
}

func TestCommandSourceCopiesConfiguration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	command := &OIDCCommand{Args: []string{"oidc-helper", "original"}, Dir: dir}
	provider, err := New(Options{
		OrganizationID:   "org",
		ServiceAccountID: "account",
		Source:           &OIDCSource{Command: command},
	})
	require.NoError(t, err)
	t.Cleanup(provider.Close)

	command.Args[1] = "mutated"
	command.Dir = "mutated-dir"
	assert.Equal(t, &OIDCCommand{Args: []string{"oidc-helper", "original"}, Dir: dir}, provider.options.Source.Command)
}
