package inference

import (
	"context"
	"errors"
	"fmt"
	"time"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
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
	InstanceID    types.String `tfsdk:"instance_id"`
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
	Name      types.String                `tfsdk:"name"`
	Resources *CapacityClaimResourcesModel `tfsdk:"resources"`
}

func (r *InferenceCapacityClaimResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inference_capacity_claim"
}

func (r *InferenceCapacityClaimResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Create and manage [CoreWeave Managed Inference](https://docs.coreweave.com/products/inference) capacity claims.",
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
				MarkdownDescription: "The current status of the capacity claim.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of when the capacity claim was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of when the capacity claim was last updated.",
			},
			"conditions": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Detailed status conditions for the capacity claim.",
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
					"instance_id": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The instance type to reserve by ID specifier (e.g. `gb200-4x`). Case insensitive.",
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
						MarkdownDescription: fmt.Sprintf("The capacity type for the capacity claim. Must be one of: %s.", coreweave.EnumMarkdownValues(inferencev1.CapacityType_name, true)),
					},
					"zones": schema.SetAttribute{
						ElementType:         types.StringType,
						Required:            true,
						MarkdownDescription: "The availability zones where the capacity claim may use resources from (e.g. `US-WEST-04A`). Case insensitive. At least one is required.",
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
	resp.Diagnostics.Append(setFromCapacityClaim(&data, createResp.Msg.GetCapacityClaim())...)
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
				tflog.Error(ctx, "failed to poll capacity claim", map[string]interface{}{"error": err})
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
	}

	resp.Diagnostics.Append(setFromCapacityClaim(&data, cc)...)
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

	resp.Diagnostics.Append(setFromCapacityClaim(&data, getResp.Msg.GetCapacityClaim())...)
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
	}

	resp.Diagnostics.Append(setFromCapacityClaim(&data, cc)...)
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
			InstanceId:    m.Resources.InstanceID.ValueString(),
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
func setFromCapacityClaim(m *InferenceCapacityClaimResourceModel, cc *inferencev1.CapacityClaim) (diagnostics diag.Diagnostics) {
	spec := cc.GetSpec()
	status := cc.GetStatus()

	m.ID = types.StringValue(spec.GetId())
	m.OrganizationID = types.StringValue(spec.GetOrganizationId())
	m.Name = types.StringValue(spec.GetName())
	m.Status = types.StringValue(status.GetStatus().String())
	m.AllocatedInstances = types.Int64Value(int64(status.GetAllocatedInstances()))
	m.PendingInstances = types.Int64Value(int64(status.GetPendingInstances()))

	if status.GetCreatedAt() != nil {
		m.CreatedAt = types.StringValue(status.GetCreatedAt().AsTime().Format(time.RFC3339))
	}
	if status.GetUpdatedAt() != nil {
		m.UpdatedAt = types.StringValue(status.GetUpdatedAt().AsTime().Format(time.RFC3339))
	}

	// resources
	if res := spec.GetResources(); res != nil {
		if m.Resources == nil {
			m.Resources = &CapacityClaimResourcesModel{}
		}
		m.Resources.InstanceID = types.StringValue(res.GetInstanceId())
		m.Resources.InstanceCount = types.Int64Value(int64(res.GetInstanceCount()))
		m.Resources.CapacityType = types.StringValue(res.GetCapacityType().String())

		zoneVals := make([]attr.Value, 0, len(res.GetZones()))
		for _, z := range res.GetZones() {
			zoneVals = append(zoneVals, types.StringValue(z))
		}
		zoneSet, diags := types.SetValue(types.StringType, zoneVals)
		diagnostics.Append(diags...)
		m.Resources.Zones = zoneSet
	}

	// conditions
	condVals := make([]attr.Value, 0, len(status.GetConditions()))
	for _, c := range status.GetConditions() {
		lastUpdate := ""
		if c.GetLastUpdateTime() != nil {
			lastUpdate = c.GetLastUpdateTime().AsTime().Format(time.RFC3339)
		}
		condObj, diags := types.ObjectValue(conditionAttrTypes, map[string]attr.Value{
			"type":             types.StringValue(c.GetType()),
			"status":           types.StringValue(c.GetStatus().String()),
			"last_update_time": types.StringValue(lastUpdate),
			"reason":           types.StringValue(c.GetReason()),
			"message":          types.StringValue(c.GetMessage()),
		})
		diagnostics.Append(diags...)
		condVals = append(condVals, condObj)
	}
	condList, diags := types.ListValue(types.ObjectType{AttrTypes: conditionAttrTypes}, condVals)
	diagnostics.Append(diags...)
	m.Conditions = condList

	return diagnostics
}

