package arca

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	arcav1beta1 "buf.build/gen/go/coreweave/arca/protocolbuffers/go/coreweave/arca/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const (
	defaultBranch       = "main"
	defaultManifestPath = ".arca/apps.yaml"
	readyState          = "ready"
	pendingState        = "pending"
)

var (
	_ resource.Resource                = &AdvancedModeResource{}
	_ resource.ResourceWithImportState = &AdvancedModeResource{}

	installationIDPattern = regexp.MustCompile(`^[0-9]+$`)
	errReconciliation     = errors.New("advanced mode reconciliation failed")
)

func NewAdvancedModeResource() resource.Resource {
	return &AdvancedModeResource{}
}

type AdvancedModeResource struct {
	client *coreweave.ArcaClient
}

type AdvancedModeResourceModel struct {
	ID                    types.String `tfsdk:"id"`
	GitHubInstallationID  types.String `tfsdk:"github_installation_id"`
	Repository            types.String `tfsdk:"repository"`
	Branch                types.String `tfsdk:"branch"`
	ManifestPath          types.String `tfsdk:"manifest_path"`
	AllowDestroyApps      types.Bool   `tfsdk:"allow_destroy_apps"`
	Connected             types.Bool   `tfsdk:"connected"`
	Ready                 types.Bool   `tfsdk:"ready"`
	AppCount              types.Int64  `tfsdk:"app_count"`
	AvailableRepositories types.List   `tfsdk:"available_repositories"`
	Conditions            types.List   `tfsdk:"conditions"`
	LastError             types.String `tfsdk:"last_error"`
	LastReconciledAt      types.String `tfsdk:"last_reconciled_at"`
}

type availableRepositoryModel struct {
	URL         types.String `tfsdk:"url"`
	Permissions types.List   `tfsdk:"permissions"`
}

type conditionModel struct {
	Type               types.String `tfsdk:"type"`
	Status             types.String `tfsdk:"status"`
	Reason             types.String `tfsdk:"reason"`
	Message            types.String `tfsdk:"message"`
	LastTransitionTime types.String `tfsdk:"last_transition_time"`
	ObservedGeneration types.Int64  `tfsdk:"observed_generation"`
}

var availableRepositoryAttrTypes = map[string]attr.Type{
	"url":         types.StringType,
	"permissions": types.ListType{ElemType: types.StringType},
}

var conditionAttrTypes = map[string]attr.Type{
	"type":                 types.StringType,
	"status":               types.StringType,
	"reason":               types.StringType,
	"message":              types.StringType,
	"last_transition_time": types.StringType,
	"observed_generation":  types.Int64Type,
}

func (r *AdvancedModeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_arca_advanced_mode"
}

func (r *AdvancedModeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Enables and manages Arca Advanced Mode for the organization authenticated by the provider token. Advanced Mode reconciles applications declared in `.arca/apps.yaml` from a connected GitHub repository.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The server-generated Advanced Mode identifier.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"github_installation_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The GitHub App installation ID already connected to the authenticated CoreWeave organization.",
				Validators:          []validator.String{stringvalidator.RegexMatches(installationIDPattern, "must contain only decimal digits")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"repository": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The GitHub repository containing the Arca application manifest.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"branch": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(defaultBranch),
				MarkdownDescription: "The Git branch containing the application manifest. Defaults to `main`.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"manifest_path": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(defaultManifestPath),
				MarkdownDescription: "The repository-relative Arca application manifest path. Currently fixed to `.arca/apps.yaml`.",
				Validators:          []validator.String{stringvalidator.OneOf(defaultManifestPath)},
			},
			"allow_destroy_apps": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "When true, Terraform may disable Advanced Mode even when Arca-managed applications still exist. This sends `force=true` during deletion.",
			},
			"connected": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether Arca can authenticate through the GitHub App installation.",
			},
			"ready": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the current Advanced Mode configuration has reconciled successfully.",
			},
			"app_count": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The number of applications currently managed through Advanced Mode.",
			},
			"available_repositories": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Repositories accessible through the selected GitHub App installation.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"url":         schema.StringAttribute{Computed: true},
					"permissions": schema.ListAttribute{Computed: true, ElementType: types.StringType},
				}},
			},
			"conditions": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Detailed reconciliation conditions reported by Arca.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"type":                 schema.StringAttribute{Computed: true},
					"status":               schema.StringAttribute{Computed: true},
					"reason":               schema.StringAttribute{Computed: true},
					"message":              schema.StringAttribute{Computed: true},
					"last_transition_time": schema.StringAttribute{Computed: true},
					"observed_generation":  schema.Int64Attribute{Computed: true},
				}},
			},
			"last_error": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The most recent Advanced Mode reconciliation error.",
			},
			"last_reconciled_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of the last successful reconciliation.",
			},
		},
	}
}

func (r *AdvancedModeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *coreweave.Client, got %T.", req.ProviderData))
		return
	}
	r.client = client.Arca
}

func (r *AdvancedModeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data AdvancedModeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := r.client.CreateAdvancedMode(ctx, connect.NewRequest(&arcav1beta1.CreateAdvancedModeRequest{
		GithubInstallationId: data.GitHubInstallationID.ValueString(),
		Repository:           data.Repository.ValueString(),
		Branch:               data.Branch.ValueString(),
		ManifestPath:         data.ManifestPath.ValueString(),
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if !setAdvancedModeState(ctx, &data, result.Msg.GetAdvancedMode(), &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	mode, err := r.waitUntilReady(ctx)
	if err != nil {
		handleReconciliationError(ctx, err, &resp.Diagnostics)
		return
	}
	if !setAdvancedModeState(ctx, &data, mode, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AdvancedModeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AdvancedModeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := r.client.GetAdvancedMode(ctx, connect.NewRequest(&arcav1beta1.GetAdvancedModeRequest{}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if !setAdvancedModeState(ctx, &data, result.Msg.GetAdvancedMode(), &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AdvancedModeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state AdvancedModeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	paths := advancedModeUpdatePaths(plan, state)
	if len(paths) == 0 {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}
	result, err := r.client.UpdateAdvancedMode(ctx, connect.NewRequest(&arcav1beta1.UpdateAdvancedModeRequest{
		Repository:   plan.Repository.ValueString(),
		Branch:       plan.Branch.ValueString(),
		ManifestPath: plan.ManifestPath.ValueString(),
		UpdateMask:   &fieldmaskpb.FieldMask{Paths: paths},
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if !setAdvancedModeState(ctx, &plan, result.Msg.GetAdvancedMode(), &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	mode, err := r.waitUntilReady(ctx)
	if err != nil {
		handleReconciliationError(ctx, err, &resp.Diagnostics)
		return
	}
	if !setAdvancedModeState(ctx, &plan, mode, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AdvancedModeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data AdvancedModeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.DeleteAdvancedMode(ctx, connect.NewRequest(&arcav1beta1.DeleteAdvancedModeRequest{
		Force: data.AllowDestroyApps.ValueBool(),
	}))
	if err != nil && !coreweave.IsNotFoundError(err) {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
	}
}

func (r *AdvancedModeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *AdvancedModeResource) waitUntilReady(ctx context.Context) (*arcav1beta1.AdvancedMode, error) {
	conf := retry.StateChangeConf{
		Pending:    []string{pendingState},
		Target:     []string{readyState},
		Timeout:    10 * time.Minute,
		MinTimeout: 2 * time.Second,
		Refresh: func() (interface{}, string, error) {
			result, err := r.client.GetAdvancedMode(ctx, connect.NewRequest(&arcav1beta1.GetAdvancedModeRequest{}))
			if err != nil {
				tflog.Error(ctx, "failed to poll Arca Advanced Mode", map[string]interface{}{"error": err.Error()})
				return nil, pendingState, err
			}
			mode := result.Msg.GetAdvancedMode()
			state, err := advancedModeReadiness(mode)
			return mode, state, err
		},
	}

	raw, err := conf.WaitForStateContext(ctx)
	if err != nil {
		return nil, err
	}
	mode, ok := raw.(*arcav1beta1.AdvancedMode)
	if !ok {
		return nil, fmt.Errorf("unexpected polling result %T", raw)
	}
	return mode, nil
}

func advancedModeReadiness(mode *arcav1beta1.AdvancedMode) (string, error) {
	if mode == nil {
		return pendingState, errors.New("arca returned an empty advanced mode resource")
	}
	if mode.GetReady() {
		return readyState, nil
	}
	for _, condition := range mode.GetConditions() {
		if condition.GetType() == "Ready" && condition.GetStatus() == "False" && condition.GetObservedGeneration() > 0 {
			message := condition.GetMessage()
			if mode.GetLastError() != "" {
				message = mode.GetLastError()
			}
			return pendingState, fmt.Errorf("%w: %s", errReconciliation, message)
		}
	}
	return pendingState, nil
}

func handleReconciliationError(ctx context.Context, err error, diagnostics *diag.Diagnostics) {
	if errors.Is(err, errReconciliation) {
		diagnostics.AddError("Advanced Mode reconciliation failed", err.Error())
		return
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		coreweave.HandleAPIError(ctx, connectErr, diagnostics)
		return
	}
	tflog.Error(ctx, "failed waiting for Arca Advanced Mode", map[string]interface{}{"error": err.Error()})
	diagnostics.AddError("Failed waiting for Advanced Mode", err.Error())
}

func advancedModeUpdatePaths(plan, state AdvancedModeResourceModel) []string {
	paths := make([]string, 0, 3)
	if !plan.Repository.Equal(state.Repository) {
		paths = append(paths, "repository")
	}
	if !plan.Branch.Equal(state.Branch) {
		paths = append(paths, "branch")
	}
	if !plan.ManifestPath.Equal(state.ManifestPath) {
		paths = append(paths, "manifest_path")
	}
	return paths
}

func setAdvancedModeState(ctx context.Context, data *AdvancedModeResourceModel, mode *arcav1beta1.AdvancedMode, diagnostics *diag.Diagnostics) bool {
	if mode == nil {
		diagnostics.AddError("Invalid Advanced Mode response", "Arca returned an empty Advanced Mode resource.")
		return false
	}

	data.ID = types.StringValue(mode.GetName())
	data.GitHubInstallationID = types.StringValue(mode.GetGithubInstallationId())
	data.Repository = types.StringValue(mode.GetRepository())
	data.Branch = types.StringValue(mode.GetBranch())
	data.ManifestPath = types.StringValue(mode.GetManifestPath())
	data.Connected = types.BoolValue(mode.GetConnected())
	data.Ready = types.BoolValue(mode.GetReady())
	data.AppCount = types.Int64Value(int64(mode.GetAppCount()))
	data.LastError = types.StringValue(mode.GetLastError())
	data.LastReconciledAt = types.StringValue(mode.GetLastReconciledAt())
	if data.AllowDestroyApps.IsNull() || data.AllowDestroyApps.IsUnknown() {
		data.AllowDestroyApps = types.BoolValue(false)
	}

	repositories := make([]availableRepositoryModel, 0, len(mode.GetAvailableRepositories()))
	for _, repository := range mode.GetAvailableRepositories() {
		permissions, diags := types.ListValueFrom(ctx, types.StringType, repository.GetPermissions())
		if diags.HasError() {
			diagnostics.AddError("Invalid repository status", diags.Errors()[0].Detail())
			return false
		}
		repositories = append(repositories, availableRepositoryModel{
			URL:         types.StringValue(repository.GetUrl()),
			Permissions: permissions,
		})
	}
	availableRepositories, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: availableRepositoryAttrTypes}, repositories)
	if diags.HasError() {
		diagnostics.AddError("Invalid repository status", diags.Errors()[0].Detail())
		return false
	}
	data.AvailableRepositories = availableRepositories

	conditions := make([]conditionModel, 0, len(mode.GetConditions()))
	for _, condition := range mode.GetConditions() {
		conditions = append(conditions, conditionModel{
			Type:               types.StringValue(condition.GetType()),
			Status:             types.StringValue(condition.GetStatus()),
			Reason:             types.StringValue(condition.GetReason()),
			Message:            types.StringValue(condition.GetMessage()),
			LastTransitionTime: types.StringValue(condition.GetLastTransitionTime()),
			ObservedGeneration: types.Int64Value(condition.GetObservedGeneration()),
		})
	}
	conditionValues, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: conditionAttrTypes}, conditions)
	if diags.HasError() {
		diagnostics.AddError("Invalid Advanced Mode conditions", diags.Errors()[0].Detail())
		return false
	}
	data.Conditions = conditionValues
	return true
}
