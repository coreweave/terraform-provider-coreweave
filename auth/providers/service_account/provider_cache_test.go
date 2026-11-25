//nolint:goconst // Repeated values make independent test cases easier to read.
package serviceaccount

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/auth/cache"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSharedCacheHardRefreshOnly(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := cache.NewMemory()
		initial := providers.Credential{Token: "initial", ExpiresAt: time.Now().Add(15 * time.Minute)}
		replacement := providers.Credential{Token: "replacement", ExpiresAt: initial.ExpiresAt.Add(15 * time.Minute)}
		exchanges := 0
		newProvider := func() *Provider {
			provider, err := New(Options{
				ServiceAccountID: "account",
				OrganizationID:   "org",
				TokenSourceID:    "test-source",
				TokenSource:      func(context.Context) (string, error) { return testOIDCToken, nil },
				Cache:            store,
				HardRefreshOnly:  true,
			})
			require.NoError(t, err)
			t.Cleanup(provider.Close)
			provider.exchange = func(context.Context, string) (providers.Credential, error) {
				exchanges++
				if exchanges == 1 {
					return initial, nil
				}
				return replacement, nil
			}
			return provider
		}
		first, second := newProvider(), newProvider()

		credential, err := first.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Empty(t, cmp.Diff(initial, credential))

		time.Sleep(time.Until(initial.ExpiresAt.Add(-hardRefreshWindow - time.Second)))
		credential, err = second.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Empty(t, cmp.Diff(initial, credential))
		synctest.Wait()
		assert.Equal(t, 1, exchanges)

		time.Sleep(2 * time.Second)
		started := time.Now()
		credential, err = second.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Empty(t, cmp.Diff(replacement, credential))
		assert.Equal(t, tokenWarmup, time.Since(started))

		credential, err = first.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Empty(t, cmp.Diff(replacement, credential))
		assert.Equal(t, 2, exchanges)
	})
}
