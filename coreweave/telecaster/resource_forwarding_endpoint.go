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
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
	Ref    ForwardingEndpointRefModel     `tfsdk:"ref"`
	Spec   ForwardingEndpointSpecModel   `tfsdk:"spec"`
	Status types.Object `tfsdk:"status"`
}

func (e *ForwardingEndpointResourceModel) Set(data *telecastertypesv1beta1.ForwardingEndpoint) {
	e.Ref = ForwardingEndpointRefModel{
		Slug: types.StringValue(data.Ref.Slug),
	}

	e.Spec = ForwardingEndpointSpecModel{
		DisplayName: types.StringValue(data.Spec.DisplayName),
	}

	ctx := context.Background()
	status, diag := types.ObjectValueFrom(ctx, e.Status.AttributeTypes(ctx), e.Status.Attributes())
	if diag.HasError() {
		panic(diag)
	}
	e.Status = status

	// e.Status = ForwardingPipelineStatusModel{
	// 	CreatedAt:    timetypes.NewRFC3339TimeValue(data.Status.CreatedAt.AsTime()),
	// 	UpdatedAt:    timetypes.NewRFC3339TimeValue(data.Status.UpdatedAt.AsTime()),
	// 	State:        types.StringValue(data.Status.State.String()),
	// 	StateCode:    types.Int32Value(int32(data.Status.State.Number())),
	// 	StateMessage: types.StringPointerValue(data.Status.StateMessage),
	// }

	switch cfg := data.Spec.Config.(type) {
	case *telecastertypesv1beta1.ForwardingEndpointSpec_Kafka:
		e.Spec.Kafka = &ForwardingEndpointKafkaModel{
			BootstrapEndpoints: types.StringValue(cfg.Kafka.BootstrapEndpoints),
			Topic:              types.StringValue(cfg.Kafka.Topic),
		}
		if cfg.Kafka.Tls != nil {
			e.Spec.Kafka.TLS = &TLSConfigModel{
				CertificateAuthorityData: types.StringValue(cfg.Kafka.Tls.CertificateAuthorityData),
			}
		}
		scramAuth, ok := cfg.Kafka.Auth.(*telecastertypesv1beta1.KafkaConfig_Scram)
		if !ok {
			panic(fmt.Sprintf("unknown kafka auth type: %T", cfg.Kafka.Auth))
		}
		e.Spec.Kafka.ScramAuth = &KafkaScramAuthModel{
			Secret: &SecretRefModel{
				Slug: types.StringValue(scramAuth.Scram.Secret.Slug),
			},
			UsernameKey: types.StringValue(scramAuth.Scram.UsernameKey),
			PasswordKey: types.StringValue(scramAuth.Scram.PasswordKey),
		}
	case *telecastertypesv1beta1.ForwardingEndpointSpec_Prometheus:
		e.Spec.Prometheus = &ForwardingEndpointPrometheusModel{
			Endpoint: types.StringValue(cfg.Prometheus.Endpoint),
		}
		if cfg.Prometheus.Tls != nil {
			e.Spec.Prometheus.TLS = &TLSConfigModel{
				CertificateAuthorityData: types.StringValue(cfg.Prometheus.Tls.CertificateAuthorityData),
			}
		}
		if cfg.Prometheus.BasicAuth != nil {
			e.Spec.Prometheus.BasicAuth = &PrometheusBasicAuthModel{
				Secret: &SecretRefModel{
					Slug: types.StringValue(cfg.Prometheus.BasicAuth.Secret.Slug),
				},
				UsernameKey: types.StringValue(cfg.Prometheus.BasicAuth.UsernameKey),
				PasswordKey: types.StringValue(cfg.Prometheus.BasicAuth.PasswordKey),
			}
		}
	case *telecastertypesv1beta1.ForwardingEndpointSpec_S3:
		e.Spec.S3 = &ForwardingEndpointS3Model{
			URI:    types.StringValue(cfg.S3.Uri),
			Region: types.StringValue(cfg.S3.Region),
		}
		if cfg.S3.Credentials != nil {
			e.Spec.S3.Credentials = &S3CredentialsModel{
				Secret: &SecretRefModel{
					Slug: types.StringValue(cfg.S3.Credentials.Secret.Slug),
				},
				AccessKeyIDKey:     types.StringValue(cfg.S3.Credentials.AccessKeyIdKey),
				SecretAccessKeyKey: types.StringValue(cfg.S3.Credentials.SecretAccessKeyKey),
			}
		}
	case *telecastertypesv1beta1.ForwardingEndpointSpec_Https:
		e.Spec.HTTPS = &ForwardingEndpointHTTPSModel{
			Endpoint: types.StringValue(cfg.Https.Endpoint),
		}
		if cfg.Https.Tls != nil {
			e.Spec.HTTPS.TLS = &TLSConfigModel{
				CertificateAuthorityData: types.StringValue(cfg.Https.Tls.CertificateAuthorityData),
			}
		}
		if cfg.Https.BasicAuth != nil {
			e.Spec.HTTPS.BasicAuth = &HTTPSBasicAuthModel{
				Secret: &SecretRefModel{
					Slug: types.StringValue(cfg.Https.BasicAuth.Secret.Slug),
				},
				UsernameKey: types.StringValue(cfg.Https.BasicAuth.UsernameKey),
				PasswordKey: types.StringValue(cfg.Https.BasicAuth.PasswordKey),
			}
		}
	default:
		panic(fmt.Sprintf("unknown forwarding endpoint config type: %T", cfg))
	}
}

type ForwardingEndpointRefModel struct {
	Slug types.String `tfsdk:"slug"`
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

type ForwardingEndpointKafkaModel struct {
	BootstrapEndpoints types.String         `tfsdk:"bootstrap_endpoints"`
	Topic              types.String         `tfsdk:"topic"`
	TLS                *TLSConfigModel      `tfsdk:"tls"`
	ScramAuth          *KafkaScramAuthModel `tfsdk:"scram_auth"`
}

type ForwardingEndpointPrometheusModel struct {
	Endpoint  types.String              `tfsdk:"endpoint"`
	TLS       *TLSConfigModel           `tfsdk:"tls"`
	BasicAuth *PrometheusBasicAuthModel `tfsdk:"basic_auth"`
}

type ForwardingEndpointS3Model struct {
	URI         types.String        `tfsdk:"uri"`
	Region      types.String        `tfsdk:"region"`
	Credentials *S3CredentialsModel `tfsdk:"credentials"`
}

type ForwardingEndpointHTTPSModel struct {
	Endpoint  types.String         `tfsdk:"endpoint"`
	TLS       *TLSConfigModel      `tfsdk:"tls"`
	BasicAuth *HTTPSBasicAuthModel `tfsdk:"basic_auth"`
}

type KafkaScramAuthModel struct {
	Secret      *SecretRefModel `tfsdk:"secret"`
	UsernameKey types.String    `tfsdk:"username_key"`
	PasswordKey types.String    `tfsdk:"password_key"`
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

func (e *ForwardingEndpointResourceModel) ToProto() (*telecastertypesv1beta1.ForwardingEndpoint, error) {
	if e == nil {
		return nil, nil
	}

	endpoint := &telecastertypesv1beta1.ForwardingEndpoint{
		Ref: &telecastertypesv1beta1.ForwardingEndpointRef{
			Slug: e.Ref.Slug.String(),
		},
	}

	endpoint.Ref = &telecastertypesv1beta1.ForwardingEndpointRef{
		Slug: e.Ref.Slug.ValueString(),
	}
	spec, err := e.Spec.ToProto()
	if err != nil {
		return nil, fmt.Errorf("failed to convert spec to proto object: %w", err)
	}
	endpoint.Spec = spec

	return endpoint, nil
}

func (s *ForwardingEndpointSpecModel) ToProto() (*telecastertypesv1beta1.ForwardingEndpointSpec, error) {
	if s == nil {
		return nil, nil
	}

	spec := &telecastertypesv1beta1.ForwardingEndpointSpec{
		DisplayName: s.DisplayName.ValueString(),
	}

	configuredImplementations := make([]string, 0)
	if s.Kafka != nil {
		configuredImplementations = append(configuredImplementations, "kafka")
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_Kafka{
			Kafka: &telecastertypesv1beta1.KafkaConfig{
				BootstrapEndpoints: s.Kafka.BootstrapEndpoints.ValueString(),
				Topic:              s.Kafka.Topic.ValueString(),
				Tls:                s.Kafka.TLS.ToProto(),
				Auth:               s.Kafka.ScramAuth.ToProto(),
			},
		}
	}

	if s.Prometheus != nil {
		configuredImplementations = append(configuredImplementations, "prometheus")
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_Prometheus{
			Prometheus: &telecastertypesv1beta1.PrometheusRemoteWriteConfig{
				Endpoint:  s.Prometheus.Endpoint.ValueString(),
				Tls:       s.Prometheus.TLS.ToProto(),
				BasicAuth: s.Prometheus.BasicAuth.ToProto(),
			},
		}
	}

	if s.S3 != nil {
		configuredImplementations = append(configuredImplementations, "s3")
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_S3{
			S3: &telecastertypesv1beta1.S3Config{
				Uri:         s.S3.URI.ValueString(),
				Region:      s.S3.Region.ValueString(),
				Credentials: s.S3.Credentials.ToProto(),
			},
		}
	}

	if s.HTTPS != nil {
		configuredImplementations = append(configuredImplementations, "https")
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_Https{
			Https: &telecastertypesv1beta1.HTTPSConfig{
				Endpoint:  s.HTTPS.Endpoint.ValueString(),
				Tls:       s.HTTPS.TLS.ToProto(),
				BasicAuth: s.HTTPS.BasicAuth.ToProto(),
			},
		}
	}

	if len(configuredImplementations) != 1 {
		return nil, fmt.Errorf("exactly one auth method should be set, got %d: %s", len(configuredImplementations), strings.Join(configuredImplementations, ", "))
	}

	return spec, nil
}

func (f *ForwardingEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("slug"), req, resp)
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

	endpointProto, err := data.ToProto()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Telecaster Forwarding Endpoint",
			fmt.Sprintf("Could not convert forwarding endpoint to proto object: %v", err),
		)
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
				Ref: data.Ref.ToProto(),
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

	data.Set(endpoint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (f *ForwardingEndpointResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := f.Client.DeleteEndpoint(ctx, connect.NewRequest(&clusterv1beta1.DeleteEndpointRequest{
		Ref: data.Ref.ToProto(),
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
				Ref: data.Ref.ToProto(),
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

	getResp, err := f.Client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
		Ref: data.Ref.ToProto(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(getResp.Msg.Endpoint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (f *ForwardingEndpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpointProto, err := data.ToProto()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Updating Telecaster Forwarding Endpoint",
			fmt.Sprintf("Could not convert forwarding endpoint to proto object: %v", err),
		)
		return
	}

	if _, err = f.Client.UpdateEndpoint(ctx, connect.NewRequest(&clusterv1beta1.UpdateEndpointRequest{
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
				Ref: data.Ref.ToProto(),
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

	data.Set(endpoint.Msg.Endpoint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
