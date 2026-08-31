package coreweave

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"buf.build/gen/go/coreweave/cks/connectrpc/go/coreweave/cks/v1beta1/cksv1beta1connect"
	"buf.build/gen/go/coreweave/cwobject/connectrpc/go/cwobject/v1/cwobjectv1connect"
	"buf.build/gen/go/coreweave/inference/connectrpc/go/coreweave/inference/v1alpha1/inferencev1alpha1connect"
	"buf.build/gen/go/coreweave/networking/connectrpc/go/coreweave/networking/v1beta1/networkingv1beta1connect"
	"buf.build/gen/go/coreweave/workload-federation/connectrpc/go/coreweave/workload_federation/control_plane/v1beta1/control_planev1beta1connect"
	"connectrpc.com/connect"

	"github.com/aws/aws-sdk-go-v2/aws"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

type ClientOptions struct {
	S3AttemptTimeout time.Duration
}

func NewClient(endpoint string, s3Endpoint string, timeout time.Duration, token string, userAgent string, interceptors ...connect.Interceptor) *Client {
	return NewClientWithOptions(endpoint, s3Endpoint, timeout, token, userAgent, ClientOptions{}, interceptors...)
}

func NewClientWithOptions(
	endpoint string,
	s3Endpoint string,
	timeout time.Duration,
	token string,
	userAgent string,
	options ClientOptions,
	interceptors ...connect.Interceptor,
) *Client {
	rc := retryablehttp.NewClient()
	rc.HTTPClient.Timeout = timeout
	rc.RetryMax = 10
	rc.RetryWaitMin = 200 * time.Millisecond
	rc.RetryWaitMax = 5 * time.Second
	// Jittered exponential back-off (min*2^n) with capping.
	rc.Backoff = retryablehttp.DefaultBackoff
	// Retry transient transport failures, 429 responses, and server errors other
	// than 501 while rejecting permanent request and certificate failures.
	rc.CheckRetry = RetryPolicy

	c := rc.StandardClient()

	return &Client{
		ClusterServiceClient: cksv1beta1connect.NewClusterServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),
		VPCServiceClient:     networkingv1beta1connect.NewVPCServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),
		WFControlPlaneServiceClient: control_planev1beta1connect.NewWFControlPlaneServiceClient(
			c,
			endpoint,
			connect.WithInterceptors(interceptors...),
		),
		CWObjectClient: cwobjectv1connect.NewCWObjectClient(c, endpoint, connect.WithInterceptors(interceptors...)),
		Inference: &InferenceClient{
			DeploymentServiceClient:    inferencev1alpha1connect.NewDeploymentServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),
			CapacityClaimServiceClient: inferencev1alpha1connect.NewCapacityClaimServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),
			GatewayServiceClient:       inferencev1alpha1connect.NewGatewayServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),
		},
		apiEndpoint:      endpoint,
		httpClient:       c,
		s3Endpoint:       s3Endpoint,
		s3AttemptTimeout: options.S3AttemptTimeout,
		token:            token,
		userAgent:        userAgent,
	}
}

// InferenceClient groups all inference service clients.
type InferenceClient struct {
	inferencev1alpha1connect.DeploymentServiceClient
	inferencev1alpha1connect.CapacityClaimServiceClient
	inferencev1alpha1connect.GatewayServiceClient
}

type Client struct {
	cksv1beta1connect.ClusterServiceClient
	networkingv1beta1connect.VPCServiceClient
	control_planev1beta1connect.WFControlPlaneServiceClient
	cwobjectv1connect.CWObjectClient

	Inference *InferenceClient

	apiEndpoint      string
	httpClient       *http.Client
	s3Endpoint       string
	s3AttemptTimeout time.Duration
	s3HTTPTransport  http.RoundTripper
	s3Retryer        func() aws.Retryer
	s3Now            func() time.Time
	token            string
	userAgent        string
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
			"Unexpected Error",
			fmt.Sprintf("An unexpected error occurred: %q. Please check the provider logs for more details.", err.Error()),
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
			fmt.Sprintf("An unexpected server error occurred: %q. Please check the provider logs for more details.", err.Error()),
		)
	}

	// safeguard for any buggy case statements
	if !diagnostics.HasError() {
		diagnostics.AddError(connectErr.Error(), connectErr.Message())
	}
}
