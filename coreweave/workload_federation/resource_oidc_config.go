package workloadfederation

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	controlplanev1beta1 "buf.build/gen/go/coreweave/workload-federation/protocolbuffers/go/coreweave/workload_federation/control_plane/v1beta1"
	"connectrpc.com/connect"
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
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	_ resource.Resource                = &OIDCConfigResource{}
	_ resource.ResourceWithConfigure   = &OIDCConfigResource{}
	_ resource.ResourceWithImportState = &OIDCConfigResource{}
)

var oidcConfigNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// oidcIssuerURLValidator requires HTTPS except for loopback development issuers.
type oidcIssuerURLValidator struct{}

func (oidcIssuerURLValidator) Description(context.Context) string {
	return "must be a valid HTTPS OIDC issuer URL, or an HTTP URL on a loopback host"
}

func (oidcIssuerURLValidator) MarkdownDescription(context.Context) string {
	return "Must be a valid HTTPS OIDC issuer URL. HTTP is permitted only for loopback development issuers."
}

func (oidcIssuerURLValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	parsed, err := url.ParseRequestURI(req.ConfigValue.ValueString())
	if err != nil || parsed.Host == "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid OIDC issuer URL", "The issuer_url value must be a valid HTTPS URL with a host. HTTP is permitted only for loopback development issuers.")
		return
	}

	hostname := strings.ToLower(parsed.Hostname())
	loopback := hostname == "localhost" || strings.HasSuffix(hostname, ".localhost")
	if address, parseErr := netip.ParseAddr(hostname); parseErr == nil {
		loopback = address.IsLoopback()
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !loopback) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid OIDC issuer URL", "The issuer_url value must use HTTPS unless it refers to a loopback development issuer.")
	}
}

// OIDCConfigResource manages an external OIDC provider trusted for workload federation.
type OIDCConfigResource struct {
	client *coreweave.Client
}

// OIDCConfigResourceModel describes the Terraform state for an OIDC config.
type OIDCConfigResourceModel struct {
	ID             types.String `tfsdk:"id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	IssuerURL      types.String `tfsdk:"issuer_url"`
	Audience       types.String `tfsdk:"audience"`
	Active         types.Bool   `tfsdk:"active"`
	DeactivatedAt  types.String `tfsdk:"deactivated_at"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

func NewOIDCConfigResource() resource.Resource {
	return &OIDCConfigResource{}
}

func (r *OIDCConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workload_federation_oidc_config"
}

func (r *OIDCConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages an external OpenID Connect (OIDC) trust configuration for CoreWeave workload identity federation.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The server-assigned unique identifier for the OIDC configuration.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier of the CoreWeave organization that owns the OIDC configuration.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "A human-readable name for the OIDC configuration. Must contain only letters, numbers, hyphens, and underscores.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					stringvalidator.RegexMatches(oidcConfigNamePattern, "must contain only letters, numbers, hyphens, and underscores"),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "An optional human-readable description of the OIDC configuration.",
				Validators: []validator.String{
					stringvalidator.UTF8LengthAtMost(1024),
				},
			},
			"issuer_url": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The issuer URL of the external OIDC provider. HTTPS is required except for loopback development issuers.",
				Validators: []validator.String{
					stringvalidator.UTF8LengthAtMost(1024),
					oidcIssuerURLValidator{},
				},
			},
			"audience": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The expected audience claim in tokens issued by the external OIDC provider.",
				Validators: []validator.String{
					stringvalidator.UTF8LengthBetween(1, 1024),
				},
			},
			"active": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Whether tokens issued by this OIDC provider are accepted for workload identity federation. Defaults to `true` when omitted on creation. Once a value has been applied it is retained: removing the attribute from configuration keeps the last applied value rather than reverting to `true`, so a configuration that was deactivated stays deactivated. Set `active = true` explicitly to reactivate one.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"deactivated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 timestamp at which the OIDC configuration was deactivated, or null while it is active.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 timestamp at which the OIDC configuration was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 timestamp at which the OIDC configuration was last updated.",
			},
		},
	}
}

func (r *OIDCConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (m *OIDCConfigResourceModel) ToCreateRequest() *controlplanev1beta1.CreateOIDCConfigRequest {
	req := &controlplanev1beta1.CreateOIDCConfigRequest{
		Name:      m.Name.ValueString(),
		IssuerUrl: m.IssuerURL.ValueString(),
		Audience:  m.Audience.ValueString(),
	}

	if !m.Description.IsNull() && !m.Description.IsUnknown() {
		req.Description = m.Description.ValueStringPointer()
	}
	if !m.Active.IsNull() && !m.Active.IsUnknown() {
		req.Active = m.Active.ValueBoolPointer()
	}

	return req
}

// ToUpdateRequest constructs a minimal update request from planned and prior state.
func (m *OIDCConfigResourceModel) ToUpdateRequest(state *OIDCConfigResourceModel) *controlplanev1beta1.UpdateOIDCConfigRequest {
	req := &controlplanev1beta1.UpdateOIDCConfigRequest{
		Uid:        m.ID.ValueString(),
		UpdateMask: &fieldmaskpb.FieldMask{},
	}

	// An unknown planned value carries no target to send: ValueStringPointer and
	// ValueBoolPointer only nil-check null, so an unknown would serialize as the
	// zero value and the mask path would ask the API to apply it. Skip instead,
	// leaving the attribute for the read-back to resolve.
	if !m.Name.Equal(state.Name) && !m.Name.IsUnknown() {
		req.Name = m.Name.ValueStringPointer()
		req.UpdateMask.Paths = append(req.UpdateMask.Paths, "name")
	}
	if !m.IssuerURL.Equal(state.IssuerURL) && !m.IssuerURL.IsUnknown() {
		req.IssuerUrl = m.IssuerURL.ValueStringPointer()
		req.UpdateMask.Paths = append(req.UpdateMask.Paths, "issuer_url")
	}
	if !m.Audience.Equal(state.Audience) && !m.Audience.IsUnknown() {
		req.Audience = m.Audience.ValueStringPointer()
		req.UpdateMask.Paths = append(req.UpdateMask.Paths, "audience")
	}
	if !m.Description.Equal(state.Description) && !m.Description.IsUnknown() {
		// A null description is sent as an unset field with the path in the mask,
		// which is how the API is asked to clear it.
		if !m.Description.IsNull() {
			req.Description = m.Description.ValueStringPointer()
		}
		req.UpdateMask.Paths = append(req.UpdateMask.Paths, "description")
	}
	if !m.Active.Equal(state.Active) && !m.Active.IsUnknown() {
		req.Active = m.Active.ValueBoolPointer()
		req.UpdateMask.Paths = append(req.UpdateMask.Paths, "active")
	}

	return req
}

func timestampValue(timestamp *timestamppb.Timestamp) types.String {
	if timestamp == nil {
		return types.StringNull()
	}
	return types.StringValue(timestamp.AsTime().Format(time.RFC3339))
}

// SetFromProto replaces all remote fields in the model so out-of-band changes are reflected as drift.
func (m *OIDCConfigResourceModel) SetFromProto(config *controlplanev1beta1.OIDCConfig) error {
	if config == nil {
		return fmt.Errorf("OIDC configuration is missing")
	}
	if config.GetUid() == "" {
		return fmt.Errorf("OIDC configuration ID is missing")
	}

	m.ID = types.StringValue(config.GetUid())
	m.OrganizationID = types.StringValue(config.GetOrgUid())
	m.Name = types.StringValue(config.GetName())
	if config.HasDescription() {
		m.Description = types.StringValue(config.GetDescription())
	} else {
		m.Description = types.StringNull()
	}
	m.IssuerURL = types.StringValue(config.GetIssuerUrl())
	m.Audience = types.StringValue(config.GetAudience())
	// The API represents activation state solely through deactivated_at: an
	// absent timestamp is active, while a populated timestamp is inactive.
	m.Active = types.BoolValue(!config.HasDeactivatedAt())
	m.DeactivatedAt = timestampValue(config.GetDeactivatedAt())
	m.CreatedAt = timestampValue(config.GetCreatedAt())
	m.UpdatedAt = timestampValue(config.GetUpdatedAt())
	return nil
}

// FillUnknownFromProto resolves only the attributes still unknown in the plan,
// leaving every planned value intact. Use it when the API reports no mutation
// took place: overwriting a planned value with a remote one that drifted out of
// band would contradict the plan and fail the apply with "Provider produced
// inconsistent result after apply".
func (m *OIDCConfigResourceModel) FillUnknownFromProto(config *controlplanev1beta1.OIDCConfig) error {
	if config == nil {
		return fmt.Errorf("OIDC configuration is missing")
	}
	if config.GetUid() == "" {
		return fmt.Errorf("OIDC configuration ID is missing")
	}

	if m.ID.IsUnknown() {
		m.ID = types.StringValue(config.GetUid())
	}
	if m.OrganizationID.IsUnknown() {
		m.OrganizationID = types.StringValue(config.GetOrgUid())
	}
	if m.Name.IsUnknown() {
		m.Name = types.StringValue(config.GetName())
	}
	if m.Description.IsUnknown() {
		if config.HasDescription() {
			m.Description = types.StringValue(config.GetDescription())
		} else {
			m.Description = types.StringNull()
		}
	}
	if m.IssuerURL.IsUnknown() {
		m.IssuerURL = types.StringValue(config.GetIssuerUrl())
	}
	if m.Audience.IsUnknown() {
		m.Audience = types.StringValue(config.GetAudience())
	}
	if m.Active.IsUnknown() {
		m.Active = types.BoolValue(!config.HasDeactivatedAt())
	}
	if m.DeactivatedAt.IsUnknown() {
		m.DeactivatedAt = timestampValue(config.GetDeactivatedAt())
	}
	if m.CreatedAt.IsUnknown() {
		m.CreatedAt = timestampValue(config.GetCreatedAt())
	}
	if m.UpdatedAt.IsUnknown() {
		m.UpdatedAt = timestampValue(config.GetUpdatedAt())
	}
	return nil
}

func (r *OIDCConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data OIDCConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createResp, err := r.client.CreateOIDCConfig(ctx, connect.NewRequest(data.ToCreateRequest()))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	if err := data.SetFromProto(createResp.Msg.GetConfig()); err != nil {
		resp.Diagnostics.AddError(
			"OIDC Configuration ID Missing After Creation",
			"The workload federation API reported a successful creation but did not return the created configuration or its ID. Terraform will not adopt a configuration by matching mutable fields. Locate the configuration in your organization and import it before applying again.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OIDCConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OIDCConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	readResp, err := r.client.GetOIDCConfig(ctx, connect.NewRequest(&controlplanev1beta1.GetOIDCConfigRequest{
		Uid: data.ID.ValueString(),
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	if err := data.SetFromProto(readResp.Msg.GetConfig()); err != nil {
		resp.Diagnostics.AddError("Unexpected API Response", fmt.Sprintf("The workload federation API returned an invalid OIDC configuration while reading state: %s.", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OIDCConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan OIDCConfigResourceModel
	var state OIDCConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateReq := plan.ToUpdateRequest(&state)
	if len(updateReq.UpdateMask.Paths) == 0 {
		readResp, err := r.client.GetOIDCConfig(ctx, connect.NewRequest(&controlplanev1beta1.GetOIDCConfigRequest{
			Uid: state.ID.ValueString(),
		}))
		if err != nil {
			if coreweave.IsNotFoundError(err) {
				resp.Diagnostics.AddError(
					"Unable to Update OIDC Configuration",
					fmt.Sprintf("The workload federation API returned Not Found while Terraform was applying an update. Terraform retained the prior state so the next refresh can determine whether the configuration was deleted or is temporarily inaccessible. API error: %s", err),
				)
				return
			}
			coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
			return
		}

		// Nothing was mutated, so the planned values stand; the read only exists to
		// resolve computed attributes that are still unknown.
		if err := plan.FillUnknownFromProto(readResp.Msg.GetConfig()); err != nil {
			resp.Diagnostics.AddError("Unexpected API Response", fmt.Sprintf("The workload federation API returned an invalid OIDC configuration after a no-op update: %s.", err))
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	updateResp, err := r.client.UpdateOIDCConfig(ctx, connect.NewRequest(updateReq))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.Diagnostics.AddError(
				"Unable to Update OIDC Configuration",
				fmt.Sprintf("The workload federation API returned Not Found while Terraform was applying an update. Terraform retained the prior state so the next refresh can determine whether the configuration was deleted or is temporarily inaccessible. API error: %s", err),
			)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	if err := plan.SetFromProto(updateResp.Msg.GetConfig()); err != nil {
		readResp, readErr := r.client.GetOIDCConfig(ctx, connect.NewRequest(&controlplanev1beta1.GetOIDCConfigRequest{
			Uid: state.ID.ValueString(),
		}))
		if readErr != nil {
			coreweave.HandleAPIError(ctx, readErr, &resp.Diagnostics)
			return
		}
		if readBackErr := plan.SetFromProto(readResp.Msg.GetConfig()); readBackErr != nil {
			resp.Diagnostics.AddError("Unexpected API Response", fmt.Sprintf("The workload federation API returned an invalid OIDC configuration after update and the subsequent read could not recover it: %s.", readBackErr))
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *OIDCConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OIDCConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.DeleteOIDCConfig(ctx, connect.NewRequest(&controlplanev1beta1.DeleteOIDCConfigRequest{
		Uid: data.ID.ValueString(),
	}))
	if err != nil && !coreweave.IsNotFoundError(err) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
	}
}

func (r *OIDCConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
