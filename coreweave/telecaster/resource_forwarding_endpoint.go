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
	diagnostics.Append(spec.Set(data.Spec)...)
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
	if ref == nil {
		return
	} else if r == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil ForwardingEndpointRefModel")
		return
	}
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
	DisplayName types.String                       `tfsdk:"display_name"`
	Kafka       *ForwardingEndpointKafkaModel      `tfsdk:"kafka"`
	Prometheus  *ForwardingEndpointPrometheusModel `tfsdk:"prometheus"`
	S3          *ForwardingEndpointS3Model         `tfsdk:"s3"`
	HTTPS       *ForwardingEndpointHTTPSModel      `tfsdk:"https"`
}

func (e *ForwardingEndpointSpecModel) Set(proto *telecastertypesv1beta1.ForwardingEndpointSpec) (diagnostics diag.Diagnostics) {
	if e == nil {
		return nil
	}
	e.DisplayName = types.StringValue(proto.DisplayName)
	switch cfg := proto.Config.(type) {
	case *telecastertypesv1beta1.ForwardingEndpointSpec_Kafka:
		var kafka ForwardingEndpointKafkaModel
		diagnostics.Append(kafka.Set(cfg.Kafka)...)
		e.Kafka = &kafka
	}
	// TODO
	return
}

type ForwardingEndpointKafkaModel struct {
	BootstrapEndpoints types.String         `tfsdk:"bootstrap_endpoints"`
	Topic              types.String         `tfsdk:"topic"`
	TLS                *TLSConfigModel      `tfsdk:"tls"`
	ScramAuth          *KafkaScramAuthModel `tfsdk:"scram_auth"`
}

func (k *ForwardingEndpointKafkaModel) Set(proto *telecastertypesv1beta1.KafkaConfig) (diagnostics diag.Diagnostics) {
	if proto == nil {
		return
	} else if k == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil ForwardingEndpointKafkaModel")
		return
	}
	k.BootstrapEndpoints = types.StringValue(proto.BootstrapEndpoints)
	k.Topic = types.StringValue(proto.Topic)
	if proto.Tls != nil {
		k.TLS = new(TLSConfigModel)
		diagnostics.Append(k.TLS.Set(proto.Tls)...)
	}
	return
}

func (k *ForwardingEndpointKafkaModel) ToProto() *telecastertypesv1beta1.ForwardingEndpointSpec_Kafka {
	if k == nil {
		return nil
	}

	return &telecastertypesv1beta1.ForwardingEndpointSpec_Kafka{
		Kafka: &telecastertypesv1beta1.KafkaConfig{
			BootstrapEndpoints: k.BootstrapEndpoints.ValueString(),
			Topic:              k.Topic.ValueString(),
			Tls:                k.TLS.ToProto(),
			Auth:               k.ScramAuth.ToProto(),
		},
	}
}

type ForwardingEndpointPrometheusModel struct {
	Endpoint  types.String              `tfsdk:"endpoint"`
	TLS       *TLSConfigModel           `tfsdk:"tls"`
	BasicAuth *PrometheusBasicAuthModel `tfsdk:"basic_auth"`
}

func (p *ForwardingEndpointPrometheusModel) Set(prom *telecastertypesv1beta1.PrometheusRemoteWriteConfig) (diagnostics diag.Diagnostics) {
	if prom == nil {
		return nil
	} else if p == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil ForwardingEndpointPrometheusModel")
		return
	}

	p.Endpoint = types.StringValue(prom.Endpoint)
	if prom.Tls != nil {
		p.TLS = new(TLSConfigModel)
		diagnostics.Append(p.TLS.Set(prom.Tls)...)
	} else {
		p.TLS = nil
	}

	if prom.BasicAuth != nil {
		p.BasicAuth = &PrometheusBasicAuthModel{
			Secret: &SecretRefModel{
				Slug: types.StringValue(prom.BasicAuth.Secret.Slug),
			},
			UsernameKey: types.StringValue(prom.BasicAuth.UsernameKey),
			PasswordKey: types.StringValue(prom.BasicAuth.PasswordKey),
		}
	} else {
		p.BasicAuth = nil
	}

	return
}

func (p *ForwardingEndpointPrometheusModel) ToProto() *telecastertypesv1beta1.ForwardingEndpointSpec_Prometheus {
	if p == nil {
		return nil
	}

	return &telecastertypesv1beta1.ForwardingEndpointSpec_Prometheus{
		Prometheus: &telecastertypesv1beta1.PrometheusRemoteWriteConfig{
			Endpoint:  p.Endpoint.ValueString(),
			Tls:       p.TLS.ToProto(),
			BasicAuth: p.BasicAuth.ToProto(),
		},
	}
}

type ForwardingEndpointS3Model struct {
	URI         types.String        `tfsdk:"uri"`
	Region      types.String        `tfsdk:"region"`
	Credentials *S3CredentialsModel `tfsdk:"credentials"`
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

	if s3.Credentials != nil {
		s.Credentials = new(S3CredentialsModel)
		diagnostics.Append(s.Credentials.Set(s3.Credentials)...)
	} else {
		s.Credentials = nil
	}

	return
}

func (s *ForwardingEndpointS3Model) ToProto() *telecastertypesv1beta1.ForwardingEndpointSpec_S3 {
	if s == nil {
		return nil
	}

	return &telecastertypesv1beta1.ForwardingEndpointSpec_S3{
		S3: &telecastertypesv1beta1.S3Config{
			Uri:         s.URI.ValueString(),
			Region:      s.Region.ValueString(),
			Credentials: s.Credentials.ToProto(),
		},
	}
}

type ForwardingEndpointHTTPSModel struct {
	Endpoint  types.String         `tfsdk:"endpoint"`
	TLS       *TLSConfigModel      `tfsdk:"tls"`
	BasicAuth *HTTPSBasicAuthModel `tfsdk:"basic_auth"`
}

func (h *ForwardingEndpointHTTPSModel) Set(https *telecastertypesv1beta1.HTTPSConfig) (diagnostics diag.Diagnostics) {
	if https == nil {
		return nil
	} else if h == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil ForwardingEndpointHTTPSModel")
		return
	}
	h.Endpoint = types.StringValue(https.Endpoint)

	if https.Tls != nil {
		h.TLS = new(TLSConfigModel)
		diagnostics.Append(h.TLS.Set(https.Tls)...)
	} else {
		h.TLS = nil
	}

	if https.BasicAuth != nil {
		h.BasicAuth = new(HTTPSBasicAuthModel)
		diagnostics.Append(h.BasicAuth.Set(https.BasicAuth)...)
	} else {
		h.BasicAuth = nil
	}

	return
}

func (h *ForwardingEndpointHTTPSModel) ToProto() *telecastertypesv1beta1.ForwardingEndpointSpec_Https {
	if h == nil {
		return nil
	}

	return &telecastertypesv1beta1.ForwardingEndpointSpec_Https{
		Https: &telecastertypesv1beta1.HTTPSConfig{
			Endpoint:  h.Endpoint.ValueString(),
			Tls:       h.TLS.ToProto(),
			BasicAuth: h.BasicAuth.ToProto(),
		},
	}
}

type KafkaScramAuthModel struct {
	Secret      *SecretRefModel `tfsdk:"secret"`
	UsernameKey types.String    `tfsdk:"username_key"`
	PasswordKey types.String    `tfsdk:"password_key"`
}

func (k *KafkaScramAuthModel) Set(proto *telecastertypesv1beta1.KafkaScramAuth) (diagnostics diag.Diagnostics) {
	if proto == nil {
		return nil
	} else if k == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil KafkaScramAuthModel")
		return
	}
	k.Secret = &SecretRefModel{
		Slug: types.StringValue(proto.Secret.Slug),
	}
	k.UsernameKey = types.StringValue(proto.UsernameKey)
	k.PasswordKey = types.StringValue(proto.PasswordKey)
	return
}

func (k *KafkaScramAuthModel) ToProto() *telecastertypesv1beta1.KafkaConfig_Scram {
	if k == nil {
		return nil
	}

	return &telecastertypesv1beta1.KafkaConfig_Scram{
		Scram: &telecastertypesv1beta1.KafkaScramAuth{
			Secret:      k.Secret.ToProto(),
			UsernameKey: k.UsernameKey.ValueString(),
			PasswordKey: k.PasswordKey.ValueString(),
		},
	}
}

type PrometheusBasicAuthModel struct {
	Secret      *SecretRefModel `tfsdk:"secret"`
	UsernameKey types.String    `tfsdk:"username_key"`
	PasswordKey types.String    `tfsdk:"password_key"`
}

func (p *PrometheusBasicAuthModel) ToProto() *telecastertypesv1beta1.PrometheusBasicAuth {
	if p == nil {
		return nil
	}

	return &telecastertypesv1beta1.PrometheusBasicAuth{
		Secret:      p.Secret.ToProto(),
		UsernameKey: p.UsernameKey.ValueString(),
		PasswordKey: p.PasswordKey.ValueString(),
	}
}

type HTTPSBasicAuthModel struct {
	Secret      *SecretRefModel `tfsdk:"secret"`
	UsernameKey types.String    `tfsdk:"username_key"`
	PasswordKey types.String    `tfsdk:"password_key"`
}

func (h *HTTPSBasicAuthModel) Set(basicauth *telecastertypesv1beta1.HTTPSBasicAuth) (diagnostics diag.Diagnostics) {
	if basicauth == nil {
		return
	} else if h == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil HTTPSBasicAuthModel")
		return
	}

	h.Secret = new(SecretRefModel)
	diagnostics.Append(h.Secret.Set(basicauth.Secret)...)
	if diagnostics.HasError() {
		return
	}

	h.UsernameKey = types.StringValue(basicauth.UsernameKey)
	h.PasswordKey = types.StringValue(basicauth.PasswordKey)

	return
}

func (h *HTTPSBasicAuthModel) ToProto() *telecastertypesv1beta1.HTTPSBasicAuth {
	if h == nil {
		return nil
	}

	return &telecastertypesv1beta1.HTTPSBasicAuth{
		Secret:      h.Secret.ToProto(),
		UsernameKey: h.UsernameKey.ValueString(),
		PasswordKey: h.PasswordKey.ValueString(),
	}
}

type S3CredentialsModel struct {
	Secret             *SecretRefModel `tfsdk:"secret"`
	AccessKeyIDKey     types.String    `tfsdk:"access_key_id_key"`
	SecretAccessKeyKey types.String    `tfsdk:"secret_access_key_key"`
}

func (s *S3CredentialsModel) Set(creds *telecastertypesv1beta1.S3Credentials) (diagnostics diag.Diagnostics) {
	if creds == nil {
		return nil
	} else if s == nil {
		diagnostics.AddError("nil receiver", "nil receiver: cannot call Set() on nil S3CredentialsModel")
		return
	}

	s.Secret = new(SecretRefModel)
	diagnostics.Append(s.Secret.Set(creds.Secret)...)
	if diagnostics.HasError() {
		return
	}

	s.AccessKeyIDKey = types.StringValue(creds.AccessKeyIdKey)
	s.SecretAccessKeyKey = types.StringValue(creds.SecretAccessKeyKey)
	return
}

func (s *S3CredentialsModel) ToProto() *telecastertypesv1beta1.S3Credentials {
	if s == nil {
		return nil
	}

	return &telecastertypesv1beta1.S3Credentials{
		Secret:             s.Secret.ToProto(),
		AccessKeyIdKey:     s.AccessKeyIDKey.ValueString(),
		SecretAccessKeyKey: s.SecretAccessKeyKey.ValueString(),
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
	specProto, diags := spec.ToProto()
	diagnostics.Append(diags...)

	if diagnostics.HasError() {
		return nil, diagnostics
	}

	return &telecastertypesv1beta1.ForwardingEndpoint{
		Ref:  refProto,
		Spec: specProto,
	}, nil
}

func (s *ForwardingEndpointSpecModel) ToProto() (spec *telecastertypesv1beta1.ForwardingEndpointSpec, diagnostics diag.Diagnostics) {
	if s == nil {
		return nil, nil
	}

	spec = &telecastertypesv1beta1.ForwardingEndpointSpec{
		DisplayName: s.DisplayName.ValueString(),
	}

	configuredImplementations := make([]string, 0)
	if s.Kafka != nil {
		configuredImplementations = append(configuredImplementations, "kafka")
		spec.Config = s.Kafka.ToProto()
	}

	if s.Prometheus != nil {
		configuredImplementations = append(configuredImplementations, "prometheus")
		spec.Config = s.Prometheus.ToProto()
	}

	if s.S3 != nil {
		configuredImplementations = append(configuredImplementations, "s3")
		spec.Config = s.S3.ToProto()
	}

	if s.HTTPS != nil {
		configuredImplementations = append(configuredImplementations, "https")
		spec.Config = s.HTTPS.ToProto()
	}

	if len(configuredImplementations) != 1 {
		diagnostics.AddError(
			"Invalid Forwarding Endpoint Spec",
			fmt.Sprintf("exactly one auth method should be set, got %d: %s", len(configuredImplementations), strings.Join(configuredImplementations, ", ")),
		)
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
	if status == nil {
		return
	}
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
									"secret":       secretRefSchema(),
									"username_key": secretKeySchemaAttribute("username"),
									"password_key": secretKeySchemaAttribute("password"),
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
									"secret":       secretRefSchema(),
									"username_key": secretKeySchemaAttribute("username"),
									"password_key": secretKeySchemaAttribute("password"),
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
									"secret":                secretRefSchema(),
									"access_key_id_key":     secretKeySchemaAttribute("access_key_id"),
									"secret_access_key_key": secretKeySchemaAttribute("secret_access_key"),
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
									"secret":       secretRefSchema(),
									"username_key": secretKeySchemaAttribute("username"),
									"password_key": secretKeySchemaAttribute("password"),
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
