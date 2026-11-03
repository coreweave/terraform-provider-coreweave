package inference

import (
	"context"
	"fmt"
	"maps"
	"slices"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	RuntimeEngineOptions types.Map `tfsdk:"runtime_engine_options"`
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

	runtimeVersionOptionsAttrTypes = map[string]attr.Type{
		"lifecycle":      types.StringType,
		"config_options": types.ListType{ElemType: types.StringType},
		"env_options":    types.ListType{ElemType: types.StringType},
	}

	runtimeEngineOptionsAttrTypes = map[string]attr.Type{
		"versions": types.MapType{ElemType: types.ObjectType{AttrTypes: runtimeVersionOptionsAttrTypes}},
	}
)

const deprecatedByRuntimeEngineOptions = "Use `runtime_engine_options`, which reports options and lifecycle per runtime version. This attribute will be removed in a future release."

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
				DeprecationMessage:  deprecatedByRuntimeEngineOptions,
				MarkdownDescription: "Available runtime versions per engine (keyed by engine name). Includes deprecated versions, which cannot be used for new deployments.",
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
				DeprecationMessage:  deprecatedByRuntimeEngineOptions,
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
				DeprecationMessage:  deprecatedByRuntimeEngineOptions,
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
			"runtime_engine_options": schema.MapNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Available runtime versions and their options per engine (keyed by engine name).",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"versions": schema.MapNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Options per runtime version (keyed by semver version).",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"lifecycle": schema.StringAttribute{
										Computed: true,
										MarkdownDescription: "Lifecycle state of the version: `LIFECYCLE_STABLE`, `LIFECYCLE_UNSTABLE`, or `LIFECYCLE_DEPRECATED`. " +
											"Deprecated versions remain available only to existing deployments already using them; " +
											"new deployments must use another version.",
									},
									"config_options": schema.ListAttribute{
										Computed:            true,
										ElementType:         types.StringType,
										MarkdownDescription: "Configuration keys accepted by the `engine_config` field for this version, sorted.",
									},
									"env_options": schema.ListAttribute{
										Computed:            true,
										ElementType:         types.StringType,
										MarkdownDescription: "Environment variable names accepted by the `engine_env` field for this version, sorted.",
									},
								},
							},
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
	paramsResp, err := d.client.GetDeploymentParameters(ctx, &inferencev1.GetDeploymentParametersRequest{})
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	msg := paramsResp

	var data InferenceDeploymentParametersDataSourceModel

	// gateway_ids
	gwVals := make([]attr.Value, len(msg.GetGatewayIds()))
	for i, id := range msg.GetGatewayIds() {
		gwVals[i] = types.StringValue(id)
	}
	data.GatewayIds = types.SetValueMust(types.StringType, gwVals)

	// The deprecated per-engine fields are still populated server-side during the
	// runtime_engine_options migration window, so keep surfacing them unchanged
	// rather than deriving them from the per-version options (which would change
	// their meaning from a per-engine set to a union across versions). They are
	// removed once the server stops populating them.
	rp := msg.GetRuntimeParameters()

	// runtime_versions: map[string]object{versions: list[string]}
	rvMap := make(map[string]attr.Value)
	if rp != nil {
		for engine, rv := range rp.GetRuntimeVersions() { //nolint:staticcheck // SA1019: deprecated, still populated during migration.
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
	if rp != nil {
		for engine, rco := range rp.GetRuntimeConfigOptions() { //nolint:staticcheck // SA1019: deprecated, still populated during migration.
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
	if rp != nil {
		for engine, eeo := range rp.GetEngineEnvOptions() { //nolint:staticcheck // SA1019: deprecated, still populated during migration.
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

	engineOptions, diags := runtimeEngineOptionsValue(rp.GetRuntimeEngineOptions())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.RuntimeEngineOptions = engineOptions

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

// runtimeEngineOptionsValue converts the per-engine, per-version runtime options
// into the runtime_engine_options attribute value. Option names arrive as proto
// map keys, so they are sorted to keep the lists stable across reads.
func runtimeEngineOptionsValue(
	in map[string]*inferencev1.DeploymentRuntimeParameters_RuntimeEngineOptions,
) (types.Map, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	versionObjType := types.ObjectType{AttrTypes: runtimeVersionOptionsAttrTypes}
	engineObjType := types.ObjectType{AttrTypes: runtimeEngineOptionsAttrTypes}

	engines := make(map[string]attr.Value, len(in))
	for engine, eo := range in {
		versions := make(map[string]attr.Value, len(eo.GetVersions()))
		for version, vo := range eo.GetVersions() {
			obj, diags := types.ObjectValue(runtimeVersionOptionsAttrTypes, map[string]attr.Value{
				"lifecycle":      types.StringValue(vo.GetLifecycle().String()),
				"config_options": sortedKeysList(vo.GetConfigOptions()),
				"env_options":    sortedKeysList(vo.GetEnvOptions()),
			})
			diagnostics.Append(diags...)
			if diagnostics.HasError() {
				return types.MapNull(engineObjType), diagnostics
			}
			versions[version] = obj
		}

		versionsMap, diags := types.MapValue(versionObjType, versions)
		diagnostics.Append(diags...)
		if diagnostics.HasError() {
			return types.MapNull(engineObjType), diagnostics
		}
		obj, diags := types.ObjectValue(runtimeEngineOptionsAttrTypes, map[string]attr.Value{"versions": versionsMap})
		diagnostics.Append(diags...)
		if diagnostics.HasError() {
			return types.MapNull(engineObjType), diagnostics
		}
		engines[engine] = obj
	}

	out, diags := types.MapValue(engineObjType, engines)
	diagnostics.Append(diags...)
	return out, diagnostics
}

func sortedKeysList[V any](m map[string]V) types.List {
	keys := slices.Sorted(maps.Keys(m))
	vals := make([]attr.Value, len(keys))
	for i, k := range keys {
		vals[i] = types.StringValue(k)
	}
	return types.ListValueMust(types.StringType, vals)
}
