package model

import (
	"context"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ForwardingPipelineRef struct {
	Slug types.String `tfsdk:"slug"`
}

func (m *ForwardingPipelineRef) Set(ref *typesv1beta1.ForwardingPipelineRef) {
	r := m
	r.Slug = types.StringValue(ref.Slug)
}

func (m *ForwardingPipelineRef) ToMsg() (msg *typesv1beta1.ForwardingPipelineRef) {
	if m == nil {
		return nil
	}

	msg = &typesv1beta1.ForwardingPipelineRef{
		Slug: m.Slug.ValueString(),
	}

	return msg
}

type ForwardingPipelineSpec struct {
	Source      TelemetryStreamRef    `tfsdk:"source"`
	Destination ForwardingEndpointRef `tfsdk:"destination"`
	Enabled     types.Bool            `tfsdk:"enabled"`
}

func (m *ForwardingPipelineSpec) Set(spec *typesv1beta1.ForwardingPipelineSpec) {
	m.Source.Set(spec.GetSource())
	m.Destination.Set(spec.GetDestination())
	m.Enabled = types.BoolValue(spec.Enabled)
}

func (m *ForwardingPipelineSpec) ToMsg() (msg *typesv1beta1.ForwardingPipelineSpec) {
	if m == nil {
		return nil
	}

	msg = &typesv1beta1.ForwardingPipelineSpec{
		Enabled: m.Enabled.ValueBool(),
	}

	msg.Source = m.Source.ToMsg()
	msg.Destination = m.Destination.ToMsg()

	return msg
}

type ForwardingPipelineStatus struct {
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

func (s *ForwardingPipelineStatus) Set(status *typesv1beta1.ForwardingPipelineStatus) {
	s.CreatedAt = timestampToTimeValue(status.GetCreatedAt())
	s.UpdatedAt = timestampToTimeValue(status.GetUpdatedAt())
	s.State = types.StringValue(status.GetState().String())
	s.StateMessage = types.StringPointerValue(status.StateMessage)
}

// ForwardingPipeline is a flattened model that combines ref, spec, and status
// fields at the top level, similar to endpoint resources and stream data source.
type ForwardingPipeline struct {
	Slug types.String `tfsdk:"slug"`

	SourceSlug      types.String `tfsdk:"source_slug"`
	DestinationSlug types.String `tfsdk:"destination_slug"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	ZoneSlugs       types.Set    `tfsdk:"zone_slugs"`

	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

// Set sets the model from a ForwardingPipeline message.
func (m *ForwardingPipeline) Set(ctx context.Context, pipeline *typesv1beta1.ForwardingPipeline) (diagnostics diag.Diagnostics) {
	if pipeline == nil {
		return diagnostics
	}

	spec := pipeline.GetSpec()
	status := pipeline.GetStatus()

	m.Slug = types.StringValue(pipeline.GetRef().GetSlug())

	if spec.GetSource() != nil {
		m.SourceSlug = types.StringValue(spec.GetSource().GetSlug())
	}
	if spec.GetDestination() != nil {
		m.DestinationSlug = types.StringValue(spec.GetDestination().GetSlug())
	}

	// enabled is Optional with no default, so Terraform requires post-apply
	// state to equal the config. The server always answers with a concrete
	// bool, so writing it over a null config would fail the apply with
	// "provider produced inconsistent result". Leaving it null is also the
	// honest reading: the forwarding workflow never consults the stored value,
	// so there is no server-side setting for an omitted attribute to drift from.
	if !m.Enabled.IsNull() {
		m.Enabled = types.BoolValue(spec.GetEnabled())
	}

	// Empty means "all zones with an active cluster registration", which is
	// Terraform's null. An empty set would instead assert "no zones".
	if len(spec.GetZoneSlugs()) == 0 {
		m.ZoneSlugs = types.SetNull(types.StringType)
	} else {
		zoneSlugs, diags := types.SetValueFrom(ctx, types.StringType, spec.GetZoneSlugs())
		diagnostics.Append(diags...)
		m.ZoneSlugs = zoneSlugs
	}

	m.CreatedAt = timestampToTimeValue(status.GetCreatedAt())
	m.UpdatedAt = timestampToTimeValue(status.GetUpdatedAt())
	m.State = types.StringValue(status.GetState().String())
	if status.HasStateMessage() {
		m.StateMessage = types.StringValue(status.GetStateMessage())
	} else {
		m.StateMessage = types.StringNull()
	}

	return diagnostics
}

// ToMsg converts the model to a ForwardingPipeline message.
func (m *ForwardingPipeline) ToMsg(ctx context.Context) (msg *typesv1beta1.ForwardingPipeline, diagnostics diag.Diagnostics) {
	if m == nil {
		return nil, nil
	}

	var zoneSlugs []string
	if !m.ZoneSlugs.IsNull() && !m.ZoneSlugs.IsUnknown() {
		diagnostics.Append(m.ZoneSlugs.ElementsAs(ctx, &zoneSlugs, false)...)
	}

	var createdAt, updatedAt *timestamppb.Timestamp

	if !m.UpdatedAt.IsNull() && !m.UpdatedAt.IsUnknown() {
		updatedAtTime, diags := m.UpdatedAt.ValueRFC3339Time()
		diagnostics.Append(diags...)
		updatedAt = timestamppb.New(updatedAtTime)
	}

	if !m.CreatedAt.IsNull() && !m.CreatedAt.IsUnknown() {
		createdAtTime, diags := m.CreatedAt.ValueRFC3339Time()
		diagnostics.Append(diags...)
		createdAt = timestamppb.New(createdAtTime)
	}

	if diagnostics.HasError() {
		return nil, diagnostics
	}

	msg = &typesv1beta1.ForwardingPipeline{
		Ref: &typesv1beta1.ForwardingPipelineRef{
			Slug: m.Slug.ValueString(),
		},
		Spec: &typesv1beta1.ForwardingPipelineSpec{
			Enabled: m.Enabled.ValueBool(),
			Source: &typesv1beta1.TelemetryStreamRef{
				Slug: m.SourceSlug.ValueString(),
			},
			Destination: &typesv1beta1.ForwardingEndpointRef{
				Slug: m.DestinationSlug.ValueString(),
			},
			ZoneSlugs: zoneSlugs,
		},
		Status: &typesv1beta1.ForwardingPipelineStatus{
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
			State:        pipelineStateFromName(m.State),
			StateMessage: m.StateMessage.ValueStringPointer(),
		},
	}

	return msg, diagnostics
}

// pipelineStateFromName maps the status string back onto the proto enum.
// Status is OUTPUT_ONLY and the provider never sends it, so an unrecognized or
// unknown value degrades to UNSPECIFIED rather than raising a diagnostic.
func pipelineStateFromName(state types.String) typesv1beta1.ForwardingPipelineState {
	if state.IsNull() || state.IsUnknown() {
		return typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_UNSPECIFIED
	}

	return typesv1beta1.ForwardingPipelineState(typesv1beta1.ForwardingPipelineState_value[state.ValueString()])
}

// ToRef returns a ForwardingPipelineRef from the model.
func (m *ForwardingPipeline) ToRef() *typesv1beta1.ForwardingPipelineRef {
	if m == nil {
		return nil
	}

	return &typesv1beta1.ForwardingPipelineRef{
		Slug: m.Slug.ValueString(),
	}
}
