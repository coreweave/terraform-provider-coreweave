package model

import (
	"context"
	"fmt"
	"maps"
	"strings"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// KafkaCredentials contains authentication credentials for Kafka endpoints.
type KafkaCredentialsModel struct {
	Scram *KafkaScramCredentialsModel `tfsdk:"scram"`
}

func (k *KafkaCredentialsModel) Set(ctx context.Context, msg *typesv1beta1.KafkaCredentials) (diagnostics diag.Diagnostics) {
	if msg == nil {
		return nil
	}

	k.Scram = nil

	switch kind := msg.WhichAuth(); kind {
	case typesv1beta1.KafkaCredentials_Auth_not_set_case:
		diagnostics.AddError("no auth set for kafka credentials", "Auth must be set when using kafka credentials")
	case typesv1beta1.KafkaCredentials_Scram_case:
		k.Scram = new(KafkaScramCredentialsModel)
		diagnostics.Append(k.Scram.Set(ctx, msg.GetScram())...)
	default:
		diagnostics.AddError("Unsupported Kafka auth type", fmt.Sprintf("unsupported kafka auth type: %s (%d)", kind.String(), kind))
	}

	return
}

func (k *KafkaCredentialsModel) ToMsg() (msg *typesv1beta1.KafkaCredentials, diagnostics diag.Diagnostics) {
	if k == nil {
		return nil, nil
	}

	msg = &typesv1beta1.KafkaCredentials{}
	implementations := make([]string, 0)

	if k.Scram != nil {
		implementations = append(implementations, "scram")
		scram, diags := k.Scram.ToMsg()
		diagnostics.Append(diags...)
		msg.SetScram(scram)
	}

	if len(implementations) != 1 {
		diagnostics.AddError(
			"Invalid KafkaCredentials",
			fmt.Sprintf("Exactly one of scram must be set, got %d: %s", len(implementations), strings.Join(implementations, ", ")),
		)
	}
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	return
}

// KafkaScramCredentials contains username and password for SCRAM authentication.
type KafkaScramCredentialsModel struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func (k *KafkaScramCredentialsModel) Set(ctx context.Context, msg *typesv1beta1.KafkaScramCredentials) (diagnostics diag.Diagnostics) {
	k.Username = types.StringValue(msg.Username)
	k.Password = types.StringValue(msg.Password)
	return
}

func (k *KafkaScramCredentialsModel) ToMsg() (msg *typesv1beta1.KafkaScramCredentials, diagnostics diag.Diagnostics) {
	if k == nil {
		return
	}

	msg = &typesv1beta1.KafkaScramCredentials{
		Username: k.Username.ValueString(),
		Password: k.Password.ValueString(),
	}

	return
}

// BasicAuthCredentials contains username and password for HTTP Basic authentication.
type BasicAuthCredentialsModel struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func (b *BasicAuthCredentialsModel) Set(ctx context.Context, msg *typesv1beta1.BasicAuthCredentials) (diagnostics diag.Diagnostics) {
	b.Username = types.StringValue(msg.Username)
	b.Password = types.StringValue(msg.Password)
	return
}

func (b *BasicAuthCredentialsModel) ToMsg() (msg *typesv1beta1.BasicAuthCredentials, diagnostics diag.Diagnostics) {
	if b == nil {
		return
	}

	msg = &typesv1beta1.BasicAuthCredentials{
		Username: b.Username.ValueString(),
		Password: b.Password.ValueString(),
	}

	return
}

// BearerTokenCredentials contains a bearer token for HTTP Authorization header authentication.
type BearerTokenCredentialsModel struct {
	Token types.String `tfsdk:"token"`
}

func (b *BearerTokenCredentialsModel) Set(ctx context.Context, msg *typesv1beta1.BearerTokenCredentials) (diagnostics diag.Diagnostics) {
	b.Token = types.StringValue(msg.Token)
	return
}

func (b *BearerTokenCredentialsModel) ToMsg() (msg *typesv1beta1.BearerTokenCredentials, diagnostics diag.Diagnostics) {
	if b == nil {
		return
	}

	msg = &typesv1beta1.BearerTokenCredentials{
		Token: b.Token.ValueString(),
	}

	return
}

// AuthHeadersCredentials contains custom HTTP headers for authentication.
type AuthHeadersCredentialsModel struct {
	Headers map[string]string `tfsdk:"headers"`
}

func (h *AuthHeadersCredentialsModel) Set(ctx context.Context, msg *typesv1beta1.AuthHeadersCredentials) (diagnostics diag.Diagnostics) {
	h.Headers = maps.Clone(msg.Headers)
	return
}

func (h *AuthHeadersCredentialsModel) ToMsg() (msg *typesv1beta1.AuthHeadersCredentials, diagnostics diag.Diagnostics) {
	if h == nil {
		return
	}

	msg = &typesv1beta1.AuthHeadersCredentials{
		Headers: maps.Clone(h.Headers),
	}

	return
}

// PrometheusCredentials contains authentication credentials for Prometheus Remote Write endpoints.
type PrometheusCredentialsModel struct {
	BasicAuth   *BasicAuthCredentialsModel   `tfsdk:"basic_auth"`
	BearerToken *BearerTokenCredentialsModel `tfsdk:"bearer_token"`
	AuthHeaders *AuthHeadersCredentialsModel `tfsdk:"auth_headers"`
}

func (p *PrometheusCredentialsModel) Set(ctx context.Context, msg *typesv1beta1.PrometheusCredentials) (diagnostics diag.Diagnostics) {
	if msg == nil {
		return nil
	}

	p.BasicAuth = nil
	p.BearerToken = nil
	p.AuthHeaders = nil

	switch kind := msg.WhichAuth(); kind {
	case typesv1beta1.PrometheusCredentials_Auth_not_set_case:
		diagnostics.AddError("no auth set for prometheus", "Auth must be set when using prometheus endpoint")
	case typesv1beta1.PrometheusCredentials_BasicAuth_case:
		p.BasicAuth = new(BasicAuthCredentialsModel)
		diagnostics.Append(p.BasicAuth.Set(ctx, msg.GetBasicAuth())...)
	case typesv1beta1.PrometheusCredentials_BearerToken_case:
		p.BearerToken = new(BearerTokenCredentialsModel)
		diagnostics.Append(p.BearerToken.Set(ctx, msg.GetBearerToken())...)
	case typesv1beta1.PrometheusCredentials_AuthHeaders_case:
		p.AuthHeaders = new(AuthHeadersCredentialsModel)
		diagnostics.Append(p.AuthHeaders.Set(ctx, msg.GetAuthHeaders())...)
	default:
		diagnostics.AddError("Unsupported Prometheus auth type", fmt.Sprintf("unsupported prometheus auth type: %s (%d)", kind.String(), kind))
	}

	return
}

func (p *PrometheusCredentialsModel) ToMsg() (msg *typesv1beta1.PrometheusCredentials, diagnostics diag.Diagnostics) {
	if p == nil {
		return nil, nil
	}

	msg = &typesv1beta1.PrometheusCredentials{}
	implementations := make([]string, 0)

	if p.BasicAuth != nil {
		implementations = append(implementations, "basic_auth")
		basicAuth, diags := p.BasicAuth.ToMsg()
		diagnostics.Append(diags...)
		msg.SetBasicAuth(basicAuth)
	}
	if p.BearerToken != nil {
		implementations = append(implementations, "bearer_token")
		bearerToken, diags := p.BearerToken.ToMsg()
		diagnostics.Append(diags...)
		msg.SetBearerToken(bearerToken)
	}
	if p.AuthHeaders != nil {
		implementations = append(implementations, "auth_headers")
		headersModel, diags := p.AuthHeaders.ToMsg()
		diagnostics.Append(diags...)
		msg.SetAuthHeaders(headersModel)
	}

	if len(implementations) != 1 {
		diagnostics.AddError(
			"Invalid PrometheusCredentials",
			fmt.Sprintf("Exactly one of basic_auth, bearer_token, or auth_headers must be set, got %d: %s", len(implementations), strings.Join(implementations, ", ")),
		)
	}
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	return
}

// HTTPSCredentials contains authentication credentials for HTTPS endpoints.
type HTTPSCredentialsModel struct {
	BasicAuth   *BasicAuthCredentialsModel   `tfsdk:"basic_auth"`
	BearerToken *BearerTokenCredentialsModel `tfsdk:"bearer_token"`
	AuthHeaders *AuthHeadersCredentialsModel `tfsdk:"auth_headers"`
}

func (h *HTTPSCredentialsModel) Set(ctx context.Context, msg *typesv1beta1.HTTPSCredentials) (diagnostics diag.Diagnostics) {
	if msg == nil {
		return nil
	}

	h.BasicAuth = nil
	h.BearerToken = nil
	h.AuthHeaders = nil

	switch kind := msg.WhichAuth(); kind {
	case typesv1beta1.HTTPSCredentials_Auth_not_set_case:
		diagnostics.AddError("no auth set for https credentials", "Auth must be set when using https credentials")
	case typesv1beta1.HTTPSCredentials_BasicAuth_case:
		h.BasicAuth = new(BasicAuthCredentialsModel)
		diagnostics.Append(h.BasicAuth.Set(ctx, msg.GetBasicAuth())...)
	case typesv1beta1.HTTPSCredentials_BearerToken_case:
		h.BearerToken = new(BearerTokenCredentialsModel)
		diagnostics.Append(h.BearerToken.Set(ctx, msg.GetBearerToken())...)
	case typesv1beta1.HTTPSCredentials_AuthHeaders_case:
		h.AuthHeaders = new(AuthHeadersCredentialsModel)
		diagnostics.Append(h.AuthHeaders.Set(ctx, msg.GetAuthHeaders())...)
	default:
		diagnostics.AddError("Unsupported HTTPS auth type", fmt.Sprintf("unsupported https auth type: %s (%d)", kind.String(), kind))
	}

	return
}

func (h *HTTPSCredentialsModel) ToMsg() (msg *typesv1beta1.HTTPSCredentials, diagnostics diag.Diagnostics) {
	if h == nil {
		return nil, nil
	}

	msg = &typesv1beta1.HTTPSCredentials{}
	implementations := make([]string, 0)

	if h.BasicAuth != nil {
		implementations = append(implementations, "basic_auth")
		basicAuth, diags := h.BasicAuth.ToMsg()
		diagnostics.Append(diags...)
		msg.SetBasicAuth(basicAuth)
	}
	if h.BearerToken != nil {
		implementations = append(implementations, "bearer_token")
		bearerToken, diags := h.BearerToken.ToMsg()
		diagnostics.Append(diags...)
		msg.SetBearerToken(bearerToken)
	}
	if h.AuthHeaders != nil {
		implementations = append(implementations, "auth_headers")
		headersModel, diags := h.AuthHeaders.ToMsg()
		diagnostics.Append(diags...)
		msg.SetAuthHeaders(headersModel)
	}

	if len(implementations) != 1 {
		diagnostics.AddError(
			"Invalid HTTPSCredentials",
			fmt.Sprintf("Exactly one of basic_auth, bearer_token, or auth_headers must be set, got %d: %s", len(implementations), strings.Join(implementations, ", ")),
		)
	}
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	return
}

// S3Credentials contains AWS credentials for S3 bucket access.
type S3CredentialsModel struct {
	AccessKeyID     types.String `tfsdk:"access_key_id"`
	SecretAccessKey types.String `tfsdk:"secret_access_key"`
	SessionToken    types.String `tfsdk:"session_token"`
}

func (s *S3CredentialsModel) Set(ctx context.Context, msg *typesv1beta1.S3Credentials) (diagnostics diag.Diagnostics) {
	s.AccessKeyID = types.StringValue(msg.AccessKeyId)
	s.SecretAccessKey = types.StringValue(msg.SecretAccessKey)
	if msg.SessionToken == "" {
		s.SessionToken = types.StringNull()
	} else {
		s.SessionToken = types.StringValue(msg.SessionToken)
	}

	return
}

func (s *S3CredentialsModel) ToMsg() (msg *typesv1beta1.S3Credentials, diagnostics diag.Diagnostics) {
	if s == nil {
		return
	}

	msg = &typesv1beta1.S3Credentials{
		AccessKeyId:     s.AccessKeyID.ValueString(),
		SecretAccessKey: s.SecretAccessKey.ValueString(),
		SessionToken:    s.SessionToken.ValueString(),
	}

	return
}
