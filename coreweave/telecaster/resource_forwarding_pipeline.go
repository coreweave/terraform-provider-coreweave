package telecaster

import (
	"context"
	"fmt"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/cw/telecaster/svc/cluster/v1beta1"
	telecastertypesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/cw/telecaster/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

var (
	_ resource.ResourceWithConfigure   = &ResourceForwardingPipeline{}
	_ resource.ResourceWithImportState = &ResourceForwardingPipeline{}
)

func NewForwardingPipelineResource() resource.Resource {
	return &ResourceForwardingPipeline{}
}

type ResourceForwardingPipeline struct {
	coretf.CoreResource
}

type ResourceForwardingPipelineModel struct {
	Ref    ForwardingPipelineRefModel    `tfsdk:",inline"`
	Spec   ForwardingPipelineSpecModel   `tfsdk:"spec"`
	Status ForwardingPipelineStatusModel `tfsdk:"status"`
}

type ForwardingPipelineRefModel struct {
	Slug types.String `tfsdk:"slug"`
}

func (m ForwardingPipelineRefModel) ToProto() *telecastertypesv1beta1.ForwardingPipelineRef {
	return &telecastertypesv1beta1.ForwardingPipelineRef{
		Slug: m.Slug.ValueString(),
	}
}

type ForwardingPipelineSpecModel struct {
	Source      TelemetryStreamRefModel    `tfsdk:"source"`
	Destination ForwardingEndpointRefModel `tfsdk:"destination"`
	Enabled     types.Bool                 `tfsdk:"enabled"`
}

func (m ForwardingPipelineSpecModel) ToProto() *telecastertypesv1beta1.ForwardingPipelineSpec {
	return &telecastertypesv1beta1.ForwardingPipelineSpec{
		Source: &telecastertypesv1beta1.TelemetryStreamRef{
			Slug: m.Source.Slug.ValueString(),
		},
		Destination: &telecastertypesv1beta1.ForwardingEndpointRef{
			Slug: m.Destination.Slug.ValueString(),
		},
		Enabled: m.Enabled.ValueBool(),
	}
}

type ForwardingPipelineStatusModel struct {
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"message"`
}

func (m *ResourceForwardingPipelineModel) Set(pipeline *telecastertypesv1beta1.ForwardingPipeline) {
	if pipeline.Ref != nil {
		m.Ref.Slug = types.StringValue(pipeline.Ref.Slug)
	}

	if pipeline.Spec != nil {
		m.Spec.Source.Slug = types.StringValue(pipeline.Spec.Source.Slug)
		m.Spec.Destination.Slug = types.StringValue(pipeline.Spec.Destination.Slug)
		m.Spec.Enabled = types.BoolValue(pipeline.Spec.Enabled)
	}

	if pipeline.Status != nil {
		m.Status.CreatedAt = timetypes.NewRFC3339TimeValue(pipeline.Status.CreatedAt.AsTime())
		m.Status.UpdatedAt = timetypes.NewRFC3339TimeValue(pipeline.Status.UpdatedAt.AsTime())
		m.Status.StateCode = types.Int32Value(int32(pipeline.Status.State.Number()))
		m.Status.State = types.StringValue(pipeline.Status.State.String())
		m.Status.StateMessage = types.StringPointerValue(pipeline.Status.StateMessage)
	}
}

func (r *ResourceForwardingPipeline) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}

func (r *ResourceForwardingPipeline) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_forwarding_pipeline"
}

func (r *ResourceForwardingPipeline) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster forwarding pipeline",
		Attributes: map[string]schema.Attribute{
			"slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the forwarding pipeline. Used as a unique identifier.",
				Computed:            true,
			},
			"spec": schema.SingleNestedAttribute{
				MarkdownDescription: "The specification for the forwarding pipeline.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"telemetry_stream": schema.SingleNestedAttribute{
						MarkdownDescription: "The telemetry stream to forward.",
						Required:            true,
						Attributes:          map[string]schema.Attribute{},
					},
					"forwarding_endpoint": schema.SingleNestedAttribute{
						MarkdownDescription: "The forwarding endpoint to send data to.",
						Required:            true,
						Attributes:          map[string]schema.Attribute{},
					},
					"enabled": schema.BoolAttribute{
						MarkdownDescription: "Whether the forwarding pipeline is enabled.",
						Required:            true,
					},
				},
			},
			"status": schema.SingleNestedAttribute{
				MarkdownDescription: "The status of the forwarding pipeline.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"created_at": schema.StringAttribute{
						MarkdownDescription: "The time the forwarding pipeline was created.",
						Computed:            true,
						CustomType:          timetypes.RFC3339Type{},
					},
					"updated_at": schema.StringAttribute{
						MarkdownDescription: "The time the forwarding pipeline was last updated.",
						Computed:            true,
						CustomType:          timetypes.RFC3339Type{},
					},
					"state_code": schema.Int32Attribute{
						MarkdownDescription: "The numeric state code of the forwarding pipeline.",
						Computed:            true,
					},
					"state": schema.StringAttribute{
						MarkdownDescription: "The current state of the forwarding pipeline.",
						Computed:            true,
					},
					"message": schema.StringAttribute{
						MarkdownDescription: "A message associated with the current state of the forwarding pipeline.",
						Computed:            true,
					},
				},
			},
		},
	}
}

func (r *ResourceForwardingPipeline) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ResourceForwardingPipelineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipeline, err := r.Client.CreatePipeline(ctx, connect.NewRequest(&clusterv1beta1.CreatePipelineRequest{
		Spec: data.Spec.ToProto(),
	}))
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Telecaster Forwarding Pipeline",
			fmt.Sprintf("Could not create Telecaster Forwarding Pipeline %q: %v", data.Ref.Slug.ValueString(), err),
		)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_PENDING.String(),
		},
		Target: []string{
			telecastertypesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := r.Client.GetPipeline(ctx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
				Ref: pipeline.Msg.Pipeline.Ref,
			}))
			if err != nil {
				return nil, telecastertypesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_UNSPECIFIED.String(), err
			}
			return getResp.Msg.Pipeline, getResp.Msg.Pipeline.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	_, err = pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(pipeline.Msg.Pipeline)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ResourceForwardingPipeline) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ResourceForwardingPipelineModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.Client.DeletePipeline(ctx, connect.NewRequest(&clusterv1beta1.DeletePipelineRequest{
		Ref: data.Ref.ToProto(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting Telecaster Forwarding Pipeline",
			fmt.Sprintf("Could not delete Telecaster Forwarding Pipeline %q: %v", data.Ref.Slug.ValueString(), err),
		)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{
			"deleted",
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := r.Client.GetPipeline(ctx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
				Ref: data.Ref.ToProto(),
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return nil, "deleted", nil
				}
				return nil, telecastertypesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_UNSPECIFIED.String(), err
			}
			return getResp.Msg.Pipeline, getResp.Msg.Pipeline.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	_, err = pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (r *ResourceForwardingPipeline) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ResourceForwardingPipelineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipeline, err := r.Client.GetPipeline(ctx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
		Ref: data.Ref.ToProto(),
	}))
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Telecaster Forwarding Pipeline",
			fmt.Sprintf("Could not read Telecaster Forwarding Pipeline %q: %v", data.Ref.Slug.ValueString(), err),
		)
		return
	}

	data.Set(pipeline.Msg.Pipeline)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ResourceForwardingPipeline) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ResourceForwardingPipelineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.Client.UpdatePipeline(ctx, connect.NewRequest(&clusterv1beta1.UpdatePipelineRequest{
		Ref: data.Ref.ToProto(),
	})); err != nil {
		resp.Diagnostics.AddError(
			"Error Updating Telecaster Forwarding Pipeline",
			fmt.Sprintf("Could not update Telecaster Forwarding Pipeline %q: %v", data.Ref.Slug.ValueString(), err),
		)
		return
	}

	pipeline, err := r.Client.GetPipeline(ctx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
		Ref: data.Ref.ToProto(),
	}))
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Telecaster Forwarding Pipeline",
			fmt.Sprintf("Could not read Telecaster Forwarding Pipeline %q: %v", data.Ref.Slug.ValueString(), err),
		)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_PENDING.String(),
		},
		Target: []string{
			telecastertypesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := r.Client.GetPipeline(ctx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
				Ref: data.Ref.ToProto(),
			}))
			if err != nil {
				return nil, telecastertypesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_UNSPECIFIED.String(), err
			}
			return getResp.Msg.Pipeline, getResp.Msg.Pipeline.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	_, err = pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(pipeline.Msg.Pipeline)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
