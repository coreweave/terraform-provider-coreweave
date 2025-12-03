package telecaster

import (
	"context"
	"fmt"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int32planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

// endpointCommon contains common fields for all forwarding endpoint types.
// This struct captures the flattened ref and status, plus the display_name from spec.
// Endpoint-specific resources embed this and add their own config fields.
type endpointCommon struct {
	// From ref
	Slug types.String `tfsdk:"slug"`

	// From spec (common)
	DisplayName types.String `tfsdk:"display_name"`

	// Status fields (computed)
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

func (e *endpointCommon) setFromEndpoint(endpoint *typesv1beta1.ForwardingEndpoint) (diagnostics diag.Diagnostics) {
	if endpoint == nil {
		return
	}

	// Set ref fields
	if endpoint.Ref != nil {
		e.Slug = types.StringValue(endpoint.Ref.Slug)
	}

	// Set spec fields
	if endpoint.Spec != nil {
		e.DisplayName = types.StringValue(endpoint.Spec.DisplayName)
	}

	// Set status fields
	if endpoint.Status != nil {
		var status model.ForwardingEndpointStatusModel
		diagnostics.Append(status.Set(endpoint.Status)...)
		e.CreatedAt = status.CreatedAt
		e.UpdatedAt = status.UpdatedAt
		e.StateCode = status.StateCode
		e.State = status.State
		e.StateMessage = status.StateMessage
	}

	return
}

func (e *endpointCommon) toRef() *typesv1beta1.ForwardingEndpointRef {
	return &typesv1beta1.ForwardingEndpointRef{
		Slug: e.Slug.ValueString(),
	}
}

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

func pollForEndpointReady(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef, timeout time.Duration) (*typesv1beta1.ForwardingEndpoint, error) {
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
		Timeout: timeout,
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

func pollForEndpointDeleted(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef, timeout time.Duration) error {
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
		Timeout: timeout,
	}

	_, err := pollConf.WaitForStateContext(ctx)
	return err
}

func readEndpointBySlug(ctx context.Context, client *coreweave.Client, slug string) (*typesv1beta1.ForwardingEndpoint, error) {
	ref := &typesv1beta1.ForwardingEndpointRef{Slug: slug}
	getResp, err := client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
		Ref: ref,
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			return nil, nil
		}
		return nil, err
	}
	return getResp.Msg.GetEndpoint(), nil
}

func deleteEndpoint(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef, timeout time.Duration, diags *diag.Diagnostics) {
	if _, err := client.DeleteEndpoint(ctx, connect.NewRequest(&clusterv1beta1.DeleteEndpointRequest{
		Ref: ref,
	})); err != nil {
		coreweave.HandleAPIError(ctx, err, diags)
		return
	}

	if err := pollForEndpointDeleted(ctx, client, ref, timeout); err != nil {
		coreweave.HandleAPIError(ctx, err, diags)
	}
}

func (e *endpointCommon) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}
