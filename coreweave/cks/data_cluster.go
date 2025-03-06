package cks

import (
	"bytes"
	"context"
	"fmt"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource = &ClusterDataSource{}
)

func NewClusterDataSource() datasource.DataSource {
	return &ClusterDataSource{}
}

type ClusterDataSource struct {
	client *coreweave.Client
}

type ClusterDataSourceModel struct {
	Id                  types.String              `tfsdk:"id"`
	VpcId               types.String              `tfsdk:"vpc_id"`
	Zone                types.String              `tfsdk:"zone"`
	Name                types.String              `tfsdk:"name"`
	Version             types.String              `tfsdk:"version"`
	Public              types.Bool                `tfsdk:"public"`
	PodCidrName         types.String              `tfsdk:"pod_cidr_name"`
	ServiceCidrName     types.String              `tfsdk:"service_cidr_name"`
	InternalLBCidrNames types.Set                 `tfsdk:"internal_lb_cidr_names"`
	AuditPolicy         types.String              `tfsdk:"audit_policy"`
	Oidc                *OidcResourceModel        `tfsdk:"oidc"`
	AuthNWebhook        *AuthWebhookResourceModel `tfsdk:"authn_webhook"`
	AuthZWebhook        *AuthWebhookResourceModel `tfsdk:"authz_webhook"`
	ApiServerEndpoint   types.String              `tfsdk:"api_server_endpoint"`
}

func (d *ClusterDataSourceModel) Set(cluster *cksv1beta1.Cluster) {
	if cluster == nil {
		return
	}

	d.Id = types.StringValue(cluster.Id)
	d.VpcId = types.StringValue(cluster.VpcId)
	d.Zone = types.StringValue(cluster.Zone)
	d.Name = types.StringValue(cluster.Name)
	d.Version = types.StringValue(cluster.Version)
	d.Public = types.BoolValue(cluster.Public)

	if cluster.AuditPolicy == "" {
		d.AuditPolicy = types.StringNull()
	} else {
		d.AuditPolicy = types.StringValue(cluster.AuditPolicy)
	}

	if cluster.Network != nil {
		d.PodCidrName = types.StringValue(cluster.Network.PodCidrName)
		d.ServiceCidrName = types.StringValue(cluster.Network.ServiceCidrName)
		internalLbCidrs := []attr.Value{}
		for _, c := range cluster.Network.InternalLbCidrNames {
			internalLbCidrs = append(internalLbCidrs, types.StringValue(c))
		}
		d.InternalLBCidrNames = types.SetValueMust(types.StringType, internalLbCidrs)
	}

	if !oidcIsEmpty(cluster.Oidc) {
		oidc := OidcResourceModel{}
		oidc.Set(cluster.Oidc)
		d.Oidc = &oidc
	} else {
		d.Oidc = nil
	}

	if !authWebhookEmpty(cluster.AuthnWebhook) {
		d.AuthNWebhook = &AuthWebhookResourceModel{
			Server: types.StringValue(cluster.AuthnWebhook.Server),
			CA:     types.StringValue(cluster.AuthnWebhook.Ca),
		}
	} else {
		d.AuthNWebhook = nil
	}

	if !authWebhookEmpty(cluster.AuthzWebhook) {
		d.AuthZWebhook = &AuthWebhookResourceModel{
			Server: types.StringValue(cluster.AuthzWebhook.Server),
			CA:     types.StringValue(cluster.AuthzWebhook.Ca),
		}
	} else {
		d.AuthZWebhook = nil
	}

	d.ApiServerEndpoint = types.StringValue(cluster.ApiServerEndpoint)
}

func MustRenderClusterDataSopurce(ctx context.Context, resourceName string, cluster *ClusterDataSourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	block := body.AppendNewBlock("data", []string{"coreweave_cks_cluster", resourceName})
	// todo: we should probably delineate better between when fields are tokens, and when they are not. However, since ID is derived, it makes little sense to assume it is a known value.
	block.Body().SetAttributeRaw("id", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(cluster.Id.ValueString())}})

	return string(file.Bytes())
}

// Metadata implements datasource.DataSource.
func (d *ClusterDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cks_cluster"
}

func (d *ClusterDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "CoreWeave Kubernetes Cluster",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The ID of the cluster.",
				Required:            true,
			},
			"vpc_id": schema.StringAttribute{
				MarkdownDescription: "The VPC ID of the cluster.",
				Computed:            true,
			},
			"zone": schema.StringAttribute{
				MarkdownDescription: "The zone of the cluster.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the cluster.",
				Computed:            true,
			},
			"version": schema.StringAttribute{
				MarkdownDescription: "The version of the cluster.",
				Computed:            true,
			},
			"public": schema.BoolAttribute{
				MarkdownDescription: "Whether the cluster is public.",
				Computed:            true,
			},
			"pod_cidr_name": schema.StringAttribute{
				MarkdownDescription: "The pod CIDR name of the cluster.",
				Computed:            true,
			},
			"service_cidr_name": schema.StringAttribute{
				MarkdownDescription: "The service CIDR name of the cluster.",
				Computed:            true,
			},
			"internal_lb_cidr_names": schema.SetAttribute{
				MarkdownDescription: "The internal load balancer CIDR names of the cluster.",
				Computed:            true,
				ElementType:         types.StringType,
			},
			"audit_policy": schema.StringAttribute{
				MarkdownDescription: "The audit policy of the cluster.",
				Computed:            true,
			},
			"oidc": schema.SingleNestedAttribute{
				MarkdownDescription: "The OIDC configuration of the cluster.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"issuer_url": schema.StringAttribute{
						MarkdownDescription: "The issuer URL of the OIDC configuration.",
						Computed:            true,
					},
					"client_id": schema.StringAttribute{
						MarkdownDescription: "The client ID of the OIDC configuration.",
						Computed:            true,
					},
				},
			},
			"authn_webhook": schema.SingleNestedAttribute{
				MarkdownDescription: "The authentication webhook configuration of the cluster.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"server": schema.StringAttribute{
						MarkdownDescription: "The server URL of the authentication webhook.",
						Computed:            true,
					},
					"ca": schema.StringAttribute{
						MarkdownDescription: "The CA certificate of the authentication webhook.",
						Computed:            true,
					},
				},
			},
			"authz_webhook": schema.SingleNestedAttribute{
				MarkdownDescription: "The authorization webhook configuration of the cluster.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"server": schema.StringAttribute{
						MarkdownDescription: "The server URL of the authorization webhook.",
						Computed:            true,
					},
					"ca": schema.StringAttribute{
						MarkdownDescription: "The CA certificate of the authorization webhook.",
						Computed:            true,
					},
				},
			},
			"api_server_endpoint": schema.StringAttribute{
				MarkdownDescription: "The API server endpoint of the cluster.",
				Computed:            true,
			},
		},
	}
}

func (d *ClusterDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *coreweave.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	d.client = client
}

func (d *ClusterDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	data := new(ClusterDataSourceModel)
	resp.Diagnostics.Append(req.Config.Get(ctx, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cluster, err := d.client.GetCluster(ctx, connect.NewRequest(&cksv1beta1.GetClusterRequest{
		Id: data.Id.ValueString(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(cluster.Msg.Cluster)
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}

func MustRenderClusterDataSource(ctx context.Context, resourceName string, cluster *ClusterDataSourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("data", []string{"coreweave_cks_cluster", resourceName})
	resourceBody := resource.Body()

	resourceBody.SetAttributeRaw("id", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(cluster.Id.ValueString())}})

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}
