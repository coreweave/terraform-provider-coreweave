package telecaster

import (
	"context"
	"fmt"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	telecastertypesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
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
	Ref    types.Object `tfsdk:"ref"`
	Spec   types.Object `tfsdk:"spec"`
	Status types.Object `tfsdk:"status"`
}

func (m *ResourceForwardingPipelineModel) Set(ctx context.Context, pipeline *telecastertypesv1beta1.ForwardingPipeline) (diagnostics diag.Diagnostics) {
	var ref model.ForwardingPipelineRefModel
	diagnostics.Append(m.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(ref.Set(pipeline.Ref)...)
	refObj, diags := types.ObjectValueFrom(ctx, m.Ref.AttributeTypes(ctx), ref)
	diagnostics.Append(diags...)
	m.Ref = refObj

	var spec model.ForwardingPipelineSpecModel
	diagnostics.Append(m.Spec.As(ctx, &spec, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(spec.Set(pipeline.Spec)...)
	specObject, diags := types.ObjectValueFrom(ctx, m.Spec.AttributeTypes(ctx), spec)
	diagnostics.Append(diags...)
	m.Spec = specObject

	var status model.ForwardingPipelineStatusModel
	diagnostics.Append(m.Status.As(ctx, &status, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(status.Set(pipeline.Status)...)
	statusObject, diags := types.ObjectValueFrom(ctx, m.Status.AttributeTypes(ctx), status)
	diagnostics.Append(diags...)
	m.Status = statusObject

	return
}

func (m *ResourceForwardingPipelineModel) ToMsg(ctx context.Context) (msg *telecastertypesv1beta1.ForwardingPipeline, diagnostics diag.Diagnostics) {
	if m == nil {
		return
	}

	var diags diag.Diagnostics
	msg = &telecastertypesv1beta1.ForwardingPipeline{}

	var ref model.ForwardingPipelineRefModel
	diagnostics.Append(m.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	msg.Ref, diags = ref.ToMsg()
	diagnostics.Append(diags...)

	var spec model.ForwardingPipelineSpecModel
	diagnostics.Append(m.Spec.As(ctx, &spec, basetypes.ObjectAsOptions{})...)
	msg.Spec, diags = spec.ToMsg()
	diagnostics.Append(diags...)

	// status not implemented because it is not needed for any ops.

	if diagnostics.HasError() {
		return nil, diagnostics
	}

	return
}

func (r *ResourceForwardingPipeline) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}

func (r *ResourceForwardingPipeline) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telecaster_forwarding_pipeline"
}

func (r *ResourceForwardingPipeline) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster forwarding pipeline",
		Attributes: map[string]schema.Attribute{
			"ref": schema.SingleNestedAttribute{
				MarkdownDescription: "Reference to the Telecaster forwarding pipeline.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"slug": schema.StringAttribute{
						MarkdownDescription: "The slug of the forwarding pipeline. Used as a unique identifier.",
						Required:            true,
					},
				},
			},
			"spec": schema.SingleNestedAttribute{
				MarkdownDescription: "The specification for the forwarding pipeline.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"source": schema.SingleNestedAttribute{
						MarkdownDescription: "The telemetry stream to forward.",
						Required:            true,
						Attributes: map[string]schema.Attribute{
							"slug": schema.StringAttribute{
								MarkdownDescription: "The slug of the telemetry stream.",
								Required:            true,
							},
						},
					},
					"destination": schema.SingleNestedAttribute{
						MarkdownDescription: "The forwarding endpoint to send data to.",
						Required:            true,
						Attributes: map[string]schema.Attribute{
							"slug": schema.StringAttribute{
								MarkdownDescription: "The slug of the forwarding endpoint.",
								Required:            true,
							},
						},
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
					"state_message": schema.StringAttribute{
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

	pipelineMsg, diags := data.ToMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipeline, err := r.Client.CreatePipeline(ctx, connect.NewRequest(&clusterv1beta1.CreatePipelineRequest{
		Ref:  pipelineMsg.Ref,
		Spec: pipelineMsg.Spec,
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		resp.Diagnostics.AddError(
			"Error Creating Telecaster Forwarding Pipeline",
			fmt.Sprintf("Could not create Telecaster Forwarding Pipeline %q: %v", pipelineMsg.Ref.Slug, err),
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

	if finalPipelineRaw, err := pollConf.WaitForStateContext(ctx); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	} else if finalPipeline, ok := finalPipelineRaw.(*telecastertypesv1beta1.ForwardingPipeline); !ok {
		resp.Diagnostics.AddError(
			"Telecaster Pipeline Type Assertion Error",
			fmt.Sprintf("Expected ForwardingPipeline type but got %T instead. This is a bug in the provider.", finalPipelineRaw),
		)
	} else {
		resp.Diagnostics.Append(data.Set(ctx, finalPipeline)...)
	}

	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ResourceForwardingPipeline) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ResourceForwardingPipelineModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipelineMsg, diags := data.ToMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.Client.DeletePipeline(ctx, connect.NewRequest(&clusterv1beta1.DeletePipelineRequest{
		Ref: pipelineMsg.Ref,
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting Telecaster Forwarding Pipeline",
			fmt.Sprintf("Could not delete Telecaster Forwarding Pipeline %q: %v", pipelineMsg.Ref.Slug, err),
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
				Ref: pipelineMsg.Ref,
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

	if _, err = pollConf.WaitForStateContext(ctx); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *ResourceForwardingPipeline) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ResourceForwardingPipelineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipelineMsg, diags := data.ToMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := r.Client.GetPipeline(ctx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
		Ref: pipelineMsg.Ref,
	}))
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Telecaster Forwarding Pipeline",
			fmt.Sprintf("Could not read Telecaster Forwarding Pipeline %q: %v", pipelineMsg.Ref.Slug, err),
		)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, getResp.Msg.Pipeline)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ResourceForwardingPipeline) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ResourceForwardingPipelineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipelineMsg, diags := data.ToMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.Client.UpdatePipeline(ctx, connect.NewRequest(&clusterv1beta1.UpdatePipelineRequest{
		Ref: pipelineMsg.Ref,
		Spec: pipelineMsg.Spec,
	})); err != nil {
		resp.Diagnostics.AddError(
			"Error Updating Telecaster Forwarding Pipeline",
			fmt.Sprintf("Could not update Telecaster Forwarding Pipeline %q: %v", pipelineMsg.Ref.Slug, err),
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
				Ref: pipelineMsg.Ref,
			}))
			if err != nil {
				return nil, telecastertypesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_UNSPECIFIED.String(), err
			}
			return getResp.Msg.Pipeline, getResp.Msg.Pipeline.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	finalPipelineRaw, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	} else if finalPipeline, ok := finalPipelineRaw.(*typesv1beta1.ForwardingPipeline); !ok {
		resp.Diagnostics.AddError(
			"Telecaster Pipeline Type Assertion Error",
			fmt.Sprintf("Expected Pipeline type but got %T instead. This is a bug in the provider.", finalPipelineRaw),
		)
	} else {
		resp.Diagnostics.Append(data.Set(ctx, finalPipeline)...)
	}

	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
