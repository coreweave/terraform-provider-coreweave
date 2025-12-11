package observability

import (
	"context"
	"fmt"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int32planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

const (
	pipelineTimeout = 10 * time.Minute
)

var (
	_ resource.ResourceWithConfigure      = &ResourceForwardingPipeline{}
	_ resource.ResourceWithValidateConfig = &ResourceForwardingPipeline{}
	_ resource.ResourceWithImportState    = &ResourceForwardingPipeline{}
)

func NewForwardingPipelineResource() resource.Resource {
	return &ResourceForwardingPipeline{}
}

type ResourceForwardingPipeline struct {
	coretf.CoreResource
}

// ValidateConfig implements resource.ResourceWithValidateConfig.
func (r *ResourceForwardingPipeline) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data model.ForwardingPipeline
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Skip validation if any values are unknown (e.g., during plan with resource references)
	if data.SourceSlug.IsUnknown() || data.DestinationSlug.IsUnknown() {
		return
	}

	msg, diags := data.ToMsg()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := protovalidate.Validate(msg); err != nil {
		resp.Diagnostics.AddError("Validation Error", err.Error())
	}
}

func (r *ResourceForwardingPipeline) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
}

func (r *ResourceForwardingPipeline) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_observability_telemetry_relay_pipeline"
}

func (r *ResourceForwardingPipeline) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telemetry Relay forwarding pipeline. Connects a telemetry stream to a forwarding endpoint.",
		Attributes: map[string]schema.Attribute{
			// Ref fields
			"slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the forwarding pipeline. Used as a unique identifier.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},

			// Spec fields
			"source_slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the telemetry stream to forward.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"destination_slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the forwarding endpoint to send data to.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the forwarding pipeline is enabled.",
				Required:            true,
			},

			// Status fields
			"created_at": schema.StringAttribute{
				MarkdownDescription: "The time the forwarding pipeline was created.",
				Computed:            true,
				CustomType:          timetypes.RFC3339Type{},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "The time the forwarding pipeline was last updated.",
				Computed:            true,
				CustomType:          timetypes.RFC3339Type{},
			},
			"state_code": schema.Int32Attribute{
				MarkdownDescription: "The numeric state code of the forwarding pipeline.",
				Computed:            true,
				PlanModifiers: []planmodifier.Int32{
					int32planmodifier.UseStateForUnknown(),
				},
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "The current state of the forwarding pipeline.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"state_message": schema.StringAttribute{
				MarkdownDescription: "A message associated with the current state of the forwarding pipeline.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *ResourceForwardingPipeline) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data model.ForwardingPipeline

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipelineMsg, diags := data.ToMsg()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.Client.CreatePipeline(ctx, connect.NewRequest(&clusterv1beta1.CreatePipelineRequest{
		Ref:  pipelineMsg.Ref,
		Spec: pipelineMsg.Spec,
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	pipeline, err := pollForPipelineActive(ctx, r.Client, createResp.Msg.Pipeline.Ref)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(pipeline)
	resp.State.Set(ctx, &data)
}

func (r *ResourceForwardingPipeline) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data model.ForwardingPipeline

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ref := data.ToRef()

	if _, err := r.Client.DeletePipeline(ctx, connect.NewRequest(&clusterv1beta1.DeletePipelineRequest{
		Ref: ref,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error deleting Telemetry Relay forwarding pipeline", fmt.Sprintf("failed to delete pipeline %q: %v", ref.Slug, err))
		return
	}

	// Poll until the pipeline is fully deleted
	pollConf := retry.StateChangeConf{
		Pending: []string{
			typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_PENDING.String(),
		},
		Target: []string{},
		Refresh: func() (any, string, error) {
			result, err := r.Client.GetPipeline(ctx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
				Ref: ref,
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return nil, "", nil
				}
				return nil, typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_UNSPECIFIED.String(), err
			}
			pipeline := result.Msg.Pipeline
			return pipeline, pipeline.Status.State.String(), nil
		},
		Timeout: pipelineTimeout,
	}

	if _, err := pollConf.WaitForStateContext(ctx); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.State.RemoveResource(ctx)
}

func (r *ResourceForwardingPipeline) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data model.ForwardingPipeline
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ref := &typesv1beta1.ForwardingPipelineRef{Slug: data.Slug.ValueString()}
	getResp, err := r.Client.GetPipeline(ctx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
		Ref: ref,
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(getResp.Msg.Pipeline)
	resp.State.Set(ctx, &data)
}

func (r *ResourceForwardingPipeline) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data model.ForwardingPipeline
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipelineMsg, diags := data.ToMsg()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.Client.UpdatePipeline(ctx, connect.NewRequest(&clusterv1beta1.UpdatePipelineRequest{
		Ref:  pipelineMsg.Ref,
		Spec: pipelineMsg.Spec,
	})); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	pipeline, err := pollForPipelineActive(ctx, r.Client, pipelineMsg.Ref)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(pipeline)
	resp.State.Set(ctx, &data)
}

func pollForPipelineActive(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingPipelineRef) (*typesv1beta1.ForwardingPipeline, error) {
	pollConf := retry.StateChangeConf{
		Pending: []string{
			typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_PENDING.String(),
		},
		Target: []string{
			typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := client.GetPipeline(ctx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
				Ref: ref,
			}))
			if err != nil {
				return nil, typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_UNSPECIFIED.String(), err
			}
			return getResp.Msg.Pipeline, getResp.Msg.Pipeline.Status.State.String(), nil
		},
		Timeout: pipelineTimeout,
	}

	rawPipeline, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		return nil, err
	}

	pipeline, ok := rawPipeline.(*typesv1beta1.ForwardingPipeline)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T when waiting for forwarding pipeline", rawPipeline)
	}

	return pipeline, nil
}
