//nolint:goconst // Repeated values make independent test cases easier to read.
package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
	serviceaccount "github.com/coreweave/terraform-provider-coreweave/auth/providers/service_account"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers/static"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The transport sends one request with the bearer token, returns the server's
// 401, and leaves the original request's Header nil.
func TestCredentialsInjectBearerWithoutMutatingOrReplayingRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Equal(t, "Bearer static-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	creds, err := New(
		WithAPIToken("static-token"),
		WithEndpoint(server.URL),
		WithTransport(server.Client().Transport),
	)
	require.NoError(t, err)
	t.Cleanup(creds.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/resource", nil)
	require.NoError(t, err)
	req.Header = nil

	response, err := creds.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	assert.Nil(t, req.Header)
	assert.EqualValues(t, 1, requests.Load())
}

// The configured endpoint sets the origin for bearer authentication.
func TestCredentialsMatchConfiguredOrigin(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint string
		target   string
		wantAuth bool
	}{
		"public API with default HTTPS port":      {target: "https://API.COREWEAVE.COM:443/probe", wantAuth: true},
		"custom endpoint":                         {endpoint: "http://localhost:8080", target: "http://localhost:8080", wantAuth: true},
		"default HTTP port":                       {endpoint: "http://api.coreweave.com:80", target: "http://api.coreweave.com", wantAuth: true},
		"custom endpoint excludes public default": {endpoint: "http://localhost:8080", target: "https://api.coreweave.com"},
		"internal API requires opt-in":            {target: "https://api.int.coreweave.com"},
		"sandbox API requires opt-in":             {target: "https://api.cwsandbox.com"},
		"hostname lookalike":                      {target: "https://api.coreweave.com.example.test"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			creds, err := newCredentials(lookupEnvironment(map[string]string{
				envAPIToken: "static-token",
				envEndpoint: test.endpoint,
			}))
			require.NoError(t, err)
			t.Cleanup(creds.Close)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, test.target, nil)
			require.NoError(t, err)
			assert.Equal(t, test.wantAuth, creds.authenticates(req))
		})
	}
}

// WithAllowedOrigins adds specific gateways alongside the configured endpoint.
func TestCredentialsAllowExplicitGatewayOrigins(t *testing.T) {
	t.Parallel()

	const endpoint = "http://localhost:8080"
	creds, err := newCredentials(lookupEnvironment(map[string]string{
		envAPIToken: "static-token",
		envEndpoint: endpoint,
	}), WithAllowedOrigins("https://api.int.coreweave.com", "https://api.cwsandbox.com"))
	require.NoError(t, err)
	t.Cleanup(creds.Close)

	tests := map[string]struct {
		target      string
		requestHost string
		opaque      bool
		wantAuth    bool
	}{
		"configured endpoint":                 {target: endpoint, wantAuth: true},
		"internal API":                        {target: "https://api.int.coreweave.com", wantAuth: true},
		"sandbox API with default HTTPS port": {target: "https://API.CWSANDBOX.COM:443", wantAuth: true},
		"unlisted public default":             {target: "https://api.coreweave.com"},
		"unlisted subdomain":                  {target: "https://tenant.api.cwsandbox.com"},
		"gateway HTTP":                        {target: "http://api.int.coreweave.com"},
		"gateway custom port":                 {target: "https://api.int.coreweave.com:8443"},
		"different Host field":                {target: "https://api.cwsandbox.com", requestHost: "api.coreweave.com"},
		"opaque target":                       {target: "https://api.cwsandbox.com/probe", opaque: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, test.target, nil)
			require.NoError(t, err)
			if test.requestHost != "" {
				req.Host = test.requestHost
			}
			if test.opaque {
				req.URL.Opaque = "//" + req.URL.Host + req.URL.Path
			}
			assert.Equal(t, test.wantAuth, creds.authenticates(req))
		})
	}
}

// A closed transport rejects requests to its configured origin before dispatch.
func TestCredentialsRejectAuthenticatedRequestsAfterClose(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	provider, err := static.New("caller-token")
	require.NoError(t, err)
	t.Cleanup(provider.Close)

	creds, err := newCredentials(lookupEnvironment(nil), WithProvider(provider), WithEndpoint(server.URL))
	require.NoError(t, err)
	creds.Close()

	client := &http.Client{Transport: creds, Timeout: 5 * time.Second}
	response, err := client.Get(server.URL)
	require.ErrorIs(t, err, providers.ErrClosed)
	assert.Nil(t, response)
	assert.Zero(t, requests.Load())
}

// An explicit blank endpoint selects the default.
// Setup rejects invalid endpoint URLs and malformed allowlist entries.
func TestCredentialsValidateConfiguredOrigin(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint         string
		allowedOrigins   []string
		options          []Option
		expectedResponse string
		expectedError    error
	}{
		"empty override selects default despite an invalid environment endpoint": {
			endpoint:         "ftp://ignored.example",
			options:          []Option{WithEndpoint("")},
			expectedResponse: "https://api.coreweave.com",
		},
		"service account endpoint overrides the environment": {
			endpoint: "https://other.example",
			options: []Option{WithServiceAccount(serviceaccount.Options{
				ServiceAccountID: "service-account",
				OrganizationID:   "org",
				Endpoint:         "https://api.int.coreweave.com",
				TokenSource:      func(context.Context) (string, error) { return "oidc-token", nil },
			})},
			expectedResponse: "https://api.int.coreweave.com",
		},
		"invalid origin": {
			endpoint:      "ftp://username:password@example.test",
			expectedError: errors.New("COREWEAVE_API_ENDPOINT must be an HTTP or HTTPS base URL"),
		},
		"blank allowed origin": {
			allowedOrigins: []string{""},
			expectedError:  errors.New("allowed origins must be HTTP or HTTPS origins"),
		},
		"wildcard allowed origin": {
			allowedOrigins: []string{"https://*.cwsandbox.com"},
			expectedError:  errors.New("allowed origins must be HTTP or HTTPS origins"),
		},
		"allowed origin with path": {
			allowedOrigins: []string{"https://api.cwsandbox.com/sandbox"},
			expectedError:  errors.New("allowed origins must be HTTP or HTTPS origins"),
		},
		"allowed origin without scheme": {
			allowedOrigins: []string{"api.cwsandbox.com"},
			expectedError:  errors.New("allowed origins must be HTTP or HTTPS origins"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			options := append([]Option{WithAllowedOrigins(test.allowedOrigins...)}, test.options...)
			creds, err := newCredentials(lookupEnvironment(map[string]string{
				envAPIToken: "static-token",
				envEndpoint: test.endpoint,
			}), options...)
			if test.expectedError != nil {
				require.EqualError(t, err, test.expectedError.Error())
				assert.Nil(t, creds)
				return
			}
			require.NoError(t, err)
			t.Cleanup(creds.Close)
			require.NotEmpty(t, creds.origins)
			assert.Equal(t, test.expectedResponse, creds.origins[0].String())
		})
	}
}

// An unlisted origin gets no bearer token and triggers no exchange. A request
// to an allowed gateway exchanges the OIDC token and sends the minted token.
func TestCredentialsUseMintedWorkloadToken(t *testing.T) {
	t.Parallel()

	var sourceCalls, exchanges atomic.Int32
	reply, err := protojson.Marshal(directoryv1alpha.ServiceAccountOidcAuthResponse_builder{
		BearerToken: proto.String("minted-token"),
		ExpireTime:  timestamppb.New(time.Now().Add(time.Hour)),
	}.Build())
	require.NoError(t, err)
	exchange := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exchanges.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	}))
	t.Cleanup(exchange.Close)

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer minted-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(gateway.Close)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(other.Close)

	creds, err := newCredentials(lookupEnvironment(map[string]string{
		envAPIToken: "ambient-token",
		envEndpoint: "ftp://ignored.example",
	}), WithServiceAccount(serviceaccount.Options{
		ServiceAccountID: "service-account",
		OrganizationID:   "org",
		Endpoint:         "ftp://ignored-workload-endpoint.example",
		Transport:        exchange.Client().Transport,
		TokenSource: func(context.Context) (string, error) {
			sourceCalls.Add(1)
			return "external-oidc-token", nil
		},
	}), WithEndpoint(exchange.URL), WithAllowedOrigins(gateway.URL))
	require.NoError(t, err)
	t.Cleanup(creds.Close)
	assert.Same(t, http.DefaultTransport, creds.transport)
	client := &http.Client{Transport: creds, Timeout: 25 * time.Second}

	response, err := client.Get(other.URL)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	assert.Zero(t, sourceCalls.Load())
	assert.Zero(t, exchanges.Load())

	response, err = client.Get(gateway.URL + "/resource")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	assert.EqualValues(t, 1, sourceCalls.Load())
	assert.EqualValues(t, 1, exchanges.Load())
}

// A credential retrieval error reaches the caller and closes the request body
// before dispatch.
func TestCredentialsCloseRequestBodyOnRetrievalError(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	creds, err := newCredentials(lookupEnvironment(nil), WithProvider(&static.Provider{}), WithEndpoint(server.URL))
	require.NoError(t, err)
	t.Cleanup(creds.Close)

	body, err := os.CreateTemp(t.TempDir(), "body-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = body.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, body)
	require.NoError(t, err)
	response, err := creds.RoundTrip(req)
	require.ErrorIs(t, err, providers.ErrCredentialsNotConfigured)
	assert.Nil(t, response)
	assert.Zero(t, requests.Load())
	_, err = body.Read(make([]byte, 1))
	assert.ErrorIs(t, err, os.ErrClosed)
}
