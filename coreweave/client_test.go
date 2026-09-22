package coreweave_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ coreweave.AccessTokenSource = accessTokenSourceFunc(nil)

type accessTokenSourceFunc func(context.Context) (string, error)

func (f accessTokenSourceFunc) Token(ctx context.Context) (string, error) {
	return f(ctx)
}

func TestNewClientValidatesItsInputs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint string
		source   coreweave.AccessTokenSource
		wantErr  string
	}{
		"nil token source": {
			endpoint: "https://api.example.test",
			source:   nil,
			wantErr:  "access token source is required",
		},
		"unparseable endpoint": {
			endpoint: "https://api.example.test:port",
			source:   accessTokenSourceFunc(func(context.Context) (string, error) { return "token", nil }),
			wantErr:  "parsing endpoint",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, err := coreweave.NewClient(test.endpoint, "https://objects.example.test", time.Second, test.source, "test-user-agent")
			require.ErrorContains(t, err, test.wantErr)
			assert.Nil(t, client)
		})
	}
}

func TestNewClientAuthenticatesRequests(t *testing.T) {
	t.Parallel()

	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"principal":{"uid":"principal-123","orgUid":"org-456"}}`))
	}))
	t.Cleanup(server.Close)

	source := accessTokenSourceFunc(func(context.Context) (string, error) { return "static-token", nil })
	client, err := coreweave.NewClient(server.URL, "https://objects.example.test", time.Second, source, "test-user-agent")
	require.NoError(t, err)

	_, err = client.GetCallerIdentity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Bearer static-token", authorization)
}

func TestClientPropagatesTokenSourceErrors(t *testing.T) {
	t.Parallel()

	calls := 0
	source := accessTokenSourceFunc(func(context.Context) (string, error) {
		calls++
		return "", errors.New("token refresh failed")
	})
	client, err := coreweave.NewClient(
		"https://api.example.test",
		"https://objects.example.test",
		time.Second,
		source,
		"test-user-agent",
	)
	require.NoError(t, err)

	t.Run("ConnectRPC", func(t *testing.T) {
		_, err := client.GetCluster(t.Context(), connect.NewRequest(&cksv1beta1.GetClusterRequest{}))
		require.ErrorContains(t, err, "getting access token: token refresh failed")

		var connectErr *connect.Error
		require.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())

		var diagnostics diag.Diagnostics
		coreweave.HandleAPIError(t.Context(), err, &diagnostics)
		require.Len(t, diagnostics, 1)
		assert.Equal(t, "Unauthenticated", diagnostics[0].Summary())
	})

	t.Run("raw HTTP", func(t *testing.T) {
		_, err := client.GetCallerIdentity(t.Context())
		require.ErrorContains(t, err, "getting access token: token refresh failed")
	})

	assert.Equal(t, 2, calls, "token-source failures must not be retried")
}

func TestClientPreservesTokenSourceCancellation(t *testing.T) {
	t.Parallel()

	calls := 0
	source := accessTokenSourceFunc(func(context.Context) (string, error) {
		calls++
		return "", context.Canceled
	})
	client, err := coreweave.NewClient(
		"https://api.example.test",
		"https://objects.example.test",
		time.Second,
		source,
		"test-user-agent",
	)
	require.NoError(t, err)

	_, err = client.GetCluster(t.Context(), connect.NewRequest(&cksv1beta1.GetClusterRequest{}))
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeCanceled, connectErr.Code())
	assert.Equal(t, 1, calls, "a canceled request must not be retried")
}

// A deadline belongs to a single attempt, so a token refresh that runs out of
// time gets another one -- unlike every other token source failure, which means
// the source itself gave up.
func TestClientRetriesTokenSourceDeadline(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"principal":{"uid":"principal-123","orgUid":"org-456"}}`))
	}))
	t.Cleanup(server.Close)

	calls := 0
	source := accessTokenSourceFunc(func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", context.DeadlineExceeded
		}
		return "token", nil
	})
	client, err := coreweave.NewClient(server.URL, "https://objects.example.test", time.Second, source, "test-user-agent")
	require.NoError(t, err)

	identity, err := client.GetCallerIdentity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "principal-123", identity.PrincipalID)
	assert.Equal(t, 2, calls, "the attempt that ran out of time must be retried")
}

func TestClientRefreshesTokenForRawHTTPRetry(t *testing.T) {
	t.Parallel()

	var authorizationHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizationHeaders = append(authorizationHeaders, r.Header.Get("Authorization"))
		if len(authorizationHeaders) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"principal":{"uid":"principal-123","orgUid":"org-456"}}`))
	}))
	t.Cleanup(server.Close)

	call := 0
	source := accessTokenSourceFunc(func(context.Context) (string, error) {
		call++
		return fmt.Sprintf("token-%d", call), nil
	})
	client, err := coreweave.NewClient(server.URL, "https://objects.example.test", time.Second, source, "test-user-agent")
	require.NoError(t, err)

	identity, err := client.GetCallerIdentity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "principal-123", identity.PrincipalID)
	assert.Equal(t, []string{"Bearer token-1", "Bearer token-2"}, authorizationHeaders)
}

func TestClientRefreshesTokenForConnectRetry(t *testing.T) {
	t.Parallel()

	var authorizationHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizationHeaders = append(authorizationHeaders, r.Header.Get("Authorization"))
		if len(authorizationHeaders) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	call := 0
	source := accessTokenSourceFunc(func(context.Context) (string, error) {
		call++
		return fmt.Sprintf("token-%d", call), nil
	})
	client, err := coreweave.NewClient(server.URL, "https://objects.example.test", time.Second, source, "test-user-agent")
	require.NoError(t, err)

	_, err = client.GetCluster(t.Context(), connect.NewRequest(&cksv1beta1.GetClusterRequest{}))
	require.Error(t, err)
	assert.Equal(t, []string{"Bearer token-1", "Bearer token-2"}, authorizationHeaders)
}

func TestClientDoesNotRetryConnectCreate(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/proto")
	}))
	t.Cleanup(server.Close)

	source := accessTokenSourceFunc(func(context.Context) (string, error) { return "retry-policy-token", nil })
	client, err := coreweave.NewClient(server.URL, "https://unused.example.test", time.Second, source, "test-user-agent")
	require.NoError(t, err)

	_, err = client.CreateCluster(t.Context(), connect.NewRequest(&cksv1beta1.CreateClusterRequest{}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeUnavailable, connectErr.Code())
	assert.Equal(t, int32(1), calls.Load())
}

func TestHandleAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want diag.Diagnostics
	}{
		{
			name: "Generic error",
			err:  errors.New("weird error"),
			want: diag.Diagnostics{diag.NewErrorDiagnostic(
				"Unexpected Error",
				"An unexpected error occurred: \"weird error\". Please check the provider logs for more details.",
			)},
		}, {
			name: "Internal error",
			err:  connect.NewError(connect.CodeInternal, errors.New("Internal server error")),
			want: diag.Diagnostics{diag.NewErrorDiagnostic(
				"Internal Error",
				"An unexpected server error occurred: \"internal: Internal server error\". Please check the provider logs for more details.",
			)},
		}, {
			name: "Canceled error",
			err:  connect.NewError(connect.CodeCanceled, errors.New("token request canceled")),
			want: diag.Diagnostics{diag.NewErrorDiagnostic(
				"Request Canceled",
				"canceled: token request canceled",
			)},
		}, {
			name: "Deadline exceeded error",
			err:  connect.NewError(connect.CodeDeadlineExceeded, errors.New("token request timed out")),
			want: diag.Diagnostics{diag.NewErrorDiagnostic(
				"Request Timed Out",
				"deadline_exceeded: token request timed out",
			)},
		}, {
			name: "Unavailable error",
			err:  connect.NewError(connect.CodeUnavailable, errors.New("identity provider unreachable")),
			want: diag.Diagnostics{diag.NewErrorDiagnostic(
				"Service Unavailable",
				"unavailable: identity provider unreachable",
			)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var diagnostics diag.Diagnostics
			coreweave.HandleAPIError(t.Context(), tt.err, &diagnostics)
			assert.Equal(t, tt.want, diagnostics)
		})
	}
}
