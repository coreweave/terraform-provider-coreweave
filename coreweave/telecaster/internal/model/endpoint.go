package model

import (
	"fmt"
	"strings"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type ForwardingEndpointRefModel struct {
	Slug types.String `tfsdk:"slug"`
}

func (r *ForwardingEndpointRefModel) Set(ref *typesv1beta1.ForwardingEndpointRef) (diagnostics diag.Diagnostics) {
	r.Slug = types.StringValue(ref.Slug)
	return
}

func (r *ForwardingEndpointRefModel) ToMsg() (msg *typesv1beta1.ForwardingEndpointRef, diagnostics diag.Diagnostics) {
	if r == nil {
		return
	}

	msg = &typesv1beta1.ForwardingEndpointRef{
		Slug: r.Slug.ValueString(),
	}
	return
}

type ForwardingEndpointSpecModel struct {
	DisplayName types.String                       `tfsdk:"display_name"`
	Prometheus  *ForwardingEndpointPrometheusModel `tfsdk:"prometheus"`
	S3          *ForwardingEndpointS3Model         `tfsdk:"s3"`
	HTTPS       *ForwardingEndpointHTTPSModel      `tfsdk:"https"`
}

func (e *ForwardingEndpointSpecModel) Set(msg *typesv1beta1.ForwardingEndpointSpec) (diagnostics diag.Diagnostics) {
	e.DisplayName = types.StringValue(msg.DisplayName)

	e.Prometheus = nil
	e.S3 = nil
	e.HTTPS = nil

	switch kind := msg.WhichConfig(); kind {
	case typesv1beta1.ForwardingEndpointSpec_Config_not_set_case:
		diagnostics.AddError("no config set for forwarding endpoint", "Config must be set when using forwarding endpoint")
	case typesv1beta1.ForwardingEndpointSpec_Prometheus_case:
		e.Prometheus = new(ForwardingEndpointPrometheusModel)
		diagnostics.Append(e.Prometheus.Set(msg.GetPrometheus())...)
	case typesv1beta1.ForwardingEndpointSpec_S3_case:
		e.S3 = new(ForwardingEndpointS3Model)
		diagnostics.Append(e.S3.Set(msg.GetS3())...)
	case typesv1beta1.ForwardingEndpointSpec_Https_case:
		e.HTTPS = new(ForwardingEndpointHTTPSModel)
		diagnostics.Append(e.HTTPS.Set(msg.GetHttps())...)
	default:
		diagnostics.AddError("Unsupported forwarding endpoint type", fmt.Sprintf("unsupported forwarding endpoint config type: %s (%d)", kind.String(), kind))
	}

	return
}

type ForwardingEndpointPrometheusModel struct {
	Endpoint types.String    `tfsdk:"endpoint"`
	TLS      *TLSConfigModel `tfsdk:"tls"`
}

func (p *ForwardingEndpointPrometheusModel) Set(prom *typesv1beta1.PrometheusRemoteWriteConfig) (diagnostics diag.Diagnostics) {
	p.Endpoint = types.StringValue(prom.Endpoint)
	var tls *TLSConfigModel
	if prom.Tls != nil {
		tls = new(TLSConfigModel)
		tls.Set(prom.Tls)
	}
	p.TLS = tls

	return
}

func (p *ForwardingEndpointPrometheusModel) ToMsg() (msg *typesv1beta1.PrometheusRemoteWriteConfig, diagnostics diag.Diagnostics) {
	if p == nil {
		return
	}

	msg = &typesv1beta1.PrometheusRemoteWriteConfig{
		Endpoint: p.Endpoint.ValueString(),
		Tls:      p.TLS.ToMsg(),
	}

	return
}

type ForwardingEndpointS3Model struct {
	URI                 types.String `tfsdk:"uri"`
	Region              types.String `tfsdk:"region"`
	RequiresCredentials types.Bool   `tfsdk:"requires_credentials"`
}

func (s *ForwardingEndpointS3Model) Set(s3 *typesv1beta1.S3Config) (diagnostics diag.Diagnostics) {
	s.URI = types.StringValue(s3.Uri)
	s.Region = types.StringValue(s3.Region)
	s.RequiresCredentials = types.BoolValue(s3.RequiresCredentials)
	return
}

func (s *ForwardingEndpointS3Model) ToMsg() (msg *typesv1beta1.S3Config, diagnostics diag.Diagnostics) {
	if s == nil {
		return
	}

	msg = &typesv1beta1.S3Config{
		Uri:                 s.URI.ValueString(),
		Region:              s.Region.ValueString(),
		RequiresCredentials: s.RequiresCredentials.ValueBool(),
	}

	return
}

type ForwardingEndpointHTTPSModel struct {
	Endpoint types.String    `tfsdk:"endpoint"`
	TLS      *TLSConfigModel `tfsdk:"tls"`
}

func (h *ForwardingEndpointHTTPSModel) Set(https *typesv1beta1.HTTPSConfig) (diagnostics diag.Diagnostics) {
	h.Endpoint = types.StringValue(https.Endpoint)

	var tls *TLSConfigModel
	if https.Tls != nil {
		tls = new(TLSConfigModel)
		tls.Set(https.Tls)
	}
	h.TLS = tls

	return
}

func (h *ForwardingEndpointHTTPSModel) ToMsg() (msg *typesv1beta1.HTTPSConfig, diagnostics diag.Diagnostics) {
	if h == nil {
		return
	}

	msg = &typesv1beta1.HTTPSConfig{
		Endpoint: h.Endpoint.ValueString(),
		Tls:      h.TLS.ToMsg(),
	}

	return
}

func (s *ForwardingEndpointSpecModel) ToMsg() (spec *typesv1beta1.ForwardingEndpointSpec, diagnostics diag.Diagnostics) {
	if s == nil {
		return nil, nil
	}

	spec = &typesv1beta1.ForwardingEndpointSpec{
		DisplayName: s.DisplayName.ValueString(),
	}

	configuredImplementations := make([]string, 0)

	if s.Prometheus != nil {
		configuredImplementations = append(configuredImplementations, "prometheus")
		prometheusMsg, diags := s.Prometheus.ToMsg()
		diagnostics.Append(diags...)
		spec.SetPrometheus(prometheusMsg)
	}

	if s.S3 != nil {
		configuredImplementations = append(configuredImplementations, "s3")
		s3Msg, diags := s.S3.ToMsg()
		diagnostics.Append(diags...)
		spec.SetS3(s3Msg)
	}

	if s.HTTPS != nil {
		configuredImplementations = append(configuredImplementations, "https")
		httpsMsg, diags := s.HTTPS.ToMsg()
		diagnostics.Append(diags...)
		spec.SetHttps(httpsMsg)
	}

	if len(configuredImplementations) != 1 {
		diagnostics.AddError(
			"Invalid Forwarding Endpoint Spec",
			fmt.Sprintf("exactly 1 auth method should be set, got %d: %s", len(configuredImplementations), strings.Join(configuredImplementations, ", ")),
		)
	}

	if diagnostics.HasError() {
		return nil, diagnostics
	}

	return spec, nil
}

// ForwardingEndpointCredentialsModel holds authentication credentials for forwarding endpoints.
// Only one of Prometheus, HTTPS, or S3 should be set, matching the endpoint type.
type ForwardingEndpointCredentialsModel struct {
	Prometheus *PrometheusCredentialsModel `tfsdk:"prometheus"`
	HTTPS      *HTTPSCredentialsModel      `tfsdk:"https"`
	S3         *S3CredentialsModel         `tfsdk:"s3"`
}

type ForwardingEndpointStatusModel struct {
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

func (s *ForwardingEndpointStatusModel) Set(status *typesv1beta1.ForwardingEndpointStatus) (diagnostics diag.Diagnostics) {
	s.CreatedAt = timestampToTimeValue(status.CreatedAt)
	s.UpdatedAt = timestampToTimeValue(status.UpdatedAt)
	s.StateCode = types.Int32Value(int32(status.State.Number()))
	s.State = types.StringValue(status.State.String())
	s.StateMessage = types.StringPointerValue(status.StateMessage)
	return
}
