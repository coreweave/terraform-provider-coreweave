package telecaster

import (
	"context"
	"fmt"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int32planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

const (
	endpointTimeout = 5 * time.Minute
)

func commonEndpointSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"slug": schema.StringAttribute{
			MarkdownDescription: "The slug of the forwarding endpoint. Used as a unique identifier.",
			Required:            true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
		},
		"display_name": schema.StringAttribute{
			MarkdownDescription: "The display name of the forwarding endpoint.",
			Required:            true,
		},
		"created_at": schema.StringAttribute{
			MarkdownDescription: "The creation time of the forwarding endpoint.",
			Computed:            true,
			CustomType:          timetypes.RFC3339Type{},
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		},
		"updated_at": schema.StringAttribute{
			MarkdownDescription: "The last update time of the forwarding endpoint.",
			Computed:            true,
			CustomType:          timetypes.RFC3339Type{},
		},
		"state_code": schema.Int32Attribute{
			MarkdownDescription: "The state code of the forwarding endpoint.",
			Computed:            true,
			PlanModifiers: []planmodifier.Int32{
				int32planmodifier.UseStateForUnknown(),
			},
		},
		"state": schema.StringAttribute{
			MarkdownDescription: "The state of the forwarding endpoint.",
			Computed:            true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		},
		"state_message": schema.StringAttribute{
			MarkdownDescription: "The state message of the forwarding endpoint.",
			Computed:            true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		},
	}
}

func pollForEndpointReady(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef) (*typesv1beta1.ForwardingEndpoint, error) {
	pollConf := retry.StateChangeConf{
		Pending: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: ref,
			}))
			if err != nil {
				return nil, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			endpoint := getResp.Msg.GetEndpoint()
			return endpoint, endpoint.GetStatus().GetState().String(), nil
		},
		Timeout: endpointTimeout,
	}

	rawEndpoint, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		return nil, err
	}

	endpoint, ok := rawEndpoint.(*typesv1beta1.ForwardingEndpoint)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T when waiting for forwarding endpoint", rawEndpoint)
	}

	return endpoint, nil
}

func readEndpoint(ctx context.Context, client *coreweave.Client, slug string, diags *diag.Diagnostics) *typesv1beta1.ForwardingEndpoint {
	ref := &typesv1beta1.ForwardingEndpointRef{Slug: slug}
	getResp, err := client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
		Ref: ref,
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			return nil
		}
		coreweave.HandleAPIError(ctx, err, diags)
		return nil
	}

	return getResp.Msg.GetEndpoint()
}

func createEndpoint(ctx context.Context, client *coreweave.Client, req *clusterv1beta1.CreateEndpointRequest) (endpoint *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	if _, err := client.CreateEndpoint(ctx, connect.NewRequest(req)); err != nil {
		coreweave.HandleAPIError(ctx, err, &diagnostics)
		return
	}

	endpoint, err := pollForEndpointReady(ctx, client, req.Ref)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &diagnostics)
		return
	}

	return endpoint, diagnostics
}

func updateEndpoint(ctx context.Context, client *coreweave.Client, req *clusterv1beta1.UpdateEndpointRequest) (endpoint *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	_, err := client.UpdateEndpoint(ctx, connect.NewRequest(req))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &diagnostics)
		return
	}

	endpoint, err = pollForEndpointReady(ctx, client, req.Ref)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &diagnostics)
		return
	}
	return
}

// deleteEndpoint is the common Delete implementation for all forwarding endpoint resources.
// The data parameter should be a struct that has a ToMsg() method returning (*typesv1beta1.ForwardingEndpoint, diag.Diagnostics).
func deleteEndpoint(ctx context.Context, client *coreweave.Client, data interface {
	ToMsg() (*typesv1beta1.ForwardingEndpoint, diag.Diagnostics)
}) (diagnostics diag.Diagnostics) {
	endpointMsg, diags := data.ToMsg()
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return
	}

	if err := deleteEndpointAndWait(ctx, client, endpointMsg.GetRef()); err != nil {
		diagnostics.AddError("Error deleting Telecaster endpoint", err.Error())
		return
	}

	return
}

func deleteEndpointAndWait(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef) error {
	if _, err := client.DeleteEndpoint(ctx, connect.NewRequest(&clusterv1beta1.DeleteEndpointRequest{
		Ref: ref,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil
		}

		return fmt.Errorf("failed to delete endpoint %q: %w", ref.Slug, err)
	}

	// Poll until the endpoint is fully deleted
	pollConf := retry.StateChangeConf{
		Pending: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{},
		Refresh: func() (any, string, error) {
			result, err := client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: ref,
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return nil, "", nil
				}
				return nil, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			endpoint := result.Msg.GetEndpoint()
			return endpoint, endpoint.GetStatus().GetState().String(), nil
		},
		Timeout: endpointTimeout,
	}

	if _, err := pollConf.WaitForStateContext(ctx); err != nil {
		return fmt.Errorf("endpoint was not deleted: %w", err)
	}

	return nil
}
