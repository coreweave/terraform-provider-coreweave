package model

import (
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
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
	StateCode    types.Int32       `tfsdk:"state_code"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

func (s *ForwardingPipelineStatus) Set(status *typesv1beta1.ForwardingPipelineStatus) {
	s.CreatedAt = timestampToTimeValue(status.CreatedAt)
	s.UpdatedAt = timestampToTimeValue(status.UpdatedAt)
	s.StateCode = types.Int32Value(int32(status.State.Number()))
	s.State = types.StringValue(status.State.String())
	s.StateMessage = types.StringPointerValue(status.StateMessage)
}

// ForwardingPipeline is a flattened model that combines ref, spec, and status
// fields at the top level, similar to endpoint resources and stream data source.
type ForwardingPipeline struct {
	Slug types.String `tfsdk:"slug"`

	SourceSlug      types.String `tfsdk:"source_slug"`
	DestinationSlug types.String `tfsdk:"destination_slug"`
	Enabled         types.Bool   `tfsdk:"enabled"`

	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

// Set sets the model from a ForwardingPipeline message.
func (m *ForwardingPipeline) Set(pipeline *typesv1beta1.ForwardingPipeline) {
	m.Slug = types.StringValue(pipeline.Ref.Slug)

	if pipeline.Spec.Source != nil {
		m.SourceSlug = types.StringValue(pipeline.Spec.Source.Slug)
	}
	if pipeline.Spec.Destination != nil {
		m.DestinationSlug = types.StringValue(pipeline.Spec.Destination.Slug)
	}
	m.Enabled = types.BoolValue(pipeline.Spec.Enabled)

	m.CreatedAt = timestampToTimeValue(pipeline.Status.CreatedAt)
	m.UpdatedAt = timestampToTimeValue(pipeline.Status.UpdatedAt)
	m.StateCode = types.Int32Value(int32(pipeline.Status.State.Number()))
	m.State = types.StringValue(pipeline.Status.State.String())
	m.StateMessage = types.StringPointerValue(pipeline.Status.StateMessage)
}

// ToMsg converts the model to a ForwardingPipeline message.
func (m *ForwardingPipeline) ToMsg() (msg *typesv1beta1.ForwardingPipeline, diagnostics diag.Diagnostics) {
	if m == nil {
		return nil, nil
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
		},
		Status: &typesv1beta1.ForwardingPipelineStatus{
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
			State:        typesv1beta1.ForwardingPipelineState(m.StateCode.ValueInt32()),
			StateMessage: m.StateMessage.ValueStringPointer(),
		},
	}

	return msg, diagnostics
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
