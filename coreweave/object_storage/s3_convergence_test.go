package objectstorage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go"
)

func TestRunBucketMutationPhaseRetriesInvalidRegion(t *testing.T) {
	t.Parallel()

	calls := 0
	waits := 0
	err := runBucketMutationPhase(
		t.Context(),
		s3PhaseMetadata{phase: "test mutation", bucket: "target"},
		immediateS3PhaseOptions(&waits),
		func(context.Context) error {
			calls++
			if calls == 1 {
				return &smithy.GenericAPIError{Code: errInvalidRegion}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("runBucketMutationPhase() error = %v", err)
	}
	if calls != 2 || waits != 1 {
		t.Fatalf("calls = %d, waits = %d; want 2, 1", calls, waits)
	}
}

func TestRunBucketMutationPhaseDoesNotRetryPermanentErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]error{
		"generic non-head 400": responseErrorWithCause(t, 400, &smithy.GenericAPIError{Code: "BadRequest"}),
		"access denied":        &smithy.GenericAPIError{Code: "AccessDenied"},
		"invalid signature":    &smithy.GenericAPIError{Code: "SignatureDoesNotMatch"},
	}
	for name, mutationErr := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			waits := 0
			err := runBucketMutationPhase(
				t.Context(),
				s3PhaseMetadata{phase: "test mutation", bucket: "target"},
				immediateS3PhaseOptions(&waits),
				func(context.Context) error {
					calls++
					return mutationErr
				},
			)
			if err == nil {
				t.Fatal("runBucketMutationPhase() error = nil, want permanent failure")
			}
			if calls != 1 || waits != 0 {
				t.Fatalf("calls = %d, waits = %d; want 1, 0", calls, waits)
			}
		})
	}
}

func TestRunBucketReadbackPhaseRetriesNonConvergedResult(t *testing.T) {
	t.Parallel()

	calls := 0
	waits := 0
	err := runBucketReadbackPhase(
		t.Context(),
		s3PhaseMetadata{phase: "test readback", bucket: "target"},
		immediateS3PhaseOptions(&waits),
		func(context.Context) (bool, error) {
			calls++
			return calls == 3, nil
		},
	)
	if err != nil {
		t.Fatalf("runBucketReadbackPhase() error = %v", err)
	}
	if calls != 3 || waits != 2 {
		t.Fatalf("calls = %d, waits = %d; want 3, 2", calls, waits)
	}
}

func TestRunBucketMutationPhaseHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	waits := 0
	err := runBucketMutationPhase(
		ctx,
		s3PhaseMetadata{phase: "test mutation", bucket: "target"},
		immediateS3PhaseOptions(&waits),
		func(context.Context) error {
			calls++
			return &smithy.GenericAPIError{Code: errInvalidRegion}
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runBucketMutationPhase() error = %v, want context.Canceled", err)
	}
	if calls != 0 || waits != 0 {
		t.Fatalf("calls = %d, waits = %d; want 0, 0", calls, waits)
	}
}

func TestRunBucketMutationPhaseReportsBoundedFailure(t *testing.T) {
	t.Parallel()

	calls := 0
	err := runBucketMutationPhase(
		t.Context(),
		s3PhaseMetadata{phase: "bucket policy application", bucket: "target"},
		s3PhaseOptions{
			delay: func(int) time.Duration { return 0 },
			wait: func(context.Context, time.Duration) error {
				return context.DeadlineExceeded
			},
		},
		func(context.Context) error {
			calls++
			return &smithy.GenericAPIError{Code: errInvalidRegion}
		},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runBucketMutationPhase() error = %v, want context.DeadlineExceeded", err)
	}
	var phaseErr *s3PhaseError
	if !errors.As(err, &phaseErr) {
		t.Fatalf("runBucketMutationPhase() error type = %T, want *s3PhaseError", err)
	}
	if calls != 1 || phaseErr.attempts != 1 || phaseErr.phase != "bucket policy application" {
		t.Fatalf("calls = %d, phase error = %#v; want one bucket policy application attempt", calls, phaseErr)
	}
	if message := err.Error(); !strings.Contains(message, "bucket policy application failed after 1 attempt") {
		t.Fatalf("error = %q, want phase and attempt diagnostics", message)
	}
}

func immediateS3PhaseOptions(waits *int) s3PhaseOptions {
	return s3PhaseOptions{
		now:   time.Now,
		delay: func(int) time.Duration { return 0 },
		wait: func(ctx context.Context, _ time.Duration) error {
			*waits++
			return ctx.Err()
		},
	}
}
