package cache

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryEntry(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemory().(*memoryCache)
		const key = "key"
		require.NoError(t, store.WithEntry(t.Context(), key, func(entry Entry) error {
			data := []byte("record")
			require.NoError(t, entry.Save(data))
			data[0] = 'X'
			loaded, err := entry.Load()
			require.NoError(t, err)
			require.Equal(t, "record", string(loaded))
			loaded[0] = 'Y'

			ctx, cancel := context.WithCancel(t.Context())
			waiter := make(chan error, 1)
			go func() {
				waiter <- store.WithEntry(ctx, key, func(Entry) error { return nil })
			}()
			synctest.Wait()
			cancel()
			require.ErrorIs(t, <-waiter, context.Canceled)
			return nil
		}))

		require.NoError(t, store.WithEntry(t.Context(), key, func(entry Entry) error {
			loaded, err := entry.Load()
			require.NoError(t, err)
			assert.Equal(t, "record", string(loaded))
			return entry.Delete()
		}))
		assert.Empty(t, store.entries)
	})
}
