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

var _ datasource.DataSource = &InferenceParametersDataSource{}

func NewInferenceParametersDataSource() datasource.DataSource {
	return &InferenceParametersDataSource{}
}

type InferenceParametersDataSource struct {
	client *coreweave.Client
}

// InferenceParametersDataSourceModel describes the data source data model.
type InferenceParametersDataSourceModel struct {
	GatewayIds           types.List `tfsdk:"gateway_ids"`
	RuntimeVersions      types.Map  `tfsdk:"runtime_versions"`
	RuntimeConfigOptions types.Map  `tfsdk:"runtime_config_options"`
	InstanceTypes        types.List `tfsdk:"instance_types"`
}

var (
	runtimeVersionsAttrTypes = map[string]attr.Type{
		"versions": types.ListType{ElemType: types.StringType},
	}

	runtimeConfigOptionsAttrTypes = map[string]attr.Type{
		"allowed_keys": types.ListType{ElemType: types.StringType},
	}
)

func (d *InferenceParametersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inference_parameters"
}

func (d *InferenceParametersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieve available parameter values for CoreWeave Managed Inference deployments.",
		Attributes: map[string]schema.Attribute{
			"gateway_ids": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Gateway IDs available for the current organization.",
			},
			"runtime_versions": schema.MapNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Available runtime versions per engine (keyed by engine name).",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"versions": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
			"runtime_config_options": schema.MapNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Available runtime config options per engine (keyed by engine name).",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"allowed_keys": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
			"instance_types": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Available instance types for deployments.",
			},
		},
	}
}

func (d *InferenceParametersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	d.client = client
}

func (d *InferenceParametersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	paramsResp, err := d.client.GetDeploymentParameters(ctx, connect.NewRequest(&inferencev1.GetDeploymentParametersRequest{}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	msg := paramsResp.Msg

	var data InferenceParametersDataSourceModel

	// gateway_ids
	gwVals := make([]attr.Value, 0, len(msg.GetGatewayIds()))
	for _, id := range msg.GetGatewayIds() {
		gwVals = append(gwVals, types.StringValue(id))
	}
	data.GatewayIds = types.ListValueMust(types.StringType, gwVals)

	// runtime_versions: map[string]object{versions: list[string]}
	rvMap := make(map[string]attr.Value)
	if rp := msg.GetRuntimeParameters(); rp != nil {
		for engine, rv := range rp.GetRuntimeVersions() {
			versionVals := make([]attr.Value, 0, len(rv.GetVersions()))
			for _, v := range rv.GetVersions() {
				versionVals = append(versionVals, types.StringValue(v))
			}
			obj, diags := types.ObjectValue(runtimeVersionsAttrTypes, map[string]attr.Value{
				"versions": types.ListValueMust(types.StringType, versionVals),
			})
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			rvMap[engine] = obj
		}
	}
	data.RuntimeVersions = types.MapValueMust(types.ObjectType{AttrTypes: runtimeVersionsAttrTypes}, rvMap)

	// runtime_config_options: map[string]object{allowed_keys: list[string]}
	rcoMap := make(map[string]attr.Value)
	if rp := msg.GetRuntimeParameters(); rp != nil {
		for engine, rco := range rp.GetRuntimeConfigOptions() {
			keyVals := make([]attr.Value, 0, len(rco.GetAllowedKeys()))
			for _, k := range rco.GetAllowedKeys() {
				keyVals = append(keyVals, types.StringValue(k))
			}
			obj, diags := types.ObjectValue(runtimeConfigOptionsAttrTypes, map[string]attr.Value{
				"allowed_keys": types.ListValueMust(types.StringType, keyVals),
			})
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			rcoMap[engine] = obj
		}
	}
	data.RuntimeConfigOptions = types.MapValueMust(types.ObjectType{AttrTypes: runtimeConfigOptionsAttrTypes}, rcoMap)

	// instance_types
	var instanceTypeVals []attr.Value
	if resourceParams := msg.GetResourceParameters(); resourceParams != nil {
		instanceTypes := resourceParams.GetInstanceTypes()
		instanceTypeVals = make([]attr.Value, 0, len(instanceTypes))
		for _, it := range instanceTypes {
			instanceTypeVals = append(instanceTypeVals, types.StringValue(it))
		}
	}
	data.InstanceTypes = types.ListValueMust(types.StringType, instanceTypeVals)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
