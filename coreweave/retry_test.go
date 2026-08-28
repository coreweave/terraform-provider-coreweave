package coreweave

import (
	"crypto/tls"
	"errors"
	"net/url"
	"testing"
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
			if retry {
				t.Fatal("baseRetryPolicy() marked a permanent request error retryable")
			}
			if !errors.Is(err, requestErr) {
				t.Fatalf("baseRetryPolicy() error = %v, want wrapped request error %v", err, requestErr)
			}
		})
	}
}
