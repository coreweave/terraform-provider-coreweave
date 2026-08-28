package coreweave_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A structurally valid JWT; the provider only checks its shape before exchange.
const testWorkloadIdentityJWT = "e30.e30.c2lnbmF0dXJl"

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

// Resource Read handlers call RemoveResource when IsNotFoundError is true, so
// an authentication failure must never satisfy it however the authentication
// endpoint answered. Otherwise a missing service account or trust
// configuration would silently delete healthy resources from Terraform state.
// Not parallel: t.Setenv cannot be used alongside t.Parallel.
func TestIsNotFoundErrorIgnoresTokenSourceFailures(t *testing.T) {
	// A 404 from the authentication endpoint, which is what a missing service
	// account or trust configuration returns.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"service account not found"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv(auth.TerraformCloudWorkloadIdentityTokenEnvVar, testWorkloadIdentityJWT)

	source, err := auth.NewWorkloadIdentityTokenSource(t.Context(), server.URL, "sa-uid", "test-user-agent", time.Second)
	require.NoError(t, err)
	client, err := coreweave.NewClient(server.URL, "https://objects.example.test", time.Second, source, "test-user-agent")
	require.NoError(t, err)

	_, err = client.GetCluster(t.Context(), connect.NewRequest(&cksv1beta1.GetClusterRequest{}))
	require.Error(t, err)
	assert.False(t, coreweave.IsNotFoundError(err),
		"a token source failure must not be read as a deleted resource")
	// The exchange's real status stays visible to the operator.
	assert.ErrorContains(t, err, "HTTP 404 (not_found): service account not found")

	// A genuine NotFound from the API itself still removes the resource.
	notFound := connect.NewError(connect.CodeNotFound, errors.New("cluster not found"))
	assert.True(t, coreweave.IsNotFoundError(notFound))
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

// A token source owns its own retry policy and budget, so a deadline it reports
// is its budget running out, not this attempt's. Retrying here would repeat the
// source's entire retry sequence once per attempt.
func TestClientDoesNotRetryTokenSourceDeadline(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"principal":{"uid":"principal-123","orgUid":"org-456"}}`))
	}))
	t.Cleanup(server.Close)

	calls := 0
	source := accessTokenSourceFunc(func(context.Context) (string, error) {
		calls++
		return "", context.DeadlineExceeded
	})
	client, err := coreweave.NewClient(server.URL, "https://objects.example.test", time.Second, source, "test-user-agent")
	require.NoError(t, err)

	_, err = client.GetCluster(t.Context(), connect.NewRequest(&cksv1beta1.GetClusterRequest{}))
	require.Error(t, err)
	assert.Equal(t, 1, calls, "the source already exhausted its own budget; do not repeat it")

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeDeadlineExceeded, connectErr.Code())
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
