package model

import (
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type ForwardingPipelineRefModel struct {
	Slug types.String `tfsdk:"slug"`
}

func (m *ForwardingPipelineRefModel) Set(ref *typesv1beta1.ForwardingPipelineRef) {
	r := m
	r.Slug = types.StringValue(ref.Slug)
}

func (m *ForwardingPipelineRefModel) ToMsg() (msg *typesv1beta1.ForwardingPipelineRef) {
	if m == nil {
		return nil
	}

	msg = &typesv1beta1.ForwardingPipelineRef{
		Slug: m.Slug.ValueString(),
	}

	return msg
}

type ForwardingPipelineSpecModel struct {
	Source      TelemetryStreamRefModel    `tfsdk:"source"`
	Destination ForwardingEndpointRefModel `tfsdk:"destination"`
	Enabled     types.Bool                 `tfsdk:"enabled"`
}

func (m *ForwardingPipelineSpecModel) Set(spec *typesv1beta1.ForwardingPipelineSpec) {
	m.Source.Set(spec.GetSource())
	m.Destination.Set(spec.GetDestination())
	m.Enabled = types.BoolValue(spec.Enabled)
}

func (m *ForwardingPipelineSpecModel) ToMsg() (msg *typesv1beta1.ForwardingPipelineSpec) {
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

type ForwardingPipelineStatusModel struct {
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

func (s *ForwardingPipelineStatusModel) Set(status *typesv1beta1.ForwardingPipelineStatus) {
	s.CreatedAt = timestampToTimeValue(status.CreatedAt)
	s.UpdatedAt = timestampToTimeValue(status.UpdatedAt)
	s.StateCode = types.Int32Value(int32(status.State.Number()))
	s.State = types.StringValue(status.State.String())
	s.StateMessage = types.StringPointerValue(status.StateMessage)
}
