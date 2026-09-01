package coreweave

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
)

type retriesDisabledContextKey struct{}

func withoutRetries(ctx context.Context) context.Context {
	return context.WithValue(ctx, retriesDisabledContextKey{}, true)
}

var (
	// A regular expression to match the error returned by net/http when the
	// configured number of redirects is exhausted. This error isn't typed
	// specifically so we resort to matching on the error string.
	redirectsErrorRe = regexp.MustCompile(`stopped after \d+ redirects\z`)

	// A regular expression to match the error returned by net/http when the
	// scheme specified in the URL is invalid. This error isn't typed
	// specifically so we resort to matching on the error string.
	schemeErrorRe = regexp.MustCompile(`unsupported protocol scheme`)

	// A regular expression to match the error returned by net/http when a
	// request header or value is invalid. This error isn't typed
	// specifically so we resort to matching on the error string.
	invalidHeaderErrorRe = regexp.MustCompile(`invalid header`)

	// A regular expression to match the error returned by net/http when the
	// TLS certificate is not trusted. This error isn't typed
	// specifically so we resort to matching on the error string.
	notTrustedErrorRe = regexp.MustCompile(`certificate is not trusted`)
)

func baseRetryPolicy(resp *http.Response, err error) (bool, error) {
	if err != nil {
		if permanentErr := permanentRequestError(err); permanentErr != nil {
			return false, permanentErr
		}

		// The error is likely recoverable so retry.
		return true, nil
	}

	// 429 Too Many Requests is recoverable. Sometimes the server puts
	// a Retry-After response header to indicate when the server is
	// available to start processing request from client.
	if resp.StatusCode == http.StatusTooManyRequests {
		return true, nil
	}

	// Check the response code. We retry on 500-range responses to allow
	// the server time to recover, as 500's are typically not permanent
	// errors and may relate to outages on the server side. This will catch
	// invalid response codes as well, like 0 and 999.
	if resp.StatusCode == 0 || (resp.StatusCode >= 500 && resp.StatusCode != http.StatusNotImplemented) {
		return true, fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}

	return false, nil
}

// permanentRequestError returns the request error when retrying cannot change
// the outcome, such as malformed requests, exhausted redirects, or certificate
// verification failures.
func permanentRequestError(err error) error {
	var certificateErr *tls.CertificateVerificationError
	if errors.As(err, &certificateErr) {
		return err
	}

	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return nil
	}

	if redirectsErrorRe.MatchString(urlErr.Error()) ||
		schemeErrorRe.MatchString(urlErr.Error()) ||
		invalidHeaderErrorRe.MatchString(urlErr.Error()) ||
		notTrustedErrorRe.MatchString(urlErr.Error()) {
		return urlErr
	}

	return nil
}

// IsPermanentRequestError reports whether retrying a malformed request,
// exhausted redirect, or certificate-verification failure cannot change the
// outcome.
func IsPermanentRequestError(err error) bool {
	return permanentRequestError(err) != nil
}

func RetryPolicy(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if ctx.Err() != nil {
		// do not retry on context.Canceled errors
		if errors.Is(ctx.Err(), context.Canceled) {
			return false, ctx.Err()
		}

		// context.DeadlineExceeded is retried to handle intermittent timeouts
		return true, ctx.Err()
	}
	if disabled, _ := ctx.Value(retriesDisabledContextKey{}).(bool); disabled {
		return false, nil
	}

	// Retry an attempt-level timeout with a freshly resolved token.
	if errors.Is(err, context.DeadlineExceeded) {
		return true, err
	}

	// Token sources own their retry policy; do not retry their other failures.
	if auth.IsTokenSourceError(err) {
		return false, err
	}

	return baseRetryPolicy(resp, err)
}
