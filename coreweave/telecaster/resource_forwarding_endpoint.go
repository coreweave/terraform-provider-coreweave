package telecaster

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	clusterv1beta1 "github.com/coreweave/o11y-mgmt/gen/cw/telecaster/svc/cluster/v1beta1"
	telecastertypesv1beta1 "github.com/coreweave/o11y-mgmt/gen/cw/telecaster/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource = &ForwardingEndpointResource{}
)

func NewForwardingPipelineResource() *ForwardingEndpointResource {
	return &ForwardingEndpointResource{}
}

type ForwardingEndpointResource struct {
	client *coreweave.Client
}

type ForwardingEndpointResourceModel struct {
	ForwardingEndpointRefModel

	Spec *ForwardingEndpointSpecModel `tfsdk:"spec"`
}

func (e *ForwardingEndpointResourceModel) Set(data *telecastertypesv1beta1.ForwardingEndpoint) {
	e.ForwardingEndpointRefModel = ForwardingEndpointRefModel{
		Slug: types.StringValue(data.Ref.Slug),
	}

	e.Spec = &ForwardingEndpointSpecModel{
		DisplayName: types.StringValue(data.Spec.DisplayName),
	}

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
		switch auth := cfg.Kafka.Auth.(type) {
		case *telecastertypesv1beta1.KafkaConfig_Scram:
			e.Spec.Kafka.ScramAuth = &KafkaScramAuthModel{
				Secret: &SecretRefModel{
					Slug: types.StringValue(auth.Scram.Secret.Slug),
				},
				UsernameKey: types.StringValue(auth.Scram.UsernameKey),
				PasswordKey: types.StringValue(auth.Scram.PasswordKey),
			}
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
				AccessKeyIdKey:     types.StringValue(cfg.S3.Credentials.AccessKeyIdKey),
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

func (r *ForwardingEndpointRefModel) toProtoObject() *telecastertypesv1beta1.ForwardingEndpointRef {
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

func (k *KafkaScramAuthModel) toProtoObject() *telecastertypesv1beta1.KafkaConfig_Scram {
	if k == nil {
		return nil
	}

	return &telecastertypesv1beta1.KafkaConfig_Scram{
		Scram: &telecastertypesv1beta1.KafkaScramAuth{
			Secret:      k.Secret.toProtoObject(),
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

func (p *PrometheusBasicAuthModel) toProtoObject() *telecastertypesv1beta1.PrometheusBasicAuth {
	if p == nil {
		return nil
	}

	return &telecastertypesv1beta1.PrometheusBasicAuth{
		Secret:      p.Secret.toProtoObject(),
		UsernameKey: p.UsernameKey.ValueString(),
		PasswordKey: p.PasswordKey.ValueString(),
	}
}

type HTTPSBasicAuthModel struct {
	Secret      *SecretRefModel `tfsdk:"secret"`
	UsernameKey types.String    `tfsdk:"username_key"`
	PasswordKey types.String    `tfsdk:"password_key"`
}

func (h *HTTPSBasicAuthModel) toProtoObject() *telecastertypesv1beta1.HTTPSBasicAuth {
	if h == nil {
		return nil
	}

	return &telecastertypesv1beta1.HTTPSBasicAuth{
		Secret:      h.Secret.toProtoObject(),
		UsernameKey: h.UsernameKey.ValueString(),
		PasswordKey: h.PasswordKey.ValueString(),
	}
}

type S3CredentialsModel struct {
	Secret             *SecretRefModel `tfsdk:"secret"`
	AccessKeyIdKey     types.String    `tfsdk:"access_key_id_key"`
	SecretAccessKeyKey types.String    `tfsdk:"secret_access_key_key"`
}

func (s *S3CredentialsModel) toProtoObject() *telecastertypesv1beta1.S3Credentials {
	if s == nil {
		return nil
	}

	return &telecastertypesv1beta1.S3Credentials{
		Secret:             s.Secret.toProtoObject(),
		AccessKeyIdKey:     s.AccessKeyIdKey.ValueString(),
		SecretAccessKeyKey: s.SecretAccessKeyKey.ValueString(),
	}
}

func (e *ForwardingEndpointResourceModel) toProtoObject() (*telecastertypesv1beta1.ForwardingEndpoint, error) {
	endpoint := &telecastertypesv1beta1.ForwardingEndpoint{
		Ref: &telecastertypesv1beta1.ForwardingEndpointRef{
			Slug: e.Slug.String(),
		},
	}

	endpoint.Ref = &telecastertypesv1beta1.ForwardingEndpointRef{
		Slug: e.Slug.ValueString(),
	}
	spec, err := e.Spec.toProtoObject()
	if err != nil {
		return nil, fmt.Errorf("failed to convert spec to proto object: %w", err)
	}
	endpoint.Spec = spec

	return endpoint, nil
}

func (s *ForwardingEndpointSpecModel) toProtoObject() (*telecastertypesv1beta1.ForwardingEndpointSpec, error) {
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
				Tls:                s.Kafka.TLS.toProtoObject(),
				Auth:               s.Kafka.ScramAuth.toProtoObject(),
			},
		}
	}

	if s.Prometheus != nil {
		configuredImplementations = append(configuredImplementations, "prometheus")
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_Prometheus{
			Prometheus: &telecastertypesv1beta1.PrometheusRemoteWriteConfig{
				Endpoint:  s.Prometheus.Endpoint.ValueString(),
				Tls:       s.Prometheus.TLS.toProtoObject(),
				BasicAuth: s.Prometheus.BasicAuth.toProtoObject(),
			},
		}
	}

	if s.S3 != nil {
		configuredImplementations = append(configuredImplementations, "s3")
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_S3{
			S3: &telecastertypesv1beta1.S3Config{
				Uri:         s.S3.URI.ValueString(),
				Region:      s.S3.Region.ValueString(),
				Credentials: s.S3.Credentials.toProtoObject(),
			},
		}
	}

	if s.HTTPS != nil {
		configuredImplementations = append(configuredImplementations, "https")
		spec.Config = &telecastertypesv1beta1.ForwardingEndpointSpec_Https{
			Https: &telecastertypesv1beta1.HTTPSConfig{
				Endpoint:  s.HTTPS.Endpoint.ValueString(),
				Tls:       s.HTTPS.TLS.toProtoObject(),
				BasicAuth: s.HTTPS.BasicAuth.toProtoObject(),
			},
		}
	}

	if len(configuredImplementations) != 1 {
		return nil, fmt.Errorf("exactly one auth method should be set, got %d: %s", len(configuredImplementations), strings.Join(configuredImplementations, ", "))
	}

	return spec, nil
}

func (f *ForwardingEndpointResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_forwarding_endpoint"
}

func (f *ForwardingEndpointResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Telecaster forwarding endpoint",
		Attributes: map[string]schema.Attribute{
			"slug": schema.StringAttribute{
				MarkdownDescription: "The slug of the forwarding endpoint. Used as a unique identifier.",
				Required:            true,
				Computed:            false,
				Optional:            false,
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
		},
	}
}

func (f *ForwardingEndpointResource) Create(context.Context, resource.CreateRequest, *resource.CreateResponse) {
	panic("unimplemented")
}

func (f *ForwardingEndpointResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
	panic("unimplemented")
}

func (f *ForwardingEndpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ForwardingEndpointResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := f.client.TelecasterServiceClient.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
		Ref: data.ForwardingEndpointRefModel.toProtoObject(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Reading Telecaster Forwarding Endpoint",
			fmt.Sprintf("Could not read Telecaster Forwarding Endpoint %s: %v", data.Slug.ValueString(), err),
		)
		return
	}

	data.Set(getResp.Msg.Endpoint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (f *ForwardingEndpointResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
	panic("unimplemented")
}
