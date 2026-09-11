package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/hashicorp/terraform-plugin-log/tflogtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testJWT() string {
	encode := base64.RawURLEncoding.EncodeToString
	return encode([]byte(`{"alg":"RS256"}`)) + "." + encode([]byte(`{"sub":"run"}`)) + "." + encode([]byte("signature"))
}

func TestWorkloadIdentityTokenSourceCacheIdentity(t *testing.T) {
	t.Parallel()

	first := &WorkloadIdentityTokenSource{serviceAccountUID: "service-account-one"}
	same := &WorkloadIdentityTokenSource{serviceAccountUID: "service-account-one"}
	different := &WorkloadIdentityTokenSource{serviceAccountUID: "service-account-two"}

	assert.Equal(t, first.CacheIdentity(), same.CacheIdentity())
	assert.NotEqual(t, first.CacheIdentity(), different.CacheIdentity())
	assert.NotContains(t, first.CacheIdentity(), "service-account-one")
}

func TestNewWorkloadIdentityTokenSourceValidatesConfiguration(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		uid       string
		token     string
		present   bool
		wantError string
	}{
		"missing service account UID":  {uid: " ", token: testJWT(), present: true, wantError: "service_account_uid must not be empty"},
		"missing environment variable": {uid: "sa-uid", present: false, wantError: "is not set"},
		"empty environment variable":   {uid: "sa-uid", token: " ", present: true, wantError: "is empty"},
		"malformed JWT":                {uid: "sa-uid", token: "not-a-jwt", present: true, wantError: "not a well-formed JWT"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source := &WorkloadIdentityTokenSource{
				serviceAccountUID: test.uid,
				getenv: func(string) (string, bool) {
					return test.token, test.present
				},
			}

			if test.uid == " " {
				_, err := NewWorkloadIdentityTokenSource(t.Context(), "https://api.example.test", test.uid, "", time.Second)
				require.ErrorContains(t, err, test.wantError)
				return
			}
			_, err := source.externalOIDCToken()
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestWorkloadIdentityTokenSourceExchangesCachesAndRefreshes(t *testing.T) {
	var calls atomic.Int32
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/coreweave.directory.v1alpha.InternalDirectoryService/ServiceAccountOidcAuth", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "1", r.Header.Get("Connect-Protocol-Version"))
		assert.Equal(t, "test-agent", r.Header.Get("User-Agent"))
		assert.Empty(t, r.Header.Get("Authorization"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, fmt.Sprintf(`{"oidcToken":%q,"serviceAccountUid":"sa-uid","durationSeconds":"3600"}`, testJWT()), string(body))

		call := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"bearerToken":"token-%d","expireTime":"%s"}`, call, now.Add(time.Hour).Format(time.RFC3339))
	}))
	defer server.Close()

	source := &WorkloadIdentityTokenSource{
		client:            server.Client(),
		endpoint:          server.URL + serviceAccountOIDCAuthProcedure,
		serviceAccountUID: "sa-uid",
		userAgent:         "test-agent",
		now:               func() time.Time { return now },
		getenv:            func(string) (string, bool) { return testJWT(), true },
	}

	token, err := source.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "token-1", token)

	token, err = source.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "token-1", token)
	assert.EqualValues(t, 1, calls.Load())

	now = now.Add(59*time.Minute + time.Second)
	token, err = source.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "token-2", token)
	assert.EqualValues(t, 2, calls.Load())
}

// The exchange speaks JSON to a Connect handler, so the field names are the
// protojson names of coreweave.directory.v1alpha.ServiceAccountOidcAuth{Request,Response}:
// oidc_token, service_account_uid, duration_seconds (int64) and bearer_token,
// expire_time. Asserting the raw JSON keys keeps a rename from going unnoticed,
// which decoding into the client's own structs would not.
func TestWorkloadIdentityExchangeMatchesProtoJSONNames(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		_, _ = fmt.Fprint(w, `{"bearerToken":"minted","expireTime":"2099-01-01T00:00:00Z"}`)
	}))
	defer server.Close()

	source := &WorkloadIdentityTokenSource{
		client:            server.Client(),
		endpoint:          server.URL,
		serviceAccountUID: "sa-uid",
		now:               time.Now,
		getenv:            func(string) (string, bool) { return testJWT(), true },
	}
	token, err := source.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "minted", token)

	assert.Equal(t, []string{"durationSeconds", "oidcToken", "serviceAccountUid"}, sortedKeys(request))
	assert.Equal(t, testJWT(), request["oidcToken"])
	assert.Equal(t, "sa-uid", request["serviceAccountUid"])
	// protojson renders int64 as a decimal string, and the field is capped at
	// 28800 (8 hours) by the server's validation rules.
	assert.Equal(t, "3600", request["durationSeconds"])
}

// The exchange runs on the context of the API request that needed the token,
// so it must leave that request time to actually be made.
// How the caller's timeout is divided. The exchange must leave room for the
// request that needed the token, every attempt plus its backoff must fit in
// what is left, and an attempt too short to finish a handshake is worth less
// than a retry, so a small budget buys fewer attempts rather than shorter ones.
func TestExchangeBudgetAndPlan(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		timeout     time.Duration
		wantRetries bool
	}{
		"no timeout":              {timeout: 0, wantRetries: false},
		"tiny timeout":            {timeout: time.Millisecond, wantRetries: false},
		"below the attempt floor": {timeout: 5 * time.Second, wantRetries: false},
		"provider default":        {timeout: 10 * time.Second, wantRetries: true},
		"generous timeout":        {timeout: 30 * time.Second, wantRetries: true},
		"very generous timeout":   {timeout: 2 * time.Minute, wantRetries: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			budget := exchangeBudget(test.timeout)
			plan := planExchange(budget)

			if test.timeout == 0 {
				assert.Zero(t, budget)
				assert.Zero(t, plan.attemptTimeout)
				return
			}

			assert.Less(t, budget, test.timeout, "the exchange must not spend the caller's whole deadline")
			assert.Positive(t, plan.attemptTimeout)
			assert.LessOrEqual(t, plan.attemptTimeout, budget)
			assert.LessOrEqual(t, plan.retryMax, exchangeRetryMax)

			worstCase := time.Duration(plan.retryMax+1)*plan.attemptTimeout +
				time.Duration(plan.retryMax)*exchangeRetryWaitMax
			assert.LessOrEqual(t, worstCase, budget, "attempts plus backoff must fit inside the budget")

			assert.Equal(t, test.wantRetries, plan.retryMax > 0)
			if test.wantRetries {
				assert.GreaterOrEqual(t, plan.attemptTimeout, minExchangeAttemptTimeout)
			} else {
				// Nothing is gained by splitting a budget this small.
				assert.Equal(t, budget, plan.attemptTimeout)
			}
		})
	}

	// A larger budget buys more retries, not just longer attempts.
	assert.Greater(t, planExchange(exchangeBudget(2*time.Minute)).retryMax,
		planExchange(exchangeBudget(10*time.Second)).retryMax)
}

func TestExchangeResponseErrorCodeMapping(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status   int
		body     string
		wantCode connect.Code
	}{
		"code from body": {
			status: http.StatusForbidden, body: `{"code":"permission_denied","message":"denied"}`,
			wantCode: connect.CodePermissionDenied,
		},
		// "unknown" parses successfully but tells an operator nothing, so the
		// status has to supply the code instead.
		"unknown code falls back to status": {
			status: http.StatusUnauthorized, body: `{"code":"unknown","message":"nope"}`,
			wantCode: connect.CodeUnauthenticated,
		},
		"internal server error is not transient": {
			status: http.StatusInternalServerError, body: `{}`,
			wantCode: connect.CodeInternal,
		},
		"bad gateway is transient": {
			status: http.StatusBadGateway, body: `{}`,
			wantCode: connect.CodeUnavailable,
		},
		"service unavailable is transient": {
			status: http.StatusServiceUnavailable, body: `{}`,
			wantCode: connect.CodeUnavailable,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body))}
			code, ok := tokenSourceConnectCodeValue(&TokenSourceError{err: exchangeResponseError(resp, nil)})

			require.True(t, ok, "the exchange error must carry a Connect code")
			assert.Equal(t, test.wantCode, code)
		})
	}
}

// newTestTokenSource builds a source pointed at server with the exchange
// plumbing stubbed out, for tests about a single response rather than retries.
func newTestTokenSource(server *httptest.Server) *WorkloadIdentityTokenSource {
	return &WorkloadIdentityTokenSource{
		client:            server.Client(),
		endpoint:          server.URL,
		serviceAccountUID: "sa-uid",
		now:               time.Now,
		getenv:            func(string) (string, bool) { return testJWT(), true },
	}
}

func writeMintedToken(w http.ResponseWriter) {
	_, _ = fmt.Fprintf(w, `{"bearerToken":"minted","expireTime":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
}

// breakConnection drops the TCP connection mid-response, which reaches the
// client as a transport error rather than an HTTP status.
func breakConnection(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unavailable", http.StatusInternalServerError)
		return
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijacking failed", http.StatusInternalServerError)
		return
	}
	_ = connection.Close()
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Which exchange failures buy another attempt. A transient answer or a broken
// connection is worth repeating; a rejection is the server's final word, and
// repeating it only delays the failure.
func TestWorkloadIdentityTokenSourceRetriesOnlyTransientFailures(t *testing.T) {
	permanent := func(status int) func(int32, http.ResponseWriter) {
		return func(_ int32, w http.ResponseWriter) {
			w.WriteHeader(status)
			_, _ = fmt.Fprint(w, `{"code":"permission_denied","message":"exchange rejected"}`)
		}
	}

	tests := map[string]struct {
		respond   func(attempt int32, w http.ResponseWriter)
		wantCalls int32
		wantErr   string
	}{
		"service unavailable then success": {
			respond: func(attempt int32, w http.ResponseWriter) {
				if attempt < 3 {
					http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
					return
				}
				writeMintedToken(w)
			},
			wantCalls: 3,
		},
		"broken connection then success": {
			respond: func(attempt int32, w http.ResponseWriter) {
				if attempt == 1 {
					breakConnection(w)
					return
				}
				writeMintedToken(w)
			},
			wantCalls: 2,
		},
		"malformed identity":      {respond: permanent(http.StatusBadRequest), wantCalls: 1, wantErr: "HTTP 400"},
		"invalid identity":        {respond: permanent(http.StatusUnauthorized), wantCalls: 1, wantErr: "HTTP 401"},
		"trust expression denied": {respond: permanent(http.StatusForbidden), wantCalls: 1, wantErr: "HTTP 403"},
		"service account missing": {respond: permanent(http.StatusNotFound), wantCalls: 1, wantErr: "HTTP 404"},
		// Deterministic, unlike the 502/503/504 the policy does retry.
		"internal server error": {respond: permanent(http.StatusInternalServerError), wantCalls: 1, wantErr: "HTTP 500"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(TerraformCloudWorkloadIdentityTokenEnvVar, testJWT())

			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				test.respond(calls.Add(1), w)
			}))
			defer server.Close()

			// Long enough for the budget to afford the full retry count while
			// keeping each attempt above minExchangeAttemptTimeout; a shorter
			// timeout deliberately buys fewer retries.
			source, err := NewWorkloadIdentityTokenSource(t.Context(), server.URL, "sa-uid", "", 30*time.Second)
			require.NoError(t, err)
			token, err := source.Token(t.Context())

			if test.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, "minted", token)
			} else {
				require.ErrorContains(t, err, test.wantErr)
			}
			assert.Equal(t, test.wantCalls, calls.Load())
		})
	}
}

func TestWorkloadIdentityTokenSourceCanceledWaiterDoesNotBlock(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = fmt.Fprint(w, `{"bearerToken":"token","expireTime":"2099-01-01T00:00:00Z"}`)
	}))
	defer server.Close()

	source := &WorkloadIdentityTokenSource{
		client:            server.Client(),
		endpoint:          server.URL,
		serviceAccountUID: "sa-uid",
		now:               time.Now,
		getenv:            func(string) (string, bool) { return testJWT(), true },
	}
	leaderDone := make(chan error, 1)
	go func() {
		_, err := source.Token(context.Background())
		leaderDone <- err
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waiterDone := make(chan error, 1)
	go func() {
		_, err := source.Token(ctx)
		waiterDone <- err
	}()

	select {
	case err := <-waiterDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(500 * time.Millisecond):
		close(release)
		t.Fatal("canceled token waiter remained blocked behind the active refresh")
	}

	close(release)
	require.NoError(t, <-leaderDone)
}

// Everything the exchange must make of a 2xx body, and every way it must
// refuse one. The field names are the protojson names of
// coreweave.directory.v1alpha.ServiceAccountOidcAuthResponse.
func TestWorkloadIdentityTokenSourceDecodesExchangeResponse(t *testing.T) {
	tests := map[string]struct {
		status    int
		body      string
		wantToken string
		wantError string
	}{
		"lowerCamelCase names": {
			status: http.StatusOK, body: `{"bearerToken":"minted","expireTime":"2099-01-01T00:00:00Z"}`,
			wantToken: "minted",
		},
		// A handler configured with UseProtoNames emits snake_case instead.
		"proto names": {
			status: http.StatusOK, body: `{"bearer_token":"minted","expire_time":"2099-01-01T00:00:00Z"}`,
			wantToken: "minted",
		},
		"server error": {
			status: http.StatusServiceUnavailable, body: `{}`,
			wantError: "HTTP 503",
		},
		"connect error": {
			status: http.StatusForbidden, body: `{"code":"permission_denied","message":"OIDC trust expression did not match"}`,
			wantError: "HTTP 403 (permission_denied): OIDC trust expression did not match",
		},
		"invalid JSON": {
			status: http.StatusOK, body: `{`,
			wantError: "decoding",
		},
		"empty bearer token": {
			status: http.StatusOK, body: `{"expireTime":"2099-01-01T00:00:00Z"}`,
			wantError: "empty bearer token",
		},
		"missing expire time": {
			status: http.StatusOK, body: `{"bearerToken":"token"}`,
			wantError: "missing or expired expire_time",
		},
		"expired expire time": {
			status: http.StatusOK, body: `{"bearerToken":"token","expireTime":"2000-01-01T00:00:00Z"}`,
			wantError: "missing or expired expire_time",
		},
		// The old, incorrect field name must not be silently accepted.
		"unrecognized expiry field": {
			status: http.StatusOK, body: `{"bearerToken":"token","expiration":"2099-01-01T00:00:00Z"}`,
			wantError: "missing or expired expire_time",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			source := newTestTokenSource(server)
			token, err := source.Token(t.Context())

			if test.wantError == "" {
				require.NoError(t, err)
				assert.Equal(t, test.wantToken, token)
				return
			}
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

// A server may issue a shorter lifetime than requested. If the fixed refresh
// window were applied blindly, such a token would be due for replacement the
// moment it arrived and every attempt would mint another one.
func TestRefreshAtScalesToShortLifetimes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

	for _, lifetime := range []time.Duration{time.Hour, 15 * time.Minute, time.Minute, 30 * time.Second, time.Second} {
		expire := now.Add(lifetime)
		got := refreshAt(now, expire)

		assert.True(t, got.After(now), "lifetime %s must leave a usable window", lifetime)
		assert.True(t, got.Before(expire), "lifetime %s must refresh before expiry", lifetime)
	}

	// A long-lived token still uses the full fixed window.
	assert.Equal(t, now.Add(time.Hour-refreshBeforeExpiry), refreshAt(now, now.Add(time.Hour)))
}

func TestShortLivedTokenIsNotReExchangedEveryCall(t *testing.T) {
	var calls atomic.Int32
	now := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)

	// A 30-second lifetime: shorter than refreshBeforeExpiry.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprintf(w, `{"bearerToken":"token","expireTime":%q}`, now.Add(30*time.Second).Format(time.RFC3339))
	}))
	defer server.Close()

	source := &WorkloadIdentityTokenSource{
		client:            server.Client(),
		endpoint:          server.URL,
		serviceAccountUID: "sa-uid",
		now:               func() time.Time { return now },
		getenv:            func(string) (string, bool) { return testJWT(), true },
	}

	for range 5 {
		token, err := source.Token(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "token", token)
	}
	assert.EqualValues(t, 1, calls.Load(), "a short-lived token must still be cached, not re-minted per call")
}

// The single-flight contract: concurrent callers that find no live token must
// coalesce onto one exchange and all receive its result.
func TestTokenSingleFlightsConcurrentRefreshes(t *testing.T) {
	const callers = 32

	var exchanges atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exchanges.Add(1)
		<-release // hold the exchange open until every caller is waiting
		_, _ = fmt.Fprint(w, `{"bearerToken":"minted","expireTime":"2099-01-01T00:00:00Z"}`)
	}))
	defer server.Close()

	source := &WorkloadIdentityTokenSource{
		client:            server.Client(),
		endpoint:          server.URL,
		serviceAccountUID: "sa-uid",
		now:               time.Now,
		getenv:            func(string) (string, bool) { return testJWT(), true },
	}

	start := make(chan struct{})
	tokens := make(chan string, callers)
	errs := make(chan error, callers)
	var running sync.WaitGroup
	for range callers {
		running.Add(1)
		go func() {
			defer running.Done()
			<-start
			token, err := source.Token(t.Context())
			tokens <- token
			errs <- err
		}()
	}

	close(start)
	// Let the callers pile up on the in-flight refresh before it completes.
	assert.Eventually(t, func() bool { return exchanges.Load() == 1 }, time.Second, time.Millisecond)
	close(release)
	running.Wait()

	for range callers {
		require.NoError(t, <-errs)
		assert.Equal(t, "minted", <-tokens)
	}
	assert.EqualValues(t, 1, exchanges.Load(), "concurrent callers must share one exchange")

	// A later caller is served from the cache, not by another exchange.
	token, err := source.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "minted", token)
	assert.EqualValues(t, 1, exchanges.Load())
}

func TestExchangeRetryPolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status    int
		err       error
		wantRetry bool
	}{
		"transport error":      {err: errors.New("connection reset by peer"), wantRetry: true},
		"too many requests":    {status: http.StatusTooManyRequests, wantRetry: true},
		"bad gateway":          {status: http.StatusBadGateway, wantRetry: true},
		"service unavailable":  {status: http.StatusServiceUnavailable, wantRetry: true},
		"gateway timeout":      {status: http.StatusGatewayTimeout, wantRetry: true},
		"internal server":      {status: http.StatusInternalServerError, wantRetry: false},
		"not implemented":      {status: http.StatusNotImplemented, wantRetry: false},
		"insufficient storage": {status: http.StatusInsufficientStorage, wantRetry: false},
		"unauthorized":         {status: http.StatusUnauthorized, wantRetry: false},
		"forbidden":            {status: http.StatusForbidden, wantRetry: false},
		"not found":            {status: http.StatusNotFound, wantRetry: false},
		"ok":                   {status: http.StatusOK, wantRetry: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var resp *http.Response
			if test.err == nil {
				resp = &http.Response{StatusCode: test.status}
			}
			retry, err := exchangeRetryPolicy(t.Context(), resp, test.err)
			require.NoError(t, err)
			assert.Equal(t, test.wantRetry, retry)
		})
	}
}

func TestExchangeBackoffIsJitteredAndBounded(t *testing.T) {
	t.Parallel()

	for attempt := range exchangeRetryMax + 1 {
		// Distinct values within a single attempt number: an unjittered
		// exponential schedule varies across attempts but is fixed within one.
		seen := map[time.Duration]struct{}{}
		for range 200 {
			wait := exchangeBackoff(exchangeRetryWaitMin, exchangeRetryWaitMax, attempt, nil)
			assert.GreaterOrEqual(t, wait, exchangeRetryWaitMin, "attempt %d", attempt)
			assert.LessOrEqual(t, wait, exchangeRetryWaitMax, "attempt %d", attempt)
			seen[wait] = struct{}{}
		}
		assert.Greater(t, len(seen), 1, "attempt %d must be jittered, not a fixed wait", attempt)
	}
}

// A server message is copied into a Terraform diagnostic, so credential
// material it echoes back must never survive into the error.
func TestExchangeErrorRedactsCredentialMaterial(t *testing.T) {
	oidcToken := testJWT()
	const bearerToken = "cw-bearer-token-value"
	claims, err := base64.RawURLEncoding.DecodeString(strings.Split(oidcToken, ".")[1])
	require.NoError(t, err)

	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if request == nil {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			_, _ = fmt.Fprintf(w, `{"bearerToken":%q,"expireTime":"2099-01-01T00:00:00Z"}`, bearerToken)
			return
		}
		// A server that echoes everything back: the assertion, its decoded
		// claims, and the token it previously issued.
		w.WriteHeader(http.StatusForbidden)
		message := fmt.Sprintf("rejected assertion %s with claims %s for bearer %s", oidcToken, claims, bearerToken)
		_, _ = fmt.Fprintf(w, `{"code":"permission_denied","message":%q}`, message)
	}))
	defer server.Close()

	now := time.Now()
	source := &WorkloadIdentityTokenSource{
		client:            server.Client(),
		endpoint:          server.URL,
		serviceAccountUID: "sa-uid",
		now:               func() time.Time { return now },
		getenv:            func(string) (string, bool) { return oidcToken, true },
	}

	token, err := source.Token(t.Context())
	require.NoError(t, err)
	require.Equal(t, bearerToken, token)

	// Force a refresh so the second, echoing response is the one that errors.
	now = now.Add(100 * 365 * 24 * time.Hour)
	_, err = source.Token(t.Context())

	require.Error(t, err)
	assert.NotContains(t, err.Error(), oidcToken, "the external assertion must not reach diagnostics")
	assert.NotContains(t, err.Error(), string(claims), "decoded assertion claims must not reach diagnostics")
	assert.NotContains(t, err.Error(), bearerToken, "the issued token must not reach diagnostics")
	// The operator still learns why it failed.
	assert.Contains(t, err.Error(), "permission_denied")
	assert.Contains(t, err.Error(), "[REDACTED]")
}

// A leader that exhausts its exchange budget has learned something true for
// every waiter, so waiters must inherit that failure rather than each re-running
// the full retry sequence in turn.
func TestTokenDoesNotReRunExchangeAfterLeaderDeadline(t *testing.T) {
	const callers = 8

	var exchanges atomic.Int32
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		select {
		case <-blocked:
		case <-r.Context().Done():
		}
		_, _ = fmt.Fprint(w, `{"bearerToken":"minted","expireTime":"2099-01-01T00:00:00Z"}`)
	}))
	defer server.Close()
	// Runs before server.Close (defers are LIFO), which would otherwise block
	// waiting for the handler this channel releases.
	defer close(blocked)

	source := &WorkloadIdentityTokenSource{
		// A budget small enough that the blocked handler exhausts it.
		client:            newExchangeClientFor(t, server, 150*time.Millisecond),
		budget:            150 * time.Millisecond,
		endpoint:          server.URL,
		serviceAccountUID: "sa-uid",
		now:               time.Now,
		getenv:            func(string) (string, bool) { return testJWT(), true },
	}

	start := make(chan struct{})
	errs := make(chan error, callers)
	var running sync.WaitGroup
	for range callers {
		running.Add(1)
		go func() {
			defer running.Done()
			<-start
			_, err := source.Token(t.Context())
			errs <- err
		}()
	}
	close(start)
	running.Wait()

	for range callers {
		require.ErrorIs(t, <-errs, context.DeadlineExceeded)
	}
	assert.LessOrEqual(t, exchanges.Load(), int32(1),
		"waiters must inherit the leader's deadline, not each re-run the exchange")
}

func newExchangeClientFor(t *testing.T, server *httptest.Server, budget time.Duration) *http.Client {
	t.Helper()

	client := server.Client()
	client.Timeout = planExchange(budget).attemptTimeout
	return client
}

// A leader whose own caller ran out of time learned nothing about the waiters,
// so a waiter with time left must take over rather than inherit that deadline.
func TestTokenReElectsWhenLeaderCallerExpires(t *testing.T) {
	var exchanges atomic.Int32
	leaderArrived := make(chan struct{})
	releaseLeader := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if exchanges.Add(1) == 1 {
			// Hold the first exchange open past its caller's short deadline.
			close(leaderArrived)
			<-releaseLeader
			return
		}
		_, _ = fmt.Fprint(w, `{"bearerToken":"minted","expireTime":"2099-01-01T00:00:00Z"}`)
	}))
	defer server.Close()
	// Runs before server.Close (defers are LIFO), which would otherwise block
	// waiting for the held handler.
	defer close(releaseLeader)

	source := &WorkloadIdentityTokenSource{
		client:            server.Client(),
		endpoint:          server.URL,
		serviceAccountUID: "sa-uid",
		now:               time.Now,
		getenv:            func(string) (string, bool) { return testJWT(), true },
	}

	// The leader's own request context expires well before the budget would.
	leaderCtx, cancelLeader := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancelLeader()

	var running sync.WaitGroup
	running.Add(1)
	go func() {
		defer running.Done()
		_, err := source.Token(leaderCtx)
		assert.ErrorIs(t, err, context.DeadlineExceeded, "the leader inherits its own caller's deadline")
	}()

	// A waiter with a full-length context, arriving while the leader is in flight.
	<-leaderArrived
	token, err := source.Token(t.Context())

	require.NoError(t, err, "a waiter with time left must not inherit the leader's caller deadline")
	assert.Equal(t, "minted", token)
	assert.EqualValues(t, 2, exchanges.Load(), "the waiter must run its own exchange")
	running.Wait()
}

// A short http_timeout silently buys zero exchange retries, and nothing above
// this client retries a token source error, so the operator has to be told.
func TestNewWorkloadIdentityTokenSourceWarnsWhenRetriesAreDisabled(t *testing.T) {
	t.Setenv(TerraformCloudWorkloadIdentityTokenEnvVar, testJWT())

	tests := map[string]struct {
		timeout      time.Duration
		wantRetries  bool
		wantWarnings int
	}{
		"short timeout disables retries": {timeout: 5 * time.Second, wantRetries: false, wantWarnings: 1},
		"default timeout keeps retries":  {timeout: 10 * time.Second, wantRetries: true, wantWarnings: 0},
		"long timeout keeps retries":     {timeout: 30 * time.Second, wantRetries: true, wantWarnings: 0},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			logs := &bytes.Buffer{}
			ctx := tflogtest.RootLogger(t.Context(), logs)

			_, err := NewWorkloadIdentityTokenSource(ctx, "https://api.example.test", "sa-uid", "", test.timeout)
			require.NoError(t, err)

			plan := planExchange(exchangeBudget(test.timeout))
			assert.Equal(t, test.wantRetries, plan.retryMax > 0)

			entries, err := tflogtest.MultilineJSONDecode(logs)
			require.NoError(t, err)
			warnings := 0
			for _, entry := range entries {
				if entry["@level"] == "warn" && strings.Contains(entry["@message"].(string), "too short to retry") {
					warnings++
				}
			}
			assert.Equal(t, test.wantWarnings, warnings)
		})
	}
}
