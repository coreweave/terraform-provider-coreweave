package coreweave

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"buf.build/gen/go/coreweave/cks/connectrpc/go/coreweave/cks/v1beta1/cksv1beta1connect"
	"buf.build/gen/go/coreweave/cwobject/connectrpc/go/cwobject/v1/cwobjectv1connect"
	"buf.build/gen/go/coreweave/networking/connectrpc/go/coreweave/networking/v1beta1/networkingv1beta1connect"
	"connectrpc.com/connect"

	telecasterclusterv1beta1connect "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/connectrpc/go/coreweave/telecaster/svc/cluster/v1beta1/clusterv1beta1connect"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

func NewClient(endpoint string, s3Endpoint string, timeout time.Duration, interceptors ...connect.Interceptor) *Client {
	rc := retryablehttp.NewClient()
	rc.HTTPClient.Timeout = timeout
	rc.RetryMax = 10
	// TODO: Remove this, or formalize it.
	if max, found := os.LookupEnv("COREWEAVE_HTTP_RETRY_MAX"); found {
		if maxInt, err := strconv.Atoi(max); err == nil {
			rc.RetryMax = maxInt
		}
	}
	rc.RetryWaitMin = 200 * time.Millisecond
	rc.RetryWaitMax = 5 * time.Second
	// Jittered exponential back-off (min*2^n) with capping.
	rc.Backoff = retryablehttp.DefaultBackoff
	// Treat only idempotent verbs + 502/503/504 + transport errors as retryable.
	rc.CheckRetry = RetryPolicy

	c := rc.StandardClient()

	return &Client{
		ClusterServiceClient:    cksv1beta1connect.NewClusterServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),
		VPCServiceClient:        networkingv1beta1connect.NewVPCServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),
		CWObjectClient:          cwobjectv1connect.NewCWObjectClient(c, endpoint, connect.WithInterceptors(interceptors...)),
		TelecasterServiceClient: telecasterclusterv1beta1connect.NewTelecasterServiceClient(c, endpoint, connect.WithInterceptors(interceptors...)),

		s3Endpoint: s3Endpoint,
	}
}

type Client struct {
	cksv1beta1connect.ClusterServiceClient
	networkingv1beta1connect.VPCServiceClient
	cwobjectv1connect.CWObjectClient
	telecasterclusterv1beta1connect.TelecasterServiceClient

	s3Endpoint string
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
	case connect.CodeInternal:
		diagnostics.AddError(
			"Internal Error",
			connectErr.Error(),
		)

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
			"Unexpected Error",
			fmt.Sprintf("An unexpected server error occurred: %q. Please check the provider logs for more details.", err.Error()),
		)
	}

	// safeguard for any buggy case statements
	if !diagnostics.HasError() {
		diagnostics.AddError(connectErr.Error(), connectErr.Message())
	}
}
