package model

import (
	"maps"

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
	Headers map[string]string `tfsdk:"headers"`
}

func (h *AuthHeadersCredentials) ToMsg() (msg *typesv1beta1.AuthHeadersCredentials) {
	if h == nil {
		return nil
	}

	msg = &typesv1beta1.AuthHeadersCredentials{
		Headers: maps.Clone(h.Headers),
	}

	return msg
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
		msg.SetAuthHeaders(h.AuthHeaders.ToMsg())
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
