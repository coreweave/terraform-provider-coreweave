//nolint:goconst // Repeated values make independent test cases easier to read.
package serviceaccount

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/auth/internal/config"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testOIDCToken = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ3b3JrbG9hZCJ9.c2ln"

func TestNew(t *testing.T) {
	t.Parallel()

	tokenSource := func(context.Context) (string, error) { return testOIDCToken, nil }
	tests := map[string]struct {
		change           func(*Options)
		expectedResponse Options
		expectedError    error
	}{
		"defaults optional settings": {
			expectedResponse: Options{
				ServiceAccountID:  "test-account",
				OrganizationID:    "test-org",
				Endpoint:          "https://api.coreweave.com",
				RequestedLifetime: 15 * time.Minute,
				ExchangeTimeout:   2 * time.Minute,
			},
		},
		"reports all missing required fields": {
			change: func(opts *Options) {
				opts.ServiceAccountID = " "
				opts.OrganizationID = " "
				opts.TokenSource = nil
			},
			expectedError: errors.New("invalid workload identity configuration: ServiceAccountID is required; " +
				"OrganizationID is required; TokenSource is required"),
		},
		"validates the endpoint": {
			change:        func(opts *Options) { opts.Endpoint = "https://:443" },
			expectedError: errors.New("invalid workload identity configuration: Endpoint must be an HTTP or HTTPS base URL"),
		},
		"accepts the minimum requested lifetime": {
			change: func(opts *Options) { opts.RequestedLifetime = 5 * time.Minute },
			expectedResponse: Options{
				ServiceAccountID:  "test-account",
				OrganizationID:    "test-org",
				Endpoint:          "https://api.coreweave.com",
				RequestedLifetime: 5 * time.Minute,
				ExchangeTimeout:   2 * time.Minute,
			},
		},
		"rejects a requested lifetime below five minutes": {
			change:        func(opts *Options) { opts.RequestedLifetime = 4*time.Minute + 59*time.Second },
			expectedError: errors.New("invalid workload identity configuration: RequestedLifetime must be a whole-second duration of at least 5m"),
		},
		"requires a whole-second requested lifetime": {
			change:        func(opts *Options) { opts.RequestedLifetime = 5*time.Minute + 500*time.Millisecond },
			expectedError: errors.New("invalid workload identity configuration: RequestedLifetime must be a whole-second duration of at least 5m"),
		},
		"rejects a negative requested lifetime": {
			change:        func(opts *Options) { opts.RequestedLifetime = -time.Second },
			expectedError: errors.New("invalid workload identity configuration: RequestedLifetime must be a whole-second duration of at least 5m"),
		},
		"requires a positive exchange timeout": {
			change:        func(opts *Options) { opts.ExchangeTimeout = -time.Second },
			expectedError: errors.New("invalid workload identity configuration: ExchangeTimeout must be a positive duration"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := Options{
				ServiceAccountID: "test-account",
				OrganizationID:   "test-org",
				TokenSource:      tokenSource,
			}
			if tt.change != nil {
				tt.change(&opts)
			}
			provider, err := New(opts)
			if tt.expectedError != nil {
				require.EqualError(t, err, tt.expectedError.Error())
				assert.Nil(t, provider)
				return
			}

			require.NoError(t, err)
			t.Cleanup(provider.Close)
			assert.NotNil(t, provider.exchange)
			assert.Empty(t, cmp.Diff(tt.expectedResponse, provider.options, cmpopts.IgnoreFields(Options{}, "TokenSource")))
			assert.NotNil(t, provider.options.TokenSource)
		})
	}
}

// Explicit options override the environment. Omitted values still use it.
func TestFromEnvironmentPrefersExplicitOptions(t *testing.T) {
	t.Parallel()

	environment := map[string]string{
		envServiceAccountID:        "ambient-account",
		config.EnvOrganizationID:   "ambient-org",
		envServiceAccountTokenFile: "/ignored-token",
		envTokenCommand:            "ignored-token-command",
		envTokenDuration:           "invalid",
		envExchangeTimeout:         "invalid",
	}
	provider, err := FromEnvironment(Options{
		ServiceAccountID:  "service-account",
		RequestedLifetime: 20 * time.Minute,
		ExchangeTimeout:   5 * time.Second,
		TokenSource:       func(context.Context) (string, error) { return testOIDCToken, nil },
	}, func(name string) string { return environment[name] })
	require.NoError(t, err)
	t.Cleanup(provider.Close)

	expected := Options{
		ServiceAccountID:  "service-account",
		OrganizationID:    "ambient-org",
		Endpoint:          "https://api.coreweave.com",
		RequestedLifetime: 20 * time.Minute,
		ExchangeTimeout:   5 * time.Second,
	}
	assert.Empty(t, cmp.Diff(expected, provider.options, cmpopts.IgnoreFields(Options{}, "TokenSource")))
	require.NotNil(t, provider.options.TokenSource)
	token, err := provider.options.TokenSource(t.Context())
	require.NoError(t, err)
	assert.Equal(t, testOIDCToken, token)
}

func TestFromEnvironmentReportsInvalidOptionsAndEnvironment(t *testing.T) {
	t.Parallel()

	environment := map[string]string{envExchangeTimeout: "0s"}
	provider, err := FromEnvironment(Options{
		ServiceAccountID:  "service-account",
		OrganizationID:    "org",
		RequestedLifetime: 5*time.Minute + 500*time.Millisecond,
		TokenSource:       func(context.Context) (string, error) { return testOIDCToken, nil },
	}, func(name string) string { return environment[name] })
	require.EqualError(t, err, "invalid workload identity configuration: RequestedLifetime must be a whole-second duration of at least 5m; COREWEAVE_AUTH_EXCHANGE_TIMEOUT must be a positive duration")
	assert.Nil(t, provider)
}

func TestDetectDistinguishesSourceOnly(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		environment   map[string]string
		expectedError error
	}{
		"source only bypasses ambient exchange settings": {
			environment: map[string]string{
				envServiceAccountTokenFile: "token",
				config.EnvAPIEndpoint:      "invalid",
				envTokenDuration:           "invalid",
				envExchangeTimeout:         "invalid",
			},
			expectedError: ErrSourceOnly,
		},
		"command only": {
			environment:   map[string]string{envTokenCommand: "token-helper"},
			expectedError: ErrSourceOnly,
		},
		"conflicting sources": {
			environment: map[string]string{envServiceAccountTokenFile: "token", envTokenCommand: "token-helper"},
			expectedError: errors.New("invalid workload identity configuration: COREWEAVE_SERVICE_ACCOUNT_ID is required; COREWEAVE_SERVICE_ACCOUNT_ORG_ID is required; " +
				"configure exactly one of COREWEAVE_SERVICE_ACCOUNT_TOKEN_FILE and COREWEAVE_SERVICE_ACCOUNT_TOKEN_CMD"),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			provider, err := Detect(func(name string) string { return tt.environment[name] })
			require.EqualError(t, err, tt.expectedError.Error())
			assert.Equal(t, errors.Is(tt.expectedError, ErrSourceOnly), errors.Is(err, ErrSourceOnly))
			assert.Nil(t, provider)
		})
	}
}

func TestFromEnvironmentRequiresIDsWithTokenSource(t *testing.T) {
	t.Parallel()

	environment := map[string]string{envServiceAccountTokenFile: "token"}
	provider, err := FromEnvironment(Options{}, func(name string) string { return environment[name] })
	require.EqualError(t, err, "invalid workload identity configuration: COREWEAVE_SERVICE_ACCOUNT_ID is required; COREWEAVE_SERVICE_ACCOUNT_ORG_ID is required")
	assert.NotErrorIs(t, err, ErrSourceOnly)
	assert.Nil(t, provider)
}

func TestTokenExchangeConnectHonorsRetryAfter(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().UTC().Add(time.Hour)
	reply, err := protojson.Marshal(directoryv1alpha.ServiceAccountOidcAuthResponse_builder{
		BearerToken: proto.String("minted-token"),
		ExpireTime:  timestamppb.New(expiresAt),
	}.Build())
	require.NoError(t, err)
	expectedRequest := directoryv1alpha.ServiceAccountOidcAuthRequest_builder{
		OrgUid:            proto.String("test-org"),
		ServiceAccountUid: proto.String("test-account"),
		OidcToken:         proto.String(testOIDCToken),
		DurationSeconds:   proto.Int64(1800),
	}.Build()
	rateLimited := connect.NewError(connect.CodeResourceExhausted, errors.New("sensitive server message"))
	for _, message := range []proto.Message{
		&anypb.Any{TypeUrl: "type.googleapis.com/example.Unknown", Value: []byte{8, 1}},
		&errdetails.ErrorInfo{Reason: "RATE_LIMITED"},
	} {
		detail, err := connect.NewErrorDetail(message)
		require.NoError(t, err)
		rateLimited.AddDetail(detail)
	}
	errorWriter := connect.NewErrorWriter()
	retryAfterDate := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	var attempts, sourceCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/coreweave.auth.v1alpha.TokenService/ServiceAccountOidcAuth", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "1", r.Header.Get("Connect-Protocol-Version"))
		var request directoryv1alpha.ServiceAccountOidcAuthRequest
		body, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) || !assert.NoError(t, protojson.Unmarshal(body, &request)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Empty(t, cmp.Diff(expectedRequest, &request, protocmp.Transform()))

		switch attempts.Add(1) {
		case 1, 3:
			w.Header().Set("Retry-After", "0")
			assert.NoError(t, errorWriter.Write(w, r, rateLimited))
		case 2:
			w.Header().Set("Retry-After", retryAfterDate.Format(http.TimeFormat))
			http.Error(w, "gateway unavailable", http.StatusServiceUnavailable)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(reply)
		}
	}))
	t.Cleanup(server.Close)

	provider, err := New(Options{
		ServiceAccountID:  "test-account",
		OrganizationID:    "test-org",
		Endpoint:          server.URL,
		RequestedLifetime: 30 * time.Minute,
		ExchangeTimeout:   5 * time.Second,
		Transport:         server.Client().Transport,
		TokenSource: func(context.Context) (string, error) {
			sourceCalls.Add(1)
			return testOIDCToken, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(provider.Close)
	assert.NoError(t, provider.ctx.Err())
	assert.Zero(t, sourceCalls.Load())
	assert.Zero(t, attempts.Load())

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	_, err = provider.exchange(ctx, testOIDCToken)
	var exchangeErr *ExchangeError
	require.ErrorAs(t, err, &exchangeErr)
	require.EqualError(t, err, "auth: token exchange failed (HTTP 429)")
	assert.Equal(t, http.StatusTooManyRequests, exchangeErr.StatusCode)
	assert.Equal(t, "RATE_LIMITED", exchangeErr.Reason)
	assert.Nil(t, errors.Unwrap(exchangeErr))
	assert.False(t, exchangeRetryAt(err).IsZero(), "Retry-After: 0 overrides backoff")
	_, err = provider.exchange(ctx, testOIDCToken)
	require.ErrorAs(t, err, &exchangeErr)
	assert.Equal(t, http.StatusServiceUnavailable, exchangeErr.StatusCode)
	assert.WithinDuration(t, retryAfterDate, exchangeRetryAt(err), 0)
	assert.Positive(t, exchangeErr.RetryAfter)

	credential, err := provider.exchangeWithRetry()
	require.NoError(t, err)
	assert.Empty(t, cmp.Diff(providers.Credential{Token: "minted-token", ExpiresAt: expiresAt}, credential))
	assert.EqualValues(t, 4, attempts.Load())
	assert.EqualValues(t, 1, sourceCalls.Load())

	provider.Close()
	assertReusesExchangeConnection(t, server.Client(), server.URL)
}

func TestTokenExchangeHTTPResponses(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	reply := `{"bearerToken":"minted-token","futureField":"ignored","expireTime":"` + expiresAt.Format(time.RFC3339) + `"}`
	tests := map[string]struct {
		status           int
		body             string
		keepBodyOpen     bool
		expectedResponse providers.Credential
		expectedError    error
		shouldRetry      bool
	}{
		"accepts a JSON reply over HTTP/1.1": {
			status:           http.StatusOK,
			body:             reply,
			expectedResponse: providers.Credential{Token: "minted-token", ExpiresAt: expiresAt},
		},
		"rejects an oversized reply while the response is still open": {
			status:        http.StatusOK,
			body:          reply + strings.Repeat(" ", 64<<10),
			keepBodyOpen:  true,
			expectedError: errors.New("auth: invalid token exchange response"),
		},
		"HTTP 500 stays terminal despite a retryable code in its body": {
			status:        http.StatusInternalServerError,
			body:          `{"code":"unavailable","message":"gateway unavailable","details":[null]}`,
			expectedError: errors.New("auth: token exchange failed (HTTP 500)"),
		},
		"retries HTTP 502 from a gateway": {
			status:        http.StatusBadGateway,
			expectedError: errors.New("auth: token exchange failed (HTTP 502)"),
			shouldRetry:   true,
		},
		"retries HTTP 503 from a gateway despite its JSON error code": {
			status:        http.StatusServiceUnavailable,
			body:          `{"code":"internal","message":"gateway unavailable"}`,
			expectedError: errors.New("auth: token exchange failed (HTTP 503)"),
			shouldRetry:   true,
		},
		"reports HTTP 504 from a Directory error": {
			status:        http.StatusGatewayTimeout,
			body:          `{"code":"deadline_exceeded","message":"server deadline exceeded"}`,
			expectedError: errors.New("auth: token exchange failed (HTTP 504)"),
			shouldRetry:   true,
		},
		"refuses to replay the token to a redirect": {
			status:        http.StatusTemporaryRedirect,
			expectedError: errors.New("auth: token exchange failed (HTTP 307)"),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			release := make(chan struct{})
			defer close(release)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, 1, r.ProtoMajor)
				assert.Equal(t, "/tenant%2Fsandbox/coreweave.auth.v1alpha.TokenService/ServiceAccountOidcAuth", r.URL.EscapedPath())
				w.Header().Set("Content-Type", "application/json")
				if tt.status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/redirected")
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
				if tt.keepBodyOpen {
					assert.NoError(t, http.NewResponseController(w).Flush())
					<-release
				}
			}))
			t.Cleanup(server.Close)

			provider, err := New(Options{
				ServiceAccountID: "test-account",
				OrganizationID:   "test-org",
				Endpoint:         server.URL + "/tenant%2Fsandbox/",
				TokenSource:      func(context.Context) (string, error) { return testOIDCToken, nil },
			})
			require.NoError(t, err)
			t.Cleanup(provider.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			credential, err := provider.exchange(ctx, testOIDCToken)
			if tt.expectedError != nil {
				require.EqualError(t, err, tt.expectedError.Error())
				var exchangeErr *ExchangeError
				require.ErrorAs(t, err, &exchangeErr)
				assert.Equal(t, tt.status, exchangeErr.StatusCode)
				assert.Equal(t, tt.shouldRetry, retryableExchangeError(err))
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, cmp.Diff(tt.expectedResponse, credential))
			assert.EqualValues(t, 1, requests.Load())
		})
	}
}

func TestWorkloadIdentitySoftAndHardCallersShareRetryAndWarmup(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		initialExpiry := time.Now().Add(30 * time.Minute)
		initial := providers.Credential{Token: "initial", ExpiresAt: initialExpiry}
		replacement := providers.Credential{Token: "replacement", ExpiresAt: initialExpiry.Add(time.Hour)}
		var exchanges atomic.Int32
		provider := newTestWorkloadProvider(t, func(context.Context, string) (providers.Credential, error) {
			switch exchanges.Add(1) {
			case 1:
				return initial, nil
			case 2:
				return providers.Credential{}, &ExchangeError{StatusCode: http.StatusTooManyRequests, retryAt: time.Now()}
			default:
				return replacement, nil
			}
		})
		provider.options.ExchangeTimeout = time.Second

		started := time.Now()
		credential, err := provider.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Empty(t, cmp.Diff(initial, credential))
		assert.Equal(t, 10*time.Second, time.Since(started))

		provider.mu.Lock()
		cached := provider.cached
		provider.mu.Unlock()
		assert.Equal(t, initial, cached.Credential)
		softAt := cached.SoftRefreshAt
		require.GreaterOrEqual(t, initialExpiry.Sub(softAt), 5*time.Minute)
		require.LessOrEqual(t, initialExpiry.Sub(softAt), 7*time.Minute)
		time.Sleep(time.Until(softAt))
		credential, err = provider.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Empty(t, cmp.Diff(initial, credential))
		synctest.Wait()
		assert.EqualValues(t, 1, exchanges.Load(), "credential remains live at the soft threshold")

		time.Sleep(time.Until(initialExpiry.Add(-2*time.Minute - 5*time.Second)))
		credential, err = provider.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Empty(t, cmp.Diff(initial, credential))
		synctest.Wait()
		assert.EqualValues(t, 3, exchanges.Load())

		time.Sleep(6 * time.Second)
		started = time.Now()
		credential, err = provider.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Empty(t, cmp.Diff(replacement, credential))
		assert.Equal(t, 4*time.Second, time.Since(started))
		assert.EqualValues(t, 3, exchanges.Load())

		provider.Close()
		assert.Equal(t, cachedCredential{}, provider.cached)
	})
}

// The one-minute hold applies to both refresh windows and includes warm-up.
func TestWorkloadIdentityDefersRefreshForFirstMinute(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		lifetime     time.Duration
		expectedWait time.Duration
	}{
		"soft refresh at the minimum lifetime": {lifetime: 5 * time.Minute},
		"hard refresh":                         {lifetime: 2 * time.Minute, expectedWait: 10 * time.Second},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				receivedAt := time.Now()
				initial := providers.Credential{Token: "initial", ExpiresAt: receivedAt.Add(test.lifetime)}
				replacement := providers.Credential{Token: "replacement", ExpiresAt: receivedAt.Add(15 * time.Minute)}
				var exchanges atomic.Int32
				provider := newTestWorkloadProvider(t, func(context.Context, string) (providers.Credential, error) {
					if exchanges.Add(1) == 1 {
						return initial, nil
					}
					return replacement, nil
				})

				credential, err := provider.Retrieve(t.Context())
				require.NoError(t, err)
				assert.Equal(t, initial, credential)
				assert.Equal(t, 10*time.Second, time.Since(receivedAt))

				time.Sleep(time.Until(receivedAt.Add(time.Minute - time.Nanosecond)))
				credential, err = provider.Retrieve(t.Context())
				require.NoError(t, err)
				assert.Equal(t, initial, credential)
				synctest.Wait()
				assert.EqualValues(t, 1, exchanges.Load())

				time.Sleep(time.Nanosecond)
				refreshStarted := time.Now()
				_, err = provider.Retrieve(t.Context())
				require.NoError(t, err)
				assert.Equal(t, test.expectedWait, time.Since(refreshStarted))
				synctest.Wait()
				assert.EqualValues(t, 2, exchanges.Load())
			})
		})
	}
}

func TestWorkloadIdentityDiscardsExpiredCredentialOnAccess(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		expiry := time.Now().Add(11 * time.Second)
		initial := providers.Credential{Token: "initial", ExpiresAt: expiry}
		failure := errors.New("replacement exchange failed")
		var provider *Provider
		var exchanges atomic.Int32
		provider = newTestWorkloadProvider(t, func(context.Context, string) (providers.Credential, error) {
			if exchanges.Add(1) == 1 {
				return initial, nil
			}
			provider.mu.Lock()
			cached := provider.cached
			provider.mu.Unlock()
			assert.Equal(t, cachedCredential{}, cached)
			return providers.Credential{}, failure
		})

		credential, err := provider.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, initial, credential)

		time.Sleep(time.Until(expiry))
		synctest.Wait()
		assert.Equal(t, initial, provider.cached.Credential)

		credential, err = provider.Retrieve(t.Context())
		require.ErrorIs(t, err, failure)
		assert.Equal(t, providers.Credential{}, credential)
	})
}

func TestWorkloadIdentityRetriesOutliveCallerCancellation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		var attempts []time.Duration
		var flightStarted time.Time
		failure := errors.New("unavailable")
		provider := newTestWorkloadProvider(t, func(ctx context.Context, _ string) (providers.Credential, error) {
			attempts = append(attempts, time.Since(started))
			attempt := len(attempts)
			deadline, ok := ctx.Deadline()
			if !assert.True(t, ok, "the exchange has a deadline") {
				return providers.Credential{}, failure
			}
			if (attempt-1)%6 == 0 {
				flightStarted = time.Now()
			}
			assert.Equal(t, flightStarted.Add(2*time.Minute), deadline)
			if attempt == 1 {
				time.Sleep(10 * time.Second)
				assert.NoError(t, ctx.Err())
				return providers.Credential{}, &ExchangeError{StatusCode: http.StatusGatewayTimeout}
			}
			if attempt == 2 {
				return providers.Credential{}, &ExchangeError{cause: errExchangeRequestFailed}
			}
			var retryAt time.Time
			if attempt == 12 {
				retryAt = time.Now().Add(time.Minute)
			}
			return providers.Credential{}, &ExchangeError{StatusCode: http.StatusServiceUnavailable, cause: failure, retryAt: retryAt}
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cancelTimer := time.AfterFunc(time.Second, cancel)
		defer cancelTimer.Stop()

		_, err := provider.Retrieve(ctx)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, time.Second, time.Since(started))

		// Let the first retry run before another caller joins.
		time.Sleep(time.Until(started.Add(13 * time.Second)))
		_, err = provider.Retrieve(t.Context())
		require.ErrorIs(t, err, failure)
		assert.Equal(t, []time.Duration{
			0, 12 * time.Second, 16 * time.Second, 24 * time.Second, 40 * time.Second, 72 * time.Second,
		}, attempts)

		for i, delay := range []time.Duration{10 * time.Second, time.Minute} {
			time.Sleep(delay - time.Nanosecond)
			_, err = provider.Retrieve(t.Context())
			require.ErrorIs(t, err, failure)
			var exchangeErr *ExchangeError
			require.ErrorAs(t, err, &exchangeErr)
			assert.Equal(t, time.Nanosecond, exchangeErr.RetryAfter)

			time.Sleep(time.Nanosecond)
			restartedAt := time.Since(started)
			_, err = provider.Retrieve(t.Context())
			require.ErrorIs(t, err, failure)
			require.Len(t, attempts, (i+2)*6)
			assert.Equal(t, restartedAt, attempts[(i+1)*6])
		}
	})
}

func TestExchangeTimeoutIncludesTokenReadAndRetryWait(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		provider, err := New(Options{
			ServiceAccountID: "test-account",
			OrganizationID:   "test-org",
			ExchangeTimeout:  5 * time.Second,
			TokenSource: func(context.Context) (string, error) {
				time.Sleep(time.Second)
				return testOIDCToken, nil
			},
		})
		require.NoError(t, err)
		t.Cleanup(provider.Close)

		var calls atomic.Int32
		provider.exchange = func(ctx context.Context, _ string) (providers.Credential, error) {
			calls.Add(1)
			assert.Equal(t, time.Second, time.Since(started))
			deadline, ok := ctx.Deadline()
			assert.True(t, ok)
			assert.Equal(t, started.Add(5*time.Second), deadline)
			return providers.Credential{}, &ExchangeError{
				StatusCode: http.StatusTooManyRequests,
				retryAt:    time.Now().Add(time.Minute),
			}
		}

		credential, err := provider.Retrieve(t.Context())
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, providers.Credential{}, credential)
		assert.Equal(t, 5*time.Second, time.Since(started))

		expected := &ExchangeError{
			RetryAfter: 56 * time.Second,
			cause:      context.DeadlineExceeded,
			retryAt:    started.Add(61 * time.Second),
		}
		var exchangeErr *ExchangeError
		require.ErrorAs(t, err, &exchangeErr)
		assert.Equal(t, expected, exchangeErr)

		credential, err = provider.Retrieve(t.Context())
		require.ErrorAs(t, err, &exchangeErr)
		assert.Equal(t, expected, exchangeErr)
		assert.Equal(t, providers.Credential{}, credential)
		assert.EqualValues(t, 1, calls.Load())
	})
}

func TestDetectedWorkloadIdentityCanCloseBeforeFirstUse(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		envServiceAccountID:        "test-account",
		config.EnvOrganizationID:   "test-org",
		envServiceAccountTokenFile: filepath.Join(t.TempDir(), "unused-oidc-token"),
	}
	provider, err := Detect(func(name string) string { return env[name] })
	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.NoError(t, provider.ctx.Err())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	credential, err := provider.Retrieve(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, providers.Credential{}, credential)

	provider.Close()
	provider.Close()

	credential, err = provider.Retrieve(t.Context())
	require.ErrorIs(t, err, providers.ErrClosed)
	assert.Equal(t, providers.Credential{}, credential)
	assert.ErrorIs(t, provider.ctx.Err(), context.Canceled)
}

func newTestWorkloadProvider(t *testing.T, exchange tokenExchange) *Provider {
	t.Helper()
	provider, err := New(Options{
		ServiceAccountID: "test-account",
		OrganizationID:   "test-org",
		TokenSource: func(context.Context) (string, error) {
			return testOIDCToken, nil
		},
	})
	require.NoError(t, err)
	provider.exchange = exchange
	t.Cleanup(provider.Close)
	return provider
}

func assertReusesExchangeConnection(t *testing.T, client *http.Client, endpoint string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var reused atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) },
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	assert.True(t, reused.Load(), "the transport should keep the idle exchange connection after provider Close")
}
