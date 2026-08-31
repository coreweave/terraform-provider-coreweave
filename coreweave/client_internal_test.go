package coreweave

import (
	"testing"
	"time"

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
