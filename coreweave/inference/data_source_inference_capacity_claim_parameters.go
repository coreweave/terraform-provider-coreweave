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

var _ datasource.DataSource = &CapacityClaimParametersDataSource{}

func NewCapacityClaimParametersDataSource() datasource.DataSource {
	return &CapacityClaimParametersDataSource{}
}

type CapacityClaimParametersDataSource struct {
	client *coreweave.InferenceClient
}

// CapacityClaimParametersDataSourceModel describes the data source data model.
type CapacityClaimParametersDataSourceModel struct {
	ZoneInstanceTypes types.Map `tfsdk:"zone_instance_types"`
}

var zoneInstanceTypesAttrTypes = map[string]attr.Type{
	"instance_ids": types.ListType{ElemType: types.StringType},
}

func (d *CapacityClaimParametersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inference_capacity_claim_parameters"
}

func (d *CapacityClaimParametersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieve available capacity claim parameters for CoreWeave Managed Inference.",
		Attributes: map[string]schema.Attribute{
			"zone_instance_types": schema.MapNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Available instance types per zone (keyed by zone name).",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"instance_ids": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
		},
	}
}

func (d *CapacityClaimParametersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CapacityClaimParametersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	paramsResp, err := d.client.GetCapacityClaimParameters(ctx, connect.NewRequest(&inferencev1.GetCapacityClaimParametersRequest{}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	msg := paramsResp.Msg

	var data CapacityClaimParametersDataSourceModel

	// zone_instance_types: map[string]object{instance_ids: list[string]}
	zitMap := make(map[string]attr.Value)
	for zone, instanceTypes := range msg.GetZoneInstanceTypes() {
		idVals := make([]attr.Value, 0, len(instanceTypes.GetInstanceIds()))
		for _, id := range instanceTypes.GetInstanceIds() {
			idVals = append(idVals, types.StringValue(id))
		}
		obj, diags := types.ObjectValue(zoneInstanceTypesAttrTypes, map[string]attr.Value{
			"instance_ids": types.ListValueMust(types.StringType, idVals),
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		zitMap[zone] = obj
	}
	data.ZoneInstanceTypes = types.MapValueMust(types.ObjectType{AttrTypes: zoneInstanceTypesAttrTypes}, zitMap)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
