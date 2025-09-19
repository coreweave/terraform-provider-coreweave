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

type ClusterDataSourceModel = ClusterResourceModel // aliased so that, if implementations between datasource and resource ever need to deviate, the symbols are appropriately coupled.

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
			"status": schema.StringAttribute{
				MarkdownDescription: "The status of the cluster.",
				Computed:            true,
			},
			"service_account_oidc_issuer_url": schema.StringAttribute{
				MarkdownDescription: "The URL of the OIDC issuer for the cluster's service account tokens. This value corresponds to the --service-account-issuer flag on the kube-apiserver.",
				Computed:            true,
			},
		},
	}
}

func (d *ClusterDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(cluster.Msg.Cluster)
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}

func MustRenderClusterDataSource(_ context.Context, resourceName string, cluster *ClusterDataSourceModel) string {
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
