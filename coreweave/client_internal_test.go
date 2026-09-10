package coreweave

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
)

func TestNewClientWithOptionsAppliesS3AttemptTimeout(t *testing.T) {
	t.Parallel()

	const timeout = 17 * time.Second
	client, err := NewClientWithOptions(
		"https://api.example.test",
		"https://objects.example.test",
		time.Second,
		auth.NewStaticTokenSource("token"),
		"test-user-agent",
		ClientOptions{S3AttemptTimeout: timeout},
	)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	if got := client.s3HttpClient().Timeout; got != timeout {
		t.Fatalf("S3 HTTP attempt timeout = %s, want %s", got, timeout)
	}
}

func TestClassifyServiceAccountClientError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err      error
		wantCode connect.Code
	}{
		"retry exhaustion": {err: errors.New("giving up after 11 attempts: unexpected HTTP status 503"), wantCode: connect.CodeUnavailable},
		"canceled":         {err: fmt.Errorf("request failed: %w", context.Canceled), wantCode: connect.CodeCanceled},
		"deadline":         {err: fmt.Errorf("request failed: %w", context.DeadlineExceeded), wantCode: connect.CodeDeadlineExceeded},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var connectErr *connect.Error
			if !errors.As(classifyServiceAccountClientError(test.err), &connectErr) {
				t.Fatal("classified error is not a Connect error")
			}
			if connectErr.Code() != test.wantCode {
				t.Fatalf("code = %s, want %s", connectErr.Code(), test.wantCode)
			}
		})
	}
}

func TestSafeConnectCodeForHTTPError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status   int
		apiCode  string
		parsed   bool
		wantCode connect.Code
	}{
		"Connect not found":         {status: http.StatusNotFound, apiCode: "not_found", parsed: true, wantCode: connect.CodeNotFound},
		"unparseable not found":     {status: http.StatusNotFound, parsed: false, wantCode: connect.CodeUnknown},
		"missing code on not found": {status: http.StatusNotFound, parsed: true, wantCode: connect.CodeUnknown},
		"request timeout":           {status: http.StatusRequestTimeout, parsed: false, wantCode: connect.CodeDeadlineExceeded},
		"internal server error":     {status: http.StatusInternalServerError, parsed: false, wantCode: connect.CodeInternal},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := safeConnectCodeForHTTPError(test.status, test.apiCode, test.parsed); got != test.wantCode {
				t.Fatalf("code = %s, want %s", got, test.wantCode)
			}
		})
	}
}
