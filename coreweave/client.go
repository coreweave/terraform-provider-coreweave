package coreweave

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"buf.build/gen/go/coreweave/cks/connectrpc/go/coreweave/cks/v1beta1/cksv1beta1connect"
	"buf.build/gen/go/coreweave/cwobject/connectrpc/go/cwobject/v1/cwobjectv1connect"
	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"buf.build/gen/go/coreweave/networking/connectrpc/go/coreweave/networking/v1beta1/networkingv1beta1connect"
	"connectrpc.com/connect"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/go-cleanhttp"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	// A regular expression to match the error returned by net/http when the
	// configured number of redirects is exhausted. This error isn't typed
	// specifically so we resort to matching on the error string.
	redirectsErrorRe = regexp.MustCompile(`stopped after \d+ redirects\z`)

	// A regular expression to match the error returned by net/http when the
	// scheme specified in the URL is invalid. This error isn't typed
	// specifically so we resort to matching on the error string.
	schemeErrorRe = regexp.MustCompile(`unsupported protocol scheme`)

	// A regular expression to match the error returned by net/http when a
	// request header or value is invalid. This error isn't typed
	// specifically so we resort to matching on the error string.
	invalidHeaderErrorRe = regexp.MustCompile(`invalid header`)

	// A regular expression to match the error returned by net/http when the
	// TLS certificate is not trusted. This error isn't typed
	// specifically so we resort to matching on the error string.
	notTrustedErrorRe = regexp.MustCompile(`certificate is not trusted`)
)

func baseRetryPolicy(resp *http.Response, err error) (bool, error) {
	if err != nil {
		var v *url.Error
		if errors.Is(err, v) {
			// Don't retry if the error was due to too many redirects.
			if redirectsErrorRe.MatchString(v.Error()) {
				return false, v
			}

			// Don't retry if the error was due to an invalid protocol scheme.
			if schemeErrorRe.MatchString(v.Error()) {
				return false, v
			}

			// Don't retry if the error was due to an invalid header.
			if invalidHeaderErrorRe.MatchString(v.Error()) {
				return false, v
			}

			// Don't retry if the error was due to TLS cert verification failure.
			if notTrustedErrorRe.MatchString(v.Error()) {
				return false, v
			}
			if errors.Is(v, &tls.CertificateVerificationError{}) {
				return false, v
			}
		}

		// The error is likely recoverable so retry.
		return true, nil
	}

	// 429 Too Many Requests is recoverable. Sometimes the server puts
	// a Retry-After response header to indicate when the server is
	// available to start processing request from client.
	if resp.StatusCode == http.StatusTooManyRequests {
		return true, nil
	}

	// Check the response code. We retry on 500-range responses to allow
	// the server time to recover, as 500's are typically not permanent
	// errors and may relate to outages on the server side. This will catch
	// invalid response codes as well, like 0 and 999.
	if resp.StatusCode == 0 || (resp.StatusCode >= 500 && resp.StatusCode != http.StatusNotImplemented) {
		return true, fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}

	return false, nil
}

func RetryPolicy(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if ctx.Err() != nil {
		// do not retry on context.Canceled errors
		if errors.Is(ctx.Err(), context.Canceled) {
			return false, ctx.Err()
		}

		// context.DeadlineExceeded is retried to handle intermittent timeouts
		return true, ctx.Err()
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true, err
	}

	return baseRetryPolicy(resp, err)
}

func NewClient(endpoint string, s3Endpoint string, timeout time.Duration, interceptors ...connect.Interceptor) *Client {
	rc := retryablehttp.NewClient()
	rc.HTTPClient.Timeout = timeout
	rc.RetryMax = 10
	rc.RetryWaitMin = 200 * time.Millisecond
	rc.RetryWaitMax = 5 * time.Second
	// Jittered exponential back-off (min*2^n) with capping.
	rc.Backoff = retryablehttp.DefaultBackoff
	// Treat only idempotent verbs + 502/503/504 + transport errors as retryable.
	rc.CheckRetry = RetryPolicy

	c := rc.StandardClient()

	return &Client{
		ClusterServiceClient: cksv1beta1connect.NewClusterServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),
		VPCServiceClient:     networkingv1beta1connect.NewVPCServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),
		CWObjectClient:       cwobjectv1connect.NewCWObjectClient(c, endpoint, connect.WithInterceptors(interceptors...)),

		s3Endpoint: s3Endpoint,
	}
}

type Client struct {
	cksv1beta1connect.ClusterServiceClient
	networkingv1beta1connect.VPCServiceClient
	cwobjectv1connect.CWObjectClient

	s3Endpoint string
}

func (c *Client) S3Client(ctx context.Context, zone string) (*s3.Client, error) {
	rc := retryablehttp.NewClient()
	rc.HTTPClient.Timeout = 30 * time.Second
	// cleanhttp.DefaultTransport disables keep-alives & idle connections
	// this helps us avoid S3 DNS caching, which can make creating/deleting buckets inconsistent
	rc.HTTPClient.Transport = cleanhttp.DefaultTransport()
	rc.RetryMax = 10
	rc.RetryWaitMin = 200 * time.Millisecond
	rc.RetryWaitMax = 5 * time.Second
	// Jittered exponential back-off (min*2^n) with capping.
	rc.Backoff = retryablehttp.DefaultBackoff
	// Treat only idempotent verbs + 502/503/504 + transport errors as retryable.
	rc.CheckRetry = RetryPolicy
	httpClient := rc.StandardClient()

	resp, err := c.CreateAccessKeyFromJWT(ctx, connect.NewRequest(&cwobjectv1.CreateAccessKeyFromJWTRequest{
		DurationSeconds: wrapperspb.UInt32(600), // 10 minutes
	}))
	if err != nil {
		return nil, err
	}

	awsConfig, err := config.LoadDefaultConfig(ctx,
		config.WithHTTPClient(httpClient),
		config.WithRegion(zone),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(resp.Msg.AccessKeyId, resp.Msg.SecretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	s3Client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		o.UsePathStyle = false
		o.BaseEndpoint = aws.String(c.s3Endpoint)
	})

	return s3Client, nil
}

func IsNotFoundError(err error) bool {
	var connectErr *connect.Error
	return errors.As(err, &connectErr) && connectErr.Code() == connect.CodeNotFound
}

//nolint:gocyclo
func HandleAPIError(ctx context.Context, err error, diagnostics *diag.Diagnostics) {
	// Check if the error is a ConnectRPC error
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		tflog.Error(ctx, "unexpected error", map[string]interface{}{
			"error": err,
		})
		diagnostics.AddError(
			"Internal Error",
			"An unexpected error occurred. Please check the provider logs for more details.",
		)
		return
	}

	details := connectErr.Details()

	//nolint:exhaustive
	switch connectErr.Code() {
	case connect.CodeNotFound:
		for _, d := range details {
			msg, valueErr := d.Value()
			if valueErr != nil {
				diagnostics.AddError(connectErr.Error(), connectErr.Message())
				break
			}
			if notFound, ok := msg.(*errdetails.ResourceInfo); ok {
				diagnostics.AddError(
					"Not Found",
					fmt.Sprintf("%s '%s' not found: %s", notFound.ResourceType, notFound.ResourceName, notFound.Description),
				)
				break
			}

			diagnostics.AddError(connectErr.Error(), connectErr.Message())
		}
	case connect.CodeAlreadyExists:
		for _, d := range details {
			msg, valueErr := d.Value()
			if valueErr != nil {
				diagnostics.AddError(connectErr.Error(), connectErr.Message())
				break
			}
			if alreadyExists, ok := msg.(*errdetails.ResourceInfo); ok {
				diagnostics.AddError(
					"Already Exists",
					fmt.Sprintf("%s '%s' already exists: %s", alreadyExists.ResourceType, alreadyExists.ResourceName, alreadyExists.Description),
				)
				break
			}

			diagnostics.AddError(connectErr.Error(), connectErr.Message())
		}
	case connect.CodeFailedPrecondition:
		for _, d := range details {
			msg, valueErr := d.Value()
			if valueErr != nil {
				diagnostics.AddError(connectErr.Error(), connectErr.Message())
				break
			}
			if precondition, ok := msg.(*errdetails.PreconditionFailure); ok {
				for _, violation := range precondition.Violations {
					diagnostics.AddError(
						"Failed Precondition",
						violation.Type+": "+violation.Description,
					)
				}
				break
			}

			diagnostics.AddError(connectErr.Error(), connectErr.Message())
		}

	case connect.CodeInvalidArgument:
		for _, d := range details {
			msg, valueErr := d.Value()
			if valueErr != nil {
				diagnostics.AddError(connectErr.Error(), connectErr.Message())
				break
			}
			if badRequest, ok := msg.(*errdetails.BadRequest); ok {
				for _, field := range badRequest.FieldViolations {
					diagnostics.AddError(
						"Bad Request",
						field.Field+": "+field.Description,
					)
				}
				break
			}

			diagnostics.AddError(connectErr.Error(), connectErr.Message())
		}

	case connect.CodeUnauthenticated:
		diagnostics.AddError(
			"Unauthenticated",
			connectErr.Error(),
		)

	case connect.CodePermissionDenied:
		diagnostics.AddError(
			"Unauthorized",
			connectErr.Error(),
		)

	case connect.CodeResourceExhausted:
		for _, d := range details {
			msg, valueErr := d.Value()
			if valueErr != nil {
				diagnostics.AddError(connectErr.Error(), connectErr.Message())
				break
			}
			if quotaFailure, ok := msg.(*errdetails.QuotaFailure); ok {
				for _, violation := range quotaFailure.Violations {
					diagnostics.AddError(
						"Quota Exceeded",
						violation.Subject+": "+violation.Description,
					)
				}
				break
			}

			diagnostics.AddError(connectErr.Error(), connectErr.Message())
		}

	default:
		// Log and return a generic internal error for unexpected cases
		tflog.Error(ctx, "unexpected error code", map[string]interface{}{
			"code":    connectErr.Code(),
			"message": connectErr.Message(),
		})
		diagnostics.AddError(
			"Internal Error",
			"An unexpected error occurred. Please check the provider logs for more details.",
		)
	}

	// safeguard for any buggy case statements
	if !diagnostics.HasError() {
		diagnostics.AddError(connectErr.Error(), connectErr.Message())
	}
}
