package objectstorage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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

func TestRunBucketMutationPhaseDoesNotRetryMissingResources(t *testing.T) {
	t.Parallel()

	tests := map[string]error{
		"NoSuchBucket": &smithy.GenericAPIError{Code: ErrNoSuchBucket},
		"NotFound":     &smithy.GenericAPIError{Code: errNotFound},
		"NoSuchTagSet": &smithy.GenericAPIError{Code: errNoSuchTagSet},
		"HTTP 404":     responseErrorWithCause(t, 404, errors.New("not found")),
	}
	for name, mutationErr := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			waits := 0
			err := runBucketMutationPhase(
				t.Context(),
				s3PhaseMetadata{phase: "test mutation", bucket: "missing"},
				s3PhaseOptions{
					delay: func(int) time.Duration { return 0 },
					wait: func(context.Context, time.Duration) error {
						waits++
						return context.DeadlineExceeded
					},
				},
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
			return calls >= 3, nil
		},
	)
	if err != nil {
		t.Fatalf("runBucketReadbackPhase() error = %v", err)
	}
	if calls != 4 || waits != 3 {
		t.Fatalf("calls = %d, waits = %d; want 4, 3", calls, waits)
	}
}

func TestRunBucketReadbackPhaseRequiresStableConvergence(t *testing.T) {
	t.Parallel()

	results := []bool{true, false, true, true}
	calls := 0
	waits := 0
	err := runBucketReadbackPhase(
		t.Context(),
		s3PhaseMetadata{phase: "test readback", bucket: "target"},
		immediateS3PhaseOptions(&waits),
		func(context.Context) (bool, error) {
			if calls >= len(results) {
				t.Fatalf("readback called more than %d times", len(results))
			}
			result := results[calls]
			calls++
			return result, nil
		},
	)
	if err != nil {
		t.Fatalf("runBucketReadbackPhase() error = %v", err)
	}
	if calls != len(results) || waits != len(results)-1 {
		t.Fatalf("calls = %d, waits = %d; want %d, %d", calls, waits, len(results), len(results)-1)
	}
}

func TestRunBucketReadbackPhaseTriesImmediatelyAndSpansConfirmationWindow(t *testing.T) {
	t.Parallel()

	events := []string{}
	waits := []time.Duration{}
	err := runBucketReadbackPhase(
		t.Context(),
		s3PhaseMetadata{phase: "test readback", bucket: "target"},
		s3PhaseOptions{
			now:   time.Now,
			delay: func(int) time.Duration { return 0 },
			wait: func(ctx context.Context, delay time.Duration) error {
				events = append(events, "wait")
				waits = append(waits, delay)
				return ctx.Err()
			},
		},
		func(context.Context) (bool, error) {
			events = append(events, "attempt")
			return true, nil
		},
	)
	if err != nil {
		t.Fatalf("runBucketReadbackPhase() error = %v", err)
	}
	if got, want := strings.Join(events, ","), "attempt,wait,attempt"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if len(waits) != 1 || waits[0] != 5*time.Second {
		t.Fatalf("waits = %v, want [5s] between matching readbacks", waits)
	}
}

func TestRunBucketReadbackPhaseResetsConfirmationAfterRetryableError(t *testing.T) {
	t.Parallel()

	results := []struct {
		matched bool
		err     error
	}{
		{matched: true},
		{err: &smithy.GenericAPIError{Code: errInvalidRegion}},
		{matched: true},
		{matched: true},
	}
	calls := 0
	waits := 0
	err := runBucketReadbackPhase(
		t.Context(),
		s3PhaseMetadata{phase: "test readback", bucket: "target"},
		immediateS3PhaseOptions(&waits),
		func(context.Context) (bool, error) {
			if calls >= len(results) {
				t.Fatalf("readback called more than %d times", len(results))
			}
			result := results[calls]
			calls++
			return result.matched, result.err
		},
	)
	if err != nil {
		t.Fatalf("runBucketReadbackPhase() error = %v", err)
	}
	if calls != len(results) || waits != len(results)-1 {
		t.Fatalf("calls = %d, waits = %d; want %d, %d", calls, waits, len(results), len(results)-1)
	}
}

func TestRunBucketReadbackPhaseStopsOnPermanentErrorAfterMatch(t *testing.T) {
	t.Parallel()

	calls := 0
	waits := 0
	err := runBucketReadbackPhase(
		t.Context(),
		s3PhaseMetadata{phase: "test readback", bucket: "target"},
		immediateS3PhaseOptions(&waits),
		func(context.Context) (bool, error) {
			calls++
			if calls == 1 {
				return true, nil
			}
			return false, &smithy.GenericAPIError{Code: "AccessDenied"}
		},
	)
	if err == nil {
		t.Fatal("runBucketReadbackPhase() error = nil, want permanent failure")
	}
	if calls != 2 || waits != 1 {
		t.Fatalf("calls = %d, waits = %d; want 2, 1", calls, waits)
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
			return &smithy.OperationError{
				ServiceID:     "S3",
				OperationName: "PutBucketPolicy",
				Err:           &smithy.GenericAPIError{Code: errInvalidRegion, Message: "region does not match"},
			}
		},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runBucketMutationPhase() error = %v, want context.DeadlineExceeded", err)
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != errInvalidRegion {
		t.Fatalf("runBucketMutationPhase() error = %v, want preserved InvalidRegion", err)
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
	if message := err.Error(); !strings.Contains(message, errInvalidRegion) || !strings.Contains(message, context.DeadlineExceeded.Error()) {
		t.Fatalf("error = %q, want InvalidRegion and deadline diagnostics", message)
	}

	var diagnostics diag.Diagnostics
	handleS3Error(err, &diagnostics, "target")
	if len(diagnostics.Errors()) != 1 {
		t.Fatalf("handleS3Error() diagnostics = %v, want one error", diagnostics)
	}
	detail := diagnostics.Errors()[0].Detail()
	for _, expected := range []string{"bucket policy application", "1 attempt", "PutBucketPolicy", errInvalidRegion, context.DeadlineExceeded.Error()} {
		if !strings.Contains(detail, expected) {
			t.Fatalf("handleS3Error() detail = %q, want %q", detail, expected)
		}
	}
}

func TestRunBucketReadbackPhaseReportsExhaustedSharedBudget(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	calls := 0
	err := runBucketReadbackPhase(
		ctx,
		s3PhaseMetadata{phase: "bucket policy readback", bucket: "target"},
		s3PhaseOptions{},
		func(context.Context) (bool, error) {
			calls++
			return false, nil
		},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runBucketReadbackPhase() error = %v, want context.DeadlineExceeded", err)
	}
	if calls != 0 {
		t.Fatalf("readback calls = %d, want 0", calls)
	}
	if message := err.Error(); !strings.Contains(message, "could not start") || !strings.Contains(message, "shared propagation deadline exhausted") {
		t.Fatalf("error = %q, want shared-budget-exhausted-before-start diagnostic", message)
	}
}

func TestRunS3PhaseDoesNotMislabelIndependentDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	err := runS3Phase(
		ctx,
		s3PhaseMetadata{phase: "ambiguous bucket create reconciliation", bucket: "target"},
		s3PhaseOptions{},
		func(context.Context) (bool, error) {
			t.Fatal("attempt called with an expired context")
			return false, nil
		},
		isBucketPropagationRetryableS3Error,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runS3Phase() error = %v, want context.DeadlineExceeded", err)
	}
	if message := err.Error(); strings.Contains(message, "shared propagation deadline") {
		t.Fatalf("error = %q, should not label an independent deadline as shared", message)
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
