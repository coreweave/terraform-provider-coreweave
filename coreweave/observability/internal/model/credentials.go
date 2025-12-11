package model

import (
	"context"
	"fmt"
	"maps"
	"strings"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// KafkaCredentials contains authentication credentials for Kafka endpoints.
type KafkaCredentials struct {
	Scram *KafkaScramCredentials `tfsdk:"scram"`
}

func (k *KafkaCredentials) Set(ctx context.Context, msg *typesv1beta1.KafkaCredentials) (diagnostics diag.Diagnostics) {
	if msg == nil {
		return nil
	}

	k.Scram = nil

	switch kind := msg.WhichAuth(); kind {
	case typesv1beta1.KafkaCredentials_Auth_not_set_case:
		diagnostics.AddError("no auth set for kafka credentials", "Auth must be set when using kafka credentials")
	case typesv1beta1.KafkaCredentials_Scram_case:
		k.Scram = new(KafkaScramCredentials)
		k.Scram.Set(ctx, msg.GetScram())
	default:
		diagnostics.AddError("Unsupported Kafka auth type", fmt.Sprintf("unsupported kafka auth type: %s (%d)", kind.String(), kind))
	}

	return
}

func (k *KafkaCredentials) ToMsg() (msg *typesv1beta1.KafkaCredentials, diagnostics diag.Diagnostics) {
	if k == nil {
		return nil, nil
	}

	msg = &typesv1beta1.KafkaCredentials{}
	implementations := make([]string, 0)

	if k.Scram != nil {
		implementations = append(implementations, "scram")
		scram := k.Scram.ToMsg()
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

	return msg, diagnostics
}

// KafkaScramCredentials contains username and password for SCRAM authentication.
type KafkaScramCredentials struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func (k *KafkaScramCredentials) Set(_ context.Context, msg *typesv1beta1.KafkaScramCredentials) {
	k.Username = types.StringValue(msg.Username)
	k.Password = types.StringValue(msg.Password)
}

func (k *KafkaScramCredentials) ToMsg() (msg *typesv1beta1.KafkaScramCredentials) {
	if k == nil {
		return nil
	}

	msg = &typesv1beta1.KafkaScramCredentials{
		Username: k.Username.ValueString(),
		Password: k.Password.ValueString(),
	}

	return msg
}

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

// BearerTokenCredentials contains a bearer token for HTTP Authorization header authentication.
type BearerTokenCredentials struct {
	Token types.String `tfsdk:"token"`
}

func (b *BearerTokenCredentials) Set(_ context.Context, msg *typesv1beta1.BearerTokenCredentials) {
	b.Token = types.StringValue(msg.Token)
}

func (b *BearerTokenCredentials) ToMsg() (msg *typesv1beta1.BearerTokenCredentials) {
	if b == nil {
		return nil
	}

	msg = &typesv1beta1.BearerTokenCredentials{
		Token: b.Token.ValueString(),
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

// PrometheusCredentials contains authentication credentials for Prometheus Remote Write endpoints.
type PrometheusCredentials struct {
	BasicAuth   *BasicAuthCredentials   `tfsdk:"basic_auth"`
	BearerToken *BearerTokenCredentials `tfsdk:"bearer_token"`
	AuthHeaders *AuthHeadersCredentials `tfsdk:"auth_headers"`
}

func (p *PrometheusCredentials) Set(ctx context.Context, msg *typesv1beta1.PrometheusCredentials) (diagnostics diag.Diagnostics) {
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
		p.BasicAuth = new(BasicAuthCredentials)
		p.BasicAuth.Set(ctx, msg.GetBasicAuth())
	case typesv1beta1.PrometheusCredentials_BearerToken_case:
		p.BearerToken = new(BearerTokenCredentials)
		p.BearerToken.Set(ctx, msg.GetBearerToken())
	case typesv1beta1.PrometheusCredentials_AuthHeaders_case:
		p.AuthHeaders = new(AuthHeadersCredentials)
		p.AuthHeaders.Set(ctx, msg.GetAuthHeaders())
	default:
		diagnostics.AddError("Unsupported Prometheus auth type", fmt.Sprintf("unsupported prometheus auth type: %s (%d)", kind.String(), kind))
	}

	return
}

func (p *PrometheusCredentials) ToMsg() (msg *typesv1beta1.PrometheusCredentials, diagnostics diag.Diagnostics) {
	if p == nil {
		return nil, nil
	}

	msg = &typesv1beta1.PrometheusCredentials{}
	implementations := make([]string, 0)

	if p.BasicAuth != nil {
		implementations = append(implementations, "basic_auth")
		basicAuth := p.BasicAuth.ToMsg()
		msg.SetBasicAuth(basicAuth)
	}
	if p.BearerToken != nil {
		implementations = append(implementations, "bearer_token")
		bearerToken := p.BearerToken.ToMsg()
		msg.SetBearerToken(bearerToken)
	}
	if p.AuthHeaders != nil {
		implementations = append(implementations, "auth_headers")
		headersModel := p.AuthHeaders.ToMsg()
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

	return msg, diagnostics
}

// HTTPSCredentials contains authentication credentials for HTTPS endpoints.
type HTTPSCredentials struct {
	BasicAuth   *BasicAuthCredentials   `tfsdk:"basic_auth"`
	BearerToken *BearerTokenCredentials `tfsdk:"bearer_token"`
	AuthHeaders *AuthHeadersCredentials `tfsdk:"auth_headers"`
}

func (h *HTTPSCredentials) Set(ctx context.Context, msg *typesv1beta1.HTTPSCredentials) (diagnostics diag.Diagnostics) {
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
		h.BasicAuth = new(BasicAuthCredentials)
		h.BasicAuth.Set(ctx, msg.GetBasicAuth())
	case typesv1beta1.HTTPSCredentials_BearerToken_case:
		h.BearerToken = new(BearerTokenCredentials)
		h.BearerToken.Set(ctx, msg.GetBearerToken())
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
	if h.BearerToken != nil {
		implementations = append(implementations, "bearer_token")
		bearerToken := h.BearerToken.ToMsg()
		msg.SetBearerToken(bearerToken)
	}
	if h.AuthHeaders != nil {
		implementations = append(implementations, "auth_headers")
		headersModel := h.AuthHeaders.ToMsg()
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
