package coreweave

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/hashicorp/go-cleanhttp"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	// DefaultS3AttemptTimeout preserves the historical per-attempt timeout when
	// the provider's http_timeout setting is not configured.
	DefaultS3AttemptTimeout    = 30 * time.Second
	s3CredentialRefreshTimeout = 2 * time.Minute
	s3MaxAttempts              = 3
	s3MaxBackoff               = 5 * time.Second

	errAccessDenied string = "AccessDenied"
)

type s3ClientCache struct {
	mu sync.Mutex

	client         *s3.Client
	accessKeyInfo  *cwobjectv1.CreateAccessKeyFromJWTResponse
	apiEndpoint    string
	endpoint       string
	token          string
	attemptTimeout time.Duration
	refresh        *s3ClientRefresh
}

type s3ClientRefresh struct {
	done   chan struct{}
	client *s3.Client
	err    error
}

// ConfigureProvider is invoked for each Terraform operation. Keeping the
// short-lived S3 client in a process-wide cache prevents excess access-key
// creation while the cache identity prevents reuse across provider configs.
var sharedS3ClientCache s3ClientCache

type s3Retryer struct {
	aws.Retryer
}

func (r s3Retryer) IsErrorRetryable(err error) bool {
	if IsPermanentRequestError(err) || isPermanentS3APIError(err) {
		return false
	}
	return r.Retryer.IsErrorRetryable(err)
}

func (r s3Retryer) GetAttemptToken(ctx context.Context) (func(error) error, error) {
	if retryer, ok := r.Retryer.(aws.RetryerV2); ok {
		return retryer.GetAttemptToken(ctx)
	}
	return r.GetInitialToken(), nil
}

func isPermanentS3APIError(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.ErrorCode() {
	case "AccessDenied", "AuthorizationHeaderMalformed", "InvalidAccessKeyId",
		"InvalidArgument", "InvalidRequest", "InvalidToken", "SignatureDoesNotMatch":
		return true
	default:
		return false
	}
}

func newS3Retryer(options ...func(*awsretry.StandardOptions)) aws.Retryer {
	standard := awsretry.NewStandard(func(standardOptions *awsretry.StandardOptions) {
		standardOptions.MaxAttempts = s3MaxAttempts
		standardOptions.MaxBackoff = s3MaxBackoff
		for _, option := range options {
			option(standardOptions)
		}
	})
	return s3Retryer{Retryer: standard}
}

func (c *Client) s3HttpClient() *http.Client {
	transport := c.s3HTTPTransport
	if transport == nil {
		// Disabling keep-alives and idle connections prevents stale DNS entries
		// from pinning bucket operations to a routing view that is still converging.
		transport = cleanhttp.DefaultTransport()
	}

	return &http.Client{Timeout: c.effectiveS3AttemptTimeout(), Transport: transport}
}

func (c *Client) effectiveS3AttemptTimeout() time.Duration {
	if c.s3AttemptTimeout > 0 {
		return c.s3AttemptTimeout
	}
	return DefaultS3AttemptTimeout
}

func (c *Client) currentTime() time.Time {
	if c.s3Now != nil {
		return c.s3Now()
	}
	return time.Now()
}

func (c *Client) createS3Client(ctx context.Context, zone string) (*s3.Client, *cwobjectv1.CreateAccessKeyFromJWTResponse, error) {
	resp, err := c.CreateAccessKeyFromJWT(ctx, connect.NewRequest(&cwobjectv1.CreateAccessKeyFromJWTRequest{
		DurationSeconds: wrapperspb.UInt32(60 * 15), // 15 minutes
	}))
	if err != nil {
		return nil, nil, err
	}

	retryer := c.s3Retryer
	if retryer == nil {
		retryer = func() aws.Retryer { return newS3Retryer() }
	}

	httpClient := c.s3HttpClient()
	s3Client := s3.New(s3.Options{
		BaseEndpoint: aws.String(c.s3Endpoint),
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			resp.Msg.AccessKeyId,
			resp.Msg.SecretKey,
			"",
		)),
		HTTPClient:                 httpClient,
		Region:                     zone, // must be non-empty and a valid DNS subdomain
		RetryMode:                  aws.RetryModeStandard,
		Retryer:                    retryer(),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenSupported,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenSupported,
		UsePathStyle:               false,
	})
	return s3Client, resp.Msg, nil
}

func (c *Client) S3Client(ctx context.Context, zone string) (*s3.Client, error) {
	// We use 'notempty' as the zone here because it doesn't actually matter what region is configured for cwobject.com
	// it only matters that it's not empty & a valid DNS subdomain
	s3Zone := zone
	if zone == "" {
		s3Zone = "notempty"
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		now := c.currentTime()

		sharedS3ClientCache.mu.Lock()
		identityMatches := sharedS3ClientCache.apiEndpoint == c.apiEndpoint &&
			sharedS3ClientCache.endpoint == c.s3Endpoint &&
			sharedS3ClientCache.token == c.token &&
			sharedS3ClientCache.attemptTimeout == c.effectiveS3AttemptTimeout()
		if sharedS3ClientCache.refresh != nil && !identityMatches {
			refresh := sharedS3ClientCache.refresh
			sharedS3ClientCache.mu.Unlock()
			if _, err := waitForRefresh(ctx, refresh); err != nil && ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		if !identityMatches {
			sharedS3ClientCache.client = nil
			sharedS3ClientCache.accessKeyInfo = nil
			sharedS3ClientCache.apiEndpoint = c.apiEndpoint
			sharedS3ClientCache.endpoint = c.s3Endpoint
			sharedS3ClientCache.token = c.token
			sharedS3ClientCache.attemptTimeout = c.effectiveS3AttemptTimeout()
		}

		cachedClient := sharedS3ClientCache.client
		cachedExpiry := accessKeyExpiry(sharedS3ClientCache.accessKeyInfo)
		if cachedClient != nil && cachedExpiry.Sub(now) > 5*time.Minute {
			sharedS3ClientCache.mu.Unlock()
			tflog.Info(ctx, "fetched cached s3 client")
			return cachedClient, nil
		}

		if sharedS3ClientCache.refresh != nil {
			if cachedClient != nil && cachedExpiry.After(now) {
				sharedS3ClientCache.mu.Unlock()
				tflog.Info(ctx, "serving unexpired s3 client while credential refresh is in progress")
				return cachedClient, nil
			}
			refresh := sharedS3ClientCache.refresh
			sharedS3ClientCache.mu.Unlock()
			client, err := waitForRefresh(ctx, refresh)
			if err != nil {
				return nil, err
			}
			return client, nil
		}

		refresh := &s3ClientRefresh{done: make(chan struct{})}
		sharedS3ClientCache.refresh = refresh
		sharedS3ClientCache.mu.Unlock()

		if cachedClient == nil {
			tflog.Info(ctx, "creating s3 client because one does not exist")
		} else {
			tflog.Info(ctx, "refreshing s3 client because keys expire within the next 5 minutes")
		}

		refreshCtx, cancelRefresh := context.WithTimeout(context.WithoutCancel(ctx), s3CredentialRefreshTimeout)
		go c.refreshS3Client(refreshCtx, cancelRefresh, refresh, s3Zone)
		return waitForRefresh(ctx, refresh)
	}
}

func (c *Client) refreshS3Client(
	ctx context.Context,
	cancel context.CancelFunc,
	refresh *s3ClientRefresh,
	zone string,
) {
	defer cancel()

	client, keyInfo, refreshErr := c.createS3Client(ctx, zone)
	if refreshErr == nil {
		refreshErr = validateS3Client(ctx, client)
	}

	sharedS3ClientCache.mu.Lock()
	if refreshErr == nil {
		sharedS3ClientCache.client = client
		sharedS3ClientCache.accessKeyInfo = keyInfo
	}
	fallbackClient := sharedS3ClientCache.client
	fallbackExpiry := accessKeyExpiry(sharedS3ClientCache.accessKeyInfo)
	fallbackUsable := fallbackClient != nil && fallbackExpiry.After(c.currentTime())
	refresh.client = fallbackClient
	if refreshErr != nil && !fallbackUsable {
		refresh.err = refreshErr
	}
	sharedS3ClientCache.refresh = nil
	close(refresh.done)
	sharedS3ClientCache.mu.Unlock()

	if refreshErr != nil && fallbackUsable {
		tflog.Warn(ctx, "s3 credential refresh failed; using unexpired cached client", map[string]interface{}{
			"error": refreshErr.Error(),
		})
		return
	}
	if refreshErr == nil {
		tflog.Info(ctx, "created or refreshed s3 client")
	}
}

func accessKeyExpiry(keyInfo *cwobjectv1.CreateAccessKeyFromJWTResponse) time.Time {
	if keyInfo == nil || keyInfo.GetExpiry() == nil {
		return time.Time{}
	}
	return keyInfo.GetExpiry().AsTime()
}

func waitForRefresh(ctx context.Context, refresh *s3ClientRefresh) (*s3.Client, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for s3 credential refresh: %w", ctx.Err())
	case <-refresh.done:
		return refresh.client, refresh.err
	}
}

func validateS3Client(ctx context.Context, client *s3.Client) error {
	return PollUntil("s3 access key validation", ctx, time.Second, time.Minute, func(ctx context.Context) (bool, error) {
		_, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == errAccessDenied {
				return false, nil
			}
		}
		return err == nil, err
	})
}

func resetS3ClientCache() {
	sharedS3ClientCache.mu.Lock()
	defer sharedS3ClientCache.mu.Unlock()
	sharedS3ClientCache.client = nil
	sharedS3ClientCache.accessKeyInfo = nil
	sharedS3ClientCache.apiEndpoint = ""
	sharedS3ClientCache.endpoint = ""
	sharedS3ClientCache.token = ""
	sharedS3ClientCache.attemptTimeout = 0
	sharedS3ClientCache.refresh = nil
}

// PollUntil runs check(ctx) every interval until it returns (true, nil),
// or else returns the first non‐nil error, or a timeout error.
func PollUntil(operation string, parentCtx context.Context, interval, timeout time.Duration, check func(ctx context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out polling for %s after %v: %w", operation, timeout, ctx.Err())
		case <-ticker.C:
			ok, err := check(ctx)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}
	}
}
