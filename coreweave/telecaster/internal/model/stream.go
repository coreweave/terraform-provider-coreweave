package model

import (
	"fmt"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type TelemetryStreamRefModel struct {
	Slug types.String `tfsdk:"slug"`
}

func (r *TelemetryStreamRefModel) Set(ref *typesv1beta1.TelemetryStreamRef) {
	r.Slug = types.StringValue(ref.Slug)
}

func (r *TelemetryStreamRefModel) ToMsg() (msg *typesv1beta1.TelemetryStreamRef) {
	if r == nil {
		return nil
	}

	msg = &typesv1beta1.TelemetryStreamRef{
		Slug: r.Slug.ValueString(),
	}

	return msg
}

type TelemetryStreamSpecModel struct {
	DisplayName types.String            `tfsdk:"display_name"`
	Logs        *LogsStreamSpecModel    `tfsdk:"logs"`
	Metrics     *MetricsStreamSpecModel `tfsdk:"metrics"`
}

func (s *TelemetryStreamSpecModel) Set(spec *typesv1beta1.TelemetryStreamSpec) (diagnostics diag.Diagnostics) {
	s.DisplayName = types.StringValue(spec.DisplayName)

	switch k := spec.WhichKind(); k {
	case typesv1beta1.TelemetryStreamSpec_Kind_not_set_case:
		diagnostics.AddError("Stream kind not set", "A telemetry stream must specify either logs or metrics")
	case typesv1beta1.TelemetryStreamSpec_Metrics_case:
		s.Metrics = new(MetricsStreamSpecModel)
		diagnostics.Append(s.Metrics.Set(spec.GetMetrics())...)
	case typesv1beta1.TelemetryStreamSpec_Logs_case:
		s.Logs = new(LogsStreamSpecModel)
		diagnostics.Append(s.Logs.Set(spec.GetLogs())...)
	default:
		diagnostics.AddError("Unknown Stream Spec Kind", fmt.Sprintf("spec's kind %q (%d) is not recognized by the provider. This may not be implemented in the provider yet, or may require an update.", k.String(), k))
	}

	return
}

type LogsStreamSpecModel struct {
}

func (s *LogsStreamSpecModel) Set(msg *typesv1beta1.LogsStreamSpec) (diagnostics diag.Diagnostics) {
	return
}

type MetricsStreamSpecModel struct {
}

func (s *MetricsStreamSpecModel) Set(msg *typesv1beta1.MetricsStreamSpec) (diagnostics diag.Diagnostics) {
	return
}

type TelemetryStreamStatusModel struct {
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	StateString  types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

func (s *TelemetryStreamStatusModel) Set(status *typesv1beta1.TelemetryStreamStatus) {
	s.CreatedAt = timestampToTimeValue(status.CreatedAt)
	s.UpdatedAt = timestampToTimeValue(status.UpdatedAt)
	s.StateCode = types.Int32Value(int32(status.State.Number()))
	s.StateString = types.StringValue(status.State.String())
	s.StateMessage = types.StringPointerValue(status.StateMessage)
}
