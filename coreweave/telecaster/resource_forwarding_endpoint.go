package telecaster

import (
	"context"
	"fmt"
	"strings"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	telecastertypesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/coretf"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

var (
	_ resource.ResourceWithConfigure   = &ForwardingEndpointResource{}
	_ resource.ResourceWithImportState = &ForwardingEndpointResource{}
)

func NewForwardingEndpointResource() resource.Resource {
	return &ForwardingEndpointResource{}
}

type ForwardingEndpointResource struct {
	coretf.CoreResource
}

type ForwardingEndpointResourceModel struct {
	Ref    types.Object `tfsdk:"ref"`
	Spec   types.Object `tfsdk:"spec"`
	Status types.Object `tfsdk:"status"`
}

func (e *ForwardingEndpointResourceModel) Set(ctx context.Context, data *telecastertypesv1beta1.ForwardingEndpoint) (diagnostics diag.Diagnostics) {
	var ref ForwardingEndpointRefModel
	diagnostics.Append(ref.Set(data.Ref)...)
	refObj, diags := types.ObjectValueFrom(ctx, e.Ref.AttributeTypes(ctx), &ref)
	diagnostics.Append(diags...)
	e.Ref = refObj

	var spec ForwardingEndpointSpecModel
	diagnostics.Append(spec.Set(ctx, data.Spec)...)
	specObj, diags := types.ObjectValueFrom(ctx, e.Spec.AttributeTypes(ctx), &spec)
	diagnostics.Append(diags...)
	e.Spec = specObj

	var status ForwardingEndpointStatusModel
	diagnostics.Append(status.Set(data.Status)...)
	statusObj, diags := types.ObjectValueFrom(ctx, e.Status.AttributeTypes(ctx), &status)
	diagnostics.Append(diags...)
	e.Status = statusObj

	return
}

type ForwardingEndpointRefModel struct {
	Slug types.String `tfsdk:"slug"`
}

func (r *ForwardingEndpointRefModel) Set(ref *telecastertypesv1beta1.ForwardingEndpointRef) (diagnostics diag.Diagnostics) {
	r.Slug = types.StringValue(ref.Slug)
	return
}

func (r *ForwardingEndpointRefModel) ToProto() *telecastertypesv1beta1.ForwardingEndpointRef {
	if r == nil {
		return nil
	}

	return &telecastertypesv1beta1.ForwardingEndpointRef{
		Slug: r.Slug.ValueString(),
	}
}

type ForwardingEndpointSpecModel struct {
	DisplayName types.String `tfsdk:"display_name"`
	Kafka       types.Object `tfsdk:"kafka"`
	Prometheus  types.Object `tfsdk:"prometheus"`
	S3          types.Object `tfsdk:"s3"`
	HTTPS       types.Object `tfsdk:"https"`
}

func (e *ForwardingEndpointSpecModel) Set(ctx context.Context, msg *telecastertypesv1beta1.ForwardingEndpointSpec) (diagnostics diag.Diagnostics) {
	switch cfg := msg.Config.(type) {
	case *telecastertypesv1beta1.ForwardingEndpointSpec_Kafka:
		var kafka ForwardingEndpointKafkaModel
		diagnostics.Append(kafka.Set(ctx, cfg.Kafka)...)
		kafkaObj, diags := types.ObjectValueFrom(ctx, e.Kafka.AttributeTypes(ctx), &kafka)
		diagnostics.Append(diags...)
		e.Kafka = kafkaObj
	case *telecastertypesv1beta1.ForwardingEndpointSpec_Prometheus:
		var prom ForwardingEndpointPrometheusModel
		diagnostics.Append(prom.Set(ctx, cfg.Prometheus)...)
		promObj, diags := types.ObjectValueFrom(ctx, e.Prometheus.AttributeTypes(ctx), &prom)
		diagnostics.Append(diags...)
		e.Prometheus = promObj
	case *telecastertypesv1beta1.ForwardingEndpointSpec_S3:
		var s3 ForwardingEndpointS3Model
		diagnostics.Append(s3.Set(cfg.S3)...)
		s3Obj, diags := types.ObjectValueFrom(ctx, e.S3.AttributeTypes(ctx), &s3)
		diagnostics.Append(diags...)
		e.S3 = s3Obj
	case *telecastertypesv1beta1.ForwardingEndpointSpec_Https:
		var https ForwardingEndpointHTTPSModel
		diagnostics.Append(https.Set(ctx, cfg.Https)...)
		httpsObj, diags := types.ObjectValueFrom(ctx, e.HTTPS.AttributeTypes(ctx), &https)
		diagnostics.Append(diags...)
		e.HTTPS = httpsObj
	default:
		diagnostics.AddError("Unsupported forwarding endpoint type", fmt.Sprintf("unsupported forwarding endpoint config type: %T", cfg))
	}

	if diagnostics.HasError() {
		return
	}

	e.DisplayName = types.StringValue(msg.DisplayName)
	return
}

type ForwardingEndpointKafkaModel struct {
	BootstrapEndpoints types.String `tfsdk:"bootstrap_endpoints"`
	Topic              types.String `tfsdk:"topic"`
	TLS                types.Object `tfsdk:"tls"`
	ScramAuth          types.Object `tfsdk:"scram_auth"`
}

func (k *ForwardingEndpointKafkaModel) Set(ctx context.Context, msg *telecastertypesv1beta1.KafkaConfig) (diagnostics diag.Diagnostics) {
	k.BootstrapEndpoints = types.StringValue(msg.BootstrapEndpoints)
	k.Topic = types.StringValue(msg.Topic)

	var tls *TLSConfigModel
	if msg.Tls != nil {
		var tls TLSConfigModel
		tls.Set(msg.Tls)
	}
	tlsObj, diags := types.ObjectValueFrom(ctx, k.TLS.AttributeTypes(ctx), tls)
	diagnostics.Append(diags...)
	k.TLS = tlsObj

	switch auth := msg.Auth.(type) {
	case *telecastertypesv1beta1.KafkaConfig_Scram:
		var scram KafkaScramAuthModel
		scram.Set(auth.Scram)
		scramObj, diags := types.ObjectValueFrom(ctx, k.ScramAuth.AttributeTypes(ctx), scram)
		diagnostics.Append(diags...)
		k.ScramAuth = scramObj
	case nil:
		// no auth configured
	default:
		diagnostics.AddError("Unsupported Kafka auth type", fmt.Sprintf("unsupported kafka auth type: %T", auth))
	}

	return
}

func (k *ForwardingEndpointKafkaModel) ToProto(ctx context.Context) (msg *telecastertypesv1beta1.KafkaConfig, diagnostics diag.Diagnostics) {
	if k == nil {
		return nil, nil
	}

	msg = &telecastertypesv1beta1.KafkaConfig{
		BootstrapEndpoints: k.BootstrapEndpoints.ValueString(),
		Topic:              k.Topic.ValueString(),
	}
	if !k.TLS.IsNull() {
		var tls TLSConfigModel
		diagnostics.Append(k.TLS.As(ctx, &tls, basetypes.ObjectAsOptions{})...)
		msg.Tls = tls.ToProto()
	}

	if !k.ScramAuth.IsNull() {
		var scram KafkaScramAuthModel
		diagnostics.Append(k.ScramAuth.As(ctx, &scram, basetypes.ObjectAsOptions{})...)
		msg.Auth = &telecastertypesv1beta1.KafkaConfig_Scram{
			Scram: scram.ToProto(),
		}
	}

	if diagnostics.HasError() {
		return nil, diagnostics
	}

	return
}

type ForwardingEndpointPrometheusModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	TLS      types.Object `tfsdk:"tls"`
}

func (p *ForwardingEndpointPrometheusModel) Set(ctx context.Context, prom *telecastertypesv1beta1.PrometheusRemoteWriteConfig) (diagnostics diag.Diagnostics) {
	if prom == nil {
		return nil
	} else if p == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil ForwardingEndpointPrometheusModel")
		return
	}

	p.Endpoint = types.StringValue(prom.Endpoint)
	var tls *TLSConfigModel
	if prom.Tls != nil {
		tls = new(TLSConfigModel)
		diagnostics.Append(tls.Set(prom.Tls)...)
	}
	tlsObj, diags := types.ObjectValueFrom(context.Background(), p.TLS.AttributeTypes(ctx), tls)
	diagnostics.Append(diags...)
	p.TLS = tlsObj

	return
}

func (p *ForwardingEndpointPrometheusModel) ToProto(ctx context.Context) (msg *telecastertypesv1beta1.PrometheusRemoteWriteConfig, diagnostics diag.Diagnostics) {
	if p == nil {
		return
	}

	msg = &telecastertypesv1beta1.PrometheusRemoteWriteConfig{
		Endpoint: p.Endpoint.ValueString(),
	}

	if !p.TLS.IsNull() {
		var tls *TLSConfigModel
		diagnostics.Append(p.TLS.As(ctx, tls, basetypes.ObjectAsOptions{})...)
		msg.Tls = tls.ToProto()
	}

	if diagnostics.HasError() {
		return nil, diagnostics
	}
	return
}

type ForwardingEndpointS3Model struct {
	URI                 types.String `tfsdk:"uri"`
	Region              types.String `tfsdk:"region"`
	RequiresCredentials types.Bool   `tfsdk:"requires_credentials"`
}

func (s *ForwardingEndpointS3Model) Set(s3 *telecastertypesv1beta1.S3Config) (diagnostics diag.Diagnostics) {
	if s3 == nil {
		return nil
	} else if s == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil ForwardingEndpointS3Model")
		return
	}
	s.URI = types.StringValue(s3.Uri)
	s.Region = types.StringValue(s3.Region)
	s.RequiresCredentials = types.BoolValue(s3.RequiresCredentials)

	return
}

func (s *ForwardingEndpointS3Model) ToProto() *telecastertypesv1beta1.S3Config {
	if s == nil {
		return nil
	}

	return &telecastertypesv1beta1.S3Config{
		Uri:                 s.URI.ValueString(),
		Region:              s.Region.ValueString(),
		RequiresCredentials: s.RequiresCredentials.ValueBool(),
	}
}

type ForwardingEndpointHTTPSModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	TLS      types.Object `tfsdk:"tls"`
}

func (h *ForwardingEndpointHTTPSModel) Set(ctx context.Context, https *telecastertypesv1beta1.HTTPSConfig) (diagnostics diag.Diagnostics) {
	if https == nil {
		return nil
	} else if h == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil ForwardingEndpointHTTPSModel")
		return
	}
	h.Endpoint = types.StringValue(https.Endpoint)

	var tls *TLSConfigModel
	if https.Tls != nil {
		diagnostics.Append(tls.Set(https.Tls)...)
		tlsObj, diags := types.ObjectValueFrom(ctx, h.TLS.AttributeTypes(ctx), &tls)
		diagnostics.Append(diags...)
		h.TLS = tlsObj
	} else {
		h.TLS = types.ObjectNull(h.TLS.AttributeTypes(ctx))
	}

	return
}

func (h *ForwardingEndpointHTTPSModel) ToProto() (msg *telecastertypesv1beta1.HTTPSConfig, diagnostics diag.Diagnostics) {
	if h == nil {
		return
	}

	msg = &telecastertypesv1beta1.HTTPSConfig{
		Endpoint: h.Endpoint.ValueString(),
	}

	if !h.TLS.IsNull() {
		var tls TLSConfigModel
		diagnostics.Append(h.TLS.As(context.Background(), &tls, basetypes.ObjectAsOptions{})...)
		msg.Tls = tls.ToProto()
	}

	if diagnostics.HasError() {
		return nil, diagnostics
	}
	return
}

type KafkaScramAuthModel struct {
	Mechanism types.String `tfsdk:"mechanism"`
}

func (k *KafkaScramAuthModel) Set(msg *telecastertypesv1beta1.KafkaScramAuth) {
	if msg.Mechanism == "" {
		k.Mechanism = types.StringNull()
	} else {
		k.Mechanism = types.StringValue(msg.Mechanism)
	}
}

func (k *KafkaScramAuthModel) ToProto() *telecastertypesv1beta1.KafkaScramAuth {
	if k == nil {
		return nil
	}

	return &telecastertypesv1beta1.KafkaScramAuth{
		Mechanism: k.Mechanism.ValueString(),
	}
}

type PrometheusCredentialsModel struct {
	BasicAuth              types.Object `tfsdk:"basic_auth"`
	BearerToken            types.Object `tfsdk:"bearer_token"`
	AuthHeadersCredentials types.Object `tfsdk:"auth_headers_credentials"`
}

func (p *PrometheusCredentialsModel) Set(ctx context.Context, msg *telecastertypesv1beta1.PrometheusCredentials) (diagnostics diag.Diagnostics) {
	if msg == nil {
		return nil
	}

	var oneofDiags diag.Diagnostics
	switch auth := msg.Auth.(type) {
	case *telecastertypesv1beta1.PrometheusCredentials_BasicAuth:
		var basicAuth BasicAuthCredentialsModel
		basicAuth.Set(auth.BasicAuth)
		p.BasicAuth, oneofDiags = types.ObjectValueFrom(ctx, p.BasicAuth.AttributeTypes(ctx), &basicAuth)
		diagnostics.Append(oneofDiags...)
	case *telecastertypesv1beta1.PrometheusCredentials_BearerToken:
		var bearerToken BearerTokenCredentialsModel
		bearerToken.Set(auth.BearerToken)
		p.BearerToken, oneofDiags = types.ObjectValueFrom(ctx, p.BearerToken.AttributeTypes(ctx), &bearerToken)
		diagnostics.Append(oneofDiags...)
	case *telecastertypesv1beta1.PrometheusCredentials_AuthHeaders:
		var authHeaders AuthHeadersCredentialsModel
		authHeaders.Set(ctx, auth.AuthHeaders)
		p.AuthHeadersCredentials, oneofDiags = types.ObjectValueFrom(ctx, p.AuthHeadersCredentials.AttributeTypes(ctx), &authHeaders)
		diagnostics.Append(oneofDiags...)
	}
	diagnostics.Append(oneofDiags...)

	return
}

func (p *PrometheusCredentialsModel) ToProto(ctx context.Context) (msg *telecastertypesv1beta1.PrometheusCredentials, diagnostics diag.Diagnostics) {
	if p == nil {
		return nil, nil
	}

	msg = &telecastertypesv1beta1.PrometheusCredentials{}
	implementations := make([]string, 0)

	if !p.BasicAuth.IsNull() {
		implementations = append(implementations, "basic_auth")
		var basicAuth BasicAuthCredentialsModel
		diagnostics.Append(p.BasicAuth.As(ctx, &basicAuth, basetypes.ObjectAsOptions{})...)
		msg.Auth = &telecastertypesv1beta1.PrometheusCredentials_BasicAuth{
			BasicAuth: basicAuth.ToProto(),
		}
	}
	if !p.BearerToken.IsNull() {
		implementations = append(implementations, "bearer_token")
		var bearerToken BearerTokenCredentialsModel
		diagnostics.Append(p.BearerToken.As(ctx, &bearerToken, basetypes.ObjectAsOptions{})...)
		msg.Auth = &telecastertypesv1beta1.PrometheusCredentials_BearerToken{
			BearerToken: bearerToken.ToProto(),
		}
	}
	if !p.AuthHeadersCredentials.IsNull() {
		implementations = append(implementations, "auth_headers")
		var authHeaders AuthHeadersCredentialsModel
		diagnostics.Append(p.AuthHeadersCredentials.As(ctx, &authHeaders, basetypes.ObjectAsOptions{})...)
		headersModel, diags := authHeaders.ToProto(ctx)
		diagnostics.Append(diags...)
		msg.Auth = &telecastertypesv1beta1.PrometheusCredentials_AuthHeaders{
			AuthHeaders: headersModel,
		}
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

func (b *BasicAuthCredentialsModel) Set(basicAuth *telecastertypesv1beta1.BasicAuthCredentials) {
	b.Username = types.StringValue(basicAuth.Username)
	b.Password = types.StringValue(basicAuth.Password)
}

func (b *BasicAuthCredentialsModel) ToProto() *telecastertypesv1beta1.BasicAuthCredentials {
	if b == nil {
		return nil
	}

	return &telecastertypesv1beta1.BasicAuthCredentials{
		Username: b.Username.ValueString(),
		Password: b.Password.ValueString(),
	}
}

type BearerTokenCredentialsModel struct {
	Token types.String `tfsdk:"token"`
}

func (b *BearerTokenCredentialsModel) Set(bearerToken *telecastertypesv1beta1.BearerTokenCredentials) {
	b.Token = types.StringValue(bearerToken.Token)
}

func (b *BearerTokenCredentialsModel) ToProto() *telecastertypesv1beta1.BearerTokenCredentials {
	if b == nil {
		return nil
	}

	return &telecastertypesv1beta1.BearerTokenCredentials{
		Token: b.Token.ValueString(),
	}
}

type AuthHeadersCredentialsModel struct {
	Headers types.Map `tfsdk:"headers"`
}

func (h *AuthHeadersCredentialsModel) Set(ctx context.Context, msg *telecastertypesv1beta1.AuthHeadersCredentials) (diagnostics diag.Diagnostics) {
	headers, diags := types.MapValueFrom(ctx, types.StringType, msg.Headers)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return
	}

	h.Headers = headers
	return
}

func (h *AuthHeadersCredentialsModel) ToProto(ctx context.Context) (msg *telecastertypesv1beta1.AuthHeadersCredentials, diagnostics diag.Diagnostics) {
	if h == nil {
		return nil, nil
	}

	headers := make(map[string]string)
	diagnostics.Append(h.Headers.ElementsAs(ctx, &headers, false)...)

	if diagnostics.HasError() {
		return
	}

	msg = &telecastertypesv1beta1.AuthHeadersCredentials{
		Headers: headers,
	}
	return
}

type S3CredentialsModel struct {
	AccessKeyID     types.String `tfsdk:"access_key_id"`
	SecretAccessKey types.String `tfsdk:"secret_access_key"`
	SessionToken    types.String `tfsdk:"session_token"`
}

func (s *S3CredentialsModel) Set(msg *telecastertypesv1beta1.S3Credentials) (diagnostics diag.Diagnostics) {
	s.AccessKeyID = types.StringValue(msg.AccessKeyId)
	s.SecretAccessKey = types.StringValue(msg.SecretAccessKey)
	if msg.SessionToken == "" {
		s.SessionToken = types.StringNull()
	} else {
		s.SessionToken = types.StringValue(msg.SessionToken)
	}

	return
}

func (s *S3CredentialsModel) ToProto() *telecastertypesv1beta1.S3Credentials {
	if s == nil {
		return nil
	}

	return &telecastertypesv1beta1.S3Credentials{
		AccessKeyId:     s.AccessKeyID.ValueString(),
		SecretAccessKey: s.SecretAccessKey.ValueString(),
		SessionToken:    s.SessionToken.ValueString(),
	}
}

func (e *ForwardingEndpointResourceModel) ToProto(ctx context.Context) (endpoint *telecastertypesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	if e == nil {
		return nil, nil
	}

	var (
		ref  ForwardingEndpointRefModel
		spec ForwardingEndpointSpecModel
		// status is not needed for the resource model to proto conversion.
	)
	diagnostics.Append(e.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(e.Spec.As(ctx, &spec, basetypes.ObjectAsOptions{})...)

	refProto := ref.ToProto()
	specProto, diags := spec.ToProto(ctx)
	diagnostics.Append(diags...)

	if diagnostics.HasError() {
		return nil, diagnostics
	}

	return &telecastertypesv1beta1.ForwardingEndpoint{
		Ref:  refProto,
		Spec: specProto,
	}, nil
}

func (s *ForwardingEndpointSpecModel) ToProto(ctx context.Context) (spec *telecastertypesv1beta1.ForwardingEndpointSpec, diagnostics diag.Diagnostics) {
	if s == nil {
		return nil, nil
	}

	spec = &telecastertypesv1beta1.ForwardingEndpointSpec{
		DisplayName: s.DisplayName.ValueString(),
	}

	configuredImplementations := make([]string, 0)
	if !s.Kafka.IsNull() {
		configuredImplementations = append(configuredImplementations, "kafka")
		var kafka ForwardingEndpointKafkaModel
		diagnostics.Append(s.Kafka.As(ctx, &kafka, basetypes.ObjectAsOptions{})...)
		kafkaConfig, diags := kafka.ToProto(ctx)
		diagnostics.Append(diags...)
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_Kafka{
			Kafka: kafkaConfig,
		}
	}

	if !s.Prometheus.IsNull() {
		configuredImplementations = append(configuredImplementations, "prometheus")
		var prom ForwardingEndpointPrometheusModel
		diagnostics.Append(s.Prometheus.As(ctx, &prom, basetypes.ObjectAsOptions{})...)
		promConfig, diags := prom.ToProto(ctx)
		diagnostics.Append(diags...)
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_Prometheus{
			Prometheus: promConfig,
		}
	}

	if !s.S3.IsNull() {
		configuredImplementations = append(configuredImplementations, "s3")
		var s3 ForwardingEndpointS3Model
		diagnostics.Append(s.S3.As(ctx, &s3, basetypes.ObjectAsOptions{})...)
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_S3{
			S3: s3.ToProto(),
		}
	}

	if !s.HTTPS.IsNull() {
		configuredImplementations = append(configuredImplementations, "https")
		var https ForwardingEndpointHTTPSModel
		diagnostics.Append(s.HTTPS.As(ctx, &https, basetypes.ObjectAsOptions{})...)
		httpsConfig, diags := https.ToProto()
		diagnostics.Append(diags...)
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_Https{
			Https: httpsConfig,
		}
	}

	if len(configuredImplementations) != 1 {
		diagnostics.AddError(
			"Invalid Forwarding Endpoint Spec",
			fmt.Sprintf("exactly one auth method should be set, got %d: %s", len(configuredImplementations), strings.Join(configuredImplementations, ", ")),
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

func (s *ForwardingEndpointStatusModel) Set(status *telecastertypesv1beta1.ForwardingEndpointStatus) (diagnostics diag.Diagnostics) {
	s.CreatedAt = timetypes.NewRFC3339TimeValue(status.CreatedAt.AsTime())
	s.UpdatedAt = timetypes.NewRFC3339TimeValue(status.UpdatedAt.AsTime())
	s.StateCode = types.Int32Value(int32(status.State.Number()))
	s.State = types.StringValue(status.State.String())
	s.StateMessage = types.StringPointerValue(status.StateMessage)
	return
}

func (f *ForwardingEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ref").AtName("slug"), req, resp)
}

func (f *ForwardingEndpointResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telecaster_forwarding_endpoint"
}

func (f *ForwardingEndpointResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster forwarding endpoint",
		Attributes: map[string]schema.Attribute{
			"ref": schema.SingleNestedAttribute{
				MarkdownDescription: "Identifying information for the forwarding endpoint.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"slug": schema.StringAttribute{
						MarkdownDescription: "The slug of the forwarding endpoint. Used as a unique identifier.",
						Required:            true,
					},
				},
			},
			"spec": schema.SingleNestedAttribute{
				MarkdownDescription: "The specification for the forwarding endpoint.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{
						MarkdownDescription: "The display name of the forwarding endpoint.",
						Required:            true,
					},
					"kafka": schema.SingleNestedAttribute{
						MarkdownDescription: "Kafka forwarding endpoint configuration.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"bootstrap_endpoints": schema.StringAttribute{
								MarkdownDescription: "The Kafka bootstrap endpoints.",
								Required:            true,
							},
							"topic": schema.StringAttribute{
								MarkdownDescription: "The Kafka topic.",
								Required:            true,
							},
							"tls": tlsConfigModelAttribute(),
							"scram_auth": schema.SingleNestedAttribute{
								MarkdownDescription: "SCRAM authentication configuration for Kafka.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"mechanism": schema.StringAttribute{
										MarkdownDescription: "The SCRAM mechanism (e.g., SCRAM-SHA-256, SCRAM-SHA-512).",
										Optional:            true,
									},
								},
							},
						},
					},
					"prometheus": schema.SingleNestedAttribute{
						MarkdownDescription: "Prometheus forwarding endpoint configuration.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"endpoint": schema.StringAttribute{
								MarkdownDescription: "The Prometheus remote write endpoint.",
								Required:            true,
							},
							"tls": tlsConfigModelAttribute(),
							"basic_auth": schema.SingleNestedAttribute{
								MarkdownDescription: "Basic authentication configuration for Prometheus.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"username": schema.StringAttribute{
										MarkdownDescription: "The username for basic authentication.",
										Required:            true,
										WriteOnly:           true,
									},
									"password": schema.StringAttribute{
										MarkdownDescription: "The password for basic authentication.",
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
								},
							},
						},
					},
					"s3": schema.SingleNestedAttribute{
						MarkdownDescription: "S3 forwarding endpoint configuration.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"uri": schema.StringAttribute{
								MarkdownDescription: "The S3 URI.",
								Required:            true,
							},
							"region": schema.StringAttribute{
								MarkdownDescription: "The S3 region.",
								Required:            true,
							},
							"credentials": schema.SingleNestedAttribute{
								MarkdownDescription: "Credentials configuration for S3.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"access_key_id": schema.StringAttribute{
										MarkdownDescription: "The S3 access key ID.",
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
									"secret_access_key": schema.StringAttribute{
										MarkdownDescription: "The S3 secret access key.",
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
									"session_token": schema.StringAttribute{
										MarkdownDescription: "The S3 session token.",
										Optional:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
								},
							},
						},
					},
					"https": schema.SingleNestedAttribute{
						MarkdownDescription: "HTTPS forwarding endpoint configuration.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"endpoint": schema.StringAttribute{
								MarkdownDescription: "The HTTPS endpoint.",
								Required:            true,
							},
							"tls": tlsConfigModelAttribute(),
							"basic_auth": schema.SingleNestedAttribute{
								MarkdownDescription: "Basic authentication configuration for HTTPS.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"username": schema.StringAttribute{
										MarkdownDescription: "The username for basic authentication.",
										Required:            true,
										WriteOnly:           true,
									},
									"password": schema.StringAttribute{
										MarkdownDescription: "The password for basic authentication.",
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
								},
							},
							"bearer_token": schema.SingleNestedAttribute{
								MarkdownDescription: "Bearer token authentication configuration for HTTPS.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"token": schema.StringAttribute{
										MarkdownDescription: "The bearer token.",
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
								},
							},
							"auth_headers_credentials": schema.SingleNestedAttribute{
								MarkdownDescription: "Authentication headers configuration for HTTPS.",
								Optional:            true,
								Attributes: map[string]schema.Attribute{
									"headers": schema.MapAttribute{
										MarkdownDescription: "A map of header names to values.",
										ElementType:         types.StringType,
										Required:            true,
										Sensitive:           true,
										WriteOnly:           true,
									},
								},
							},
						},
					},
				},
			},
			"status": schema.SingleNestedAttribute{
				MarkdownDescription: "The status of the forwarding endpoint.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"created_at": schema.StringAttribute{
						MarkdownDescription: "The creation time of the forwarding endpoint.",
						Computed:            true,
						CustomType:          timetypes.RFC3339Type{},
					},
					"updated_at": schema.StringAttribute{
						MarkdownDescription: "The last update time of the forwarding endpoint.",
						Computed:            true,
						CustomType:          timetypes.RFC3339Type{},
					},
					"state_code": schema.Int32Attribute{
						MarkdownDescription: "The state code of the forwarding endpoint.",
						Computed:            true,
					},
					"state": schema.StringAttribute{
						MarkdownDescription: "The state of the forwarding endpoint.",
						Computed:            true,
					},
					"state_message": schema.StringAttribute{
						MarkdownDescription: "The state message of the forwarding endpoint.",
						Computed:            true,
					},
				},
			},
		},
	}
}

func (f *ForwardingEndpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointProto, diags := data.ToProto(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := f.Client.CreateEndpoint(ctx, connect.NewRequest(&clusterv1beta1.CreateEndpointRequest{
		Ref:  endpointProto.Ref,
		Spec: endpointProto.Spec,
	})); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: endpointProto.Ref,
			}))
			if err != nil {
				return nil, telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			return getResp.Msg.Endpoint, getResp.Msg.Endpoint.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	rawEndpoint, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	endpoint, ok := rawEndpoint.(*telecastertypesv1beta1.ForwardingEndpoint)
	if !ok {
		resp.Diagnostics.AddError(
			"Error Creating Telecaster Forwarding Endpoint",
			fmt.Sprintf("Unexpected type %T when waiting for forwarding endpoint to become active", rawEndpoint),
		)
		return
	}

	data.Set(ctx, endpoint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (f *ForwardingEndpointResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var ref ForwardingEndpointRefModel
	resp.Diagnostics.Append(data.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	refProto := ref.ToProto()

	if _, err := f.Client.DeleteEndpoint(ctx, connect.NewRequest(&clusterv1beta1.DeleteEndpointRequest{
		Ref: refProto,
	})); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	const stateDeleted = "deleted"

	pollConf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{
			stateDeleted,
		},
		Refresh: func() (any, string, error) {
			result, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: refProto,
			}))
			if err != nil {
				return nil, telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			return result.Msg.Endpoint, result.Msg.Endpoint.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	_, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (f *ForwardingEndpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var ref ForwardingEndpointRefModel
	resp.Diagnostics.Append(data.Ref.As(ctx, &ref, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	refProto := ref.ToProto()

	getResp, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
		Ref: refProto,
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(ctx, getResp.Msg.Endpoint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (f *ForwardingEndpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointProto, diags := data.ToProto(ctx)
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	if _, err := f.Client.UpdateEndpoint(ctx, connect.NewRequest(&clusterv1beta1.UpdateEndpointRequest{
		Ref:  endpointProto.Ref,
		Spec: endpointProto.Spec,
	})); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	pollConf := retry.StateChangeConf{
		Pending: []string{
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{
			telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: endpointProto.Ref,
			}))
			if err != nil {
				return nil, telecastertypesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			return getResp.Msg.Endpoint, getResp.Msg.Endpoint.Status.State.String(), nil
		},
		Timeout: 10 * time.Minute,
	}

	rawEndpoint, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	endpoint, ok := rawEndpoint.(*connect.Response[clusterv1beta1.GetEndpointResponse])
	if !ok {
		resp.Diagnostics.AddError(
			"Error Updating Telecaster Forwarding Endpoint",
			fmt.Sprintf("Unexpected type %T when waiting for forwarding endpoint to become active", rawEndpoint),
		)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, endpoint.Msg.Endpoint)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
