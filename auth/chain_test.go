//nolint:goconst // Repeated values make independent test cases easier to read.
package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
	serviceaccount "github.com/coreweave/terraform-provider-coreweave/auth/providers/service_account"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers/static"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestDetect(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		environment      map[string]string
		expectedResponse *providers.Credential
		expectedError    error
	}{
		"missing credentials returns a nil provider": {},
		"whitespace credentials return a nil provider": {
			environment: map[string]string{envAPIToken: " ", envServiceAccountID: "\t", envTokenFile: " "},
		},
		"organization ID alone returns a nil provider": {
			environment: map[string]string{envOrganizationID: "org"},
		},
		"exchange settings alone return a nil provider": {
			environment: map[string]string{envEndpoint: "ftp://example.test", envExchangeTimeout: "invalid"},
		},
		"trimmed static token takes precedence over invalid workload settings": {
			environment: map[string]string{
				envAPIToken:         " api-token ",
				envServiceAccountID: "service-account",
				envEndpoint:         "ftp://example.test",
			},
			expectedResponse: &providers.Credential{Token: "api-token"},
		},
		"incomplete workload settings require an organization ID": {
			environment:   map[string]string{envServiceAccountID: "service-account", envTokenFile: "/oidc-token"},
			expectedError: errors.New(invalidWorkloadPrefix + "COREWEAVE_SERVICE_ACCOUNT_ORG_ID is required"),
		},
		"incomplete workload settings require a service account ID": {
			environment:   map[string]string{envOrganizationID: "org", envTokenCommand: "printf oidc-token"},
			expectedError: errors.New(invalidWorkloadPrefix + "COREWEAVE_SERVICE_ACCOUNT_ID is required"),
		},
		"workload settings require an OIDC token source": {
			environment:   map[string]string{envServiceAccountID: "service-account", envOrganizationID: "org"},
			expectedError: errors.New(invalidWorkloadPrefix + "configure exactly one of COREWEAVE_SERVICE_ACCOUNT_TOKEN_FILE and COREWEAVE_SERVICE_ACCOUNT_TOKEN_CMD"),
		},
		"workload token sources are mutually exclusive": {
			environment: map[string]string{
				envServiceAccountID: "service-account",
				envOrganizationID:   "org",
				envTokenFile:        "/oidc-token",
				envTokenCommand:     "printf oidc-token",
			},
			expectedError: errors.New(invalidWorkloadPrefix + "configure exactly one of COREWEAVE_SERVICE_ACCOUNT_TOKEN_FILE and COREWEAVE_SERVICE_ACCOUNT_TOKEN_CMD"),
		},
		"workload endpoint must be an HTTP or HTTPS base URL": {
			environment: map[string]string{
				envServiceAccountID: "service-account",
				envOrganizationID:   "org",
				envTokenFile:        "/oidc-token",
				envEndpoint:         "ftp://username:password@example.test",
			},
			expectedError: errors.New(invalidWorkloadPrefix + "COREWEAVE_API_ENDPOINT must be an HTTP or HTTPS base URL"),
		},
		"workload token lifetime must use whole seconds": {
			environment: map[string]string{
				envServiceAccountID: "service-account",
				envOrganizationID:   "org",
				envTokenFile:        "/oidc-token",
				envTokenDuration:    "5m500ms",
			},
			expectedError: errors.New(invalidWorkloadPrefix + "COREWEAVE_AUTH_TOKEN_DURATION must be a whole-second duration of at least 5m"),
		},
		"workload token lifetime must be at least five minutes": {
			environment: map[string]string{
				envServiceAccountID: "service-account",
				envOrganizationID:   "org",
				envTokenFile:        "/oidc-token",
				envTokenDuration:    "4m59s",
			},
			expectedError: errors.New(invalidWorkloadPrefix + "COREWEAVE_AUTH_TOKEN_DURATION must be a whole-second duration of at least 5m"),
		},
		"explicit zero lifetime in the environment is invalid": {
			environment: map[string]string{
				envServiceAccountID: "service-account",
				envOrganizationID:   "org",
				envTokenFile:        "/oidc-token",
				envTokenDuration:    "0s",
			},
			expectedError: errors.New(invalidWorkloadPrefix + "COREWEAVE_AUTH_TOKEN_DURATION must be a whole-second duration of at least 5m"),
		},
		"workload timeout must be positive": {
			environment: map[string]string{
				envServiceAccountID: "service-account",
				envOrganizationID:   "org",
				envTokenFile:        "/oidc-token",
				envExchangeTimeout:  "0s",
			},
			expectedError: errors.New(invalidWorkloadPrefix + "COREWEAVE_AUTH_EXCHANGE_TIMEOUT must be a positive duration"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider, err := newOptions{}.selectProvider(lookupEnvironment(tt.environment), nil)
			if tt.expectedError != nil {
				require.EqualError(t, err, tt.expectedError.Error())
				assert.NotErrorIs(t, err, serviceaccount.ErrSourceOnly)
			} else {
				require.NoError(t, err)
			}
			if tt.expectedResponse == nil {
				require.True(t, provider == nil, "expected a nil provider, got %T", provider)
				return
			}
			require.IsType(t, &static.Provider{}, provider)
			t.Cleanup(provider.Close)

			actual, err := provider.Retrieve(t.Context())
			require.NoError(t, err)
			assert.Empty(t, cmp.Diff(*tt.expectedResponse, actual))
		})
	}
}

// Empty configuration returns ErrCredentialsNotConfigured and no credentials.
func TestCredentialsRequireConfiguredAuthentication(t *testing.T) {
	t.Parallel()

	creds, err := newCredentials(lookupEnvironment(nil))
	require.ErrorIs(t, err, providers.ErrCredentialsNotConfigured)
	assert.Nil(t, creds)
}

// Invalid or repeated explicit credential options fail even when the environment
// supplies a valid API token.
func TestCredentialsRejectInvalidExplicitSelection(t *testing.T) {
	t.Parallel()

	var typedNil *static.Provider
	tests := map[string]struct {
		options       []Option
		expectedError error
	}{
		"blank explicit API token": {
			options:       []Option{WithAPIToken(" ")},
			expectedError: providers.ErrCredentialsNotConfigured,
		},
		"nil provider": {
			options:       []Option{WithProvider(nil)},
			expectedError: errors.New("credential provider is required"),
		},
		"typed nil provider": {
			options:       []Option{WithProvider(typedNil)},
			expectedError: errors.New("credential provider is required"),
		},
		"conflicting methods": {
			options:       []Option{WithAPIToken("explicit-token"), WithServiceAccount(serviceaccount.Options{})},
			expectedError: errors.New("only one explicit credential method may be selected"),
		},
		"duplicate API token options": {
			options:       []Option{WithAPIToken("first-token"), WithAPIToken("second-token")},
			expectedError: errors.New("only one explicit credential method may be selected"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			creds, err := newCredentials(lookupEnvironment(map[string]string{envAPIToken: "ambient-token"}), test.options...)
			if errors.Is(test.expectedError, providers.ErrCredentialsNotConfigured) {
				require.ErrorIs(t, err, test.expectedError)
			} else {
				require.EqualError(t, err, test.expectedError.Error())
			}
			assert.Nil(t, creds)
		})
	}
}

// Close releases a provider created by New. A supplied provider stays usable.
func TestCredentialsCloseOnlyOwnedProviders(t *testing.T) {
	t.Parallel()

	borrowed, err := static.New("caller-token")
	require.NoError(t, err)
	t.Cleanup(borrowed.Close)

	tests := map[string]struct {
		option              Option
		expectedBeforeClose providers.Credential
		expectedResponse    providers.Credential
		expectedError       error
	}{
		"explicit API token overrides the environment and is SDK-owned": {
			option:              WithAPIToken(" explicit-token "),
			expectedBeforeClose: providers.Credential{Token: "explicit-token"},
			expectedError:       providers.ErrClosed,
		},
		"supplied provider stays caller-owned": {
			option:              WithProvider(borrowed),
			expectedBeforeClose: providers.Credential{Token: "caller-token"},
			expectedResponse:    providers.Credential{Token: "caller-token"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			creds, err := newCredentials(lookupEnvironment(map[string]string{
				envAPIToken: "ambient-token",
			}), test.option)
			require.NoError(t, err)
			t.Cleanup(creds.Close)

			credential, err := creds.provider.Retrieve(t.Context())
			require.NoError(t, err)
			assert.Equal(t, test.expectedBeforeClose, credential)

			creds.Close()
			credential, err = creds.provider.Retrieve(t.Context())
			require.ErrorIs(t, err, test.expectedError)
			assert.Equal(t, test.expectedResponse, credential)
		})
	}
}

// Closing credentials cancels the workload exchange and unblocks Retrieve.
func TestCredentialsCloseCancelsCleartextExchange(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	requests := make(chan *directoryv1alpha.ServiceAccountOidcAuthRequest, 1)
	canceled := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := readTokenExchangeRequest(t, w, r)
		if request == nil {
			return
		}
		select {
		case requests <- request:
		case <-ctx.Done():
			return
		}
		select {
		case <-r.Context().Done():
			canceled <- r.Context().Err()
		case <-ctx.Done():
		}
	}))
	t.Cleanup(server.Close)

	creds, err := newCredentials(lookupEnvironment(map[string]string{
		envServiceAccountID: " test-account ",
		envOrganizationID:   " test-org ",
		envTokenCommand:     "printf test-oidc-token",
		envEndpoint:         server.URL,
		envTokenDuration:    "5m",
	}))
	require.NoError(t, err)
	t.Cleanup(creds.Close)

	done := make(chan error, 1)
	go func() {
		_, err := creds.provider.Retrieve(ctx)
		done <- err
	}()
	select {
	case req := <-requests:
		expected := directoryv1alpha.ServiceAccountOidcAuthRequest_builder{
			ServiceAccountUid: proto.String("test-account"),
			OrgUid:            proto.String("test-org"),
			OidcToken:         proto.String("test-oidc-token"),
			DurationSeconds:   proto.Int64(300),
		}.Build()
		assert.Empty(t, cmp.Diff(expected, req, protocmp.Transform()))
	case <-ctx.Done():
		t.Fatal("configured workload exchange did not reach the HTTP server")
	}

	creds.Close()
	select {
	case err := <-done:
		require.ErrorIs(t, err, providers.ErrClosed)
	case <-ctx.Done():
		t.Fatal("closing credentials did not unblock Retrieve")
	}
	select {
	case err := <-canceled:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal("closing credentials did not cancel the exchange request")
	}
}

func readTokenExchangeRequest(t *testing.T, w http.ResponseWriter, r *http.Request) *directoryv1alpha.ServiceAccountOidcAuthRequest {
	t.Helper()
	assert.Equal(t, http.MethodPost, r.Method)
	assert.Equal(t, "/coreweave.auth.v1alpha.TokenService/ServiceAccountOidcAuth", r.URL.Path)
	assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
	assert.Equal(t, "1", r.Header.Get("Connect-Protocol-Version"))
	body, err := io.ReadAll(r.Body)
	var request directoryv1alpha.ServiceAccountOidcAuthRequest
	if !assert.NoError(t, err) || !assert.NoError(t, protojson.Unmarshal(body, &request)) {
		w.WriteHeader(http.StatusBadRequest)
		return nil
	}
	return &request
}

func lookupEnvironment(values map[string]string) func(string) string {
	return func(name string) string {
		return values[name]
	}
}

const (
	envAPIToken           = "COREWEAVE_API_TOKEN"
	envEndpoint           = "COREWEAVE_API_ENDPOINT"
	envServiceAccountID   = "COREWEAVE_SERVICE_ACCOUNT_ID"
	envOrganizationID     = "COREWEAVE_SERVICE_ACCOUNT_ORG_ID"
	envTokenFile          = "COREWEAVE_SERVICE_ACCOUNT_TOKEN_FILE"
	envTokenCommand       = "COREWEAVE_SERVICE_ACCOUNT_TOKEN_CMD"
	envTokenDuration      = "COREWEAVE_AUTH_TOKEN_DURATION"
	envExchangeTimeout    = "COREWEAVE_AUTH_EXCHANGE_TIMEOUT"
	invalidWorkloadPrefix = "invalid workload identity configuration: "
)
