package serviceaccount

import (
	"context"
	"fmt"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const (
	serviceAccountCreateReadBackTimeout      = 30 * time.Second
	serviceAccountCreateReadBackInitialDelay = 250 * time.Millisecond
	serviceAccountCreateReadBackMaxDelay     = 5 * time.Second
)

type serviceAccountReadBackPolicy struct {
	timeout      time.Duration
	initialDelay time.Duration
	maxDelay     time.Duration
}

func defaultServiceAccountReadBackPolicy() serviceAccountReadBackPolicy {
	return serviceAccountReadBackPolicy{
		timeout:      serviceAccountCreateReadBackTimeout,
		initialDelay: serviceAccountCreateReadBackInitialDelay,
		maxDelay:     serviceAccountCreateReadBackMaxDelay,
	}
}

var (
	_ resource.Resource                = &ServiceAccountResource{}
	_ resource.ResourceWithConfigure   = &ServiceAccountResource{}
	_ resource.ResourceWithImportState = &ServiceAccountResource{}
)

type ServiceAccountResource struct {
	client         *coreweave.Client
	readBackPolicy serviceAccountReadBackPolicy
}

type ServiceAccountResourceModel struct {
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	Name           types.String `tfsdk:"name"`
	UID            types.String `tfsdk:"uid"`
	Creator        types.String `tfsdk:"creator"`
	DisplayName    types.String `tfsdk:"display_name"`
	SystemManaged  types.Bool   `tfsdk:"system_managed"`
	Active         types.Bool   `tfsdk:"active"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

func NewServiceAccountResource() resource.Resource {
	return newServiceAccountResource(defaultServiceAccountReadBackPolicy())
}

func newServiceAccountResource(policy serviceAccountReadBackPolicy) resource.Resource {
	return &ServiceAccountResource{readBackPolicy: policy}
}

func (r *ServiceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_iam_service_account"
}

func (r *ServiceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	immutable := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computed := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a CoreWeave service account.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required: true, PlanModifiers: immutable,
				MarkdownDescription: "The caller-chosen service account ID used as the final segment of the resource name.",
				Validators:          []validator.String{stringvalidator.RegexMatches(resourceIDPattern, "must start with a lowercase letter, contain only lowercase letters, digits, or hyphens, end with a letter or digit, and be at most 63 characters")},
			},
			"organization_id": schema.StringAttribute{
				Required: true, PlanModifiers: immutable,
				MarkdownDescription: "The six-character ID of the CoreWeave organization that owns the service account.",
				Validators:          []validator.String{stringvalidator.RegexMatches(organizationIDPattern, "must contain exactly six lowercase letters or digits")},
			},
			"name":    schema.StringAttribute{Computed: true, MarkdownDescription: "The canonical resource name.", PlanModifiers: computed},
			"uid":     schema.StringAttribute{Computed: true, MarkdownDescription: "The server-generated unique identifier.", PlanModifiers: computed},
			"creator": schema.StringAttribute{Computed: true, MarkdownDescription: "The canonical resource name of the user that created the service account.", PlanModifiers: computed},
			"display_name": schema.StringAttribute{
				Optional: true, MarkdownDescription: "An optional human-readable display name.",
				Validators: []validator.String{stringvalidator.UTF8LengthAtMost(63)},
			},
			"system_managed": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether CoreWeave or an organization administrator manages this service account.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"active": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the service account may authenticate.",
			},
			"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "The RFC 3339 creation timestamp.", PlanModifiers: computed},
			"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "The RFC 3339 last-update timestamp."},
		},
	}
}

func (r *ServiceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *coreweave.Client, got %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (m *ServiceAccountResourceModel) setFromProto(account *directoryv1alpha.ServiceAccount) error {
	if account == nil {
		return fmt.Errorf("service account is missing")
	}
	if account.GetName() == "" {
		return fmt.Errorf("service account name is missing")
	}
	organizationID, id, err := parseServiceAccountName(account.GetName())
	if err != nil {
		return err
	}
	if account.GetUid() == "" {
		return fmt.Errorf("service account UID is missing")
	}
	m.ID = types.StringValue(id)
	m.OrganizationID = types.StringValue(organizationID)
	m.Name = types.StringValue(account.GetName())
	m.UID = types.StringValue(account.GetUid())
	if account.HasCreator() {
		m.Creator = types.StringValue(account.GetCreator())
	} else {
		m.Creator = types.StringNull()
	}
	if account.HasDisplayName() {
		m.DisplayName = types.StringValue(account.GetDisplayName())
	} else {
		m.DisplayName = types.StringNull()
	}
	m.SystemManaged = types.BoolValue(account.GetSystemManaged())
	m.Active = types.BoolValue(account.GetActive())
	m.CreatedAt = timestampValue(account.GetCreateTime())
	m.UpdatedAt = timestampValue(account.GetUpdateTime())
	return nil
}

// fillUnknownFromProto resolves only attributes that remain unknown in the
// plan. It is used when an update request resolves to a no-op so that remote
// drift cannot overwrite values Terraform already planned to preserve.
func (m *ServiceAccountResourceModel) fillUnknownFromProto(account *directoryv1alpha.ServiceAccount) error {
	if account == nil {
		return fmt.Errorf("service account is missing")
	}
	if account.GetName() == "" {
		return fmt.Errorf("service account name is missing")
	}
	organizationID, id, err := parseServiceAccountName(account.GetName())
	if err != nil {
		return err
	}
	if account.GetUid() == "" {
		return fmt.Errorf("service account UID is missing")
	}
	if m.ID.IsUnknown() {
		m.ID = types.StringValue(id)
	}
	if m.OrganizationID.IsUnknown() {
		m.OrganizationID = types.StringValue(organizationID)
	}
	if m.Name.IsUnknown() {
		m.Name = types.StringValue(account.GetName())
	}
	if m.UID.IsUnknown() {
		m.UID = types.StringValue(account.GetUid())
	}
	if m.Creator.IsUnknown() {
		if account.HasCreator() {
			m.Creator = types.StringValue(account.GetCreator())
		} else {
			m.Creator = types.StringNull()
		}
	}
	if m.DisplayName.IsUnknown() {
		if account.HasDisplayName() {
			m.DisplayName = types.StringValue(account.GetDisplayName())
		} else {
			m.DisplayName = types.StringNull()
		}
	}
	if m.SystemManaged.IsUnknown() {
		m.SystemManaged = types.BoolValue(account.GetSystemManaged())
	}
	if m.Active.IsUnknown() {
		m.Active = types.BoolValue(account.GetActive())
	}
	if m.CreatedAt.IsUnknown() {
		m.CreatedAt = timestampValue(account.GetCreateTime())
	}
	if m.UpdatedAt.IsUnknown() {
		m.UpdatedAt = timestampValue(account.GetUpdateTime())
	}
	return nil
}

func (m *ServiceAccountResourceModel) createRequest() *directoryv1alpha.CreateServiceAccountRequest {
	account := &directoryv1alpha.ServiceAccount{}
	if !m.DisplayName.IsNull() && !m.DisplayName.IsUnknown() {
		account.SetDisplayName(m.DisplayName.ValueString())
	}
	request := &directoryv1alpha.CreateServiceAccountRequest{ServiceAccount: account}
	request.SetParent("organizations/" + m.OrganizationID.ValueString())
	request.SetServiceAccountId(m.ID.ValueString())
	return request
}

// readBackCreatedServiceAccount retries only NotFound because a successful
// create may become visible to reads shortly after the write completes. The
// shared client owns transport retries, so other codes, including Unavailable,
// are returned immediately.
func readBackCreatedServiceAccount(
	ctx context.Context,
	policy serviceAccountReadBackPolicy,
	get func(context.Context) (*directoryv1alpha.ServiceAccount, error),
) (*directoryv1alpha.ServiceAccount, error) {
	retryCtx, cancel := context.WithTimeout(ctx, policy.timeout)
	defer cancel()

	delay := policy.initialDelay
	for {
		account, err := get(retryCtx)
		if err == nil {
			return account, nil
		}
		if retryCtx.Err() != nil {
			return nil, readBackError(policy, err, ctx.Err(), retryCtx.Err())
		}
		if !coreweave.IsNotFoundError(err) {
			return nil, err
		}

		timer := time.NewTimer(delay)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return nil, readBackError(policy, err, ctx.Err(), retryCtx.Err())
		case <-timer.C:
		}

		if delay < policy.maxDelay {
			delay *= 2
			if delay > policy.maxDelay {
				delay = policy.maxDelay
			}
		}
	}
}

func readBackError(policy serviceAccountReadBackPolicy, lastErr, parentErr, retryErr error) error {
	if parentErr != nil {
		return fmt.Errorf("service account read-back was canceled (last error: %v): %w", lastErr, parentErr)
	}
	return fmt.Errorf("service account could not be read back within %s (last error: %v): %w", policy.timeout, lastErr, retryErr)
}

func (r *ServiceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ServiceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	account, err := r.client.Directory.CreateServiceAccount(ctx, data.createRequest())
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if responseErr := data.setFromProto(account); responseErr != nil {
		name := fmt.Sprintf("organizations/%s/serviceAccounts/%s", data.OrganizationID.ValueString(), data.ID.ValueString())
		readBack, readErr := readBackCreatedServiceAccount(ctx, r.readBackPolicy, func(readCtx context.Context) (*directoryv1alpha.ServiceAccount, error) {
			return r.client.Directory.GetServiceAccount(readCtx, &directoryv1alpha.GetServiceAccountRequest{Name: &name})
		})
		if readErr == nil {
			if recoveryErr := data.setFromProto(readBack); recoveryErr == nil {
				resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
				return
			} else {
				responseErr = fmt.Errorf("create response was invalid (%w) and the subsequent read returned an invalid service account (%w)", responseErr, recoveryErr)
			}
		} else {
			responseErr = fmt.Errorf("create response was invalid (%w) and the subsequent read failed (%w)", responseErr, readErr)
		}

		// Preserve the deterministic identity so Terraform can refresh or destroy
		// the successfully created account even when both API responses are invalid.
		data.Name = types.StringValue(name)
		data.UID = types.StringNull()
		data.Creator = types.StringNull()
		data.SystemManaged = types.BoolNull()
		data.Active = types.BoolNull()
		data.CreatedAt = types.StringNull()
		data.UpdatedAt = types.StringNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.AddError(
			"Unable to Read Service Account After Creation",
			fmt.Sprintf("The Directory API created %q, but Terraform could not read it back into a usable state during the %s recovery window: %s. Terraform preserved the canonical name in state, but the error marks the resource as tainted, so the next apply will delete and recreate it. To keep the existing account, run `terraform untaint <resource-address>` and then `terraform apply -refresh-only` to recover its computed attributes before continuing.", name, r.readBackPolicy.timeout, responseErr),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ServiceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ServiceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := data.Name.ValueString()
	account, err := r.client.Directory.GetServiceAccount(ctx, &directoryv1alpha.GetServiceAccountRequest{Name: &name})
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := data.setFromProto(account); err != nil {
		resp.Diagnostics.AddError("Unexpected API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ServiceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ServiceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := state.Name.ValueString()
	updateDisplayName := !plan.DisplayName.Equal(state.DisplayName) && !plan.DisplayName.IsUnknown()
	if !updateDisplayName {
		account, err := r.client.Directory.GetServiceAccount(ctx, &directoryv1alpha.GetServiceAccountRequest{Name: &name})
		if err != nil {
			coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
			return
		}
		if err := plan.fillUnknownFromProto(account); err != nil {
			resp.Diagnostics.AddError("Unexpected API Response", fmt.Sprintf("The Directory API returned an invalid service account after a no-op update: %s.", err))
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	updated := &directoryv1alpha.ServiceAccount{Name: name}
	if !plan.DisplayName.IsNull() {
		updated.SetDisplayName(plan.DisplayName.ValueString())
	}
	account, err := r.client.Directory.UpdateServiceAccount(ctx, &directoryv1alpha.UpdateServiceAccountRequest{
		ServiceAccount: updated, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"display_name"}},
	})
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := plan.setFromProto(account); err != nil {
		readBack, readErr := r.client.Directory.GetServiceAccount(ctx, &directoryv1alpha.GetServiceAccountRequest{Name: &name})
		if readErr != nil {
			coreweave.HandleAPIError(ctx, readErr, &resp.Diagnostics)
			return
		}
		if readBackErr := plan.setFromProto(readBack); readBackErr != nil {
			resp.Diagnostics.AddError("Unexpected API Response", fmt.Sprintf("The Directory API returned an invalid service account after update and the subsequent read could not recover it: %s.", readBackErr))
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ServiceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ServiceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := data.Name.ValueString()
	_, err := r.client.Directory.DeleteServiceAccount(ctx, &directoryv1alpha.DeleteServiceAccountRequest{Name: &name})
	if err != nil && !coreweave.IsNotFoundError(err) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
	}
}

func (r *ServiceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	organizationID, id, err := parseServiceAccountName(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Service Account Import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), organizationID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}
