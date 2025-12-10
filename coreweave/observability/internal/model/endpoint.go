package model

import (
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ForwardingEndpointRef struct {
	Slug types.String `tfsdk:"slug"`
}

func (r *ForwardingEndpointRef) Set(ref *typesv1beta1.ForwardingEndpointRef) {
	r.Slug = types.StringValue(ref.Slug)
}

func (r *ForwardingEndpointRef) ToMsg() (msg *typesv1beta1.ForwardingEndpointRef) {
	if r == nil {
		return nil
	}

	msg = &typesv1beta1.ForwardingEndpointRef{
		Slug: r.Slug.ValueString(),
	}
	return msg
}

// ForwardingEndpointCore is the core model for all forwarding endpoint types.
// It does not include any implementation-specific fields, and should mostly be embedded in the implementation-specific models.
type ForwardingEndpointCore struct {
	Slug types.String `tfsdk:"slug"`

	DisplayName types.String `tfsdk:"display_name"`

	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
}

// coreSet sets the model from a ForwardingEndpoint message.
// It is not exported because it should only be called by the implementation-specific models, in conjunction with additional fields.
func (m *ForwardingEndpointCore) coreSet(endpoint *typesv1beta1.ForwardingEndpoint) {
	m.Slug = types.StringValue(endpoint.Ref.Slug)
	m.DisplayName = types.StringValue(endpoint.Spec.DisplayName)
	m.CreatedAt = timestampToTimeValue(endpoint.Status.CreatedAt)
	m.UpdatedAt = timestampToTimeValue(endpoint.Status.UpdatedAt)
	m.StateCode = types.Int32Value(int32(endpoint.Status.State.Number()))
	m.State = types.StringValue(endpoint.Status.State.String())
	m.StateMessage = types.StringPointerValue(endpoint.Status.StateMessage)
}

// coreMsg converts the model to a ForwardingEndpoint message.
// It is not exported because it should only be called by the implementation-specific models, in conjunction with additional fields.
// It does not set any values associated with oneOf fields associated with different resources.
// It does initialize top-level fields which may never be nil.
func (m *ForwardingEndpointCore) coreMsg() (msg *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	if m == nil {
		return msg, diagnostics
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

	msg = &typesv1beta1.ForwardingEndpoint{
		Ref: &typesv1beta1.ForwardingEndpointRef{
			Slug: m.Slug.ValueString(),
		},
		Spec: &typesv1beta1.ForwardingEndpointSpec{
			DisplayName: m.DisplayName.ValueString(),
		},
		Status: &typesv1beta1.ForwardingEndpointStatus{
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
			State:        typesv1beta1.ForwardingEndpointState(m.StateCode.ValueInt32()),
			StateMessage: m.StateMessage.ValueStringPointer(),
		},
	}

	return msg, diagnostics
}

type ForwardingEndpointHTTPS struct {
	ForwardingEndpointCore
	Endpoint    types.String      `tfsdk:"endpoint"`
	TLS         *TLSConfig        `tfsdk:"tls"`
	Credentials *HTTPSCredentials `tfsdk:"credentials"`
}

// Set sets the model from a ForwardingEndpoint message.
// This implementation behaves a bit differently than most, because it presents a single model for both the HTTPS config and the credentials.
func (m *ForwardingEndpointHTTPS) Set(endpoint *typesv1beta1.ForwardingEndpoint) (diagnostics diag.Diagnostics) {
	if endpoint.Spec.WhichConfig() != typesv1beta1.ForwardingEndpointSpec_Https_case {
		diagnostics.AddError("Invalid Endpoint Type", "The endpoint is not an HTTPS endpoint")
		return
	}

	m.coreSet(endpoint)

	httpsConfig := endpoint.Spec.GetHttps()
	m.Endpoint = types.StringValue(httpsConfig.Endpoint)
	if httpsConfig.Tls != nil {
		m.TLS = new(TLSConfig)
		m.TLS.Set(httpsConfig.Tls)
	}

	return
}

func (m *ForwardingEndpointHTTPS) ToMsg() (msg *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	if m == nil {
		return
	}

	msg, diagnostics = m.coreMsg()
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	msg.Spec.SetHttps(&typesv1beta1.HTTPSConfig{
		Endpoint: m.Endpoint.ValueString(),
		Tls:      m.TLS.ToMsg(),
	})

	return msg, diagnostics
}

type ForwardingEndpointPrometheus struct {
	ForwardingEndpointCore
	Endpoint    types.String           `tfsdk:"endpoint"`
	TLS         *TLSConfig             `tfsdk:"tls"`
	Credentials *PrometheusCredentials `tfsdk:"credentials"`
}

func (m *ForwardingEndpointPrometheus) Set(endpoint *typesv1beta1.ForwardingEndpoint) (diagnostics diag.Diagnostics) {
	if endpoint.Spec.WhichConfig() != typesv1beta1.ForwardingEndpointSpec_Prometheus_case {
		diagnostics.AddError("Invalid Endpoint Type", "The endpoint is not a Prometheus endpoint")
		return
	}

	m.coreSet(endpoint)

	prometheusConfig := endpoint.Spec.GetPrometheus()
	m.Endpoint = types.StringValue(prometheusConfig.Endpoint)
	if prometheusConfig.Tls != nil {
		m.TLS = new(TLSConfig)
		m.TLS.Set(prometheusConfig.Tls)
	}

	return
}

func (m *ForwardingEndpointPrometheus) ToMsg() (msg *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	if m == nil {
		return
	}

	msg, diagnostics = m.coreMsg()
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	msg.Spec.SetPrometheus(&typesv1beta1.PrometheusRemoteWriteConfig{
		Endpoint: m.Endpoint.ValueString(),
		Tls:      m.TLS.ToMsg(),
	})

	return msg, diagnostics
}

type ForwardingEndpointS3 struct {
	ForwardingEndpointCore
	Bucket      types.String   `tfsdk:"bucket"`
	Region      types.String   `tfsdk:"region"`
	Credentials *S3Credentials `tfsdk:"credentials"`
}

func (m *ForwardingEndpointS3) Set(endpoint *typesv1beta1.ForwardingEndpoint) (diagnostics diag.Diagnostics) {
	if endpoint.Spec.WhichConfig() != typesv1beta1.ForwardingEndpointSpec_S3_case {
		diagnostics.AddError("Invalid Endpoint Type", "The endpoint is not an S3 endpoint")
		return
	}

	m.coreSet(endpoint)

	s3Config := endpoint.Spec.GetS3()
	m.Bucket = types.StringValue(s3Config.Uri)
	m.Region = types.StringValue(s3Config.Region)

	return
}

func (m *ForwardingEndpointS3) ToMsg() (msg *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	if m == nil {
		return
	}

	msg, diagnostics = m.coreMsg()
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	msg.Spec.SetS3(&typesv1beta1.S3Config{
		Uri:                 m.Bucket.ValueString(),
		Region:              m.Region.ValueString(),
		RequiresCredentials: m.Credentials != nil,
	})

	return msg, diagnostics
}
