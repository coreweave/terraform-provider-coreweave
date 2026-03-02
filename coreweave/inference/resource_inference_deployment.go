package inference

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                = &InferenceDeploymentResource{}
	_ resource.ResourceWithImportState = &InferenceDeploymentResource{}

	hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?$`)
	uuidPattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	semverPattern   = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

	conditionAttrTypes = map[string]attr.Type{
		"type":             types.StringType,
		"status":           types.StringType,
		"last_update_time": types.StringType,
		"reason":           types.StringType,
		"message":          types.StringType,
	}

	errDeploymentFailed = errors.New("inference deployment entered a failed state")
)

func NewInferenceDeploymentResource() resource.Resource {
	return &InferenceDeploymentResource{}
}

type InferenceDeploymentResource struct {
	client *coreweave.Client
}

// Nested model types.

type RuntimeModel struct {
	Engine       types.String `tfsdk:"engine"`
	Version      types.String `tfsdk:"version"`
	EngineConfig types.Map    `tfsdk:"engine_config"`
}

type ResourcesModel struct {
	InstanceType types.String `tfsdk:"instance_type"`
	GpuCount     types.Int64  `tfsdk:"gpu_count"`
}

type DeploymentModelConfig struct {
	Name   types.String `tfsdk:"name"`
	Bucket types.String `tfsdk:"bucket"`
	Path   types.String `tfsdk:"path"`
}

type AutoscalingModel struct {
	Min             types.Int64 `tfsdk:"min"`
	Max             types.Int64 `tfsdk:"max"`
	Priority        types.Int64 `tfsdk:"priority"`
	CapacityClasses types.List  `tfsdk:"capacity_classes"`
	Concurrency     types.Int64 `tfsdk:"concurrency"`
}

type TrafficModel struct {
	Weight types.Int64 `tfsdk:"weight"`
}

type ConditionModel struct {
	Type           types.String `tfsdk:"type"`
	Status         types.String `tfsdk:"status"`
	LastUpdateTime types.String `tfsdk:"last_update_time"`
	Reason         types.String `tfsdk:"reason"`
	Message        types.String `tfsdk:"message"`
}

// InferenceDeploymentResourceModel is the top-level Terraform state model.
type InferenceDeploymentResourceModel struct {
	// Computed
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	Status         types.String `tfsdk:"status"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
	Conditions     types.List   `tfsdk:"conditions"`
	// Required / Optional
	Name        types.String          `tfsdk:"name"`
	GatewayIds  types.List            `tfsdk:"gateway_ids"`
	Disabled    types.Bool            `tfsdk:"disabled"`
	Runtime     RuntimeModel          `tfsdk:"runtime"`
	Resources   ResourcesModel        `tfsdk:"resources"`
	Model       DeploymentModelConfig `tfsdk:"model"`
	Autoscaling AutoscalingModel      `tfsdk:"autoscaling"`
	Traffic     TrafficModel          `tfsdk:"traffic"`
}

func (r *InferenceDeploymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inference_deployment"
}

func (r *InferenceDeploymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Create and manage [CoreWeave Managed Inference](https://docs.coreweave.com/products/inference) deployments.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier of the deployment.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"organization_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The organization ID that owns the deployment.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The current status of the deployment.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of when the deployment was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of when the deployment was last updated.",
			},
			"conditions": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Detailed status conditions for the deployment.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type":             schema.StringAttribute{Computed: true},
						"status":           schema.StringAttribute{Computed: true},
						"last_update_time": schema.StringAttribute{Computed: true},
						"reason":           schema.StringAttribute{Computed: true},
						"message":          schema.StringAttribute{Computed: true},
					},
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the deployment. Must be a valid hostname label.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.RegexMatches(hostnamePattern, "must be a valid hostname label"),
				},
			},
			// TODO: clarify with managed inference team whether gateway_ids ordering is significant.
			// If not, this should be schema.SetAttribute to prevent spurious diffs on reorder.
			"gateway_ids": schema.ListAttribute{
				ElementType:         types.StringType,
				Required:            true,
				MarkdownDescription: "The gateway IDs to associate the deployment with. At least one is required.",
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					listvalidator.ValueStringsAre(
						stringvalidator.RegexMatches(uuidPattern, "must be a valid UUID"),
					),
				},
			},
			"disabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Whether the deployment is disabled.",
				Default:             booldefault.StaticBool(false),
			},
			"runtime": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Runtime selection and configuration.",
				Attributes: map[string]schema.Attribute{
					"engine": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The inference engine to use.",
						Validators: []validator.String{
							stringvalidator.OneOf("vllm"),
						},
					},
					"version": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The version of the engine. If not set, defaults to the latest available version. Must follow semver format (e.g. `1.2.3`).",
						Validators: []validator.String{
							stringvalidator.RegexMatches(semverPattern, "must be a semver string, e.g. 1.2.3"),
						},
					},
					"engine_config": schema.MapAttribute{
						ElementType:         types.StringType,
						Optional:            true,
						MarkdownDescription: "Engine-specific configuration key/value pairs.",
					},
				},
			},
			"resources": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Resource configuration for the deployment.",
				Attributes: map[string]schema.Attribute{
					"instance_type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The instance type to use.",
					},
					"gpu_count": schema.Int64Attribute{
						Required:            true,
						MarkdownDescription: "Number of GPUs per instance. Must be one of: 1, 2, 4, 8, 16.",
						Validators: []validator.Int64{
							int64validator.OneOf(1, 2, 4, 8, 16),
						},
					},
				},
			},
			"model": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Model configuration.",
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The model name used in API requests (e.g. the `/models` endpoint). Length must be 8–63 characters.",
						Validators: []validator.String{
							stringvalidator.LengthBetween(8, 63),
						},
					},
					"bucket": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The CAIOS bucket the model is stored in.",
						Validators: []validator.String{
							stringvalidator.RegexMatches(hostnamePattern, "must be a valid hostname"),
						},
					},
					"path": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The CAIOS path to the model and its configuration files.",
					},
				},
			},
			"autoscaling": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Autoscaling configuration.",
				Attributes: map[string]schema.Attribute{
					"min": schema.Int64Attribute{
						Required:            true,
						MarkdownDescription: "Minimum number of instances. Must be ≥1.",
						Validators: []validator.Int64{
							int64validator.AtLeast(1),
						},
					},
					"max": schema.Int64Attribute{
						Required:            true,
						MarkdownDescription: "Maximum number of instances. Must be ≥1.",
						Validators: []validator.Int64{
							int64validator.AtLeast(1),
						},
					},
					"priority": schema.Int64Attribute{
						Optional:            true,
						MarkdownDescription: "Priority for cross-deployment scaling (0–1000). Higher values win when there is contention.",
						Validators: []validator.Int64{
							int64validator.Between(0, 1000),
						},
					},
					"capacity_classes": schema.ListAttribute{
						ElementType:         types.StringType,
						Optional:            true,
						MarkdownDescription: `Capacity classes to use. Allowed values: "RESERVED", "ON_DEMAND".`,
						Validators: []validator.List{
							listvalidator.ValueStringsAre(
								stringvalidator.OneOf("RESERVED", "ON_DEMAND"),
							),
						},
					},
					"concurrency": schema.Int64Attribute{
						Optional:            true,
						MarkdownDescription: "Concurrency per instance target (≥1). Controls latency vs throughput tradeoffs.",
						Validators: []validator.Int64{
							int64validator.AtLeast(1),
						},
					},
				},
			},
			"traffic": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Traffic configuration.",
				Attributes: map[string]schema.Attribute{
					"weight": schema.Int64Attribute{
						Optional:            true,
						MarkdownDescription: "Traffic weight (0–1000). Values are normalized into percentages across deployments with the same model name.",
						Validators: []validator.Int64{
							int64validator.Between(0, 1000),
						},
					},
				},
			},
		},
	}
}

func (r *InferenceDeploymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *InferenceDeploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data InferenceDeploymentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.client.CreateDeployment(ctx, connect.NewRequest(toCreateRequest(ctx, &data)))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// Save initial state before polling so the resource is tracked even if polling fails.
	setFromDeployment(&data, createResp.Msg.Deployment)
	if diag := resp.State.Set(ctx, &data); diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	deploymentID := createResp.Msg.Deployment.GetSpec().GetId()

	conf := retry.StateChangeConf{
		Pending: []string{
			inferencev1.DeploymentStatus_STATUS_CREATING.String(),
			inferencev1.DeploymentStatus_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{inferencev1.DeploymentStatus_STATUS_RUNNING.String()},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetDeployment(ctx, connect.NewRequest(&inferencev1.GetDeploymentRequest{
				Id: deploymentID,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to poll deployment", map[string]interface{}{"error": err})
				return nil, inferencev1.DeploymentStatus_STATUS_UNSPECIFIED.String(), err
			}
			d := getResp.Msg.Deployment
			status := d.GetStatus().GetStatus()
			if status == inferencev1.DeploymentStatus_STATUS_ERROR || status == inferencev1.DeploymentStatus_STATUS_FAILED {
				return d, status.String(), errDeploymentFailed
			}
			return d, status.String(), nil
		},
		Timeout:    45 * time.Minute,
		MinTimeout: 5 * time.Second,
	}

	raw, err := conf.WaitForStateContext(ctx)
	if err != nil && !errors.Is(err, errDeploymentFailed) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	d, ok := raw.(*inferencev1.Deployment)
	if !ok {
		resp.Diagnostics.AddError("Unexpected polling result type", "Expected *inferencev1.Deployment. Please report this issue to the provider developers.")
		return
	}

	if d.GetStatus().GetStatus() == inferencev1.DeploymentStatus_STATUS_ERROR ||
		d.GetStatus().GetStatus() == inferencev1.DeploymentStatus_STATUS_FAILED {
		resp.Diagnostics.AddError("Deployment creation failed",
			fmt.Sprintf("Deployment entered status %s. You must destroy and recreate this resource.", d.GetStatus().GetStatus().String()))
	}

	setFromDeployment(&data, d)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InferenceDeploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data InferenceDeploymentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := r.client.GetDeployment(ctx, connect.NewRequest(&inferencev1.GetDeploymentRequest{
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

	setFromDeployment(&data, getResp.Msg.Deployment)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InferenceDeploymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data InferenceDeploymentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateResp, err := r.client.UpdateDeployment(ctx, connect.NewRequest(toUpdateRequest(ctx, &data)))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	deploymentID := updateResp.Msg.Deployment.GetSpec().GetId()

	conf := retry.StateChangeConf{
		Pending: []string{
			inferencev1.DeploymentStatus_STATUS_UPDATING.String(),
			inferencev1.DeploymentStatus_STATUS_CREATING.String(),
			inferencev1.DeploymentStatus_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{inferencev1.DeploymentStatus_STATUS_RUNNING.String()},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetDeployment(ctx, connect.NewRequest(&inferencev1.GetDeploymentRequest{
				Id: deploymentID,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to poll deployment", map[string]interface{}{"error": err.Error()})
				return nil, inferencev1.DeploymentStatus_STATUS_UNSPECIFIED.String(), err
			}
			d := getResp.Msg.Deployment
			status := d.GetStatus().GetStatus()
			if status == inferencev1.DeploymentStatus_STATUS_ERROR || status == inferencev1.DeploymentStatus_STATUS_FAILED {
				return d, status.String(), errDeploymentFailed
			}
			return d, status.String(), nil
		},
		Timeout:    20 * time.Minute,
		MinTimeout: 5 * time.Second,
	}

	raw, err := conf.WaitForStateContext(ctx)
	if err != nil && !errors.Is(err, errDeploymentFailed) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	d, ok := raw.(*inferencev1.Deployment)
	if !ok {
		resp.Diagnostics.AddError("Unexpected polling result type", "Expected *inferencev1.Deployment. Please report this issue to the provider developers.")
		return
	}

	if d.GetStatus().GetStatus() == inferencev1.DeploymentStatus_STATUS_ERROR ||
		d.GetStatus().GetStatus() == inferencev1.DeploymentStatus_STATUS_FAILED {
		resp.Diagnostics.AddError("Deployment update failed",
			fmt.Sprintf("Deployment entered status %s. Check the `conditions` attribute for details.", d.GetStatus().GetStatus().String()))
	}

	setFromDeployment(&data, d)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InferenceDeploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data InferenceDeploymentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deploymentID := data.ID.ValueString()

	_, err := r.client.DeleteDeployment(ctx, connect.NewRequest(&inferencev1.DeleteDeploymentRequest{
		Id: deploymentID,
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// deletedState is a synthetic target state returned when CodeNotFound is received,
	// since the inference proto has no STATUS_DELETED enum value.
	// TODO: follow up with the managed inference team to add STATUS_DELETED to the proto
	// so deletion can be polled deterministically via status rather than CodeNotFound.
	const deletedState = "NOT_FOUND"

	conf := retry.StateChangeConf{
		Pending: []string{
			inferencev1.DeploymentStatus_STATUS_DELETING.String(),
			inferencev1.DeploymentStatus_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{deletedState},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetDeployment(ctx, connect.NewRequest(&inferencev1.GetDeploymentRequest{
				Id: deploymentID,
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return struct{}{}, deletedState, nil
				}
				tflog.Error(ctx, "failed to poll deployment deletion", map[string]interface{}{"error": err.Error()})
				return nil, inferencev1.DeploymentStatus_STATUS_UNSPECIFIED.String(), err
			}
			d := getResp.Msg.Deployment
			return d, d.GetStatus().GetStatus().String(), nil
		},
		Timeout:    20 * time.Minute,
		MinTimeout: 5 * time.Second,
	}

	_, err = conf.WaitForStateContext(ctx)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
}

func (r *InferenceDeploymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- Helpers ---

// deploymentFields holds the proto sub-messages shared between CreateDeploymentRequest
// and UpdateDeploymentRequest. Use buildDeploymentFields to populate it.
type deploymentFields struct {
	Name        string
	GatewayIds  []string
	Disabled    bool
	Runtime     *inferencev1.DeploymentRuntime
	Resources   *inferencev1.DeploymentResources
	Model       *inferencev1.DeploymentModel
	Autoscaling *inferencev1.DeploymentAutoscaling
	Traffic     *inferencev1.DeploymentTraffic
}

// buildDeploymentFields extracts the fields common to both create and update requests
// from the Terraform resource model.
func buildDeploymentFields(ctx context.Context, m *InferenceDeploymentResourceModel) deploymentFields {
	gwIds := []string{}
	m.GatewayIds.ElementsAs(ctx, &gwIds, false)

	f := deploymentFields{
		Name:       m.Name.ValueString(),
		GatewayIds: gwIds,
		Disabled:   m.Disabled.ValueBool(),
		Runtime: &inferencev1.DeploymentRuntime{
			Engine: m.Runtime.Engine.ValueString(),
		},
		Resources: &inferencev1.DeploymentResources{
			InstanceType: m.Resources.InstanceType.ValueString(),
			GpuCount:     uint32(m.Resources.GpuCount.ValueInt64()), //nolint:gosec
		},
		Model: &inferencev1.DeploymentModel{
			Name:   m.Model.Name.ValueString(),
			Bucket: m.Model.Bucket.ValueString(),
			Path:   m.Model.Path.ValueString(),
		},
		Autoscaling: &inferencev1.DeploymentAutoscaling{
			Min: uint32(m.Autoscaling.Min.ValueInt64()), //nolint:gosec
			Max: uint32(m.Autoscaling.Max.ValueInt64()), //nolint:gosec
		},
		Traffic: &inferencev1.DeploymentTraffic{},
	}

	if !m.Runtime.Version.IsNull() && !m.Runtime.Version.IsUnknown() {
		f.Runtime.Version = m.Runtime.Version.ValueString()
	}
	if !m.Runtime.EngineConfig.IsNull() && !m.Runtime.EngineConfig.IsUnknown() {
		ec := map[string]string{}
		m.Runtime.EngineConfig.ElementsAs(ctx, &ec, false)
		f.Runtime.EngineConfig = ec
	}
	if !m.Autoscaling.Priority.IsNull() && !m.Autoscaling.Priority.IsUnknown() {
		f.Autoscaling.Priority = uint32(m.Autoscaling.Priority.ValueInt64()) //nolint:gosec
	}
	if !m.Autoscaling.Concurrency.IsNull() && !m.Autoscaling.Concurrency.IsUnknown() {
		f.Autoscaling.Concurrency = uint32(m.Autoscaling.Concurrency.ValueInt64()) //nolint:gosec
	}
	if !m.Autoscaling.CapacityClasses.IsNull() && !m.Autoscaling.CapacityClasses.IsUnknown() {
		ccs := []string{}
		m.Autoscaling.CapacityClasses.ElementsAs(ctx, &ccs, false)
		protoClasses := make([]inferencev1.DeploymentAutoscaling_CapacityClass, 0, len(ccs))
		for _, cc := range ccs {
			protoClasses = append(protoClasses, capacityClassFromString(cc))
		}
		f.Autoscaling.CapacityClasses = protoClasses
	}
	if !m.Traffic.Weight.IsNull() && !m.Traffic.Weight.IsUnknown() {
		f.Traffic.Weight = uint32(m.Traffic.Weight.ValueInt64()) //nolint:gosec
	}

	return f
}

func toCreateRequest(ctx context.Context, m *InferenceDeploymentResourceModel) *inferencev1.CreateDeploymentRequest {
	f := buildDeploymentFields(ctx, m)
	return &inferencev1.CreateDeploymentRequest{
		Name:        f.Name,
		GatewayIds:  f.GatewayIds,
		Disabled:    f.Disabled,
		Runtime:     f.Runtime,
		Resources:   f.Resources,
		Model:       f.Model,
		Autoscaling: f.Autoscaling,
		Traffic:     f.Traffic,
	}
}

func toUpdateRequest(ctx context.Context, m *InferenceDeploymentResourceModel) *inferencev1.UpdateDeploymentRequest {
	f := buildDeploymentFields(ctx, m)
	return &inferencev1.UpdateDeploymentRequest{
		Id:          m.ID.ValueString(),
		Name:        f.Name,
		GatewayIds:  f.GatewayIds,
		Disabled:    f.Disabled,
		Runtime:     f.Runtime,
		Resources:   f.Resources,
		Model:       f.Model,
		Autoscaling: f.Autoscaling,
		Traffic:     f.Traffic,
	}
}

// setFromDeployment populates all fields on the model from a proto Deployment response.
// For Optional (non-Computed) fields, null is preserved when the plan/state was null and the
// API returns the default (zero/empty) value — matching the CKS pattern for optional fields.
func setFromDeployment(m *InferenceDeploymentResourceModel, d *inferencev1.Deployment) {
	spec := d.GetSpec()
	status := d.GetStatus()

	m.ID = types.StringValue(spec.GetId())
	m.OrganizationID = types.StringValue(spec.GetOrganizationId())
	m.Name = types.StringValue(spec.GetName())
	m.Status = types.StringValue(status.GetStatus().String())
	m.Disabled = types.BoolValue(spec.GetDisabled())

	if status.GetCreatedAt() != nil {
		m.CreatedAt = types.StringValue(status.GetCreatedAt().AsTime().Format(time.RFC3339))
	}
	if status.GetUpdatedAt() != nil {
		m.UpdatedAt = types.StringValue(status.GetUpdatedAt().AsTime().Format(time.RFC3339))
	}

	// gateway_ids
	gwVals := make([]attr.Value, 0, len(spec.GetGatewayIds()))
	for _, id := range spec.GetGatewayIds() {
		gwVals = append(gwVals, types.StringValue(id))
	}
	m.GatewayIds = types.ListValueMust(types.StringType, gwVals)

	// runtime
	if rt := spec.GetRuntime(); rt != nil {
		m.Runtime.Engine = types.StringValue(rt.GetEngine())

		if m.Runtime.Version.IsNull() && rt.GetVersion() == "" {
			m.Runtime.Version = types.StringNull()
		} else {
			m.Runtime.Version = types.StringValue(rt.GetVersion())
		}

		if m.Runtime.EngineConfig.IsNull() && len(rt.GetEngineConfig()) == 0 {
			m.Runtime.EngineConfig = types.MapNull(types.StringType)
		} else {
			ecVals := make(map[string]attr.Value, len(rt.GetEngineConfig()))
			for k, v := range rt.GetEngineConfig() {
				ecVals[k] = types.StringValue(v)
			}
			m.Runtime.EngineConfig = types.MapValueMust(types.StringType, ecVals)
		}
	}

	// resources
	if res := spec.GetResources(); res != nil {
		m.Resources.InstanceType = types.StringValue(res.GetInstanceType())
		m.Resources.GpuCount = types.Int64Value(int64(res.GetGpuCount()))
	}

	// model
	if mod := spec.GetModel(); mod != nil {
		m.Model.Name = types.StringValue(mod.GetName())
		m.Model.Bucket = types.StringValue(mod.GetBucket())
		m.Model.Path = types.StringValue(mod.GetPath())
	}

	// autoscaling
	if as := spec.GetAutoscaling(); as != nil {
		m.Autoscaling.Min = types.Int64Value(int64(as.GetMin()))
		m.Autoscaling.Max = types.Int64Value(int64(as.GetMax()))

		if m.Autoscaling.Priority.IsNull() && as.GetPriority() == 0 {
			m.Autoscaling.Priority = types.Int64Null()
		} else {
			m.Autoscaling.Priority = types.Int64Value(int64(as.GetPriority()))
		}

		if m.Autoscaling.Concurrency.IsNull() && as.GetConcurrency() == 0 {
			m.Autoscaling.Concurrency = types.Int64Null()
		} else {
			m.Autoscaling.Concurrency = types.Int64Value(int64(as.GetConcurrency()))
		}

		if m.Autoscaling.CapacityClasses.IsNull() && len(as.GetCapacityClasses()) == 0 {
			m.Autoscaling.CapacityClasses = types.ListNull(types.StringType)
		} else {
			ccVals := make([]attr.Value, 0, len(as.GetCapacityClasses()))
			for _, cc := range as.GetCapacityClasses() {
				ccVals = append(ccVals, types.StringValue(capacityClassToString(cc)))
			}
			m.Autoscaling.CapacityClasses = types.ListValueMust(types.StringType, ccVals)
		}
	}

	// traffic
	if tr := spec.GetTraffic(); tr != nil {
		if m.Traffic.Weight.IsNull() && tr.GetWeight() == 0 {
			m.Traffic.Weight = types.Int64Null()
		} else {
			m.Traffic.Weight = types.Int64Value(int64(tr.GetWeight()))
		}
	}

	// conditions
	condVals := make([]attr.Value, 0, len(status.GetConditions()))
	for _, c := range status.GetConditions() {
		lastUpdate := ""
		if c.GetLastUpdateTime() != nil {
			lastUpdate = c.GetLastUpdateTime().AsTime().Format(time.RFC3339)
		}
		condVals = append(condVals, types.ObjectValueMust(conditionAttrTypes, map[string]attr.Value{
			"type":             types.StringValue(c.GetType()),
			"status":           types.StringValue(c.GetStatus().String()),
			"last_update_time": types.StringValue(lastUpdate),
			"reason":           types.StringValue(c.GetReason()),
			"message":          types.StringValue(c.GetMessage()),
		}))
	}
	m.Conditions = types.ListValueMust(types.ObjectType{AttrTypes: conditionAttrTypes}, condVals)
}

func capacityClassFromString(s string) inferencev1.DeploymentAutoscaling_CapacityClass {
	switch s {
	case "RESERVED":
		return inferencev1.DeploymentAutoscaling_CAPACITY_CLASS_RESERVED
	case "ON_DEMAND":
		return inferencev1.DeploymentAutoscaling_CAPACITY_CLASS_ON_DEMAND
	default:
		return inferencev1.DeploymentAutoscaling_CAPACITY_CLASS_UNSPECIFIED
	}
}

func capacityClassToString(cc inferencev1.DeploymentAutoscaling_CapacityClass) string {
	switch cc {
	case inferencev1.DeploymentAutoscaling_CAPACITY_CLASS_RESERVED:
		return "RESERVED"
	case inferencev1.DeploymentAutoscaling_CAPACITY_CLASS_ON_DEMAND:
		return "ON_DEMAND"
	case inferencev1.DeploymentAutoscaling_CAPACITY_CLASS_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}
