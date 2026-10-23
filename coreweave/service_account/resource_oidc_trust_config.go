package serviceaccount

import (
	"context"
	"fmt"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var (
	_ resource.Resource                = &OIDCTrustConfigResource{}
	_ resource.ResourceWithConfigure   = &OIDCTrustConfigResource{}
	_ resource.ResourceWithImportState = &OIDCTrustConfigResource{}
)

type OIDCTrustConfigResource struct {
	client *coreweave.Client
}

type OIDCTrustConfigResourceModel struct {
	ID                             types.String `tfsdk:"id"`
	OrganizationID                 types.String `tfsdk:"organization_id"`
	ServiceAccountID               types.String `tfsdk:"service_account_id"`
	Name                           types.String `tfsdk:"name"`
	UID                            types.String `tfsdk:"uid"`
	Description                    types.String `tfsdk:"description"`
	CEL                            types.String `tfsdk:"cel"`
	WorkloadFederationOIDCConfigID types.String `tfsdk:"workload_federation_oidc_config_id"`
	CreatedAt                      types.String `tfsdk:"created_at"`
	UpdatedAt                      types.String `tfsdk:"updated_at"`
}

func NewOIDCTrustConfigResource() resource.Resource { return &OIDCTrustConfigResource{} }

func (r *OIDCTrustConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account_oidc_trust_config"
}

func (r *OIDCTrustConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	immutable := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	computed := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	idValidator := []validator.String{stringvalidator.RegexMatches(resourceIDPattern, "must start with a lowercase letter, contain only lowercase letters, digits, or hyphens, end with a letter or digit, and be at most 63 characters")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages an OIDC trust rule for a CoreWeave service account.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Required: true, PlanModifiers: immutable, Validators: idValidator, MarkdownDescription: "The caller-chosen trust configuration ID."},
			"organization_id": schema.StringAttribute{
				Required: true, PlanModifiers: immutable,
				Validators:          []validator.String{stringvalidator.RegexMatches(organizationIDPattern, "must contain exactly six lowercase letters or digits")},
				MarkdownDescription: "The six-character ID of the owning CoreWeave organization.",
			},
			"service_account_id":                 schema.StringAttribute{Required: true, PlanModifiers: immutable, Validators: idValidator, MarkdownDescription: "The ID of the service account that owns this trust configuration."},
			"name":                               schema.StringAttribute{Computed: true, PlanModifiers: computed, MarkdownDescription: "The canonical resource name."},
			"uid":                                schema.StringAttribute{Computed: true, PlanModifiers: computed, MarkdownDescription: "The server-generated unique identifier."},
			"description":                        schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Validators: []validator.String{stringvalidator.UTF8LengthAtMost(2048)}, MarkdownDescription: "An optional description of the trust rule. Defaults to an empty string."},
			"cel":                                schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.UTF8LengthBetween(1, 4096)}, MarkdownDescription: "The CEL expression evaluated against verified OIDC token claims exposed through the `token` namespace."},
			"workload_federation_oidc_config_id": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(uuidPattern, "must be a UUID")}, MarkdownDescription: "The UID of the workload federation OIDC configuration used to verify tokens."},
			"created_at":                         schema.StringAttribute{Computed: true, PlanModifiers: computed, MarkdownDescription: "The RFC 3339 creation timestamp."},
			"updated_at":                         schema.StringAttribute{Computed: true, PlanModifiers: computed, MarkdownDescription: "The RFC 3339 last-update timestamp."},
		},
	}
}

func (r *OIDCTrustConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (m *OIDCTrustConfigResourceModel) setFromProto(config *directoryv1alpha.ServiceAccountOidcTrustConfig) error {
	if config == nil {
		return fmt.Errorf("service account OIDC trust configuration is missing")
	}
	organizationID, serviceAccountID, id, err := parseOIDCTrustConfigName(config.GetName())
	if err != nil {
		return err
	}
	if config.GetUid() == "" {
		return fmt.Errorf("service account OIDC trust configuration UID is missing")
	}
	m.ID = types.StringValue(id)
	m.OrganizationID = types.StringValue(organizationID)
	m.ServiceAccountID = types.StringValue(serviceAccountID)
	m.Name = types.StringValue(config.GetName())
	m.UID = types.StringValue(config.GetUid())
	if config.HasDescription() {
		m.Description = types.StringValue(config.GetDescription())
	} else {
		m.Description = types.StringNull()
	}
	m.CEL = types.StringValue(config.GetCel())
	m.WorkloadFederationOIDCConfigID = types.StringValue(config.GetWorkloadFederationOidcConfigUid())
	m.CreatedAt = timestampValue(config.GetCreateTime())
	m.UpdatedAt = timestampValue(config.GetUpdateTime())
	return nil
}

func (m *OIDCTrustConfigResourceModel) createRequest() *directoryv1alpha.CreateServiceAccountOidcTrustConfigRequest {
	config := &directoryv1alpha.ServiceAccountOidcTrustConfig{}
	if !m.Description.IsNull() && !m.Description.IsUnknown() {
		config.SetDescription(m.Description.ValueString())
	}
	config.SetCel(m.CEL.ValueString())
	config.SetWorkloadFederationOidcConfigUid(m.WorkloadFederationOIDCConfigID.ValueString())
	request := &directoryv1alpha.CreateServiceAccountOidcTrustConfigRequest{ServiceAccountOidcTrustConfig: config}
	request.SetParent(serviceAccountName(m.OrganizationID.ValueString(), m.ServiceAccountID.ValueString()))
	request.SetServiceAccountOidcTrustConfigId(m.ID.ValueString())
	return request
}

func (r *OIDCTrustConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data OIDCTrustConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	config, err := r.client.Directory.CreateServiceAccountOidcTrustConfig(ctx, data.createRequest())
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := data.setFromProto(config); err != nil {
		resp.Diagnostics.AddError("Unexpected API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OIDCTrustConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OIDCTrustConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := data.Name.ValueString()
	config, err := r.client.Directory.GetServiceAccountOidcTrustConfig(ctx, &directoryv1alpha.GetServiceAccountOidcTrustConfigRequest{Name: &name})
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := data.setFromProto(config); err != nil {
		resp.Diagnostics.AddError("Unexpected API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OIDCTrustConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state OIDCTrustConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plannedUpdatedAt := plan.UpdatedAt
	config := &directoryv1alpha.ServiceAccountOidcTrustConfig{Name: state.Name.ValueString()}
	paths := make([]string, 0, 3)
	if !plan.Description.Equal(state.Description) && !plan.Description.IsUnknown() {
		if !plan.Description.IsNull() {
			config.SetDescription(plan.Description.ValueString())
		}
		paths = append(paths, "description")
	}
	if !plan.CEL.Equal(state.CEL) && !plan.CEL.IsUnknown() {
		config.SetCel(plan.CEL.ValueString())
		paths = append(paths, "cel")
	}
	if !plan.WorkloadFederationOIDCConfigID.Equal(state.WorkloadFederationOIDCConfigID) && !plan.WorkloadFederationOIDCConfigID.IsUnknown() {
		config.SetWorkloadFederationOidcConfigUid(plan.WorkloadFederationOIDCConfigID.ValueString())
		paths = append(paths, "workload_federation_oidc_config_uid")
	}
	var remote *directoryv1alpha.ServiceAccountOidcTrustConfig
	var err error
	if len(paths) == 0 {
		name := state.Name.ValueString()
		remote, err = r.client.Directory.GetServiceAccountOidcTrustConfig(ctx, &directoryv1alpha.GetServiceAccountOidcTrustConfigRequest{Name: &name})
	} else {
		remote, err = r.client.Directory.UpdateServiceAccountOidcTrustConfig(ctx, &directoryv1alpha.UpdateServiceAccountOidcTrustConfigRequest{
			ServiceAccountOidcTrustConfig: config, UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
		})
	}
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := plan.setFromProto(remote); err != nil {
		name := state.Name.ValueString()
		readBack, readErr := r.client.Directory.GetServiceAccountOidcTrustConfig(ctx, &directoryv1alpha.GetServiceAccountOidcTrustConfigRequest{Name: &name})
		if readErr != nil {
			coreweave.HandleAPIError(ctx, readErr, &resp.Diagnostics)
			return
		}
		if readBackErr := plan.setFromProto(readBack); readBackErr != nil {
			resp.Diagnostics.AddError("Unexpected API Response", fmt.Sprintf("The Directory API returned an invalid OIDC trust configuration after update and the subsequent read could not recover it: %s.", readBackErr))
			return
		}
	}
	// updated_at uses UseStateForUnknown so no-op plans remain empty. Preserve
	// that planned value during apply; the next refresh reads the new timestamp.
	plan.UpdatedAt = plannedUpdatedAt
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *OIDCTrustConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OIDCTrustConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := data.Name.ValueString()
	_, err := r.client.Directory.DeleteServiceAccountOidcTrustConfig(ctx, &directoryv1alpha.DeleteServiceAccountOidcTrustConfigRequest{Name: &name})
	if err != nil && !coreweave.IsNotFoundError(err) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
	}
}

func (r *OIDCTrustConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	organizationID, serviceAccountID, id, err := parseOIDCTrustConfigName(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid OIDC Trust Configuration Import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), organizationID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_account_id"), serviceAccountID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}
