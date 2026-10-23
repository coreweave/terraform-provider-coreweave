package model

import (
	"context"
	"fmt"
	"maps"
	"strings"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// BasicAuthCredentials contains username and password for HTTP Basic authentication.
type BasicAuthCredentials struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func (b *BasicAuthCredentials) Set(_ context.Context, msg *typesv1beta1.BasicAuthCredentials) {
	b.Username = types.StringValue(msg.Username)
	b.Password = types.StringValue(msg.Password)
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

func (h *AuthHeadersCredentials) Set(_ context.Context, msg *typesv1beta1.AuthHeadersCredentials) {
	h.Headers = maps.Clone(msg.Headers)
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

func (h *HTTPSCredentials) Set(ctx context.Context, msg *typesv1beta1.HTTPSCredentials) (diagnostics diag.Diagnostics) {
	if msg == nil {
		return nil
	}

	h.BasicAuth = nil
	h.AuthHeaders = nil

	switch kind := msg.WhichAuth(); kind {
	case typesv1beta1.HTTPSCredentials_Auth_not_set_case:
		diagnostics.AddError("no auth set for https credentials", "Auth must be set when using https credentials")
	case typesv1beta1.HTTPSCredentials_BasicAuth_case:
		h.BasicAuth = new(BasicAuthCredentials)
		h.BasicAuth.Set(ctx, msg.GetBasicAuth())
	case typesv1beta1.HTTPSCredentials_AuthHeaders_case:
		h.AuthHeaders = new(AuthHeadersCredentials)
		h.AuthHeaders.Set(ctx, msg.GetAuthHeaders())
	default:
		diagnostics.AddError("Unsupported HTTPS auth type", fmt.Sprintf("unsupported https auth type: %s (%d)", kind.String(), kind))
	}

	return
}

func (h *HTTPSCredentials) ToMsg() (msg *typesv1beta1.HTTPSCredentials, diagnostics diag.Diagnostics) {
	if h == nil {
		return nil, nil
	}

	msg = &typesv1beta1.HTTPSCredentials{}
	implementations := make([]string, 0)

	if h.BasicAuth != nil {
		implementations = append(implementations, "basic_auth")
		basicAuth := h.BasicAuth.ToMsg()
		msg.SetBasicAuth(basicAuth)
	}
	if h.AuthHeaders != nil {
		implementations = append(implementations, "auth_headers")
		headersModel := h.AuthHeaders.ToMsg()
		msg.SetAuthHeaders(headersModel)
	}

	if len(implementations) != 1 {
		diagnostics.AddError(
			"Invalid HTTPSCredentials",
			fmt.Sprintf("Exactly one of basic_auth or auth_headers must be set, got %d: %s", len(implementations), strings.Join(implementations, ", ")),
		)
	}
	if diagnostics.HasError() {
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

func (s *S3Credentials) Set(_ context.Context, msg *typesv1beta1.S3Credentials) {
	s.AccessKeyID = types.StringValue(msg.AccessKeyId)
	s.SecretAccessKey = types.StringValue(msg.SecretAccessKey)
	if msg.SessionToken == "" {
		s.SessionToken = types.StringNull()
	} else {
		s.SessionToken = types.StringValue(msg.SessionToken)
	}
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
