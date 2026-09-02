package objectstorage

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"math/big"
	"net"
	standardhttp "net/http"
	"net/url"
	"time"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	bucketPropagationTimeout        = 5 * time.Minute
	bucketPropagationInitialBackoff = 500 * time.Millisecond
	bucketPropagationMaxBackoff     = 5 * time.Second
	bucketReadbackConfirmations     = 2
)

type s3PhaseOptions struct {
	now   func() time.Time
	delay func(attempt int) time.Duration
	wait  func(context.Context, time.Duration) error
}

func (options s3PhaseOptions) withDefaults() s3PhaseOptions {
	if options.now == nil {
		options.now = time.Now
	}
	if options.delay == nil {
		options.delay = bucketPropagationBackoff
	}
	if options.wait == nil {
		options.wait = waitForS3Backoff
	}
	return options
}

type s3PhaseMetadata struct {
	phase                   string
	bucket                  string
	zone                    string
	sharedPropagationBudget bool
}

type s3PhaseError struct {
	phase    string
	attempts int
	elapsed  time.Duration
	err      error
}

func (e *s3PhaseError) Error() string {
	if e.attempts == 0 {
		return fmt.Sprintf("%s could not start after %s: %v", e.phase, e.elapsed.Round(time.Millisecond), e.err)
	}
	return fmt.Sprintf("%s failed after %d attempt(s) in %s: %v", e.phase, e.attempts, e.elapsed.Round(time.Millisecond), e.err)
}

func (e *s3PhaseError) Unwrap() error {
	return e.err
}

func bucketPropagationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, bucketPropagationTimeout)
}

func runS3Phase(
	ctx context.Context,
	metadata s3PhaseMetadata,
	options s3PhaseOptions,
	attempt func(context.Context) (bool, error),
	retryable func(error) bool,
) error {
	options = options.withDefaults()
	started := options.now()
	var lastAttemptErr error

	for attemptNumber := 1; ; attemptNumber++ {
		if err := ctx.Err(); err != nil {
			if attemptNumber == 1 && metadata.sharedPropagationBudget && errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("shared propagation deadline exhausted before phase start: %w", err)
			}
			if lastAttemptErr != nil {
				err = errors.Join(err, lastAttemptErr)
			}
			return &s3PhaseError{phase: metadata.phase, attempts: attemptNumber - 1, elapsed: options.now().Sub(started), err: err}
		}

		done, err := attempt(ctx)
		lastAttemptErr = err
		if err == nil && done {
			return nil
		}
		if err != nil && !retryable(err) {
			return &s3PhaseError{phase: metadata.phase, attempts: attemptNumber, elapsed: options.now().Sub(started), err: err}
		}

		delay := options.delay(attemptNumber)
		errorClass, errorCode := s3ErrorMetadata(err)
		logFields := map[string]interface{}{
			"attempt":     attemptNumber,
			"bucket":      metadata.bucket,
			"elapsed":     options.now().Sub(started).String(),
			"error_class": errorClass,
			"error_code":  errorCode,
			"next_delay":  delay.String(),
			"phase":       metadata.phase,
		}
		if metadata.zone != "" {
			logFields["zone"] = metadata.zone
		}
		tflog.Debug(ctx, "waiting for S3 operation convergence", logFields)
		if waitErr := options.wait(ctx, delay); waitErr != nil {
			if lastAttemptErr != nil {
				waitErr = errors.Join(waitErr, lastAttemptErr)
			}
			return &s3PhaseError{phase: metadata.phase, attempts: attemptNumber, elapsed: options.now().Sub(started), err: waitErr}
		}
	}
}

func runBucketMutationPhase(
	ctx context.Context,
	metadata s3PhaseMetadata,
	options s3PhaseOptions,
	mutate func(context.Context) error,
) error {
	return runS3Phase(ctx, metadata, options, func(ctx context.Context) (bool, error) {
		err := mutate(ctx)
		return err == nil, err
	}, isBucketSubresourceMutationRetryableS3Error)
}

func runBucketReadbackPhase(
	ctx context.Context,
	metadata s3PhaseMetadata,
	options s3PhaseOptions,
	converged func(context.Context) (bool, error),
) error {
	// A matching response is request-local evidence, not a service readiness
	// guarantee. Require a second sample through the normal backoff path so a
	// match from one gateway does not immediately end reconciliation.
	metadata.sharedPropagationBudget = true
	consecutiveMatches := 0
	return runS3Phase(ctx, metadata, options, func(ctx context.Context) (bool, error) {
		matched, err := converged(ctx)
		if err != nil || !matched {
			consecutiveMatches = 0
			return false, err
		}

		consecutiveMatches++
		return consecutiveMatches >= bucketReadbackConfirmations, nil
	}, isBucketPropagationRetryableS3Error)
}

func bucketPropagationBackoff(attempt int) time.Duration {
	shift := min(max(attempt-1, 0), 4)
	delay := bucketPropagationInitialBackoff << shift
	if delay > bucketPropagationMaxBackoff {
		delay = bucketPropagationMaxBackoff
	}
	half := delay / 2
	jitter, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(half)+1))
	if err != nil {
		return delay
	}
	return half + time.Duration(jitter.Int64())
}

func waitForS3Backoff(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isBucketSubresourceMutationRetryableS3Error(err error) bool {
	if status, ok := s3HTTPStatus(err); ok && status == standardhttp.StatusNotFound {
		return false
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case ErrNoSuchBucket, errNotFound, errNoSuchTagSet:
			return false
		}
	}

	return isBucketPropagationRetryableS3Error(err)
}

func isBucketPropagationRetryableS3Error(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || isPermanentTransportError(err) {
		return false
	}

	var apiErr smithy.APIError
	hasAPIError := errors.As(err, &apiErr)
	if hasAPIError && isPermanentBucketAPIError(apiErr.ErrorCode()) {
		return false
	}

	if status, ok := s3HTTPStatus(err); ok && isTransientS3HTTPStatus(status, true) {
		return true
	}
	if hasAPIError {
		return isTransientBucketAPIError(apiErr.ErrorCode(), true)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func isPermanentBucketAPIError(code string) bool {
	switch code {
	case "AccessDenied", "AuthorizationHeaderMalformed", "InvalidAccessKeyId",
		"InvalidArgument", "InvalidBucketName", "InvalidRequest", "InvalidToken",
		"SignatureDoesNotMatch":
		return true
	default:
		return false
	}
}

func isTransientBucketAPIError(code string, includePropagation bool) bool {
	if includePropagation {
		switch code {
		case errInvalidRegion, ErrNoSuchBucket, errNotFound, errNoSuchTagSet:
			return true
		}
	}

	switch code {
	case "BadGateway", "GatewayTimeout", "InternalError", "InternalServerError",
		"RequestTimeout", "RequestTimeoutException", "ServiceUnavailable", "SlowDown",
		"TooManyRequests":
		return true
	default:
		return false
	}
}

func isTransientS3HTTPStatus(status int, includeNotFound bool) bool {
	if includeNotFound && status == standardhttp.StatusNotFound {
		return true
	}
	return status == standardhttp.StatusRequestTimeout ||
		status == standardhttp.StatusTooManyRequests ||
		status == standardhttp.StatusInternalServerError ||
		status == standardhttp.StatusBadGateway ||
		status == standardhttp.StatusServiceUnavailable ||
		status == standardhttp.StatusGatewayTimeout
}

func isPermanentTransportError(err error) bool {
	return coreweave.IsPermanentRequestError(err)
}

func s3HTTPStatus(err error) (int, bool) {
	var responseErr *smithyhttp.ResponseError
	if !errors.As(err, &responseErr) || responseErr.Response == nil {
		return 0, false
	}
	return responseErr.Response.StatusCode, true
}

func s3ErrorMetadata(err error) (class, code string) {
	if err == nil {
		return "not_converged", ""
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return "api", apiErr.ErrorCode()
	}
	if status, ok := s3HTTPStatus(err); ok {
		return "http", fmt.Sprintf("%d", status)
	}
	var certificateErr *tls.CertificateVerificationError
	if errors.As(err, &certificateErr) {
		return "tls_certificate", ""
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return "network", ""
	}
	return "unknown", ""
}
