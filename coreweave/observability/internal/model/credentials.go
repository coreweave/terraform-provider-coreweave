package model

import (
	"context"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// BasicAuthCredentials contains username and password for HTTP Basic authentication.
type BasicAuthCredentials struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func (b *BasicAuthCredentials) ToMsg() (msg *typesv1beta1.BasicAuthCredentials) {
	if b == nil {
		return nil
	}

	msg = &typesv1beta1.BasicAuthCredentials{
		Username: b.Username.ValueString(),
		Password: b.Password.ValueString(),
	}

	return msg
}

// AuthHeadersCredentials contains custom HTTP headers for authentication.
type AuthHeadersCredentials struct {
	Headers types.Map `tfsdk:"headers"`
}

func (h *AuthHeadersCredentials) ToMsg() (msg *typesv1beta1.AuthHeadersCredentials, diagnostics diag.Diagnostics) {
	if h == nil {
		return nil, nil
	}

	headers := make(map[string]string, len(h.Headers.Elements()))
	if !h.Headers.IsNull() && !h.Headers.IsUnknown() {
		diagnostics.Append(h.Headers.ElementsAs(context.Background(), &headers, false)...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
	}

	msg = &typesv1beta1.AuthHeadersCredentials{
		Headers: headers,
	}

	return msg, diagnostics
}

// HTTPSCredentials contains authentication credentials for HTTPS endpoints.
type HTTPSCredentials struct {
	BasicAuth   *BasicAuthCredentials   `tfsdk:"basic_auth"`
	AuthHeaders *AuthHeadersCredentials `tfsdk:"auth_headers"`
}

func (h *HTTPSCredentials) ToMsg() (msg *typesv1beta1.HTTPSCredentials, diagnostics diag.Diagnostics) {
	if h == nil {
		return nil, nil
	}

	msg = &typesv1beta1.HTTPSCredentials{}
	switch {
	case h.BasicAuth != nil && h.AuthHeaders != nil:
		diagnostics.AddError("Invalid HTTPS credentials", "Set basic_auth or auth_headers, not both.")
		return nil, diagnostics
	case h.BasicAuth != nil:
		msg.SetBasicAuth(h.BasicAuth.ToMsg())
	case h.AuthHeaders != nil:
		headers, diags := h.AuthHeaders.ToMsg()
		diagnostics.Append(diags...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
		msg.SetAuthHeaders(headers)
	default:
		diagnostics.AddError("Invalid HTTPS credentials", "One of basic_auth or auth_headers is required.")
		return nil, diagnostics
	}

	return msg, diagnostics
}

// S3Credentials contains AWS credentials for S3 bucket access.
type S3Credentials struct {
	AccessKeyID     types.String `tfsdk:"access_key_id"`
	SecretAccessKey types.String `tfsdk:"secret_access_key"`
	SessionToken    types.String `tfsdk:"session_token"`
}

func (s *S3Credentials) ToMsg() (msg *typesv1beta1.S3Credentials) {
	if s == nil {
		return nil
	}

	msg = &typesv1beta1.S3Credentials{
		AccessKeyId:     s.AccessKeyID.ValueString(),
		SecretAccessKey: s.SecretAccessKey.ValueString(),
		SessionToken:    s.SessionToken.ValueString(),
	}

	return msg
}
