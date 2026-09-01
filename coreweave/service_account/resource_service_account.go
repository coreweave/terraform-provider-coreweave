package serviceaccount

import (
	"context"
	"fmt"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &ServiceAccountResource{}
	_ resource.ResourceWithConfigure   = &ServiceAccountResource{}
	_ resource.ResourceWithImportState = &ServiceAccountResource{}
)

type ServiceAccountResource struct {
	client *coreweave.Client
}

type ServiceAccountResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	UID            types.String `tfsdk:"uid"`
	OrganizationID types.String `tfsdk:"organization_id"`
	Creator        types.String `tfsdk:"creator"`
	DisplayName    types.String `tfsdk:"display_name"`
	SystemManaged  types.Bool   `tfsdk:"system_managed"`
	Active         types.Bool   `tfsdk:"active"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

func NewServiceAccountResource() resource.Resource {
	return &ServiceAccountResource{}
}

func (r *ServiceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *ServiceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a customer-owned CoreWeave service account.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The service account resource name used by Terraform and the API, in `serviceAccounts/{service_account}` format.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The service account resource name, in `serviceAccounts/{service_account}` format.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"uid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The server-generated unique service account identifier.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The identifier of the organization that owns the service account.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"creator": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The user resource name that created the service account.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"display_name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "An optional, mutable human-readable name describing the service account's purpose. When omitted, the server may choose a value. Set it to an empty string to clear it explicitly.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"system_managed": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the service account is managed by CoreWeave backend logic. Terraform only manages customer-owned accounts.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"active": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Whether the service account may authenticate. When omitted, the server chooses the initial value and Terraform preserves it on later plans. The current API creates accounts active and applies `active = false` through an immediate, non-atomic deactivation call.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 timestamp at which the service account was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 timestamp at which the service account was last modified.",
			},
		},
	}
}

func (r *ServiceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (m *ServiceAccountResourceModel) ToCreateRequest() *coreweave.CreateServiceAccountRequest {
	account := &coreweave.ServiceAccount{}
	if !m.DisplayName.IsNull() && !m.DisplayName.IsUnknown() {
		account.DisplayName = m.DisplayName.ValueStringPointer()
	}
	return &coreweave.CreateServiceAccountRequest{ServiceAccount: account}
}

func (m *ServiceAccountResourceModel) ToDisplayNameUpdateRequest(state *ServiceAccountResourceModel) *coreweave.UpdateServiceAccountRequest {
	if m.DisplayName.IsUnknown() || m.DisplayName.Equal(state.DisplayName) {
		return nil
	}
	account := &coreweave.ServiceAccount{Name: state.ID.ValueString()}
	if !m.DisplayName.IsNull() {
		account.DisplayName = m.DisplayName.ValueStringPointer()
	}
	return &coreweave.UpdateServiceAccountRequest{
		ServiceAccount: account,
		UpdateMask:     "displayName",
	}
}

func timestampValue(value *time.Time) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(value.UTC().Format(time.RFC3339))
}

func (m *ServiceAccountResourceModel) SetFromAPI(account *coreweave.ServiceAccount) error {
	if account == nil || account.Name == "" {
		return fmt.Errorf("service account resource name is missing")
	}
	if account.UID == nil || *account.UID == "" {
		return fmt.Errorf("service account UID is missing")
	}
	if account.SystemManaged != nil && *account.SystemManaged {
		return fmt.Errorf("system-managed service accounts cannot be managed by Terraform")
	}

	m.ID = types.StringValue(account.Name)
	m.Name = types.StringValue(account.Name)
	m.UID = types.StringValue(*account.UID)
	if account.OrganizationID == nil {
		m.OrganizationID = types.StringNull()
	} else {
		m.OrganizationID = types.StringValue(*account.OrganizationID)
	}
	if account.Creator == nil {
		m.Creator = types.StringNull()
	} else {
		m.Creator = types.StringValue(*account.Creator)
	}
	if account.DisplayName == nil {
		m.DisplayName = types.StringNull()
	} else {
		m.DisplayName = types.StringValue(*account.DisplayName)
	}
	m.SystemManaged = types.BoolValue(false)
	m.Active = types.BoolValue(account.Active != nil && *account.Active)
	m.CreatedAt = timestampValue(account.CreateTime)
	m.UpdatedAt = timestampValue(account.UpdateTime)
	return nil
}

func (m *ServiceAccountResourceModel) FillUnknownFromAPI(account *coreweave.ServiceAccount) error {
	var remote ServiceAccountResourceModel
	if err := remote.SetFromAPI(account); err != nil {
		return err
	}
	if m.ID.IsUnknown() {
		m.ID = remote.ID
	}
	if m.Name.IsUnknown() {
		m.Name = remote.Name
	}
	if m.UID.IsUnknown() {
		m.UID = remote.UID
	}
	if m.OrganizationID.IsUnknown() {
		m.OrganizationID = remote.OrganizationID
	}
	if m.Creator.IsUnknown() {
		m.Creator = remote.Creator
	}
	if m.DisplayName.IsUnknown() {
		m.DisplayName = remote.DisplayName
	}
	if m.SystemManaged.IsUnknown() {
		m.SystemManaged = remote.SystemManaged
	}
	if m.Active.IsUnknown() {
		m.Active = remote.Active
	}
	if m.CreatedAt.IsUnknown() {
		m.CreatedAt = remote.CreatedAt
	}
	if m.UpdatedAt.IsUnknown() {
		m.UpdatedAt = remote.UpdatedAt
	}
	return nil
}

func (r *ServiceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ServiceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	wantedActive := plan.Active
	account, err := r.client.CreateServiceAccount(ctx, plan.ToCreateRequest())
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := plan.SetFromAPI(account); err != nil {
		resp.Diagnostics.AddError(
			"Invalid Service Account Creation Response",
			fmt.Sprintf("The directory API created a service account but returned an invalid response: %s. If the account exists remotely, import it before applying again.", err),
		)
		return
	}
	// Persist the created identity before a separate lifecycle transition so a
	// failed activate/deactivate call cannot orphan the remote account.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() || wantedActive.IsNull() || wantedActive.IsUnknown() || wantedActive.Equal(plan.Active) {
		return
	}

	if wantedActive.ValueBool() {
		account, err = r.client.ActivateServiceAccount(ctx, plan.ID.ValueString())
	} else {
		account, err = r.client.DeactivateServiceAccount(ctx, plan.ID.ValueString())
	}
	if err != nil {
		cleanupErr := r.client.DeleteServiceAccount(ctx, plan.ID.ValueString())
		if cleanupErr == nil || coreweave.IsNotFoundError(cleanupErr) {
			resp.State.RemoveResource(ctx)
			resp.Diagnostics.AddError(
				"Unable to Set Initial Service Account Activation State",
				fmt.Sprintf("The directory API created the service account but could not set active to %t: %s. Terraform deleted the new account to avoid leaving it in the wrong activation state.", wantedActive.ValueBool(), err),
			)
			return
		}
		resp.Diagnostics.AddError(
			"Unable to Set Initial Service Account Activation State",
			fmt.Sprintf("The directory API created the service account but could not set active to %t: %s. Terraform also could not delete the new account: %s. The created account remains in state so it can be reconciled on the next apply.", wantedActive.ValueBool(), err, cleanupErr),
		)
		return
	}
	if err := plan.SetFromAPI(account); err != nil {
		resp.Diagnostics.AddError("Invalid Service Account Response", fmt.Sprintf("The directory API returned an invalid service account after changing its activation state: %s.", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ServiceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ServiceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	account, err := r.client.GetServiceAccount(ctx, state.ID.ValueString())
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := state.SetFromAPI(account); err != nil {
		resp.Diagnostics.AddError("Invalid Service Account Response", fmt.Sprintf("The directory API returned an invalid service account while reading state: %s.", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ServiceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ServiceAccountResourceModel
	var state ServiceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var account *coreweave.ServiceAccount
	if update := plan.ToDisplayNameUpdateRequest(&state); update != nil {
		var err error
		account, err = r.client.UpdateServiceAccount(ctx, update)
		if err != nil {
			r.handleUpdateError(ctx, err, &resp.Diagnostics)
			return
		}
	}
	if !plan.Active.IsNull() && !plan.Active.IsUnknown() && !plan.Active.Equal(state.Active) {
		var err error
		if plan.Active.ValueBool() {
			account, err = r.client.ActivateServiceAccount(ctx, state.ID.ValueString())
		} else {
			account, err = r.client.DeactivateServiceAccount(ctx, state.ID.ValueString())
		}
		if err != nil {
			r.handleUpdateError(ctx, err, &resp.Diagnostics)
			return
		}
	}

	if account != nil {
		if err := plan.SetFromAPI(account); err != nil {
			resp.Diagnostics.AddError("Invalid Service Account Response", fmt.Sprintf("The directory API returned an invalid service account after an update: %s.", err))
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	account, err := r.client.GetServiceAccount(ctx, state.ID.ValueString())
	if err != nil {
		r.handleUpdateError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := plan.FillUnknownFromAPI(account); err != nil {
		resp.Diagnostics.AddError("Invalid Service Account Response", fmt.Sprintf("The directory API returned an invalid service account after a no-op update: %s.", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ServiceAccountResource) handleUpdateError(ctx context.Context, err error, diagnostics *diag.Diagnostics) {
	if coreweave.IsNotFoundError(err) {
		diagnostics.AddError(
			"Unable to Update Service Account",
			fmt.Sprintf("The directory API returned Not Found while Terraform was applying an update. Terraform retained the prior state so the next refresh can determine whether the service account was deleted or is temporarily inaccessible. API error: %s", err),
		)
		return
	}
	coreweave.HandleAPIError(ctx, err, diagnostics)
}

func (r *ServiceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ServiceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteServiceAccount(ctx, state.ID.ValueString()); err != nil && !coreweave.IsNotFoundError(err) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *ServiceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
