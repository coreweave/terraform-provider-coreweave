package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	TerraformCloudWorkloadIdentityTokenEnvVar = "TFC_WORKLOAD_IDENTITY_TOKEN" //nolint:gosec
	// TODO: fix the path once make public
	serviceAccountOIDCAuthProcedure = "/coreweave.directory.v1alpha.InternalDirectoryService/ServiceAccountOidcAuth"
	requestedTokenDuration          = time.Hour
	refreshBeforeExpiry             = time.Minute
	exchangeErrorResponseLimit      = 8 << 10

	exchangeRetryMax     = 2
	exchangeRetryWaitMin = 100 * time.Millisecond
	exchangeRetryWaitMax = 500 * time.Millisecond

	// The exchange is a sub-operation of the API request that needed the token
	// and runs on that request's context, so it may spend only part of that
	// request's budget. The rest has to remain for the request itself.
	exchangeBudgetNumerator   = 3
	exchangeBudgetDenominator = 4

	// A first exchange pays TCP connect, the TLS handshake, and the server's
	// own JWKS fetch and JWT validation. Attempts shorter than this time out on
	// ordinary cold-start latency, so the budget buys fewer attempts instead.
	minExchangeAttemptTimeout = 3 * time.Second
)

// WorkloadIdentityTokenSource exchanges HCP Terraform's workload identity
// token for a short-lived CoreWeave service-account token.
type WorkloadIdentityTokenSource struct {
	client            *http.Client
	budget            time.Duration
	endpoint          string
	serviceAccountUID string
	userAgent         string

	mu        sync.Mutex
	token     string
	refreshAt time.Time
	refresh   *tokenRefresh
	now       func() time.Time
	getenv    func(string) (string, bool)
}

// CacheIdentity identifies the target service-account principal without
// retaining its UID in the process-wide S3 client cache.
func (s *WorkloadIdentityTokenSource) CacheIdentity() string {
	return fmt.Sprintf("workload-identity:%x", sha256.Sum256([]byte(s.serviceAccountUID)))
}

type tokenRefresh struct {
	done  chan struct{}
	token string
	err   error

	// callerScoped records that err came from the leader's own request context
	// rather than from the exchange itself, so a waiter with time left should
	// take over instead of inheriting it.
	callerScoped bool
}

// NewWorkloadIdentityTokenSource creates a refreshable service-account token source.
func NewWorkloadIdentityTokenSource(ctx context.Context, endpoint, serviceAccountUID, userAgent string, timeout time.Duration) (*WorkloadIdentityTokenSource, error) {
	if strings.TrimSpace(serviceAccountUID) == "" {
		return nil, errors.New("authentication.workload_identity.service_account_uid must not be empty")
	}

	budget := exchangeBudget(timeout)
	if plan := planExchange(budget); plan.retryMax == 0 {
		// Say so rather than silently degrading: a token source error is not
		// retried anywhere above this client, so with no retries here a single
		// blip on the auth endpoint fails the operation outright.
		tflog.Warn(ctx, "http_timeout is too short to retry the workload identity token exchange; a single transient failure will fail the operation", map[string]any{
			"http_timeout":        timeout.String(),
			"exchange_budget":     budget.String(),
			"minimum_per_attempt": minExchangeAttemptTimeout.String(),
		})
	}
	source := &WorkloadIdentityTokenSource{
		client:            newWorkloadIdentityHTTPClient(budget),
		budget:            budget,
		endpoint:          strings.TrimRight(endpoint, "/") + serviceAccountOIDCAuthProcedure,
		serviceAccountUID: serviceAccountUID,
		userAgent:         userAgent,
		now:               time.Now,
		getenv:            os.LookupEnv,
	}
	if _, err := source.externalOIDCToken(); err != nil {
		return nil, err
	}
	return source, nil
}

func newWorkloadIdentityHTTPClient(budget time.Duration) *http.Client {
	plan := planExchange(budget)

	client := retryablehttp.NewClient()
	client.Logger = nil
	client.RetryMax = plan.retryMax
	client.RetryWaitMin = exchangeRetryWaitMin
	client.RetryWaitMax = exchangeRetryWaitMax
	client.Backoff = exchangeBackoff
	client.CheckRetry = exchangeRetryPolicy
	client.ErrorHandler = retryablehttp.PassthroughErrorHandler
	client.HTTPClient.Timeout = plan.attemptTimeout
	client.HTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client.StandardClient()
}

// exchangeRetryPolicy retries only what a second attempt can plausibly fix:
// transport failures, and the statuses that explicitly mean "try again".
//
// retryablehttp's DefaultRetryPolicy is deliberately not used. It retries every
// 5xx except 501, which would include HTTP 500 -- a deterministic server fault
// that connectCodeForHTTPStatus already classifies as CodeInternal, and which
// repeating only delays the failure.
func exchangeRetryPolicy(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		// A transport failure may not survive a second attempt. Returning it
		// here would end the sequence, so report only the decision to retry.
		return true, nil //nolint:nilerr
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true, nil
	default:
		return false, nil
	}
}

// exchangeBackoff waits a random interval in [min, exponential], which spreads
// concurrent provider runs out instead of having them all retry in step as
// retryablehttp's unjittered DefaultBackoff would. The result never exceeds
// max, which is the per-retry allowance planExchange reserved from the budget.
func exchangeBackoff(minWait, maxWait time.Duration, attemptNum int, _ *http.Response) time.Duration {
	// attemptNum+1 doublings so the first retry is jittered too: starting at
	// minWait exactly would make every concurrent caller's first retry
	// simultaneous, which is the case jitter exists to break up.
	ceiling := minWait
	for range attemptNum + 1 {
		if ceiling >= maxWait/2 {
			ceiling = maxWait
			break
		}
		ceiling *= 2
	}
	if ceiling > maxWait {
		ceiling = maxWait
	}
	if ceiling <= minWait {
		return minWait
	}
	// Jitter only has to decorrelate concurrent retries, not resist an
	// adversary, so a non-cryptographic source is the right tool.
	return minWait + time.Duration(rand.Int64N(int64(ceiling-minWait)+1)) //nolint:gosec
}

// exchangeBudget is the slice of the caller's HTTP timeout the whole exchange,
// retries and backoff included, is allowed to spend.
func exchangeBudget(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 0
	}
	return timeout * exchangeBudgetNumerator / exchangeBudgetDenominator
}

// exchangePlan is how the exchange spends its budget: how many retries it can
// afford and how long each attempt gets.
type exchangePlan struct {
	retryMax       int
	attemptTimeout time.Duration
}

// planExchange fits as many attempts into the budget as it can while keeping
// each one long enough to be useful, reserving the worst-case backoff between
// them. Trading retries for a workable per-attempt timeout matters more than
// the retry count: an attempt too short to complete a handshake fails every
// time, so three of them are worth less than one that can succeed.
func planExchange(budget time.Duration) exchangePlan {
	if budget <= 0 {
		return exchangePlan{retryMax: exchangeRetryMax}
	}

	for retries := exchangeRetryMax; retries > 0; retries-- {
		backoff := time.Duration(retries) * exchangeRetryWaitMax
		perAttempt := (budget - backoff) / time.Duration(retries+1)
		if perAttempt >= minExchangeAttemptTimeout {
			return exchangePlan{retryMax: retries, attemptTimeout: perAttempt}
		}
	}

	// Not enough budget to retry and still give each attempt a real chance;
	// spend it all on one.
	return exchangePlan{retryMax: 0, attemptTimeout: budget}
}

// Token returns a cached token or refreshes it before expiration.
func (s *WorkloadIdentityTokenSource) Token(ctx context.Context) (string, error) {
	for {
		s.mu.Lock()
		if s.liveTokenLocked() {
			token := s.token
			s.mu.Unlock()
			return token, nil
		}
		if refresh := s.refresh; refresh != nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-refresh.done:
				// A leader whose own request was canceled abandoned the
				// A leader that failed because its own caller went away learned
				// nothing about this caller, so take over and try. That is
				// bounded: it takes one departing caller per re-election.
				//
				// A failure of the exchange itself is inherited. It spent the
				// budget every waiter's exchange would spend, so taking over
				// would repeat the whole retry sequence once per waiter -- the
				// amplification single-flighting exists to prevent -- and per
				// the AccessTokenSource contract such an error is final.
				if refresh.err != nil && ctx.Err() == nil && refresh.callerScoped {
					continue
				}
				return refresh.token, refresh.err
			}
		}

		refresh := &tokenRefresh{done: make(chan struct{})}
		s.refresh = refresh
		s.mu.Unlock()

		var expiration time.Time
		refresh.token, expiration, refresh.err = s.refreshToken(ctx)
		// The exchange runs on a context derived from this caller's, so its
		// deadline is min(caller's remaining time, budget). Only the leader can
		// tell which of the two ran out, and waiters need to know: a caller
		// that ran out of time says nothing about them.
		refresh.callerScoped = refresh.err != nil && ctx.Err() != nil

		s.mu.Lock()
		if refresh.err == nil {
			s.token = refresh.token
			s.refreshAt = refreshAt(s.now(), expiration)
		}
		s.refresh = nil
		close(refresh.done)
		s.mu.Unlock()
		return refresh.token, refresh.err
	}
}

func (s *WorkloadIdentityTokenSource) refreshToken(ctx context.Context) (string, time.Time, error) {
	oidcToken, err := s.externalOIDCToken()
	if err != nil {
		return "", time.Time{}, err
	}

	token, expiration, err := s.exchange(ctx, oidcToken)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiration, nil
}

// liveTokenLocked reports whether the cached token exists and is not yet due
// for replacement. Callers must hold s.mu.
func (s *WorkloadIdentityTokenSource) liveTokenLocked() bool {
	return s.token != "" && s.now().Before(s.refreshAt)
}

// refreshAt returns when a token issued now and expiring at expireTime should
// be replaced: refreshBeforeExpiry ahead of expiry, or a quarter of the
// lifetime for a token too short-lived to spare that much.
//
// A fixed window cannot be used alone. The server may issue a shorter lifetime
// than requested, and if that lifetime is at or below the window, every token
// would be due for replacement the moment it arrived and each HTTP attempt
// would mint another one.
func refreshAt(now, expireTime time.Time) time.Time {
	window := refreshBeforeExpiry
	if quarter := expireTime.Sub(now) / 4; window > quarter {
		window = quarter
	}
	return expireTime.Add(-window)
}

func (s *WorkloadIdentityTokenSource) externalOIDCToken() (string, error) {
	token, ok := s.getenv(TerraformCloudWorkloadIdentityTokenEnvVar)
	if !ok {
		return "", fmt.Errorf("%s is not set; configure HCP Terraform dynamic provider credentials for this run", TerraformCloudWorkloadIdentityTokenEnvVar)
	}
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("%s is empty; configure HCP Terraform dynamic provider credentials for this run", TerraformCloudWorkloadIdentityTokenEnvVar)
	}
	if err := validateJWTStructure(token); err != nil {
		return "", fmt.Errorf("%s is not a well-formed JWT: %w", TerraformCloudWorkloadIdentityTokenEnvVar, err)
	}
	return token, nil
}

func validateJWTStructure(token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("expected three dot-separated segments")
	}
	for i, part := range parts {
		if part == "" {
			return fmt.Errorf("segment %d is empty", i+1)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			return fmt.Errorf("segment %d is not base64url encoded", i+1)
		}
		if i < 2 {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(decoded, &object); err != nil || object == nil {
				return fmt.Errorf("segment %d is not a JSON object", i+1)
			}
		}
	}
	return nil
}

type serviceAccountOIDCAuthRequest struct {
	OIDCToken         string `json:"oidcToken"`
	ServiceAccountUID string `json:"serviceAccountUid"`
	DurationSeconds   string `json:"durationSeconds"`
}

// serviceAccountOIDCAuthResponse mirrors
// coreweave.directory.v1alpha.ServiceAccountOidcAuthResponse, whose fields are
// bearer_token and expire_time.
type serviceAccountOIDCAuthResponse struct {
	BearerToken string
	ExpireTime  time.Time
}

// UnmarshalJSON accepts both protojson spellings of each field. A Connect
// handler emits the lowerCamelCase JSON names by default, but the same message
// marshaled with UseProtoNames arrives in snake_case.
func (r *serviceAccountOIDCAuthResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		BearerToken      string     `json:"bearerToken"`
		BearerTokenProto string     `json:"bearer_token"`
		ExpireTime       *time.Time `json:"expireTime"`
		ExpireTimeProto  *time.Time `json:"expire_time"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	r.BearerToken = wire.BearerToken
	if r.BearerToken == "" {
		r.BearerToken = wire.BearerTokenProto
	}
	switch {
	case wire.ExpireTime != nil:
		r.ExpireTime = *wire.ExpireTime
	case wire.ExpireTimeProto != nil:
		r.ExpireTime = *wire.ExpireTimeProto
	}
	return nil
}

type connectErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type exchangeHTTPError struct {
	statusCode int
	code       connect.Code
	hasCode    bool
	message    string
}

func (e *exchangeHTTPError) Error() string {
	if e.hasCode {
		if e.message != "" {
			return fmt.Sprintf("workload identity token exchange returned HTTP %d (%s): %s", e.statusCode, e.code, e.message)
		}
		return fmt.Sprintf("workload identity token exchange returned HTTP %d (%s)", e.statusCode, e.code)
	}
	if e.message != "" {
		return fmt.Sprintf("workload identity token exchange returned HTTP %d: %s", e.statusCode, e.message)
	}
	return fmt.Sprintf("workload identity token exchange returned HTTP %d", e.statusCode)
}

func (e *exchangeHTTPError) tokenSourceCode() (connect.Code, bool) {
	return e.code, e.hasCode
}

func (s *WorkloadIdentityTokenSource) exchange(ctx context.Context, oidcToken string) (string, time.Time, error) {
	payload, err := json.Marshal(serviceAccountOIDCAuthRequest{
		OIDCToken:         oidcToken,
		ServiceAccountUID: s.serviceAccountUID,
		DurationSeconds:   fmt.Sprintf("%d", int64(requestedTokenDuration/time.Second)),
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("encoding workload identity token exchange request: %w", err)
	}

	// Cap the exchange at its share of the caller's deadline so that the
	// request that needed the token still has time left to be made. Deriving
	// from ctx keeps the caller's own cancellation and deadline in force.
	if s.budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.budget)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("creating workload identity token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	if s.userAgent != "" {
		req.Header.Set("User-Agent", s.userAgent)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("exchanging workload identity token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", time.Time{}, exchangeResponseError(resp, s.secrets(oidcToken))
	}

	var result serviceAccountOIDCAuthResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", time.Time{}, fmt.Errorf("decoding workload identity token exchange response: %w", err)
	}
	if result.BearerToken == "" {
		return "", time.Time{}, errors.New("workload identity token exchange returned an empty bearer token")
	}
	if result.ExpireTime.IsZero() || !result.ExpireTime.After(s.now()) {
		return "", time.Time{}, errors.New("workload identity token exchange returned a missing or expired expire_time")
	}

	return result.BearerToken, result.ExpireTime, nil
}

func exchangeResponseError(resp *http.Response, secrets []string) error {
	exchangeErr := &exchangeHTTPError{statusCode: resp.StatusCode}
	body, err := io.ReadAll(io.LimitReader(resp.Body, exchangeErrorResponseLimit))
	if err == nil {
		var connectErr connectErrorResponse
		if json.Unmarshal(body, &connectErr) == nil {
			if strings.TrimSpace(connectErr.Message) != "" {
				exchangeErr.message = redactSecrets(strings.Join(strings.Fields(connectErr.Message), " "), secrets)
			}
			// connect.Code.UnmarshalText parses "unknown" successfully, but a
			// code of unknown tells an operator nothing. Treat it as absent so
			// the HTTP status below can supply something actionable.
			var code connect.Code
			if connectErr.Code != "" && code.UnmarshalText([]byte(connectErr.Code)) == nil && code != connect.CodeUnknown {
				exchangeErr.code = code
				exchangeErr.hasCode = true
			}
		}
	}
	if !exchangeErr.hasCode {
		exchangeErr.code, exchangeErr.hasCode = connectCodeForHTTPStatus(resp.StatusCode)
	}
	return exchangeErr
}

// secrets lists the credential material this exchange handles, so that a
// server message echoing any of it can be scrubbed before it reaches a
// diagnostic. The cached bearer token is included because a message quoting the
// Authorization header of a prior call would otherwise leak it.
func (s *WorkloadIdentityTokenSource) secrets(oidcToken string) []string {
	segments := strings.Split(oidcToken, ".")
	secrets := make([]string, 0, len(segments)*3+2)
	secrets = append(secrets, oidcToken)
	for i, segment := range segments {
		secrets = append(secrets, segment)
		if i >= 2 {
			continue
		}

		decoded, err := base64.RawURLEncoding.DecodeString(segment)
		if err != nil {
			continue
		}
		decodedText := string(decoded)
		secrets = append(secrets, decodedText, strings.Join(strings.Fields(decodedText), " "))
	}

	s.mu.Lock()
	secrets = append(secrets, s.token)
	s.mu.Unlock()

	return secrets
}

// minRedactableSecret is the shortest fragment worth scrubbing. Shorter ones
// are as likely to appear by coincidence in ordinary prose as to be credential
// material, and redacting those would corrupt the message without protecting
// anything.
const minRedactableSecret = 8

// redactSecrets removes credential material a server echoed back to us. The
// exchange sends an external assertion and receives a bearer token; neither may
// reach a Terraform diagnostic, however the server chose to phrase its error.
func redactSecrets(message string, secrets []string) string {
	for _, secret := range secrets {
		if len(secret) < minRedactableSecret {
			continue
		}
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	return message
}

func connectCodeForHTTPStatus(status int) (connect.Code, bool) {
	switch status {
	case http.StatusBadRequest:
		return connect.CodeInvalidArgument, true
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated, true
	case http.StatusForbidden:
		return connect.CodePermissionDenied, true
	case http.StatusNotFound:
		return connect.CodeNotFound, true
	case http.StatusTooManyRequests:
		return connect.CodeResourceExhausted, true
	case http.StatusNotImplemented:
		return connect.CodeUnimplemented, true
	case http.StatusInternalServerError:
		// A deterministic server-side fault, unlike the transient pair below.
		return connect.CodeInternal, true
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return connect.CodeUnavailable, true
	case http.StatusGatewayTimeout:
		return connect.CodeDeadlineExceeded, true
	default:
		return connect.CodeUnknown, false
	}
}
