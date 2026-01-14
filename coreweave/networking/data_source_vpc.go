package networking

import (
	"bytes"
	"context"
	"fmt"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var (
	_ datasource.DataSource = &VpcDataSource{}
)

func NewVpcDataSource() datasource.DataSource {
	return &VpcDataSource{}
}

type VpcDataSource struct {
	client *coreweave.Client
}

type VpcDataSourceModel = VpcResourceModel

func (d *VpcDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_networking_vpc"
}

func (d *VpcDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Query information about an existing VPC by ID. See the [CoreWeave VPC API reference](https://docs.coreweave.com/docs/products/networking/vpc/vpc-api).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The ID of the VPC.",
				Required:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the VPC.",
				Computed:            true,
			},
			"zone": schema.StringAttribute{
				MarkdownDescription: "The Availability Zone in which the VPC is located.",
				Computed:            true,
			},
			"vpc_prefixes": schema.ListNestedAttribute{
				MarkdownDescription: "A list of additional named IPv4 prefixes for the VPC.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed: true,
						},
						"value": schema.StringAttribute{
							Computed: true,
						},
					},
				},
			},
			"host_prefix": schema.StringAttribute{
				MarkdownDescription: "An IPv4 CIDR range used to allocate host addresses when booting compute into a VPC.",
				DeprecationMessage:  "Configure host_prefixes instead.",
				Computed:            true,
			},
			"host_prefixes": schema.ListNestedAttribute{
				MarkdownDescription: "",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{},
						"type": schema.StringAttribute{},
						"prefixes": schema.ListAttribute{
							ElementType: basetypes.StringType{},
						},
						"ipam": schema.SingleNestedAttribute{
							Attributes: map[string]schema.Attribute{
								"prefix_length":          schema.Int32Attribute{},
								"gateway_address_policy": schema.StringAttribute{},
							},
						},
					},
				},
			},
			"ingress": schema.SingleNestedAttribute{
				MarkdownDescription: "Settings affecting traffic entering the VPC.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"disable_public_services": schema.BoolAttribute{
						MarkdownDescription: "True if the VPC will prevent public prefixes advertised from Nodes from being imported into public-facing networks, making them inaccessible from the Internet. False otherwise.",
						Computed:            true,
					},
				},
			},
			"egress": schema.SingleNestedAttribute{
				MarkdownDescription: "Settings affecting traffic leaving the VPC.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"disable_public_access": schema.BoolAttribute{
						MarkdownDescription: "True if the VPC is blocked from consuming public Internet. False otherwise.",
						Computed:            true,
					},
				},
			},
			"dhcp": schema.SingleNestedAttribute{
				MarkdownDescription: "Settings affecting DHCP behavior within the VPC.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"dns": schema.SingleNestedAttribute{
						MarkdownDescription: "Settings affecting DNS for DHCP within the VPC",
						Computed:            true,
						Attributes: map[string]schema.Attribute{
							"servers": schema.SetAttribute{
								Optional:            true,
								MarkdownDescription: "The DNS servers advertised to DHCP clients within the VPC.",
								ElementType:         types.StringType,
							},
						},
					},
				},
			},
		},
	}
}

func (d *VpcDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *VpcDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	data := new(VpcDataSourceModel)
	resp.Diagnostics.Append(req.Config.Get(ctx, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cluster, err := d.client.GetVPC(ctx, connect.NewRequest(&networkingv1beta1.GetVPCRequest{
		Id: data.Id.ValueString(),
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	data.Set(cluster.Msg.Vpc)
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}

func MustRenderVpcDataSource(_ context.Context, resourceName string, cluster *VpcDataSourceModel) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("data", []string{"coreweave_networking_vpc", resourceName})
	resourceBody := resource.Body()

	resourceBody.SetAttributeRaw("id", hclwrite.Tokens{{Type: hclsyntax.TokenIdent, Bytes: []byte(cluster.Id.ValueString())}})

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(err)
	}
	return buf.String()
}
