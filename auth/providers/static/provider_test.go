//nolint:goconst // Repeated values make independent test cases easier to read.
package static

import (
	"context"
	"testing"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderRetrieve(t *testing.T) {
	t.Parallel()

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := map[string]struct {
		token            string
		ctx              context.Context
		expectedResponse providers.Credential
		expectedError    error
	}{
		"returns API token with unknown expiration": {
			token:            "api-token",
			ctx:              context.Background(),
			expectedResponse: providers.Credential{Token: "api-token", ExpiresAt: time.Time{}},
		},
		"honors canceled context": {
			token:         "api-token",
			ctx:           canceledCtx,
			expectedError: context.Canceled,
		},
		"reports unconfigured token": {
			ctx:           context.Background(),
			expectedError: providers.ErrCredentialsNotConfigured,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider := &Provider{}
			if tt.token != "" {
				var err error
				provider, err = New(tt.token)
				require.NoError(t, err)
			}
			actual, err := provider.Retrieve(tt.ctx)
			if tt.expectedError != nil {
				require.ErrorIs(t, err, tt.expectedError)
				assert.Empty(t, actual)
				return
			}

			require.NoError(t, err)
			assert.Empty(t, cmp.Diff(tt.expectedResponse, actual))
		})
	}
}
