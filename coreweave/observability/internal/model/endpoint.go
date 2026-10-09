package model

import (
	"fmt"
	"strings"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
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

	// CredentialsVersion is user-owned, not server state. Write-only credential
	// values are null in both plan and state, so they can never produce a diff
	// by themselves; bumping this counter is what tells Update to send a
	// replacement. Nothing on the server reads it.
	CredentialsVersion types.Int64 `tfsdk:"credentials_version"`

	CreatedAt             timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt             timetypes.RFC3339 `tfsdk:"updated_at"`
	State                 types.String      `tfsdk:"state"`
	StateMessage          types.String      `tfsdk:"state_message"`
	CredentialsConfigured types.Bool        `tfsdk:"credentials_configured"`
	CredentialsUpdatedAt  timetypes.RFC3339 `tfsdk:"credentials_updated_at"`
}

// coreSet sets the model from a ForwardingEndpoint message.
// It is not exported because it should only be called by the implementation-specific models, in conjunction with additional fields.
// It uses generated getters throughout because it is also called with the
// Create/Update RPC response, whose Status may be only partially populated.
func (m *ForwardingEndpointCore) coreSet(endpoint *typesv1beta1.ForwardingEndpoint) {
	status := endpoint.GetStatus()

	m.Slug = types.StringValue(endpoint.GetRef().GetSlug())
	m.DisplayName = types.StringValue(endpoint.GetSpec().GetDisplayName())
	m.CreatedAt = timestampToTimeValue(status.GetCreatedAt())
	m.UpdatedAt = timestampToTimeValue(status.GetUpdatedAt())
	m.State = types.StringValue(status.GetState().String())
	if status.HasStateMessage() {
		m.StateMessage = types.StringValue(status.GetStateMessage())
	} else {
		m.StateMessage = types.StringNull()
	}

	// The server never returns credentials, so this pair is the only observable
	// record that any were set. CredentialsVersion is deliberately untouched:
	// it belongs to the config, not the response.
	m.CredentialsConfigured = types.BoolValue(status.GetCredentialsConfigured())
	m.CredentialsUpdatedAt = timestampToTimeValue(status.GetCredentialsUpdatedAt())
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
			State:        endpointStateFromName(m.State),
			StateMessage: m.StateMessage.ValueStringPointer(),
		},
	}

	return msg, diagnostics
}

// endpointStateFromName maps the status string back onto the proto enum. The
// provider never sends Status — it is OUTPUT_ONLY, and Create/Update take only
// ref and spec — so an unrecognized or unknown value degrades to UNSPECIFIED
// rather than raising a diagnostic the user cannot act on.
func endpointStateFromName(state types.String) typesv1beta1.ForwardingEndpointState {
	if state.IsNull() || state.IsUnknown() {
		return typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED
	}

	return typesv1beta1.ForwardingEndpointState(typesv1beta1.ForwardingEndpointState_value[state.ValueString()])
}

type ForwardingEndpointHTTPS struct {
	ForwardingEndpointCore
	Endpoint    types.String      `tfsdk:"endpoint"`
	Compression types.String      `tfsdk:"compression"`
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
	m.Endpoint = types.StringValue(httpsConfig.GetEndpoint())
	// UNSPECIFIED means "never configured", which is Terraform's null. NONE means
	// "explicitly uncompressed" and is a value the user can set and read back.
	if compression := httpsConfig.GetCompression(); compression == typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_UNSPECIFIED {
		m.Compression = types.StringNull()
	} else {
		m.Compression = types.StringValue(compression.String())
	}
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

	compression, diags := compressionFromName(m.Compression)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	msg.Spec.SetHttps(&typesv1beta1.HTTPSConfig{
		Endpoint:    m.Endpoint.ValueString(),
		Compression: compression,
		Tls:         m.TLS.ToMsg(),
	})

	return msg, diagnostics
}

// compressionFromName maps the configured compression name onto the proto
// enum. A null config value is UNSPECIFIED, which the API treats as "not
// configured" — the distinction COMPRESSION_TYPE_NONE exists to express.
func compressionFromName(compression types.String) (typesv1beta1.HTTPSConfig_CompressionType, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	if compression.IsNull() || compression.IsUnknown() {
		return typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_UNSPECIFIED, diagnostics
	}

	name := compression.ValueString()
	value, ok := typesv1beta1.HTTPSConfig_CompressionType_value[name]
	if !ok {
		diagnostics.AddError(
			"Invalid compression type",
			fmt.Sprintf("Invalid compression type %q. Must be one of: %s.",
				name,
				strings.Join([]string{
					typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_NONE.String(),
					typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_GZIP.String(),
				}, ", "),
			),
		)
		return typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_UNSPECIFIED, diagnostics
	}

	return typesv1beta1.HTTPSConfig_CompressionType(value), diagnostics
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
	URI         types.String   `tfsdk:"uri"`
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
	m.URI = types.StringValue(s3Config.GetUri())
	m.Region = types.StringValue(s3Config.GetRegion())

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

	// requires_credentials is deprecated and ignored: the API now requires
	// credentials for every S3 endpoint, so there is nothing for it to express.
	msg.Spec.SetS3(&typesv1beta1.S3Config{
		Uri:    m.URI.ValueString(),
		Region: m.Region.ValueString(),
	})

	return msg, diagnostics
}
