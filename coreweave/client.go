package coreweave

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"buf.build/gen/go/coreweave/cks/connectrpc/go/coreweave/cks/v1beta1/cksv1beta1connect"
	"buf.build/gen/go/coreweave/networking/connectrpc/go/coreweave/networking/v1beta1/networkingv1beta1connect"
	"connectrpc.com/connect"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

func NewClient(endpoint string, interceptors ...connect.Interceptor) *Client {
	c := http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: 10 * time.Second,
	}

	return &Client{
		ClusterServiceClient: cksv1beta1connect.NewClusterServiceClient(&c, endpoint, connect.WithInterceptors(interceptors...)),
		VPCServiceClient:     networkingv1beta1connect.NewVPCServiceClient(&c, endpoint, connect.WithInterceptors(interceptors...)),
	}
}

type Client struct {
	cksv1beta1connect.ClusterServiceClient
	networkingv1beta1connect.VPCServiceClient
}

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

	switch connectErr.Code() {
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
