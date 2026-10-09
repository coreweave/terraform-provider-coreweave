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
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
	if data.SourceSlug.IsUnknown() || data.DestinationSlug.IsUnknown() || data.Slug.IsUnknown() || data.ZoneSlugs.IsUnknown() {
		return
	}

	msg, diags := data.ToMsg(ctx)
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
				MarkdownDescription: "The slug of the forwarding pipeline. Used as a unique identifier. Must be 3-65 characters, start with an alphanumeric character, and contain only alphanumeric characters and hyphens.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthBetween(slugMinLength, pipelineSlugMaxLength),
					stringvalidator.RegexMatches(slugPattern, slugPatternDetail),
				},
			},

			// Spec fields.
			//
			// source_slug and destination_slug carry no RequiresReplace:
			// UpdatePipeline accepts both, so repointing happens in place.
			// Forcing replacement would tear the pipeline down and leave a window
			// with no forwarding at all.
			"source_slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the telemetry stream to forward. Changing it repoints the pipeline in place.",
				Required:            true,
			},
			"destination_slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the forwarding endpoint to send data to. Changing it repoints the pipeline in place.",
				Required:            true,
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the forwarding pipeline is enabled. " +
					"**This is not currently enforced.** The value is stored and returned, but the forwarding workflow never reads it, so setting it to `false` does not stop data being forwarded. " +
					"To stop forwarding today, destroy the pipeline.",
				Optional: true,
			},
			"zone_slugs": schema.SetAttribute{
				MarkdownDescription: "Restricts the pipeline to these zones. Omit to use every zone with an active cluster registration for the organization. " +
					"Immutable: `UpdatePipeline` rejects any change, so editing this replaces the pipeline.",
				Optional:    true,
				ElementType: types.StringType,
				PlanModifiers: []planmodifier.Set{
					// The proto marks zone_slugs OPTIONAL and IMMUTABLE, and
					// UpdatePipeline hard-rejects a change, so this is mandatory
					// rather than a preference.
					setplanmodifier.RequiresReplace(),
				},
				Validators: []validator.Set{
					// An explicit empty set would mean "no zones" to a reader but
					// "all zones" to the server, and would read back as null — a
					// permanent diff. Make the user omit the attribute instead.
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(
						stringvalidator.LengthBetween(1, zoneSlugMaxLength),
						// The server canonicalises to lowercase, so an uppercase slug
						// in config would never match what comes back.
						stringvalidator.RegexMatches(lowercaseZonePat, "must be lowercase; the API canonicalises zone slugs and an uppercase value would produce a permanent diff"),
					),
				},
			},

			// Status fields. No state_code: it is the proto enum ordinal, it
			// duplicates `state`, and publishing it would commit us to the enum
			// numbering.
			//
			// zones_active is deliberately absent. The proto has it, but
			// db2pb.Pipeline never populates it, so shipping it would advertise a
			// permanently empty list — "zero connectors" on a healthy pipeline.
			// Adding it once the server fills it in is additive.
			"created_at": schema.StringAttribute{
				MarkdownDescription: "The time the forwarding pipeline was created.",
				Computed:            true,
				CustomType:          timetypes.RFC3339Type{},
				PlanModifiers: []planmodifier.String{
					// Fixed for the life of the pipeline, unlike the status
					// attributes below.
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "The time the forwarding pipeline was last updated.",
				Computed:            true,
				CustomType:          timetypes.RFC3339Type{},
			},
			// state and state_message move server-side independently of config —
			// a connector can fail at any time — so pinning them to state would
			// make the plan assert something it cannot know.
			"state": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("The current state of the forwarding pipeline. One of: %s.",
					coreweave.EnumMarkdownValues(typesv1beta1.ForwardingPipelineState_name, true)),
				Computed: true,
			},
			"state_message": schema.StringAttribute{
				MarkdownDescription: "Additional context about the current state, most useful when `state` is `FORWARDING_PIPELINE_STATE_ERROR`.",
				Computed:            true,
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

	pipelineMsg, diags := data.ToMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.Client.CreatePipeline(ctx, connect.NewRequest(&clusterv1beta1.CreatePipelineRequest{
		Ref:  pipelineMsg.GetRef(),
		Spec: pipelineMsg.GetSpec(),
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// Save state before polling. The pipeline exists the moment CreatePipeline
	// returns; without this, a poll failure orphans it and the next apply fails
	// AlreadyExists.
	resp.Diagnostics.Append(data.Set(ctx, createResp.Msg.GetPipeline())...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipeline, diags := awaitPipelineActive(ctx, r.Client, createResp.Msg.GetPipeline().GetRef())
	r.recordPipelineOutcome(ctx, &data, pipeline, diags, &resp.State, &resp.Diagnostics)
}

// recordPipelineOutcome writes the last observed pipeline into state and then
// surfaces any polling diagnostics.
//
// A pipeline that reaches ERROR because one of N connectors failed must fail
// the apply: the status attributes are computed-only, so a later drift from
// ACTIVE to ERROR cannot produce a plan, and a "successful" apply would hide
// the failure until someone noticed missing data. But the state write has to
// come first — state_message is the only explanation of what broke, and
// returning without it throws that away.
func (r *ResourceForwardingPipeline) recordPipelineOutcome(
	ctx context.Context,
	data *model.ForwardingPipeline,
	pipeline *typesv1beta1.ForwardingPipeline,
	pollDiags diag.Diagnostics,
	state *tfsdk.State,
	diagnostics *diag.Diagnostics,
) {
	if pipeline != nil {
		// Keep the failure detail in state so the user can read state_message.
		diagnostics.Append(data.Set(ctx, pipeline)...)
		diagnostics.Append(state.Set(ctx, data)...)
	}

	diagnostics.Append(pollDiags...)
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
		// Deleted out of band: drop it from state so the next plan re-creates
		// it, rather than reporting an error the user cannot act on.
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, getResp.Msg.GetPipeline())...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ResourceForwardingPipeline) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data model.ForwardingPipeline
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipelineMsg, diags := data.ToMsg(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateResp, err := r.Client.UpdatePipeline(ctx, connect.NewRequest(&clusterv1beta1.UpdatePipelineRequest{
		Ref:  pipelineMsg.GetRef(),
		Spec: pipelineMsg.GetSpec(),
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// Save state before polling so a poll failure doesn't leave state
	// describing the pre-update pipeline.
	resp.Diagnostics.Append(data.Set(ctx, updateResp.Msg.GetPipeline())...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pipeline, pollDiags := awaitPipelineActive(ctx, r.Client, pipelineMsg.GetRef())
	r.recordPipelineOutcome(ctx, &data, pipeline, pollDiags, &resp.State, &resp.Diagnostics)
}

// awaitPipelineActive polls the pipeline to a terminal state. It returns the
// last pipeline it saw even when it returns an error, so the caller can record
// the failing status — state_message is the only explanation of which
// connector failed.
func awaitPipelineActive(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingPipelineRef) (*typesv1beta1.ForwardingPipeline, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	var lastSeen *typesv1beta1.ForwardingPipeline

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

			pipeline := getResp.Msg.GetPipeline()
			lastSeen = pipeline
			status := pipeline.GetStatus()

			// ERROR is terminal. A pipeline fans out to one connector per source
			// Kafka cluster, so this is how a partial failure arrives.
			if status.GetState() == typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ERROR {
				return pipeline, status.GetState().String(), fmt.Errorf("pipeline %q entered state %s: %s",
					ref.GetSlug(), status.GetState().String(), pipelineStateMessage(status))
			}

			return pipeline, status.GetState().String(), nil
		},
		Timeout: pipelineTimeout,
	}

	rawPipeline, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &diagnostics)
		return lastSeen, diagnostics
	}

	pipeline, ok := rawPipeline.(*typesv1beta1.ForwardingPipeline)
	if !ok {
		diagnostics.AddError("Unexpected poll result", fmt.Sprintf("unexpected type %T when waiting for forwarding pipeline", rawPipeline))
		return lastSeen, diagnostics
	}

	return pipeline, diagnostics
}

// pipelineStateMessage returns the server's explanation for the current state,
// or a placeholder when it supplied none.
func pipelineStateMessage(status *typesv1beta1.ForwardingPipelineStatus) string {
	if msg := status.GetStateMessage(); msg != "" {
		return msg
	}
	return "no state_message was returned by the API"
}
