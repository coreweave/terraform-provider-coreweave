package containerregistry_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRegistryRequestScopedTokenExchange verifies scoped authentication, payload replay and realm validation.
func TestRegistryRequestScopedTokenExchange(t *testing.T) {
	t.Setenv("COREWEAVE_API_TOKEN", "test-principal-credential")
	var server *httptest.Server
	var scopes []string
	var payloads []string
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/token" {
			user, password, ok := r.BasicAuth()
			if !ok || user != "principal" || password != "test-principal-credential" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			scopes = append(scopes, r.URL.Query().Get("scope"))
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"token":"scoped-registry-token"}`)
			return
		}
		if r.URL.Path == "/untrusted" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="https://untrusted.example/auth/token",service="registry"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		data, _ := io.ReadAll(r.Body)
		payloads = append(payloads, string(data))
		if r.Header.Get("Authorization") != "Bearer scoped-registry-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+server.URL+`/auth/token",service="registry"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	response, err := registryRequest(t.Context(), server.Client(), http.MethodPut, server.URL+"/upload", "application/octet-stream", []byte("image-config"))
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusCreated, response.StatusCode)
	require.Equal(t, []string{"repository:tfacc/safety:pull,push"}, scopes)
	require.Equal(t, []string{"image-config", "image-config"}, payloads)
	_, err = registryRequest(t.Context(), server.Client(), http.MethodGet, server.URL+"/untrusted", "", nil)
	require.ErrorContains(t, err, "unexpected registry authentication realm")
	require.False(t, strings.Contains(err.Error(), "test-principal-credential"))
}

// TestRegistryEndpointReadiness verifies delayed routing succeeds and waiting respects cancellation.
func TestRegistryEndpointReadiness(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, waitRegistryEndpoint(ctx, server.Client(), server.URL))
	require.Equal(t, 2, calls)
	canceled, stop := context.WithCancel(t.Context())
	stop()
	require.ErrorIs(t, waitRegistryEndpoint(canceled, server.Client(), server.URL), context.Canceled)
}
