package inference

import (
	"context"
	"fmt"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &GatewayParametersDataSource{}

func NewGatewayParametersDataSource() datasource.DataSource {
	return &GatewayParametersDataSource{}
}

type GatewayParametersDataSource struct {
	client *coreweave.InferenceClient
}

// GatewayParametersDataSourceModel describes the data source data model.
type GatewayParametersDataSourceModel struct {
	Zones types.List `tfsdk:"zones"`
}

func (d *GatewayParametersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inference_gateway_parameters"
}

func (d *GatewayParametersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieve available gateway parameters for CoreWeave Managed Inference.",
		Attributes: map[string]schema.Attribute{
			"zones": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Available zones for inference gateways.",
			},
		},
	}
}

func (d *GatewayParametersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *coreweave.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	d.client = client.Inference
}

func (d *GatewayParametersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	paramsResp, err := d.client.GetGatewayParameters(ctx, connect.NewRequest(&inferencev1.GetGatewayParametersRequest{}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	msg := paramsResp.Msg

	var data GatewayParametersDataSourceModel

	// zones
	zones := msg.GetZones()
	zoneVals := make([]attr.Value, len(zones))
	for i, z := range zones {
		zoneVals[i] = types.StringValue(z)
	}
	data.Zones = types.ListValueMust(types.StringType, zoneVals)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
