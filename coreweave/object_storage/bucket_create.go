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
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	bucketPreflightPageSize  int32 = 10000
	bucketPreflightMaxPages        = 10000
	ambiguousCreateTimeout         = time.Minute
	postCreateTimeout              = 5 * time.Minute
	postCreateInitialBackoff       = 500 * time.Millisecond
	postCreateMaxBackoff           = 5 * time.Second
)

type bucketCreateAPI interface {
	ListBuckets(context.Context, *s3.ListBucketsInput, ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	CreateBucket(context.Context, *s3.CreateBucketInput, ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	GetBucketLocation(context.Context, *s3.GetBucketLocationInput, ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error)
}

type bucketPostCreateAPI interface {
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	PutBucketTagging(context.Context, *s3.PutBucketTaggingInput, ...func(*s3.Options)) (*s3.PutBucketTaggingOutput, error)
	GetBucketTagging(context.Context, *s3.GetBucketTaggingInput, ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error)
}

type bucketDeleteAPI interface {
	DeleteBucket(context.Context, *s3.DeleteBucketInput, ...func(*s3.Options)) (*s3.DeleteBucketOutput, error)
}

type bucketAlreadyExistsError struct {
	bucket string
	cause  error
}

func (e *bucketAlreadyExistsError) Error() string {
	if e.cause == nil {
		return fmt.Sprintf("bucket %q already exists", e.bucket)
	}
	return fmt.Sprintf("bucket %q already exists: %v", e.bucket, e.cause)
}

func (e *bucketAlreadyExistsError) Unwrap() error {
	return e.cause
}

// bucketNameOwned enumerates the caller's complete owned-bucket inventory from
// the base endpoint. Any ambiguity fails closed so CreateBucket is never used
// to probe ownership.
func bucketNameOwned(ctx context.Context, client interface {
	ListBuckets(context.Context, *s3.ListBucketsInput, ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
}, bucket string) (bool, error) {
	input := &s3.ListBucketsInput{MaxBuckets: aws.Int32(bucketPreflightPageSize)}
	seenTokens := make(map[string]struct{})

	for page := 1; page <= bucketPreflightMaxPages; page++ {
		output, err := client.ListBuckets(ctx, input)
		if err != nil {
			return false, fmt.Errorf("owned-bucket preflight page %d: %w", page, err)
		}
		if output == nil {
			return false, fmt.Errorf("owned-bucket preflight page %d returned no response", page)
		}

		for _, ownedBucket := range output.Buckets {
			if ownedBucket.Name != nil && *ownedBucket.Name == bucket {
				return true, nil
			}
		}

		if output.ContinuationToken == nil {
			if len(output.Buckets) >= int(bucketPreflightPageSize) {
				return false, fmt.Errorf(
					"owned-bucket preflight page %d returned %d buckets without a continuation token",
					page,
					len(output.Buckets),
				)
			}
			return false, nil
		}

		nextToken := *output.ContinuationToken
		if strings.TrimSpace(nextToken) == "" {
			return false, fmt.Errorf("owned-bucket preflight page %d returned an empty continuation token", page)
		}
		if input.ContinuationToken != nil && nextToken == *input.ContinuationToken {
			return false, fmt.Errorf("owned-bucket preflight page %d returned a non-advancing continuation token", page)
		}
		if _, seen := seenTokens[nextToken]; seen {
			return false, fmt.Errorf("owned-bucket preflight page %d repeated a continuation token", page)
		}
		seenTokens[nextToken] = struct{}{}
		input.ContinuationToken = aws.String(nextToken)
	}

	return false, fmt.Errorf("owned-bucket preflight exceeded %d pages", bucketPreflightMaxPages)
}

// createBucketSafely creates once after proving the name is absent. A lost
// response is accepted only when a fresh inventory and location lookup prove
// that the caller owns the bucket in the requested zone.
func createBucketSafely(ctx context.Context, client bucketCreateAPI, bucket, zone string) error {
	return createBucketSafelyWithOptions(ctx, client, bucket, zone, s3PhaseOptions{})
}

func createBucketSafelyWithOptions(
	ctx context.Context,
	client bucketCreateAPI,
	bucket, zone string,
	options s3PhaseOptions,
) error {
	owned, err := bucketNameOwned(ctx, client, bucket)
	if err != nil {
		return fmt.Errorf("bucket ownership preflight: %w", err)
	}
	if owned {
		return &bucketAlreadyExistsError{bucket: bucket}
	}

	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(zone),
		},
	}, func(options *s3.Options) {
		// A lost create response is reconciled through owned inventory and zone
		// proof. Retrying CreateBucket itself would send the mutation again.
		options.Retryer = aws.NopRetryer{}
	})
	if err == nil {
		return nil
	}

	var bucketExistsErr *s3types.BucketAlreadyExists
	var bucketOwnedByYouErr *s3types.BucketAlreadyOwnedByYou
	if errors.As(err, &bucketExistsErr) || errors.As(err, &bucketOwnedByYouErr) {
		return &bucketAlreadyExistsError{bucket: bucket, cause: err}
	}
	if !isAmbiguousCreateError(ctx, err) {
		return fmt.Errorf("create bucket: %w", err)
	}

	if reconcileErr := reconcileAmbiguousCreate(ctx, client, bucket, zone, options); reconcileErr != nil {
		return fmt.Errorf("reconcile ambiguous create: %w", errors.Join(err, reconcileErr))
	}

	tflog.Warn(ctx, "reconciled an ambiguous S3 create response", map[string]interface{}{
		"bucket": bucket,
		"phase":  "create",
		"zone":   zone,
	})
	return nil
}

func reconcileAmbiguousCreate(
	parentCtx context.Context,
	client bucketCreateAPI,
	bucket, zone string,
	options s3PhaseOptions,
) error {
	ctx, cancel := context.WithTimeout(parentCtx, ambiguousCreateTimeout)
	defer cancel()

	return runS3Phase(ctx, "ambiguous bucket create", bucket, zone, options, func(ctx context.Context) (bool, error) {
		owned, err := bucketNameOwned(ctx, client, bucket)
		if err != nil || !owned {
			return false, err
		}

		location, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(bucket)})
		if err != nil {
			return false, err
		}
		if location == nil || location.LocationConstraint == "" {
			return false, nil
		}
		actualZone := string(location.LocationConstraint)
		if actualZone != zone {
			return false, fmt.Errorf("owned bucket %q is in zone %q, want %q", bucket, actualZone, zone)
		}
		return true, nil
	}, isPostCreateRetryableS3Error)
}

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
		options.delay = postCreateBackoff
	}
	if options.wait == nil {
		options.wait = waitForS3Backoff
	}
	return options
}

type s3PhaseError struct {
	phase    string
	attempts int
	elapsed  time.Duration
	err      error
}

func (e *s3PhaseError) Error() string {
	return fmt.Sprintf("%s failed after %d attempt(s) in %s: %v", e.phase, e.attempts, e.elapsed.Round(time.Millisecond), e.err)
}

func (e *s3PhaseError) Unwrap() error {
	return e.err
}

func runS3Phase(
	ctx context.Context,
	phase, bucket, zone string,
	options s3PhaseOptions,
	attempt func(context.Context) (bool, error),
	retryable func(error) bool,
) error {
	options = options.withDefaults()
	started := options.now()

	for attemptNumber := 1; ; attemptNumber++ {
		if err := ctx.Err(); err != nil {
			return &s3PhaseError{phase: phase, attempts: attemptNumber - 1, elapsed: options.now().Sub(started), err: err}
		}

		done, err := attempt(ctx)
		if err == nil && done {
			return nil
		}
		if err != nil && !retryable(err) {
			return &s3PhaseError{phase: phase, attempts: attemptNumber, elapsed: options.now().Sub(started), err: err}
		}

		delay := options.delay(attemptNumber)
		errorClass, errorCode := s3ErrorMetadata(err)
		tflog.Debug(ctx, "waiting for S3 operation convergence", map[string]interface{}{
			"attempt":     attemptNumber,
			"bucket":      bucket,
			"elapsed":     options.now().Sub(started).String(),
			"error_class": errorClass,
			"error_code":  errorCode,
			"next_delay":  delay.String(),
			"phase":       phase,
			"zone":        zone,
		})
		if waitErr := options.wait(ctx, delay); waitErr != nil {
			return &s3PhaseError{phase: phase, attempts: attemptNumber, elapsed: options.now().Sub(started), err: waitErr}
		}
	}
}

func reconcileBucketAfterCreate(
	parentCtx context.Context,
	client bucketPostCreateAPI,
	bucket, zone string,
	expectedTags []s3types.Tag,
	applyTags bool,
	options s3PhaseOptions,
) error {
	ctx, cancel := context.WithTimeout(parentCtx, postCreateTimeout)
	defer cancel()

	if err := runS3Phase(ctx, "bucket readiness", bucket, zone, options, func(ctx context.Context) (bool, error) {
		_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
		return err == nil, err
	}, isBucketReadinessRetryableS3Error); err != nil {
		return err
	}

	if !applyTags {
		return nil
	}

	tagInput := &s3.PutBucketTaggingInput{
		Bucket:  aws.String(bucket),
		Tagging: &s3types.Tagging{TagSet: expectedTags},
	}
	if err := runS3Phase(ctx, "bucket tag application", bucket, zone, options, func(ctx context.Context) (bool, error) {
		_, err := client.PutBucketTagging(ctx, tagInput)
		return err == nil, err
	}, isPostCreateRetryableS3Error); err != nil {
		return err
	}

	expected := append([]s3types.Tag(nil), expectedTags...)
	slices.SortFunc(expected, cmpTag)
	return runS3Phase(ctx, "bucket tag readback", bucket, zone, options, func(ctx context.Context) (bool, error) {
		output, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(bucket)})
		if err != nil {
			return false, err
		}
		if output == nil {
			return false, nil
		}
		actual := append([]s3types.Tag(nil), output.TagSet...)
		slices.SortFunc(actual, cmpTag)
		return slices.EqualFunc(expected, actual, eqTag), nil
	}, isPostCreateRetryableS3Error)
}

func deleteBucketWithRetry(
	parentCtx context.Context,
	client bucketDeleteAPI,
	bucket, zone string,
	options s3PhaseOptions,
) error {
	ctx, cancel := context.WithTimeout(parentCtx, postCreateTimeout)
	defer cancel()

	return runS3Phase(ctx, "bucket deletion request", bucket, zone, options, func(ctx context.Context) (bool, error) {
		_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
		if err == nil || isBucketNotFoundError(err) {
			return true, nil
		}
		return false, err
	}, isPostCreateRetryableS3Error)
}

func postCreateBackoff(attempt int) time.Duration {
	shift := min(max(attempt-1, 0), 4)
	delay := postCreateInitialBackoff << shift
	if delay > postCreateMaxBackoff {
		delay = postCreateMaxBackoff
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

func isAmbiguousCreateError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || isPermanentTransportError(err) {
		return false
	}

	var apiErr smithy.APIError
	hasAPIError := errors.As(err, &apiErr)
	if hasAPIError && isPermanentBucketAPIError(apiErr.ErrorCode()) {
		return false
	}

	if status, ok := s3HTTPStatus(err); ok && isTransientS3HTTPStatus(status, false) {
		return true
	}
	if hasAPIError {
		return isTransientBucketAPIError(apiErr.ErrorCode(), false)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// The operation context is still live, so the deadline belongs to one
		// HTTP attempt and the server may have accepted the mutation.
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func isPostCreateRetryableS3Error(err error) bool {
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

func isBucketReadinessRetryableS3Error(err error) bool {
	if isPostCreateRetryableS3Error(err) {
		return true
	}

	// During initial virtual-host routing convergence, HeadBucket can collapse
	// the backend InvalidRegion response into a generic HTTP 400 BadRequest.
	// Restrict this exception to readiness; other named 400s and all tag calls
	// still fail immediately.
	var apiErr smithy.APIError
	status, hasStatus := s3HTTPStatus(err)
	return errors.As(err, &apiErr) &&
		apiErr.ErrorCode() == "BadRequest" &&
		hasStatus &&
		status == standardhttp.StatusBadRequest
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
