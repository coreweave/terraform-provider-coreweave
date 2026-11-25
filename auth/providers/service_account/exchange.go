package serviceaccount

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"buf.build/gen/go/coreweave/directory/connectrpc/go/coreweave/auth/v1alpha/authv1alphaconnect"
	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
)

const (
	maxExchangeResponseBytes = 64 << 10
	retryAfterHeader         = "Retry-After"
)

var (
	exchangeReasonPattern      = regexp.MustCompile("^[A-Z][A-Z0-9_]{0,61}[A-Z0-9]$")
	errExchangeRequestFailed   = errors.New("token exchange request failed")
	errInvalidExchangeRequest  = errors.New("cannot encode token exchange request")
	errInvalidExchangeResponse = errors.New("invalid token exchange response")
)

var exchangeBackoff = [...]time.Duration{
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	32 * time.Second,
}

// ExchangeError describes a failed token exchange without retaining the response
// body, token material, or the server's free-form error message.
type ExchangeError struct {
	// StatusCode is the HTTP response status, or zero if there was no response.
	StatusCode int
	Reason     string
	RetryAfter time.Duration

	cause   error
	retryAt time.Time
}

func (e *ExchangeError) Error() string {
	if e.cause != nil {
		return "auth: " + e.cause.Error()
	}
	if e.StatusCode != 0 {
		return "auth: token exchange failed (HTTP " + strconv.Itoa(e.StatusCode) + ")"
	}
	return "auth: token exchange failed"
}

func (e *ExchangeError) Unwrap() error { return e.cause }

type tokenExchange func(context.Context, string) (providers.Credential, error)

func (p *Provider) newTokenExchange() tokenExchange {
	httpClient := &http.Client{
		Transport: p.options.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	client := authv1alphaconnect.NewTokenServiceClient(
		exchangeHTTPClient{httpClient},
		p.options.Endpoint,
		connect.WithProtoJSON(),
		connect.WithReadMaxBytes(maxExchangeResponseBytes),
	)
	return func(ctx context.Context, oidcToken string) (providers.Credential, error) {
		// Connect can close an unsuccessful send with an empty POST.
		if !utf8.ValidString(oidcToken) || !utf8.ValidString(p.options.ServiceAccountID) || !utf8.ValidString(p.options.OrganizationID) {
			return providers.Credential{}, newExchangeError(ctx, 0, "", errInvalidExchangeRequest, time.Time{})
		}
		request := connect.NewRequest(directoryv1alpha.ServiceAccountOidcAuthRequest_builder{
			OidcToken:         proto.String(oidcToken),
			ServiceAccountUid: proto.String(p.options.ServiceAccountID),
			OrgUid:            proto.String(p.options.OrganizationID),
			DurationSeconds:   proto.Int64(int64(p.options.RequestedLifetime / time.Second)),
		}.Build())
		var metadata atomic.Pointer[exchangeHTTPResponse]
		response, err := client.ServiceAccountOidcAuth(context.WithValue(ctx, exchangeHTTPResponseKey{}, &metadata), request)
		if err != nil {
			return providers.Credential{}, exchangeResponseError(ctx, err, metadata.Load())
		}

		token := response.Msg.GetBearerToken()
		expiry := response.Msg.GetExpireTime()
		if ctx.Err() == nil && strings.TrimSpace(token) != "" && expiry != nil &&
			expiry.CheckValid() == nil && expiry.AsTime().After(time.Now()) {
			return providers.Credential{Token: token, ExpiresAt: expiry.AsTime()}, nil
		}
		return providers.Credential{}, newExchangeError(ctx, http.StatusOK, "", errInvalidExchangeResponse, time.Time{})
	}
}

type exchangeHTTPResponseKey struct{}

type exchangeHTTPResponse struct {
	status  int
	retryAt time.Time
}

// exchangeHTTPClient captures HTTP metadata before Connect decodes the response.
// A gateway can return a plain HTTP error with a Retry-After header.
type exchangeHTTPClient struct{ *http.Client }

func (c exchangeHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.Client.Do(request) //nolint:gosec // The validated, caller-configured API endpoint is intentional.
	if response != nil {
		// Connect may drain an oversized response after rejecting it. Bound
		// those reads while leaving one extra byte to detect an oversized body.
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(response.Body, maxExchangeResponseBytes+1), response.Body}
	}
	if capture, ok := request.Context().Value(exchangeHTTPResponseKey{}).(*atomic.Pointer[exchangeHTTPResponse]); ok {
		metadata := &exchangeHTTPResponse{}
		if response != nil {
			metadata.status = response.StatusCode
			if retryableHTTPStatus(metadata.status) {
				metadata.retryAt = parseExchangeRetryAfter(response.Header.Get(retryAfterHeader), time.Now())
			}
		}
		capture.Store(metadata)
	}
	return response, err
}

func exchangeResponseError(ctx context.Context, err error, response *exchangeHTTPResponse) *ExchangeError {
	if response == nil {
		return newExchangeError(ctx, 0, "", errInvalidExchangeRequest, time.Time{})
	}
	switch response.status {
	case 0:
		return newExchangeError(ctx, 0, "", errExchangeRequestFailed, time.Time{})
	case http.StatusOK:
		return newExchangeError(ctx, response.status, "", errInvalidExchangeResponse, time.Time{})
	default:
		return newExchangeError(ctx, response.status, exchangeErrorReason(err), nil, response.retryAt)
	}
}

func exchangeErrorReason(err error) string {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		for _, detail := range connectErr.Details() {
			if detail == nil || detail.Type() != "google.rpc.ErrorInfo" {
				continue
			}
			var info errdetails.ErrorInfo
			if proto.Unmarshal(detail.Bytes(), &info) == nil && exchangeReasonPattern.MatchString(info.GetReason()) {
				return info.GetReason()
			}
			break
		}
	}
	return ""
}

func newExchangeError(ctx context.Context, status int, reason string, cause error, retryAt time.Time) *ExchangeError {
	if err := ctx.Err(); err != nil {
		cause, reason = err, ""
	}
	return &ExchangeError{
		StatusCode: status,
		Reason:     reason,
		RetryAfter: max(0, time.Until(retryAt)),
		cause:      cause,
		retryAt:    retryAt,
	}
}

func (p *Provider) exchangeWithRetry() (providers.Credential, error) {
	ctx, cancel := context.WithTimeout(p.ctx, p.options.ExchangeTimeout)
	defer cancel()

	oidcToken, err := p.readOIDCToken(ctx)
	if err != nil {
		return providers.Credential{}, err
	}
	for attempt := 0; ; attempt++ {
		credential, err := p.exchange(ctx, oidcToken)
		if err == nil || ctx.Err() != nil || attempt == len(exchangeBackoff) || !retryableExchangeError(err) {
			return credential, err
		}

		delay := exchangeBackoff[attempt]
		retryAt := exchangeRetryAt(err)
		if !retryAt.IsZero() {
			delay = time.Until(retryAt)
		}
		if err := waitForExchange(ctx, delay); err != nil {
			return providers.Credential{}, newExchangeError(ctx, 0, "", nil, retryAt)
		}
	}
}

func retryableExchangeError(err error) bool {
	var exchangeErr *ExchangeError
	return errors.As(err, &exchangeErr) &&
		(retryableHTTPStatus(exchangeErr.StatusCode) || errors.Is(exchangeErr.cause, errExchangeRequestFailed))
}

func retryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func exchangeRetryAt(err error) time.Time {
	var exchangeErr *ExchangeError
	if errors.As(err, &exchangeErr) {
		return exchangeErr.retryAt
	}
	return time.Time{}
}

func withRemainingRetryDelay(err error, now, retryAt time.Time) error {
	var exchangeErr *ExchangeError
	if !errors.As(err, &exchangeErr) {
		return err
	}
	result := *exchangeErr
	result.RetryAfter = max(0, retryAt.Sub(now))
	return &result
}

func parseExchangeRetryAfter(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value != "" && strings.Trim(value, "0123456789") == "" {
		seconds, err := strconv.ParseUint(value, 10, 64)
		// Saturate an oversized wait so the duration cannot wrap into an early
		// retry. Normal waits are measured from receipt.
		if err != nil || seconds > uint64(math.MaxInt64/int64(time.Second)) {
			return now.Add(time.Duration(math.MaxInt64))
		}
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if when, err := http.ParseTime(value); err == nil {
		if when.Before(now) {
			return now
		}
		return when
	}
	return time.Time{}
}
