package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	sandboxv1beta2 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1beta2"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

var (
	_ resource.Resource                     = &ManagedRunnerResource{}
	_ resource.ResourceWithImportState      = &ManagedRunnerResource{}
	_ resource.ResourceWithConfigure        = &ManagedRunnerResource{}
	_ resource.ResourceWithConfigValidators = &ManagedRunnerResource{}

	errRunnerInstallFailed = errors.New("runner installation failed")
)

func NewManagedRunnerResource() resource.Resource {
	return &ManagedRunnerResource{}
}

type ManagedRunnerResource struct {
	client *coreweave.Client
}

// ----------------------------------------------------------------------------
// Sub-models
// ----------------------------------------------------------------------------

type MaintenanceWindowModel struct {
	Cron            types.String `tfsdk:"cron"`
	DurationSeconds types.Int32  `tfsdk:"duration_seconds"`
}

type MaintenanceExclusionModel struct {
	StartTime types.String `tfsdk:"start_time"`
	EndTime   types.String `tfsdk:"end_time"`
	Reason    types.String `tfsdk:"reason"`
}

type MaintenancePolicyModel struct {
	Windows    []MaintenanceWindowModel    `tfsdk:"windows"`
	Exclusions []MaintenanceExclusionModel `tfsdk:"exclusions"`
}

type RunnerResourceRequirementsModel struct {
	CPURequest    types.String `tfsdk:"cpu_request"`
	MemoryRequest types.String `tfsdk:"memory_request"`
	CPULimit      types.String `tfsdk:"cpu_limit"`
	MemoryLimit   types.String `tfsdk:"memory_limit"`
}

type RunnerScalingConfigModel struct {
	Replicas           types.Int32 `tfsdk:"replicas"`
	AutoscalingEnabled types.Bool  `tfsdk:"autoscaling_enabled"`
	MinReplicas        types.Int32 `tfsdk:"min_replicas"`
	MaxReplicas        types.Int32 `tfsdk:"max_replicas"`
}

type RunnerTolerationModel struct {
	Key      types.String `tfsdk:"key"`
	Operator types.String `tfsdk:"operator"`
	Value    types.String `tfsdk:"value"`
	Effect   types.String `tfsdk:"effect"`
}

type RunnerOverridesModel struct {
	NodeSelector    types.Map                        `tfsdk:"node_selector"`
	Tolerations     []RunnerTolerationModel          `tfsdk:"tolerations"`
	Resources       *RunnerResourceRequirementsModel `tfsdk:"resources"`
	Annotations     types.Map                        `tfsdk:"annotations"`
	Labels          types.Map                        `tfsdk:"labels"`
	Env             types.Map                        `tfsdk:"env"`
	Args            types.List                       `tfsdk:"args"`
	Scaling         *RunnerScalingConfigModel        `tfsdk:"scaling"`
	CPURuntimeClass types.String                     `tfsdk:"cpu_runtime_class"`
	GPURuntimeClass types.String                     `tfsdk:"gpu_runtime_class"`
}

type ProfileBindingModel struct {
	ProfileTemplateID types.String `tfsdk:"profile_template_id"`
	ProfileName       types.String `tfsdk:"profile_name"`
	IsDefault         types.Bool   `tfsdk:"is_default"`
	OverridesJSON     types.String `tfsdk:"overrides_json"`
}

type RunnerInstallErrorModel struct {
	Reason           types.String `tfsdk:"reason"`
	Message          types.String `tfsdk:"message"`
	DiagnosticDetail types.String `tfsdk:"diagnostic_detail"`
	RemediationHints types.List   `tfsdk:"remediation_hints"`
	OccurredAt       types.String `tfsdk:"occurred_at"`
}

// RunnerDeploymentSpecModel is the resolved server-side deployment spec, all Computed.
type RunnerDeploymentSpecModel struct {
	Image            types.String                     `tfsdk:"image"`
	Env              types.Map                        `tfsdk:"env"`
	Args             types.List                       `tfsdk:"args"`
	Resources        *RunnerResourceRequirementsModel `tfsdk:"resources"`
	NodeSelector     types.Map                        `tfsdk:"node_selector"`
	Tolerations      []RunnerTolerationModel          `tfsdk:"tolerations"`
	Scaling          *RunnerScalingConfigModel        `tfsdk:"scaling"`
	Annotations      types.Map                        `tfsdk:"annotations"`
	Labels           types.Map                        `tfsdk:"labels"`
	ServiceAccount   types.String                     `tfsdk:"service_account"`
	ImagePullPolicy  types.String                     `tfsdk:"image_pull_policy"`
	GatewayServer    types.String                     `tfsdk:"gateway_server"`
	CPURuntimeClass  types.String                     `tfsdk:"cpu_runtime_class"`
	GPURuntimeClass  types.String                     `tfsdk:"gpu_runtime_class"`
	InitResources    *RunnerResourceRequirementsModel `tfsdk:"init_resources"`
}

// ----------------------------------------------------------------------------
// Top-level resource model
// ----------------------------------------------------------------------------

type ManagedRunnerResourceModel struct {
	ID             types.String `tfsdk:"id"`
	RunnerID       types.String `tfsdk:"runner_id"`
	DisplayName    types.String `tfsdk:"display_name"`
	Zone           types.String `tfsdk:"zone"`
	ClusterID      types.String `tfsdk:"cluster_id"`
	ClusterName    types.String `tfsdk:"cluster_name"`
	RunnerGroupID  types.String `tfsdk:"runner_group_id"`
	ManagementMode types.String `tfsdk:"management_mode"`

	// Flattened ManagedRunnerSpec fields.
	ReleaseChannel                    types.String            `tfsdk:"release_channel"`
	MaintenancePolicy                 *MaintenancePolicyModel `tfsdk:"maintenance_policy"`
	Overrides                         *RunnerOverridesModel   `tfsdk:"overrides"`
	AllowPrivilegedProfileAnnotations types.Bool              `tfsdk:"allow_privileged_profile_annotations"`
	EnforceResourceLimits             types.Bool              `tfsdk:"enforce_resource_limits"`

	ProfileBindings []ProfileBindingModel `tfsdk:"profile_bindings"`

	// Computed status / output fields.
	InstallStatus            types.String               `tfsdk:"install_status"`
	InstallError             *RunnerInstallErrorModel   `tfsdk:"install_error"`
	ConnectionStatus         types.String               `tfsdk:"connection_status"`
	ActiveConfigVersion      types.String               `tfsdk:"active_config_version"`
	ActiveProfileSpecVersion types.String               `tfsdk:"active_profile_spec_version"`
	ActiveRevision           types.Int32                `tfsdk:"active_revision"`
	TargetRevision           types.Int32                `tfsdk:"target_revision"`
	UpdateAvailable          types.Bool                 `tfsdk:"update_available"`
	RolloutInProgress        types.Bool                 `tfsdk:"rollout_in_progress"`
	DeploymentSpec           *RunnerDeploymentSpecModel `tfsdk:"deployment_spec"`
	CreatedAt                types.String               `tfsdk:"created_at"`
	UpdatedAt                types.String               `tfsdk:"updated_at"`
	LastHeartbeatAt          types.String               `tfsdk:"last_heartbeat_at"`
}

// ----------------------------------------------------------------------------
// Schema
// ----------------------------------------------------------------------------

func (r *ManagedRunnerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sandbox_managed_runner"
}

func (r *ManagedRunnerResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(
			path.MatchRoot("cluster_id"),
			path.MatchRoot("cluster_name"),
		),
	}
}

func (r *ManagedRunnerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage a CoreWeave Sandbox platform-managed runner. " +
			"A managed runner installs the sandbox runtime onto a CKS cluster and binds one or more profile templates that sandboxes can launch against.\n\n" +
			"Concurrent updates to the same runner are not safe — serialize mutations client-side if multiple operators may apply Terraform at the same time.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID for the runner.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"runner_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Operator-assigned runner identifier (unique within the organization). Immutable after create.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"display_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Human-readable name for the runner.",
			},
			"zone": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Geographic zone (e.g., `US-EAST-04A`).",
			},
			"cluster_id": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "CKS cluster UUID the runner installs into. Exactly one of `cluster_id` or `cluster_name` must be set; the other is resolved on read. Immutable after create.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"cluster_name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "CKS cluster display name the runner installs into. Exactly one of `cluster_id` or `cluster_name` must be set; the other is resolved on read. Immutable after create.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"runner_group_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Runner group ID for scheduling affinity.",
			},
			"management_mode": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Always `MANAGEMENT_MODE_MANAGED` for this resource.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"release_channel": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Release channel for automatic updates. One of `RELEASE_CHANNEL_STABLE`, `RELEASE_CHANNEL_RAPID`. Defaults to `RELEASE_CHANNEL_STABLE`.",
				Validators: []validator.String{
					stringvalidator.OneOf(
						sandboxv1beta2.ReleaseChannel_RELEASE_CHANNEL_STABLE.String(),
						sandboxv1beta2.ReleaseChannel_RELEASE_CHANNEL_RAPID.String(),
						sandboxv1beta2.ReleaseChannel_RELEASE_CHANNEL_UNSPECIFIED.String(),
					),
				},
			},
			"maintenance_policy": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Maintenance windows and exclusion periods controlling when updates may be applied.",
				Attributes: map[string]schema.Attribute{
					"windows": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Recurring time windows during which updates are allowed.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"cron": schema.StringAttribute{
									Required:            true,
									MarkdownDescription: "Cron expression for the window start (e.g., `0 2 * * SAT`).",
									Validators: []validator.String{
										stringvalidator.LengthAtLeast(1),
									},
								},
								"duration_seconds": schema.Int32Attribute{
									Required:            true,
									MarkdownDescription: "Duration of the window in seconds.",
								},
							},
						},
					},
					"exclusions": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "One-time exclusion periods during which no updates may be applied.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"start_time": schema.StringAttribute{Required: true, MarkdownDescription: "RFC3339 start of the exclusion."},
								"end_time":   schema.StringAttribute{Required: true, MarkdownDescription: "RFC3339 end of the exclusion."},
								"reason":     schema.StringAttribute{Optional: true, MarkdownDescription: "Human-readable reason."},
							},
						},
					},
				},
			},
			"overrides": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Customer-settable overrides for the runner deployment. All fields are optional; unset fields fall back to platform defaults.",
				Attributes: map[string]schema.Attribute{
					"node_selector": schema.MapAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Node selector labels for the runner pods.",
					},
					"tolerations": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Tolerations applied to the runner pods.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"key":      schema.StringAttribute{Optional: true},
								"operator": schema.StringAttribute{Optional: true},
								"value":    schema.StringAttribute{Optional: true},
								"effect":   schema.StringAttribute{Optional: true},
							},
						},
					},
					"resources": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Resource requirements override for the runner pod.",
						Attributes:          resourceRequirementsAttributes(),
					},
					"annotations":       schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Additional annotations on runner pods."},
					"labels":            schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Additional labels on runner pods."},
					"env":               schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Additional environment variables."},
					"args":              schema.ListAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Additional command-line arguments."},
					"scaling": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Scaling configuration override.",
						Attributes:          scalingConfigAttributes(),
					},
					"cpu_runtime_class": schema.StringAttribute{Optional: true, MarkdownDescription: "Runtime class for CPU-only sandbox pods."},
					"gpu_runtime_class": schema.StringAttribute{Optional: true, MarkdownDescription: "Runtime class for GPU sandbox pods."},
				},
			},
			"allow_privileged_profile_annotations": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "When true, profile templates bound to this runner may carry pod annotations from prefix categories the platform otherwise restricts. Contact CoreWeave support before enabling. Defaults to `false`.",
			},
			"enforce_resource_limits": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "When true, sandboxes and bound profile templates must declare both memory and CPU limits. Defaults to `false`.",
			},
			"profile_bindings": schema.SetNestedAttribute{
				Required:            true,
				MarkdownDescription: "Profile templates attached to this runner. Must contain at least one entry, and exactly one entry must have `is_default = true`. Bindings are matched by `profile_template_id` on update; the desired list fully replaces the current list within one transaction.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"profile_template_id": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "ID of the profile template to attach.",
						},
						"profile_name": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Profile name on this runner. Overrides the template's name when set.",
						},
						"is_default": schema.BoolAttribute{
							Optional:            true,
							Computed:            true,
							Default:             booldefault.StaticBool(false),
							MarkdownDescription: "Whether this is the default profile for the runner. Exactly one binding must set this to `true`.",
						},
						"overrides_json": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Per-binding overrides as a JSON-encoded `ProfileSpec` fragment. Must be canonical JSON; use `jsonencode({...})` to construct.",
						},
					},
				},
			},

			// Computed status / output fields.
			"install_status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current installation lifecycle status.",
			},
			"install_error": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Populated when `install_status == RUNNER_INSTALL_STATUS_FAILED`. Cleared when status transitions back to provisioning or ready.",
				Attributes: map[string]schema.Attribute{
					"reason":            schema.StringAttribute{Computed: true},
					"message":           schema.StringAttribute{Computed: true},
					"diagnostic_detail": schema.StringAttribute{Computed: true},
					"remediation_hints": schema.ListAttribute{Computed: true, ElementType: types.StringType},
					"occurred_at":       schema.StringAttribute{Computed: true},
				},
			},
			"connection_status":           schema.StringAttribute{Computed: true, MarkdownDescription: "Live connection status to Gateway."},
			"active_config_version":       schema.StringAttribute{Computed: true, MarkdownDescription: "Active configuration version running on the runner."},
			"active_profile_spec_version": schema.StringAttribute{Computed: true, MarkdownDescription: "Active profile spec version."},
			"active_revision":             schema.Int32Attribute{Computed: true, MarkdownDescription: "Active revision number currently running."},
			"target_revision":             schema.Int32Attribute{Computed: true, MarkdownDescription: "Target revision number that should be rolled out."},
			"update_available":            schema.BoolAttribute{Computed: true, MarkdownDescription: "True when a newer version is available on the runner's release channel."},
			"rollout_in_progress":         schema.BoolAttribute{Computed: true, MarkdownDescription: "True when the runner is mid-rollout (`target_revision > active_revision`)."},
			"deployment_spec": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved server-side deployment spec for the runner, combining version defaults, org-level policies, and customer overrides.",
				Attributes:          deploymentSpecComputedAttributes(),
			},
			"created_at":        schema.StringAttribute{Computed: true},
			"updated_at":        schema.StringAttribute{Computed: true},
			"last_heartbeat_at": schema.StringAttribute{Computed: true},
		},
	}
}

func resourceRequirementsAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"cpu_request":    schema.StringAttribute{Optional: true, MarkdownDescription: "CPU request (e.g., `500m`)."},
		"memory_request": schema.StringAttribute{Optional: true, MarkdownDescription: "Memory request (e.g., `1Gi`)."},
		"cpu_limit":      schema.StringAttribute{Optional: true, MarkdownDescription: "CPU limit (e.g., `2`)."},
		"memory_limit":   schema.StringAttribute{Optional: true, MarkdownDescription: "Memory limit (e.g., `4Gi`)."},
	}
}

func scalingConfigAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"replicas":            schema.Int32Attribute{Optional: true, MarkdownDescription: "Number of replicas (when autoscaling is disabled)."},
		"autoscaling_enabled": schema.BoolAttribute{Optional: true, MarkdownDescription: "Whether autoscaling is enabled."},
		"min_replicas":        schema.Int32Attribute{Optional: true, MarkdownDescription: "Minimum replicas when autoscaling."},
		"max_replicas":        schema.Int32Attribute{Optional: true, MarkdownDescription: "Maximum replicas when autoscaling."},
	}
}

func deploymentSpecComputedAttributes() map[string]schema.Attribute {
	computedReq := func() map[string]schema.Attribute {
		return map[string]schema.Attribute{
			"cpu_request":    schema.StringAttribute{Computed: true},
			"memory_request": schema.StringAttribute{Computed: true},
			"cpu_limit":      schema.StringAttribute{Computed: true},
			"memory_limit":   schema.StringAttribute{Computed: true},
		}
	}
	return map[string]schema.Attribute{
		"image":         schema.StringAttribute{Computed: true},
		"env":           schema.MapAttribute{Computed: true, ElementType: types.StringType},
		"args":          schema.ListAttribute{Computed: true, ElementType: types.StringType},
		"resources":     schema.SingleNestedAttribute{Computed: true, Attributes: computedReq()},
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
		"init_resources":    schema.SingleNestedAttribute{Computed: true, Attributes: computedReq()},
	}
}

// Compile-time assertion that attr is referenced — keeps the import alive in case
// future schema edits drop direct usage. Cheap and self-documenting.
var _ = attr.Value(types.StringValue(""))

// ----------------------------------------------------------------------------
// Configure
// ----------------------------------------------------------------------------

func (r *ManagedRunnerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
	r.client = client
}

// ----------------------------------------------------------------------------
// Proto <-> model conversion
// ----------------------------------------------------------------------------

func (m *ManagedRunnerResourceModel) toRunner(ctx context.Context) (*sandboxv1beta2.Runner, diag.Diagnostics) {
	var diags diag.Diagnostics

	identity := &sandboxv1beta2.RunnerIdentity{
		RunnerId:      m.RunnerID.ValueString(),
		Zone:          m.Zone.ValueString(),
		ClusterId:     m.ClusterID.ValueString(),
		ClusterName:   m.ClusterName.ValueString(),
		RunnerGroupId: m.RunnerGroupID.ValueString(),
	}

	managedSpec, d := m.toManagedSpec(ctx)
	diags.Append(d...)

	bindings, d := m.toBindings()
	diags.Append(d...)

	if diags.HasError() {
		return nil, diags
	}

	return &sandboxv1beta2.Runner{
		DisplayName:     m.DisplayName.ValueString(),
		Identity:        identity,
		ManagedSpec:     managedSpec,
		ProfileBindings: bindings,
	}, nil
}

func (m *ManagedRunnerResourceModel) toManagedSpec(ctx context.Context) (*sandboxv1beta2.ManagedRunnerSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	rc := sandboxv1beta2.ReleaseChannel_RELEASE_CHANNEL_UNSPECIFIED
	if !m.ReleaseChannel.IsNull() && !m.ReleaseChannel.IsUnknown() {
		if v, ok := sandboxv1beta2.ReleaseChannel_value[m.ReleaseChannel.ValueString()]; ok {
			rc = sandboxv1beta2.ReleaseChannel(v)
		} else {
			diags.AddAttributeError(path.Root("release_channel"), "Invalid release_channel", fmt.Sprintf("unknown release channel %q", m.ReleaseChannel.ValueString()))
		}
	}

	mp, d := m.MaintenancePolicy.toProto()
	diags.Append(d...)

	overrides, d := m.Overrides.toProto(ctx)
	diags.Append(d...)

	if diags.HasError() {
		return nil, diags
	}

	return &sandboxv1beta2.ManagedRunnerSpec{
		ReleaseChannel:                    rc,
		MaintenancePolicy:                 mp,
		Overrides:                         overrides,
		AllowPrivilegedProfileAnnotations: m.AllowPrivilegedProfileAnnotations.ValueBool(),
		EnforceResourceLimits:             m.EnforceResourceLimits.ValueBool(),
	}, nil
}

func (m *ManagedRunnerResourceModel) toBindings() ([]*sandboxv1beta2.RunnerProfileBinding, diag.Diagnostics) {
	out := make([]*sandboxv1beta2.RunnerProfileBinding, 0, len(m.ProfileBindings))
	for _, b := range m.ProfileBindings {
		out = append(out, &sandboxv1beta2.RunnerProfileBinding{
			ProfileTemplateId: b.ProfileTemplateID.ValueString(),
			ProfileName:       b.ProfileName.ValueString(),
			IsDefault:         b.IsDefault.ValueBool(),
			OverridesJson:     b.OverridesJSON.ValueString(),
		})
	}
	return out, nil
}

func (mp *MaintenancePolicyModel) toProto() (*sandboxv1beta2.MaintenancePolicy, diag.Diagnostics) {
	if mp == nil {
		return nil, nil
	}
	var diags diag.Diagnostics

	out := &sandboxv1beta2.MaintenancePolicy{}
	for _, w := range mp.Windows {
		out.Windows = append(out.Windows, &sandboxv1beta2.MaintenanceWindow{
			Cron:            w.Cron.ValueString(),
			DurationSeconds: w.DurationSeconds.ValueInt32(),
		})
	}
	for _, e := range mp.Exclusions {
		excl := &sandboxv1beta2.MaintenanceExclusion{
			Reason: e.Reason.ValueString(),
		}
		if !e.StartTime.IsNull() && e.StartTime.ValueString() != "" {
			t, err := time.Parse(time.RFC3339Nano, e.StartTime.ValueString())
			if err != nil {
				diags.AddError("Invalid maintenance exclusion start_time", err.Error())
				continue
			}
			excl.StartTime = timestampFromTime(t)
		}
		if !e.EndTime.IsNull() && e.EndTime.ValueString() != "" {
			t, err := time.Parse(time.RFC3339Nano, e.EndTime.ValueString())
			if err != nil {
				diags.AddError("Invalid maintenance exclusion end_time", err.Error())
				continue
			}
			excl.EndTime = timestampFromTime(t)
		}
		out.Exclusions = append(out.Exclusions, excl)
	}
	if diags.HasError() {
		return nil, diags
	}
	return out, diags
}

func (o *RunnerOverridesModel) toProto(ctx context.Context) (*sandboxv1beta2.RunnerDeploymentOverrides, diag.Diagnostics) {
	if o == nil {
		return nil, nil
	}
	var diags diag.Diagnostics

	nodeSelector, d := stringMapToMap(ctx, o.NodeSelector)
	diags.Append(d...)
	annotations, d := stringMapToMap(ctx, o.Annotations)
	diags.Append(d...)
	labels, d := stringMapToMap(ctx, o.Labels)
	diags.Append(d...)
	env, d := stringMapToMap(ctx, o.Env)
	diags.Append(d...)
	args, d := stringListToSlice(ctx, o.Args)
	diags.Append(d...)

	if diags.HasError() {
		return nil, diags
	}

	out := &sandboxv1beta2.RunnerDeploymentOverrides{
		NodeSelector:    nodeSelector,
		Annotations:     annotations,
		Labels:          labels,
		Env:             env,
		Args:            args,
		CpuRuntimeClass: o.CPURuntimeClass.ValueString(),
		GpuRuntimeClass: o.GPURuntimeClass.ValueString(),
	}
	for _, t := range o.Tolerations {
		out.Tolerations = append(out.Tolerations, &sandboxv1beta2.RunnerToleration{
			Key:      t.Key.ValueString(),
			Operator: t.Operator.ValueString(),
			Value:    t.Value.ValueString(),
			Effect:   t.Effect.ValueString(),
		})
	}
	if o.Resources != nil {
		out.Resources = &sandboxv1beta2.RunnerResourceRequirements{
			CpuRequest:    o.Resources.CPURequest.ValueString(),
			MemoryRequest: o.Resources.MemoryRequest.ValueString(),
			CpuLimit:      o.Resources.CPULimit.ValueString(),
			MemoryLimit:   o.Resources.MemoryLimit.ValueString(),
		}
	}
	if o.Scaling != nil {
		out.Scaling = &sandboxv1beta2.RunnerScalingConfig{
			Replicas:           o.Scaling.Replicas.ValueInt32(),
			AutoscalingEnabled: o.Scaling.AutoscalingEnabled.ValueBool(),
			MinReplicas:        o.Scaling.MinReplicas.ValueInt32(),
			MaxReplicas:        o.Scaling.MaxReplicas.ValueInt32(),
		}
	}
	return out, diags
}

// Set hydrates the resource model from a proto Runner. The receiver may carry a
// prior plan/state value — we use it to preserve null-vs-empty semantics on
// maps/lists so refresh doesn't churn.
func (m *ManagedRunnerResourceModel) Set(ctx context.Context, runner *sandboxv1beta2.Runner) diag.Diagnostics {
	if runner == nil {
		return nil
	}
	var diags diag.Diagnostics

	m.ID = types.StringValue(runner.GetId())
	m.DisplayName = stringOrNull(runner.GetDisplayName())
	m.ManagementMode = types.StringValue(runner.GetManagementMode().String())

	if id := runner.GetIdentity(); id != nil {
		m.RunnerID = types.StringValue(id.GetRunnerId())
		m.Zone = types.StringValue(id.GetZone())
		m.ClusterID = stringOrNull(id.GetClusterId())
		m.ClusterName = stringOrNull(id.GetClusterName())
		m.RunnerGroupID = stringOrNull(id.GetRunnerGroupId())
	}

	if spec := runner.GetManagedSpec(); spec != nil {
		m.ReleaseChannel = types.StringValue(spec.GetReleaseChannel().String())
		m.AllowPrivilegedProfileAnnotations = types.BoolValue(spec.GetAllowPrivilegedProfileAnnotations())
		m.EnforceResourceLimits = types.BoolValue(spec.GetEnforceResourceLimits())

		mp := maintenancePolicyFromProto(spec.GetMaintenancePolicy())
		m.MaintenancePolicy = mp

		ov, d := overridesFromProto(ctx, m.Overrides, spec.GetOverrides())
		diags.Append(d...)
		m.Overrides = ov
	} else {
		m.ReleaseChannel = types.StringNull()
		m.AllowPrivilegedProfileAnnotations = types.BoolValue(false)
		m.EnforceResourceLimits = types.BoolValue(false)
		m.MaintenancePolicy = nil
		m.Overrides = nil
	}

	m.ProfileBindings = bindingsFromProto(runner.GetProfileBindings())

	m.InstallStatus = types.StringValue(runner.GetInstallStatus().String())
	m.ConnectionStatus = types.StringValue(runner.GetConnectionStatus().String())
	m.ActiveConfigVersion = stringOrNull(runner.GetActiveConfigVersion())
	m.ActiveProfileSpecVersion = stringOrNull(runner.GetActiveProfileSpecVersion())
	m.ActiveRevision = types.Int32Value(runner.GetActiveRevision())
	m.TargetRevision = types.Int32Value(runner.GetTargetRevision())
	m.UpdateAvailable = types.BoolValue(runner.GetUpdateAvailable())
	m.RolloutInProgress = types.BoolValue(runner.GetRolloutInProgress())

	m.InstallError = installErrorFromProto(runner.GetInstallError())

	dep, d := deploymentSpecFromProto(ctx, runner.GetDeploymentSpec())
	diags.Append(d...)
	m.DeploymentSpec = dep

	m.CreatedAt = timestampString(runner.GetCreatedAt())
	m.UpdatedAt = timestampString(runner.GetUpdatedAt())
	m.LastHeartbeatAt = timestampString(runner.GetLastHeartbeatAt())

	return diags
}

func maintenancePolicyFromProto(p *sandboxv1beta2.MaintenancePolicy) *MaintenancePolicyModel {
	if p == nil {
		return nil
	}
	if len(p.GetWindows()) == 0 && len(p.GetExclusions()) == 0 {
		return nil
	}
	out := &MaintenancePolicyModel{}
	for _, w := range p.GetWindows() {
		out.Windows = append(out.Windows, MaintenanceWindowModel{
			Cron:            types.StringValue(w.GetCron()),
			DurationSeconds: types.Int32Value(w.GetDurationSeconds()),
		})
	}
	for _, e := range p.GetExclusions() {
		out.Exclusions = append(out.Exclusions, MaintenanceExclusionModel{
			StartTime: timestampString(e.GetStartTime()),
			EndTime:   timestampString(e.GetEndTime()),
			Reason:    stringOrNull(e.GetReason()),
		})
	}
	return out
}

func overridesFromProto(ctx context.Context, prior *RunnerOverridesModel, p *sandboxv1beta2.RunnerDeploymentOverrides) (*RunnerOverridesModel, diag.Diagnostics) {
	if p == nil {
		return nil, nil
	}
	// Treat fully empty proto as "unset" to keep refresh stable when the user
	// did not configure overrides.
	if isEmptyOverrides(p) && prior == nil {
		return nil, nil
	}
	var diags diag.Diagnostics
	out := &RunnerOverridesModel{
		CPURuntimeClass: stringOrNull(p.GetCpuRuntimeClass()),
		GPURuntimeClass: stringOrNull(p.GetGpuRuntimeClass()),
	}

	priorNS := types.MapNull(types.StringType)
	priorAnn := types.MapNull(types.StringType)
	priorLab := types.MapNull(types.StringType)
	priorEnv := types.MapNull(types.StringType)
	priorArgs := types.ListNull(types.StringType)
	if prior != nil {
		priorNS = prior.NodeSelector
		priorAnn = prior.Annotations
		priorLab = prior.Labels
		priorEnv = prior.Env
		priorArgs = prior.Args
	}
	ns, d := stringMapFromMap(ctx, p.GetNodeSelector(), priorNS)
	diags.Append(d...)
	out.NodeSelector = ns
	ann, d := stringMapFromMap(ctx, p.GetAnnotations(), priorAnn)
	diags.Append(d...)
	out.Annotations = ann
	lab, d := stringMapFromMap(ctx, p.GetLabels(), priorLab)
	diags.Append(d...)
	out.Labels = lab
	env, d := stringMapFromMap(ctx, p.GetEnv(), priorEnv)
	diags.Append(d...)
	out.Env = env
	args, d := stringSliceToList(ctx, p.GetArgs(), priorArgs)
	diags.Append(d...)
	out.Args = args

	for _, t := range p.GetTolerations() {
		out.Tolerations = append(out.Tolerations, RunnerTolerationModel{
			Key:      stringOrNull(t.GetKey()),
			Operator: stringOrNull(t.GetOperator()),
			Value:    stringOrNull(t.GetValue()),
			Effect:   stringOrNull(t.GetEffect()),
		})
	}
	if r := p.GetResources(); r != nil && !isEmptyResourceReq(r) {
		out.Resources = &RunnerResourceRequirementsModel{
			CPURequest:    stringOrNull(r.GetCpuRequest()),
			MemoryRequest: stringOrNull(r.GetMemoryRequest()),
			CPULimit:      stringOrNull(r.GetCpuLimit()),
			MemoryLimit:   stringOrNull(r.GetMemoryLimit()),
		}
	}
	if s := p.GetScaling(); s != nil && !isEmptyScaling(s) {
		out.Scaling = &RunnerScalingConfigModel{
			Replicas:           types.Int32Value(s.GetReplicas()),
			AutoscalingEnabled: types.BoolValue(s.GetAutoscalingEnabled()),
			MinReplicas:        types.Int32Value(s.GetMinReplicas()),
			MaxReplicas:        types.Int32Value(s.GetMaxReplicas()),
		}
	}
	return out, diags
}

func isEmptyOverrides(p *sandboxv1beta2.RunnerDeploymentOverrides) bool {
	if p == nil {
		return true
	}
	return len(p.GetNodeSelector()) == 0 &&
		len(p.GetTolerations()) == 0 &&
		(p.GetResources() == nil || isEmptyResourceReq(p.GetResources())) &&
		len(p.GetAnnotations()) == 0 &&
		len(p.GetLabels()) == 0 &&
		len(p.GetEnv()) == 0 &&
		len(p.GetArgs()) == 0 &&
		(p.GetScaling() == nil || isEmptyScaling(p.GetScaling())) &&
		p.GetCpuRuntimeClass() == "" &&
		p.GetGpuRuntimeClass() == ""
}

func isEmptyResourceReq(r *sandboxv1beta2.RunnerResourceRequirements) bool {
	return r.GetCpuRequest() == "" && r.GetMemoryRequest() == "" && r.GetCpuLimit() == "" && r.GetMemoryLimit() == ""
}

func isEmptyScaling(s *sandboxv1beta2.RunnerScalingConfig) bool {
	return s.GetReplicas() == 0 && !s.GetAutoscalingEnabled() && s.GetMinReplicas() == 0 && s.GetMaxReplicas() == 0
}

func bindingsFromProto(in []*sandboxv1beta2.RunnerProfileBinding) []ProfileBindingModel {
	if len(in) == 0 {
		return nil
	}
	out := make([]ProfileBindingModel, 0, len(in))
	for _, b := range in {
		out = append(out, ProfileBindingModel{
			ProfileTemplateID: types.StringValue(b.GetProfileTemplateId()),
			ProfileName:       stringOrNull(b.GetProfileName()),
			IsDefault:         types.BoolValue(b.GetIsDefault()),
			OverridesJSON:     stringOrNull(b.GetOverridesJson()),
		})
	}
	return out
}

func installErrorFromProto(p *sandboxv1beta2.RunnerInstallError) *RunnerInstallErrorModel {
	if p == nil {
		return nil
	}
	out := &RunnerInstallErrorModel{
		Reason:           types.StringValue(p.GetReason().String()),
		Message:          stringOrNull(p.GetMessage()),
		DiagnosticDetail: stringOrNull(p.GetDiagnosticDetail()),
		OccurredAt:       timestampString(p.GetOccurredAt()),
	}
	hints := make([]attr.Value, 0, len(p.GetRemediationHints()))
	for _, h := range p.GetRemediationHints() {
		hints = append(hints, types.StringValue(h))
	}
	if len(hints) == 0 {
		out.RemediationHints = types.ListNull(types.StringType)
	} else {
		out.RemediationHints, _ = types.ListValue(types.StringType, hints)
	}
	return out
}

func deploymentSpecFromProto(ctx context.Context, p *sandboxv1beta2.RunnerDeploymentSpec) (*RunnerDeploymentSpecModel, diag.Diagnostics) {
	if p == nil {
		return nil, nil
	}
	var diags diag.Diagnostics

	out := &RunnerDeploymentSpecModel{
		Image:           types.StringValue(p.GetImage()),
		ServiceAccount:  stringOrNull(p.GetServiceAccount()),
		ImagePullPolicy: stringOrNull(p.GetImagePullPolicy()),
		GatewayServer:   stringOrNull(p.GetGatewayServer()),
		CPURuntimeClass: stringOrNull(p.GetCpuRuntimeClass()),
		GPURuntimeClass: stringOrNull(p.GetGpuRuntimeClass()),
	}

	env, d := stringMapFromMap(ctx, p.GetEnv(), types.MapNull(types.StringType))
	diags.Append(d...)
	out.Env = env

	args, d := stringSliceToList(ctx, p.GetArgs(), types.ListNull(types.StringType))
	diags.Append(d...)
	out.Args = args

	ns, d := stringMapFromMap(ctx, p.GetNodeSelector(), types.MapNull(types.StringType))
	diags.Append(d...)
	out.NodeSelector = ns

	ann, d := stringMapFromMap(ctx, p.GetAnnotations(), types.MapNull(types.StringType))
	diags.Append(d...)
	out.Annotations = ann

	lab, d := stringMapFromMap(ctx, p.GetLabels(), types.MapNull(types.StringType))
	diags.Append(d...)
	out.Labels = lab

	for _, t := range p.GetTolerations() {
		out.Tolerations = append(out.Tolerations, RunnerTolerationModel{
			Key:      stringOrNull(t.GetKey()),
			Operator: stringOrNull(t.GetOperator()),
			Value:    stringOrNull(t.GetValue()),
			Effect:   stringOrNull(t.GetEffect()),
		})
	}

	if r := p.GetResources(); r != nil {
		out.Resources = &RunnerResourceRequirementsModel{
			CPURequest:    stringOrNull(r.GetCpuRequest()),
			MemoryRequest: stringOrNull(r.GetMemoryRequest()),
			CPULimit:      stringOrNull(r.GetCpuLimit()),
			MemoryLimit:   stringOrNull(r.GetMemoryLimit()),
		}
	}
	if r := p.GetInitResources(); r != nil {
		out.InitResources = &RunnerResourceRequirementsModel{
			CPURequest:    stringOrNull(r.GetCpuRequest()),
			MemoryRequest: stringOrNull(r.GetMemoryRequest()),
			CPULimit:      stringOrNull(r.GetCpuLimit()),
			MemoryLimit:   stringOrNull(r.GetMemoryLimit()),
		}
	}
	if s := p.GetScaling(); s != nil {
		out.Scaling = &RunnerScalingConfigModel{
			Replicas:           types.Int32Value(s.GetReplicas()),
			AutoscalingEnabled: types.BoolValue(s.GetAutoscalingEnabled()),
			MinReplicas:        types.Int32Value(s.GetMinReplicas()),
			MaxReplicas:        types.Int32Value(s.GetMaxReplicas()),
		}
	}
	return out, diags
}

// ----------------------------------------------------------------------------
// CRUD
// ----------------------------------------------------------------------------

func (r *ManagedRunnerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ManagedRunnerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateExactlyOneDefaultBinding(data.ProfileBindings)...)
	if resp.Diagnostics.HasError() {
		return
	}

	runner, diags := data.toRunner(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.client.CreateManagedRunner(ctx, connect.NewRequest(&sandboxv1beta2.CreateManagedRunnerRequest{
		RunnerId: data.RunnerID.ValueString(),
		Runner:   runner,
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// Persist initial state immediately so a wait failure leaves the user able to destroy.
	resp.Diagnostics.Append(data.Set(ctx, createResp.Msg)...)
	if diag := resp.State.Set(ctx, &data); diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	final, err := r.waitForReady(ctx, createResp.Msg.GetId())
	if err != nil {
		// On install failure, refresh state with the latest install_error
		// so the user can `terraform destroy` and retry.
		if errors.Is(err, errRunnerInstallFailed) {
			data.Set(ctx, final)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			resp.Diagnostics.AddError(
				"Runner installation failed",
				"The managed runner failed to install. Inspect `install_error` in state for the failure reason and remediation hints, then destroy and re-create the resource.",
			)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, final)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ManagedRunnerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ManagedRunnerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := r.client.GetRunner(ctx, connect.NewRequest(&sandboxv1beta2.GetRunnerRequest{
		Id: data.ID.ValueString(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, getResp.Msg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ManagedRunnerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ManagedRunnerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateExactlyOneDefaultBinding(plan.ProfileBindings)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Nothing to send: short-circuit and just persist the plan as-is.
	if len(updateReq.GetUpdateMask().GetPaths()) == 0 {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	updateResp, err := r.client.UpdateManagedRunner(ctx, connect.NewRequest(updateReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	final, err := r.waitForReady(ctx, updateResp.Msg.GetId())
	if err != nil {
		if errors.Is(err, errRunnerInstallFailed) {
			plan.Set(ctx, final)
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.AddError(
				"Runner update failed",
				"The managed runner failed during update. Inspect `install_error` in state for details.",
			)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(plan.Set(ctx, final)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ManagedRunnerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ManagedRunnerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.DeleteManagedRunner(ctx, connect.NewRequest(&sandboxv1beta2.DeleteManagedRunnerRequest{
		Id: data.ID.ValueString(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	conf := retry.StateChangeConf{
		Pending: []string{
			sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_DELETING.String(),
			sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_READY.String(),
			sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_PROVISIONING.String(),
			sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_PENDING.String(),
			sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{""},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetRunner(ctx, connect.NewRequest(&sandboxv1beta2.GetRunnerRequest{
				Id: data.ID.ValueString(),
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return struct{}{}, "", nil
				}
				tflog.Error(ctx, "failed to fetch runner during delete", map[string]interface{}{"error": err.Error()})
				return nil, sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_UNSPECIFIED.String(), err
			}
			return getResp.Msg, getResp.Msg.GetInstallStatus().String(), nil
		},
		Timeout: 20 * time.Minute,
	}
	if _, err := conf.WaitForStateContext(ctx); err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (r *ManagedRunnerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ----------------------------------------------------------------------------
// Wait + helpers
// ----------------------------------------------------------------------------

func (r *ManagedRunnerResource) waitForReady(ctx context.Context, id string) (*sandboxv1beta2.Runner, error) {
	conf := retry.StateChangeConf{
		Pending: []string{
			sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_UNSPECIFIED.String(),
			sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_PENDING.String(),
			sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_PROVISIONING.String(),
		},
		Target: []string{
			sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_READY.String(),
		},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetRunner(ctx, connect.NewRequest(&sandboxv1beta2.GetRunnerRequest{
				Id: id,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to fetch runner during wait", map[string]interface{}{"error": err.Error()})
				return nil, sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_UNSPECIFIED.String(), err
			}
			return getResp.Msg, getResp.Msg.GetInstallStatus().String(), nil
		},
		Timeout: 30 * time.Minute,
	}

	raw, err := conf.WaitForStateContext(ctx)
	if err != nil {
		// If the loop terminated because the runner reached FAILED, surface a
		// typed sentinel so callers can persist install_error to state.
		if raw != nil {
			if runner, ok := raw.(*sandboxv1beta2.Runner); ok && runner.GetInstallStatus() == sandboxv1beta2.RunnerInstallStatus_RUNNER_INSTALL_STATUS_FAILED {
				return runner, errRunnerInstallFailed
			}
		}
		return nil, err
	}
	runner, ok := raw.(*sandboxv1beta2.Runner)
	if !ok {
		return nil, errors.New("unexpected response type from runner refresh")
	}
	return runner, nil
}

func validateExactlyOneDefaultBinding(bindings []ProfileBindingModel) diag.Diagnostics {
	var diags diag.Diagnostics
	defaults := 0
	for _, b := range bindings {
		if b.IsDefault.ValueBool() {
			defaults++
		}
	}
	if defaults != 1 {
		diags.AddAttributeError(
			path.Root("profile_bindings"),
			"Exactly one default profile binding required",
			fmt.Sprintf("Found %d bindings with is_default = true; exactly one is required.", defaults),
		)
	}
	return diags
}
