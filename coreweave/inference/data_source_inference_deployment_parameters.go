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

var _ datasource.DataSource = &InferenceDeploymentParametersDataSource{}

func NewInferenceDeploymentParametersDataSource() datasource.DataSource {
	return &InferenceDeploymentParametersDataSource{}
}

type InferenceDeploymentParametersDataSource struct {
	client *coreweave.InferenceClient
}

// InferenceDeploymentParametersDataSourceModel describes the data source data model.
type InferenceDeploymentParametersDataSourceModel struct {
	GatewayIds           types.Set `tfsdk:"gateway_ids"`
	RuntimeVersions      types.Map `tfsdk:"runtime_versions"`
	RuntimeConfigOptions types.Map `tfsdk:"runtime_config_options"`
	EngineEnvOptions     types.Map `tfsdk:"engine_env_options"`
	InstanceTypes        types.Set `tfsdk:"instance_types"`
}

var (
	runtimeVersionsAttrTypes = map[string]attr.Type{
		"versions": types.ListType{ElemType: types.StringType},
	}

	runtimeConfigOptionsAttrTypes = map[string]attr.Type{
		"allowed_keys": types.ListType{ElemType: types.StringType},
	}

	engineEnvOptionsAttrTypes = map[string]attr.Type{
		"allowed_names": types.ListType{ElemType: types.StringType},
	}
)

func (d *InferenceDeploymentParametersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inference_deployment_parameters"
}

func (d *InferenceDeploymentParametersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieve available parameter values for [CoreWeave Managed Inference deployments](https://docs.coreweave.com/products/inference/getting-started).",
		Attributes: map[string]schema.Attribute{
			"gateway_ids": schema.SetAttribute{
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
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Available semver versions for the engine, sorted by the API.",
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
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Configuration keys accepted by the engine's `engine_config` field.",
						},
					},
				},
			},
			"engine_env_options": schema.MapNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Available engine environment variable options per engine (keyed by engine name).",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"allowed_names": schema.ListAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Environment variable names accepted by the engine's `engine_env` field.",
						},
					},
				},
			},
			"instance_types": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Available instance types for deployments.",
			},
		},
	}
}

func (d *InferenceDeploymentParametersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *InferenceDeploymentParametersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	paramsResp, err := d.client.GetDeploymentParameters(ctx, connect.NewRequest(&inferencev1.GetDeploymentParametersRequest{}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	msg := paramsResp.Msg

	var data InferenceDeploymentParametersDataSourceModel

	// gateway_ids
	gwVals := make([]attr.Value, len(msg.GetGatewayIds()))
	for i, id := range msg.GetGatewayIds() {
		gwVals[i] = types.StringValue(id)
	}
	data.GatewayIds = types.SetValueMust(types.StringType, gwVals)

	// runtime_versions: map[string]object{versions: list[string]}
	rvMap := make(map[string]attr.Value)
	if rp := msg.GetRuntimeParameters(); rp != nil {
		for engine, rv := range rp.GetRuntimeVersions() {
			versionVals := make([]attr.Value, len(rv.GetVersions()))
			for i, v := range rv.GetVersions() {
				versionVals[i] = types.StringValue(v)
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
			keyVals := make([]attr.Value, len(rco.GetAllowedKeys()))
			for i, k := range rco.GetAllowedKeys() {
				keyVals[i] = types.StringValue(k)
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

	// engine_env_options: map[string]object{allowed_names: list[string]}
	eeoMap := make(map[string]attr.Value)
	if rp := msg.GetRuntimeParameters(); rp != nil {
		for engine, eeo := range rp.GetEngineEnvOptions() {
			nameVals := make([]attr.Value, len(eeo.GetAllowedNames()))
			for i, name := range eeo.GetAllowedNames() {
				nameVals[i] = types.StringValue(name)
			}
			obj, diags := types.ObjectValue(engineEnvOptionsAttrTypes, map[string]attr.Value{
				"allowed_names": types.ListValueMust(types.StringType, nameVals),
			})
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			eeoMap[engine] = obj
		}
	}
	data.EngineEnvOptions = types.MapValueMust(types.ObjectType{AttrTypes: engineEnvOptionsAttrTypes}, eeoMap)

	// instance_types
	var instanceTypeVals []attr.Value
	if resourceParams := msg.GetResourceParameters(); resourceParams != nil {
		instanceTypes := resourceParams.GetInstanceTypes()
		instanceTypeVals = make([]attr.Value, len(instanceTypes))
		for i, it := range instanceTypes {
			instanceTypeVals[i] = types.StringValue(it)
		}
	}
	data.InstanceTypes = types.SetValueMust(types.StringType, instanceTypeVals)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
