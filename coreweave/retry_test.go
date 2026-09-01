package coreweave

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaseRetryPolicyRejectsWrappedPermanentRequestErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]error{
		"redirect exhaustion": &url.Error{Op: "Get", URL: "https://example.test", Err: errors.New("stopped after 10 redirects")},
		"invalid scheme":      &url.Error{Op: "Get", URL: "example.test", Err: errors.New("unsupported protocol scheme")},
		"invalid header":      &url.Error{Op: "Get", URL: "https://example.test", Err: errors.New("invalid header field value")},
		"certificate": &url.Error{
			Op:  "Get",
			URL: "https://example.test",
			Err: &tls.CertificateVerificationError{Err: errors.New("certificate is not trusted")},
		},
	}

	for name, requestErr := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			retry, err := baseRetryPolicy(nil, requestErr)
			assert.False(t, retry)
			require.ErrorIs(t, err, requestErr)
		})
	}
}

func TestRetryPolicyTransportErrors(t *testing.T) {
	t.Parallel()

	urlErr := func(err error) error {
		return &url.Error{Op: "Get", URL: "https://api.example.test", Err: err}
	}

	tests := map[string]struct {
		err       error
		wantRetry bool
	}{
		"too many redirects":          {err: urlErr(errors.New("stopped after 10 redirects"))},
		"unsupported protocol scheme": {err: urlErr(errors.New(`unsupported protocol scheme "ftp"`))},
		"invalid header":              {err: urlErr(errors.New("net/http: invalid header field value"))},
		"untrusted certificate": {
			err: urlErr(&tls.CertificateVerificationError{Err: errors.New("certificate is not trusted")}),
		},
		"connection reset": {err: urlErr(errors.New("read: connection reset by peer")), wantRetry: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			retry, err := RetryPolicy(t.Context(), nil, test.err)
			assert.Equal(t, test.wantRetry, retry)
			if !test.wantRetry {
				require.Error(t, err, "non-retryable transport errors must be surfaced to the caller")
			}
		})
	}
}

func TestRetryPolicyResponses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status    int
		wantRetry bool
	}{
		"too many requests":   {status: http.StatusTooManyRequests, wantRetry: true},
		"service unavailable": {status: http.StatusServiceUnavailable, wantRetry: true},
		"not implemented":     {status: http.StatusNotImplemented},
		"unauthorized":        {status: http.StatusUnauthorized},
		"ok":                  {status: http.StatusOK},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			retry, _ := RetryPolicy(t.Context(), &http.Response{StatusCode: test.status}, nil)
			assert.Equal(t, test.wantRetry, retry)
		})
	}
}
