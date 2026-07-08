package inference

import (
	"context"
	"errors"
	"fmt"
	"time"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	cwvalidators "github.com/coreweave/terraform-provider-coreweave/internal/validators"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

var (
	_ resource.Resource                = &InferenceCapacityClaimResource{}
	_ resource.ResourceWithImportState = &InferenceCapacityClaimResource{}

	errCapacityClaimFailed = errors.New("inference capacity claim entered a failed state")
)

func NewInferenceCapacityClaimResource() resource.Resource {
	return &InferenceCapacityClaimResource{}
}

type InferenceCapacityClaimResource struct {
	client *coreweave.InferenceClient
}

// Nested model types.

type CapacityClaimResourcesModel struct {
	InstanceType  types.String `tfsdk:"instance_type"`
	InstanceCount types.Int64  `tfsdk:"instance_count"`
	CapacityType  types.String `tfsdk:"capacity_type"`
	Zones         types.Set    `tfsdk:"zones"`
}

// InferenceCapacityClaimResourceModel is the top-level Terraform state model.
type InferenceCapacityClaimResourceModel struct {
	// Computed
	ID                 types.String `tfsdk:"id"`
	OrganizationID     types.String `tfsdk:"organization_id"`
	Status             types.String `tfsdk:"status"`
	CreatedAt          types.String `tfsdk:"created_at"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
	Conditions         types.List   `tfsdk:"conditions"`
	AllocatedInstances types.Int64  `tfsdk:"allocated_instances"`
	PendingInstances   types.Int64  `tfsdk:"pending_instances"`
	// Required
	Name      types.String                 `tfsdk:"name"`
	Resources *CapacityClaimResourcesModel `tfsdk:"resources"`
}

func (r *InferenceCapacityClaimResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inference_capacity_claim"
}

func (r *InferenceCapacityClaimResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Create and manage [CoreWeave Managed Inference](https://docs.coreweave.com/products/inference) capacity claims for [Dedicated Inference](https://docs.coreweave.com/products/inference/dedicated). See [capacity claims](https://docs.coreweave.com/products/inference/scaling#capacity-claims) for capacity type details.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier of the capacity claim.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"organization_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The organization ID that owns the capacity claim.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The current status of the capacity claim. See the [Inference API overview](https://docs.coreweave.com/products/inference/reference/api-overview#status-values) for status values.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of when the capacity claim was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of when the capacity claim was last updated.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"conditions": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Detailed status conditions for the capacity claim.",
				PlanModifiers:       []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The condition type (e.g. `Ready`, `Progressing`).",
						},
						"status": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The condition status (`True`, `False`, or `Unknown`).",
						},
						"last_update_time": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC3339 timestamp of the last condition transition.",
						},
						"reason": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "A short, machine-readable reason for the condition's last transition.",
						},
						"message": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "A human-readable message about the condition's last transition.",
						},
					},
				},
			},
			"allocated_instances": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The number of instances currently allocated.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"pending_instances": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The number of instances pending allocation.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the capacity claim. Must be a valid hostname label.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.RegexMatches(hostnamePattern, "must be a valid hostname label"),
				},
			},
			"resources": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Resource configuration for the capacity claim.",
				Attributes: map[string]schema.Attribute{
					"instance_type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The instance type to reserve (e.g. `gb200-4x`).",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"instance_count": schema.Int64Attribute{
						Required:            true,
						MarkdownDescription: "The number of instances to reserve. Must be at least 1.",
						Validators: []validator.Int64{
							int64validator.AtLeast(1),
						},
					},
					"capacity_type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: fmt.Sprintf("The [capacity type](https://docs.coreweave.com/products/inference/scaling#capacity-claims) for the capacity claim. Must be one of: %s. `CAPACITY_TYPE_SERVERLESS` is deprecated and is no longer accepted. Existing claims were automatically migrated to `CAPACITY_TYPE_MANAGED`; update your configuration to use `CAPACITY_TYPE_MANAGED`.", coreweave.EnumMarkdownValuesExcludingDeprecated(inferencev1.CapacityType_CAPACITY_TYPE_MANAGED.Descriptor())),
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
						Validators: []validator.String{
							stringvalidator.OneOf(coreweave.EnumValues(inferencev1.CapacityType_name, true)...),
							// Reject deprecated values (e.g. CAPACITY_TYPE_SERVERLESS) at plan
							// time: the API rejects them, and with RequiresReplace a deprecated
							// value would otherwise produce a destructive, non-convergent plan.
							cwvalidators.RejectDeprecatedEnumValue(
								inferencev1.CapacityType_CAPACITY_TYPE_MANAGED.Descriptor(),
								`Change capacity_type to CAPACITY_TYPE_MANAGED. Existing claims were automatically migrated; no replacement will be triggered.`,
							),
						},
					},
					"zones": schema.SetAttribute{
						ElementType:         types.StringType,
						Required:            true,
						MarkdownDescription: "The availability zones where the capacity claim may use resources from (e.g. `US-WEST-04A`). At least one is required.",
						PlanModifiers:       []planmodifier.Set{setplanmodifier.RequiresReplace()},
						Validators: []validator.Set{
							setvalidator.SizeAtLeast(1),
						},
					},
				},
			},
		},
	}
}

func (r *InferenceCapacityClaimResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	r.client = client.Inference
}

func (r *InferenceCapacityClaimResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data InferenceCapacityClaimResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq, diags := toCreateCapacityClaimRequest(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.client.CreateCapacityClaim(ctx, connect.NewRequest(createReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// Save initial state before polling so the resource is tracked even if polling fails.
	resp.Diagnostics.Append(setFromCapacityClaim(&data, createResp.Msg.GetCapacityClaim(), false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	claimID := createResp.Msg.GetCapacityClaim().GetSpec().GetId()

	conf := retry.StateChangeConf{
		Pending: []string{
			inferencev1.Status_STATUS_CREATING.String(),
			inferencev1.Status_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{
			inferencev1.Status_STATUS_READY.String(),
		},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetCapacityClaim(ctx, connect.NewRequest(&inferencev1.GetCapacityClaimRequest{
				Id: claimID,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to poll capacity claim", map[string]interface{}{"error": err.Error()})
				return nil, inferencev1.Status_STATUS_UNSPECIFIED.String(), err
			}
			cc := getResp.Msg.GetCapacityClaim()
			status := cc.GetStatus().GetStatus()
			if status == inferencev1.Status_STATUS_ERROR || status == inferencev1.Status_STATUS_FAILED {
				return cc, status.String(), errCapacityClaimFailed
			}
			return cc, status.String(), nil
		},
		Timeout:    45 * time.Minute,
		MinTimeout: 5 * time.Second,
	}

	raw, err := conf.WaitForStateContext(ctx)
	if err != nil && !errors.Is(err, errCapacityClaimFailed) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	cc, ok := raw.(*inferencev1.CapacityClaim)
	if !ok {
		resp.Diagnostics.AddError("Unexpected polling result type", "Expected *inferencev1.CapacityClaim. Please report this issue to the provider developers.")
		return
	}

	if cc.GetStatus().GetStatus() == inferencev1.Status_STATUS_ERROR ||
		cc.GetStatus().GetStatus() == inferencev1.Status_STATUS_FAILED {
		resp.Diagnostics.AddError("Capacity claim creation failed",
			fmt.Sprintf("Capacity claim entered status %s. You must destroy and recreate this resource.", cc.GetStatus().GetStatus().String()))
		return
	}

	resp.Diagnostics.Append(setFromCapacityClaim(&data, cc, false)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InferenceCapacityClaimResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data InferenceCapacityClaimResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := r.client.GetCapacityClaim(ctx, connect.NewRequest(&inferencev1.GetCapacityClaimRequest{
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

	resp.Diagnostics.Append(setFromCapacityClaim(&data, getResp.Msg.GetCapacityClaim(), false)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InferenceCapacityClaimResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data InferenceCapacityClaimResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq, diags := toUpdateCapacityClaimRequest(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateResp, err := r.client.UpdateCapacityClaim(ctx, connect.NewRequest(updateReq))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	// Save intermediate state before polling so an in-flight update is not lost
	// if polling fails or times out.
	resp.Diagnostics.Append(setFromCapacityClaim(&data, updateResp.Msg.GetCapacityClaim(), true)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	claimID := updateResp.Msg.GetCapacityClaim().GetSpec().GetId()

	conf := retry.StateChangeConf{
		Pending: []string{
			inferencev1.Status_STATUS_UPDATING.String(),
			inferencev1.Status_STATUS_CREATING.String(),
			inferencev1.Status_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{
			inferencev1.Status_STATUS_READY.String(),
		},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetCapacityClaim(ctx, connect.NewRequest(&inferencev1.GetCapacityClaimRequest{
				Id: claimID,
			}))
			if err != nil {
				tflog.Error(ctx, "failed to poll capacity claim", map[string]interface{}{"error": err.Error()})
				return nil, inferencev1.Status_STATUS_UNSPECIFIED.String(), err
			}
			cc := getResp.Msg.GetCapacityClaim()
			status := cc.GetStatus().GetStatus()
			if status == inferencev1.Status_STATUS_ERROR || status == inferencev1.Status_STATUS_FAILED {
				return cc, status.String(), errCapacityClaimFailed
			}
			return cc, status.String(), nil
		},
		Timeout:    20 * time.Minute,
		MinTimeout: 5 * time.Second,
	}

	raw, err := conf.WaitForStateContext(ctx)
	if err != nil && !errors.Is(err, errCapacityClaimFailed) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	cc, ok := raw.(*inferencev1.CapacityClaim)
	if !ok {
		resp.Diagnostics.AddError("Unexpected polling result type", "Expected *inferencev1.CapacityClaim. Please report this issue to the provider developers.")
		return
	}

	if cc.GetStatus().GetStatus() == inferencev1.Status_STATUS_ERROR ||
		cc.GetStatus().GetStatus() == inferencev1.Status_STATUS_FAILED {
		resp.Diagnostics.AddError("Capacity claim update failed",
			fmt.Sprintf("Capacity claim entered status %s. Check the `conditions` attribute for details.", cc.GetStatus().GetStatus().String()))
		return
	}

	resp.Diagnostics.Append(setFromCapacityClaim(&data, cc, true)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InferenceCapacityClaimResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data InferenceCapacityClaimResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	claimID := data.ID.ValueString()

	_, err := r.client.DeleteCapacityClaim(ctx, connect.NewRequest(&inferencev1.DeleteCapacityClaimRequest{
		Id: claimID,
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
	const deletedState = "NOT_FOUND"

	conf := retry.StateChangeConf{
		Pending: []string{
			inferencev1.Status_STATUS_DELETING.String(),
			inferencev1.Status_STATUS_UNSPECIFIED.String(),
		},
		Target: []string{deletedState},
		Refresh: func() (interface{}, string, error) {
			getResp, err := r.client.GetCapacityClaim(ctx, connect.NewRequest(&inferencev1.GetCapacityClaimRequest{
				Id: claimID,
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return struct{}{}, deletedState, nil
				}
				tflog.Error(ctx, "failed to poll capacity claim deletion", map[string]interface{}{"error": err.Error()})
				return nil, inferencev1.Status_STATUS_UNSPECIFIED.String(), err
			}
			cc := getResp.Msg.GetCapacityClaim()
			return cc, cc.GetStatus().GetStatus().String(), nil
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

func (r *InferenceCapacityClaimResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- Helpers ---

// capacityClaimFields holds the proto sub-messages shared between CreateCapacityClaimRequest
// and UpdateCapacityClaimRequest. Use buildCapacityClaimFields to populate it.
type capacityClaimFields struct {
	Name      string
	Resources *inferencev1.CapacityClaimResources
}

// buildCapacityClaimFields extracts the fields common to both create and update requests
// from the Terraform resource model.
func buildCapacityClaimFields(ctx context.Context, m *InferenceCapacityClaimResourceModel) (capacityClaimFields, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	zones := []string{}
	diagnostics.Append(m.Resources.Zones.ElementsAs(ctx, &zones, false)...)
	if diagnostics.HasError() {
		return capacityClaimFields{}, diagnostics
	}

	capacityTypeStr := m.Resources.CapacityType.ValueString()
	capacityTypeVal, ok := inferencev1.CapacityType_value[capacityTypeStr]
	if !ok {
		diagnostics.AddError("Invalid capacity_type", fmt.Sprintf("Invalid capacity_type: %s. Must be one of: %s.", capacityTypeStr, coreweave.EnumMarkdownValues(inferencev1.CapacityType_name, true)))
		return capacityClaimFields{}, diagnostics
	}

	f := capacityClaimFields{
		Name: m.Name.ValueString(),
		Resources: &inferencev1.CapacityClaimResources{
			InstanceId:    m.Resources.InstanceType.ValueString(),
			InstanceCount: uint32(m.Resources.InstanceCount.ValueInt64()), //nolint:gosec
			CapacityType:  inferencev1.CapacityType(capacityTypeVal),
			Zones:         zones,
		},
	}

	return f, diagnostics
}

func toCreateCapacityClaimRequest(ctx context.Context, m *InferenceCapacityClaimResourceModel) (*inferencev1.CreateCapacityClaimRequest, diag.Diagnostics) {
	f, diags := buildCapacityClaimFields(ctx, m)
	if diags.HasError() {
		return nil, diags
	}
	return &inferencev1.CreateCapacityClaimRequest{
		Name:      f.Name,
		Resources: f.Resources,
	}, diags
}

func toUpdateCapacityClaimRequest(ctx context.Context, m *InferenceCapacityClaimResourceModel) (*inferencev1.UpdateCapacityClaimRequest, diag.Diagnostics) {
	f, diags := buildCapacityClaimFields(ctx, m)
	if diags.HasError() {
		return nil, diags
	}
	return &inferencev1.UpdateCapacityClaimRequest{
		Id:        m.ID.ValueString(),
		Resources: f.Resources,
	}, diags
}

// setFromCapacityClaim populates all fields on the model from a proto CapacityClaim response.
//
// When preserveStatusFields is true, the observed status fields (status, allocated_instances,
// pending_instances, updated_at, conditions) are left untouched on the model. These fields
// carry UseStateForUnknown plan modifiers, so during Update the plan holds their prior-state
// values; overwriting them with the fresh API response would violate Terraform's plan/apply
// consistency check. Read always refreshes them (preserveStatusFields=false).
func setFromCapacityClaim(m *InferenceCapacityClaimResourceModel, cc *inferencev1.CapacityClaim, preserveStatusFields bool) (diagnostics diag.Diagnostics) {
	spec := cc.GetSpec()
	status := cc.GetStatus()

	m.ID = types.StringValue(spec.GetId())
	m.OrganizationID = types.StringValue(spec.GetOrganizationId())
	m.Name = types.StringValue(spec.GetName())

	if !preserveStatusFields {
		m.Status = types.StringValue(status.GetStatus().String())
		m.AllocatedInstances = types.Int64Value(int64(status.GetAllocatedInstances()))
		m.PendingInstances = types.Int64Value(int64(status.GetPendingInstances()))
	}

	if status.GetCreatedAt() != nil {
		m.CreatedAt = types.StringValue(status.GetCreatedAt().AsTime().Format(time.RFC3339))
	}
	if !preserveStatusFields && status.GetUpdatedAt() != nil {
		m.UpdatedAt = types.StringValue(status.GetUpdatedAt().AsTime().Format(time.RFC3339))
	}

	// resources
	if res := spec.GetResources(); res != nil {
		if m.Resources == nil {
			m.Resources = &CapacityClaimResourcesModel{}
		}
		m.Resources.InstanceType = types.StringValue(res.GetInstanceId())
		m.Resources.InstanceCount = types.Int64Value(int64(res.GetInstanceCount()))
		m.Resources.CapacityType = types.StringValue(res.GetCapacityType().String())

		zoneVals := make([]attr.Value, len(res.GetZones()))
		for i, z := range res.GetZones() {
			zoneVals[i] = types.StringValue(z)
		}
		zoneSet, diags := types.SetValue(types.StringType, zoneVals)
		diagnostics.Append(diags...)
		m.Resources.Zones = zoneSet
	}

	// conditions
	if !preserveStatusFields {
		condList, diags := conditionsListFromStatus(status.GetConditions())
		diagnostics.Append(diags...)
		m.Conditions = condList
	}

	return diagnostics
}
