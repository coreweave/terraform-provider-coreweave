package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestResolveHTTPTimeouts(t *testing.T) {
	original, wasSet := os.LookupEnv(CoreweaveHTTPTimeoutEnvVar)
	if err := os.Unsetenv(CoreweaveHTTPTimeoutEnvVar); err != nil {
		t.Fatalf("unset %s: %v", CoreweaveHTTPTimeoutEnvVar, err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(CoreweaveHTTPTimeoutEnvVar, original)
			return
		}
		_ = os.Unsetenv(CoreweaveHTTPTimeoutEnvVar)
	})

	tests := []struct {
		name        string
		configured  types.String
		environment *string
		wantConnect time.Duration
		wantS3      time.Duration
	}{
		{
			name:        "unset preserves thirty second S3 attempt timeout",
			configured:  types.StringNull(),
			wantConnect: DefaultHTTPTimeout,
			wantS3:      30 * time.Second,
		},
		{
			name:        "provider value applies unchanged to both clients",
			configured:  types.StringValue("17s"),
			wantConnect: 17 * time.Second,
			wantS3:      17 * time.Second,
		},
		{
			name:        "environment value takes precedence unchanged",
			configured:  types.StringValue("17s"),
			environment: stringPointer("23s"),
			wantConnect: 23 * time.Second,
			wantS3:      23 * time.Second,
		},
		{
			name:        "zero provider value is rejected instead of removing the bound",
			configured:  types.StringValue("0s"),
			wantConnect: DefaultHTTPTimeout,
			wantS3:      30 * time.Second,
		},
		{
			name:        "negative environment value is rejected instead of removing the bound",
			configured:  types.StringValue("17s"),
			environment: stringPointer("-1s"),
			wantConnect: 17 * time.Second,
			wantS3:      17 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.environment == nil {
				if err := os.Unsetenv(CoreweaveHTTPTimeoutEnvVar); err != nil {
					t.Fatalf("unset %s: %v", CoreweaveHTTPTimeoutEnvVar, err)
				}
			} else {
				t.Setenv(CoreweaveHTTPTimeoutEnvVar, *tt.environment)
			}

			connectTimeout, s3Timeout := resolveHTTPTimeouts(context.Background(), tt.configured)
			if connectTimeout != tt.wantConnect || s3Timeout != tt.wantS3 {
				t.Fatalf("resolveHTTPTimeouts() = (%s, %s), want (%s, %s)", connectTimeout, s3Timeout, tt.wantConnect, tt.wantS3)
			}
		})
	}
}

func TestParseDurationRejectsNonPositiveValues(t *testing.T) {
	for _, raw := range []string{"0", "0s", "-1", "-1s"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseDuration(raw); err == nil {
				t.Fatalf("parseDuration(%q) error = nil, want non-positive duration error", raw)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}
