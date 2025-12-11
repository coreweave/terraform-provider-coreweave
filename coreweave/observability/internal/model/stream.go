package model

import (
	"fmt"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type TelemetryStreamRef struct {
	Slug types.String `tfsdk:"slug"`
}

func (r *TelemetryStreamRef) Set(ref *typesv1beta1.TelemetryStreamRef) {
	r.Slug = types.StringValue(ref.Slug)
}

func (r *TelemetryStreamRef) ToMsg() (msg *typesv1beta1.TelemetryStreamRef) {
	if r == nil {
		return nil
	}

	msg = &typesv1beta1.TelemetryStreamRef{
		Slug: r.Slug.ValueString(),
	}

	return msg
}

type TelemetryStreamSpec struct {
	DisplayName types.String       `tfsdk:"display_name"`
	Logs        *LogsStreamSpec    `tfsdk:"logs"`
	Metrics     *MetricsStreamSpec `tfsdk:"metrics"`
}

func (s *TelemetryStreamSpec) Set(spec *typesv1beta1.TelemetryStreamSpec) (diagnostics diag.Diagnostics) {
	s.DisplayName = types.StringValue(spec.DisplayName)

	switch k := spec.WhichKind(); k {
	case typesv1beta1.TelemetryStreamSpec_Kind_not_set_case:
		diagnostics.AddError("Stream kind not set", "A telemetry stream must specify either logs or metrics")
	case typesv1beta1.TelemetryStreamSpec_Metrics_case:
		s.Metrics = new(MetricsStreamSpec)
		diagnostics.Append(s.Metrics.Set(spec.GetMetrics())...)
	case typesv1beta1.TelemetryStreamSpec_Logs_case:
		s.Logs = new(LogsStreamSpec)
		diagnostics.Append(s.Logs.Set(spec.GetLogs())...)
	default:
		diagnostics.AddError("Unknown Stream Spec Kind", fmt.Sprintf("spec's kind %q (%d) is not recognized by the provider. This may not be implemented in the provider yet, or may require an update.", k.String(), k))
	}

	return
}

type LogsStreamSpec struct {
}

func (s *LogsStreamSpec) Set(msg *typesv1beta1.LogsStreamSpec) (diagnostics diag.Diagnostics) {
	return
}

type MetricsStreamSpec struct {
}

func (s *MetricsStreamSpec) Set(msg *typesv1beta1.MetricsStreamSpec) (diagnostics diag.Diagnostics) {
	return
}

type TelemetryStreamStatus struct {
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	StateString  types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

func (s *TelemetryStreamStatus) Set(status *typesv1beta1.TelemetryStreamStatus) {
	s.CreatedAt = timestampToTimeValue(status.CreatedAt)
	s.UpdatedAt = timestampToTimeValue(status.UpdatedAt)
	s.StateCode = types.Int32Value(int32(status.State.Number()))
	s.StateString = types.StringValue(status.State.String())
	s.StateMessage = types.StringPointerValue(status.StateMessage)
}

// TelemetryStreamDataSource is a flattened model for the stream data source
// that combines ref, spec, and status fields at the top level, similar to endpoint resources.
type TelemetryStreamDataSource struct {
	Slug types.String `tfsdk:"slug"`

	DisplayName types.String       `tfsdk:"display_name"`
	Kind        types.String       `tfsdk:"kind"`
	Logs        *LogsStreamSpec    `tfsdk:"logs"`
	Metrics     *MetricsStreamSpec `tfsdk:"metrics"`

	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

func (m *TelemetryStreamDataSource) Set(stream *typesv1beta1.TelemetryStream) (diagnostics diag.Diagnostics) {
	if stream == nil {
		return
	}

	m.Slug = types.StringValue(stream.Ref.Slug)

	m.DisplayName = types.StringValue(stream.Spec.DisplayName)
	m.Kind = types.StringValue(stream.Spec.WhichKind().String())

	switch k := stream.Spec.WhichKind(); k {
	case typesv1beta1.TelemetryStreamSpec_Kind_not_set_case:
		// Kind not set - leave both nil
	case typesv1beta1.TelemetryStreamSpec_Metrics_case:
		m.Metrics = new(MetricsStreamSpec)
		diagnostics.Append(m.Metrics.Set(stream.Spec.GetMetrics())...)
	case typesv1beta1.TelemetryStreamSpec_Logs_case:
		m.Logs = new(LogsStreamSpec)
		diagnostics.Append(m.Logs.Set(stream.Spec.GetLogs())...)
	default:
		diagnostics.AddError("Unknown Stream Spec Kind", fmt.Sprintf("spec's kind %q (%d) is not recognized by the provider. This may not be implemented in the provider yet, or may require an update.", k.String(), k))
	}

	m.CreatedAt = timestampToTimeValue(stream.Status.CreatedAt)
	m.UpdatedAt = timestampToTimeValue(stream.Status.UpdatedAt)
	m.StateCode = types.Int32Value(int32(stream.Status.State.Number()))
	m.State = types.StringValue(stream.Status.State.String())
	m.StateMessage = types.StringPointerValue(stream.Status.StateMessage)

	return
}

func (m *TelemetryStreamDataSource) ToRef() *typesv1beta1.TelemetryStreamRef {
	if m == nil {
		return nil
	}

	return &typesv1beta1.TelemetryStreamRef{
		Slug: m.Slug.ValueString(),
	}
}
