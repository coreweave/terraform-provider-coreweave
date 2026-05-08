package sandbox

import (
	"context"
	"fmt"

	sandboxv1beta2 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1beta2"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ManagedRunnerDataSource{}

func NewManagedRunnerDataSource() datasource.DataSource {
	return &ManagedRunnerDataSource{}
}

type ManagedRunnerDataSource struct {
	client *coreweave.Client
}

type ManagedRunnerDataSourceModel = ManagedRunnerResourceModel

func (d *ManagedRunnerDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sandbox_managed_runner"
}

func (d *ManagedRunnerDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read a CoreWeave Sandbox managed runner by ID or operator-assigned `runner_id`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Server-assigned UUID, or the operator-assigned `runner_id`. UUID lookup is preferred and falls back to `runner_id` on no-match.",
			},
			"runner_id":       schema.StringAttribute{Computed: true, MarkdownDescription: "Operator-assigned runner identifier."},
			"display_name":    schema.StringAttribute{Computed: true, MarkdownDescription: "Human-readable runner name."},
			"zone":            schema.StringAttribute{Computed: true, MarkdownDescription: "Geographic zone."},
			"cluster_id":      schema.StringAttribute{Computed: true, MarkdownDescription: "CKS cluster UUID."},
			"cluster_name":    schema.StringAttribute{Computed: true, MarkdownDescription: "CKS cluster display name."},
			"runner_group_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Runner group ID for scheduling affinity."},
			"management_mode": schema.StringAttribute{Computed: true, MarkdownDescription: "Management mode (`MANAGEMENT_MODE_MANAGED` or `MANAGEMENT_MODE_SELF_MANAGED`)."},
			"release_channel": schema.StringAttribute{Computed: true, MarkdownDescription: "Release channel for automatic updates."},
			"maintenance_policy": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"windows": schema.ListNestedAttribute{
						Computed: true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"cron":             schema.StringAttribute{Computed: true},
								"duration_seconds": schema.Int32Attribute{Computed: true},
							},
						},
					},
					"exclusions": schema.ListNestedAttribute{
						Computed: true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"start_time": schema.StringAttribute{Computed: true},
								"end_time":   schema.StringAttribute{Computed: true},
								"reason":     schema.StringAttribute{Computed: true},
							},
						},
					},
				},
			},
			"overrides": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"node_selector": schema.MapAttribute{Computed: true, ElementType: types.StringType},
					"tolerations": schema.ListNestedAttribute{
						Computed: true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"key":      schema.StringAttribute{Computed: true},
								"operator": schema.StringAttribute{Computed: true},
								"value":    schema.StringAttribute{Computed: true},
								"effect":   schema.StringAttribute{Computed: true},
							},
						},
					},
					"resources": schema.SingleNestedAttribute{
						Computed: true,
						Attributes: map[string]schema.Attribute{
							"cpu_request":    schema.StringAttribute{Computed: true},
							"memory_request": schema.StringAttribute{Computed: true},
							"cpu_limit":      schema.StringAttribute{Computed: true},
							"memory_limit":   schema.StringAttribute{Computed: true},
						},
					},
					"annotations": schema.MapAttribute{Computed: true, ElementType: types.StringType},
					"labels":      schema.MapAttribute{Computed: true, ElementType: types.StringType},
					"env":         schema.MapAttribute{Computed: true, ElementType: types.StringType},
					"args":        schema.ListAttribute{Computed: true, ElementType: types.StringType},
					"scaling": schema.SingleNestedAttribute{
						Computed: true,
						Attributes: map[string]schema.Attribute{
							"replicas":            schema.Int32Attribute{Computed: true},
							"autoscaling_enabled": schema.BoolAttribute{Computed: true},
							"min_replicas":        schema.Int32Attribute{Computed: true},
							"max_replicas":        schema.Int32Attribute{Computed: true},
						},
					},
					"cpu_runtime_class": schema.StringAttribute{Computed: true},
					"gpu_runtime_class": schema.StringAttribute{Computed: true},
				},
			},
			"allow_privileged_profile_annotations": schema.BoolAttribute{Computed: true},
			"enforce_resource_limits":              schema.BoolAttribute{Computed: true},
			"profile_bindings": schema.SetNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"profile_template_id": schema.StringAttribute{Computed: true},
						"profile_name":        schema.StringAttribute{Computed: true},
						"is_default":          schema.BoolAttribute{Computed: true},
						"overrides_json":      schema.StringAttribute{Computed: true},
					},
				},
			},
			"install_status": schema.StringAttribute{Computed: true},
			"install_error": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"reason":            schema.StringAttribute{Computed: true},
					"message":           schema.StringAttribute{Computed: true},
					"diagnostic_detail": schema.StringAttribute{Computed: true},
					"remediation_hints": schema.ListAttribute{Computed: true, ElementType: types.StringType},
					"occurred_at":       schema.StringAttribute{Computed: true},
				},
			},
			"connection_status":           schema.StringAttribute{Computed: true},
			"active_config_version":       schema.StringAttribute{Computed: true},
			"active_profile_spec_version": schema.StringAttribute{Computed: true},
			"active_revision":             schema.Int32Attribute{Computed: true},
			"target_revision":             schema.Int32Attribute{Computed: true},
			"update_available":            schema.BoolAttribute{Computed: true},
			"rollout_in_progress":         schema.BoolAttribute{Computed: true},
			"deployment_spec": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"image":         schema.StringAttribute{Computed: true},
					"env":           schema.MapAttribute{Computed: true, ElementType: types.StringType},
					"args":          schema.ListAttribute{Computed: true, ElementType: types.StringType},
					"resources": schema.SingleNestedAttribute{
						Computed: true,
						Attributes: map[string]schema.Attribute{
							"cpu_request":    schema.StringAttribute{Computed: true},
							"memory_request": schema.StringAttribute{Computed: true},
							"cpu_limit":      schema.StringAttribute{Computed: true},
							"memory_limit":   schema.StringAttribute{Computed: true},
						},
					},
					"node_selector": schema.MapAttribute{Computed: true, ElementType: types.StringType},
					"tolerations": schema.ListNestedAttribute{
						Computed: true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"key":      schema.StringAttribute{Computed: true},
								"operator": schema.StringAttribute{Computed: true},
								"value":    schema.StringAttribute{Computed: true},
								"effect":   schema.StringAttribute{Computed: true},
							},
						},
					},
					"scaling": schema.SingleNestedAttribute{
						Computed: true,
						Attributes: map[string]schema.Attribute{
							"replicas":            schema.Int32Attribute{Computed: true},
							"autoscaling_enabled": schema.BoolAttribute{Computed: true},
							"min_replicas":        schema.Int32Attribute{Computed: true},
							"max_replicas":        schema.Int32Attribute{Computed: true},
						},
					},
					"annotations":       schema.MapAttribute{Computed: true, ElementType: types.StringType},
					"labels":            schema.MapAttribute{Computed: true, ElementType: types.StringType},
					"service_account":   schema.StringAttribute{Computed: true},
					"image_pull_policy": schema.StringAttribute{Computed: true},
					"gateway_server":    schema.StringAttribute{Computed: true},
					"cpu_runtime_class": schema.StringAttribute{Computed: true},
					"gpu_runtime_class": schema.StringAttribute{Computed: true},
					"init_resources": schema.SingleNestedAttribute{
						Computed: true,
						Attributes: map[string]schema.Attribute{
							"cpu_request":    schema.StringAttribute{Computed: true},
							"memory_request": schema.StringAttribute{Computed: true},
							"cpu_limit":      schema.StringAttribute{Computed: true},
							"memory_limit":   schema.StringAttribute{Computed: true},
						},
					},
				},
			},
			"created_at":        schema.StringAttribute{Computed: true},
			"updated_at":        schema.StringAttribute{Computed: true},
			"last_heartbeat_at": schema.StringAttribute{Computed: true},
		},
	}
}

func (d *ManagedRunnerDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ManagedRunnerDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	data := new(ManagedRunnerDataSourceModel)
	resp.Diagnostics.Append(req.Config.Get(ctx, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := d.client.GetRunner(ctx, connect.NewRequest(&sandboxv1beta2.GetRunnerRequest{
		Id: data.ID.ValueString(),
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, getResp.Msg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
