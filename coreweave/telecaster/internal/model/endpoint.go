package model

import (
	"context"
	"fmt"
	"maps"
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
	Kafka       *ForwardingEndpointKafkaModel      `tfsdk:"kafka"`
	Prometheus  *ForwardingEndpointPrometheusModel `tfsdk:"prometheus"`
	S3          *ForwardingEndpointS3Model         `tfsdk:"s3"`
	HTTPS       *ForwardingEndpointHTTPSModel      `tfsdk:"https"`
}

func (e *ForwardingEndpointSpecModel) Set(msg *typesv1beta1.ForwardingEndpointSpec) (diagnostics diag.Diagnostics) {
	e.DisplayName = types.StringValue(msg.DisplayName)

	e.Kafka = nil
	e.Prometheus = nil
	e.S3 = nil
	e.HTTPS = nil

	switch kind := msg.WhichConfig(); kind {
	case typesv1beta1.ForwardingEndpointSpec_Config_not_set_case:
		diagnostics.AddError("no config set for forwarding endpoint", "Config must be set when using forwarding endpoint")
	case typesv1beta1.ForwardingEndpointSpec_Kafka_case:
		var kafka ForwardingEndpointKafkaModel
		diagnostics.Append(kafka.Set(msg.GetKafka())...)
		e.Kafka = &kafka
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

type ForwardingEndpointKafkaModel struct {
	BootstrapEndpoints types.String         `tfsdk:"bootstrap_endpoints"`
	Topic              types.String         `tfsdk:"topic"`
	TLS                *TLSConfigModel      `tfsdk:"tls"`
	ScramAuth          *KafkaScramAuthModel `tfsdk:"scram_auth"`
}

func (k *ForwardingEndpointKafkaModel) Set(msg *typesv1beta1.KafkaConfig) (diagnostics diag.Diagnostics) {
	k.BootstrapEndpoints = types.StringValue(msg.BootstrapEndpoints)
	k.Topic = types.StringValue(msg.Topic)

	var tls *TLSConfigModel
	if msg.HasTls() {
		tls = new(TLSConfigModel)
		tls.Set(msg.Tls)
	}
	k.TLS = tls

	switch kind := msg.WhichAuth(); kind {
	case typesv1beta1.KafkaConfig_Auth_not_set_case:
		diagnostics.AddError("no auth set for kafka", "Auth must be set when using kafka endpoint")
	case typesv1beta1.KafkaConfig_Scram_case:
		scram := new(KafkaScramAuthModel)
		diagnostics.Append(scram.Set(msg.GetScram())...)
		k.ScramAuth = scram
	default:
		diagnostics.AddError("Unsupported Kafka auth type", fmt.Sprintf("unsupported kafka auth type: %s (%d)", kind.String(), kind))
	}

	return
}

func (k *ForwardingEndpointKafkaModel) ToMsg() (msg *typesv1beta1.KafkaConfig, diagnostics diag.Diagnostics) {
	if k == nil {
		return nil, nil
	}

	msg = &typesv1beta1.KafkaConfig{
		BootstrapEndpoints: k.BootstrapEndpoints.ValueString(),
		Topic:              k.Topic.ValueString(),
		Tls:                k.TLS.ToMsg(),
	}

	if k.ScramAuth != nil {
		scramAuth, diags := k.ScramAuth.ToMsg()
		diagnostics.Append(diags...)
		msg.SetScram(scramAuth)
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

type KafkaScramAuthModel struct {
	Mechanism types.String `tfsdk:"mechanism"`
}

func (k *KafkaScramAuthModel) Set(msg *typesv1beta1.KafkaScramAuth) (diagnostics diag.Diagnostics) {
	if msg.Mechanism == "" {
		k.Mechanism = types.StringNull()
	} else {
		k.Mechanism = types.StringValue(msg.Mechanism)
	}
	return
}

func (k *KafkaScramAuthModel) ToMsg() (msg *typesv1beta1.KafkaScramAuth, diagnostics diag.Diagnostics) {
	if k == nil {
		return
	}

	msg = &typesv1beta1.KafkaScramAuth{
		Mechanism: k.Mechanism.ValueString(),
	}

	return
}

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

type BasicAuthCredentialsModel struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func (b *BasicAuthCredentialsModel) Set(ctx context.Context, basicAuth *typesv1beta1.BasicAuthCredentials) (diagnostics diag.Diagnostics) {
	b.Username = types.StringValue(basicAuth.Username)
	b.Password = types.StringValue(basicAuth.Password)
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

type BearerTokenCredentialsModel struct {
	Token types.String `tfsdk:"token"`
}

func (b *BearerTokenCredentialsModel) Set(ctx context.Context, bearerToken *typesv1beta1.BearerTokenCredentials) (diagnostics diag.Diagnostics) {
	b.Token = types.StringValue(bearerToken.Token)
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

func (s *ForwardingEndpointSpecModel) ToMsg() (spec *typesv1beta1.ForwardingEndpointSpec, diagnostics diag.Diagnostics) {
	if s == nil {
		return nil, nil
	}

	spec = &typesv1beta1.ForwardingEndpointSpec{
		DisplayName: s.DisplayName.ValueString(),
	}

	configuredImplementations := make([]string, 0)
	if s.Kafka != nil {
		configuredImplementations = append(configuredImplementations, "kafka")
		kafkaMsg, diags := s.Kafka.ToMsg()
		diagnostics.Append(diags...)
		spec.SetKafka(kafkaMsg)
	}

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
