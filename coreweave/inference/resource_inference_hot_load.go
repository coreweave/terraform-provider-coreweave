package inference

import (
	"context"
	"fmt"
	"regexp"
	"time"

	v1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/go-uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	_                    resource.Resource                   = &InferenceHotLoadResource{}
	_                    resource.ResourceWithImportState    = &InferenceHotLoadResource{}
	_                    resource.ResourceWithValidateConfig = &InferenceHotLoadResource{}
	operationUUIDPattern                                     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	snapshotChainTypes                                       = map[string]attr.Type{
		"identity": types.StringType, "type": types.StringType,
		"compression_format": types.StringType, "checksum_format": types.StringType, "hot_load_id": types.StringType,
	}
)

type InferenceHotLoadResource struct{ client *coreweave.InferenceClient }

type IncrementalSnapshotModel struct {
	PreviousSnapshotIdentity types.String `tfsdk:"previous_snapshot_identity"`
	CompressionFormat        types.String `tfsdk:"compression_format"`
	ChecksumFormat           types.String `tfsdk:"checksum_format"`
}

type InferenceHotLoadResourceModel struct {
	ID                          types.String              `tfsdk:"id"`
	DeploymentID                types.String              `tfsdk:"deployment_id"`
	OrganizationID              types.String              `tfsdk:"organization_id"`
	Identity                    types.String              `tfsdk:"identity"`
	Type                        types.String              `tfsdk:"type"`
	PromptCachePolicy           types.String              `tfsdk:"prompt_cache_policy"`
	IncrementalSnapshotMetadata *IncrementalSnapshotModel `tfsdk:"incremental_snapshot_metadata"`
	State                       types.String              `tfsdk:"state"`
	CreatedAt                   types.String              `tfsdk:"created_at"`
	UpdatedAt                   types.String              `tfsdk:"updated_at"`
	FinishedAt                  types.String              `tfsdk:"finished_at"`
	CanceledAt                  types.String              `tfsdk:"canceled_at"`
	SnapshotChain               types.List                `tfsdk:"snapshot_chain"`
	Conditions                  types.List                `tfsdk:"conditions"`
}

func NewInferenceHotLoadResource() resource.Resource { return &InferenceHotLoadResource{} }

func (r *InferenceHotLoadResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inference_hot_load"
}

func (r *InferenceHotLoadResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	immutable := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Submit an immutable checkpoint hot-load to a deployment configured with hot_load. Creation returns after acceptance; refresh observes progress. Destroy cancels an active operation without waiting for replica updates to stop or rolling back weights. Cancellation requires server support: if the API returns Unimplemented, destroy reports an error and retains state until the operation becomes terminal. Terminal operation history remains on the server.",
		Attributes: map[string]schema.Attribute{
			"id":                  schema.StringAttribute{Computed: true, MarkdownDescription: "Hot-load operation UUID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"deployment_id":       schema.StringAttribute{Required: true, MarkdownDescription: "UUID of the target deployment.", PlanModifiers: immutable, Validators: []validator.String{stringvalidator.RegexMatches(operationUUIDPattern, "must be a UUID")}},
			"identity":            schema.StringAttribute{Required: true, MarkdownDescription: "Snapshot identity within the deployment, a single path segment.", PlanModifiers: immutable, Validators: []validator.String{stringvalidator.LengthBetween(1, 128), stringvalidator.NoneOf(".", ".."), stringvalidator.RegexMatches(regexp.MustCompile(`^[^/]+$`), "must be a single path segment")}},
			"type":                schema.StringAttribute{Required: true, MarkdownDescription: "Snapshot type: SNAPSHOT_TYPE_FULL or SNAPSHOT_TYPE_INCREMENTAL.", PlanModifiers: immutable, Validators: []validator.String{stringvalidator.OneOf("SNAPSHOT_TYPE_FULL", "SNAPSHOT_TYPE_INCREMENTAL")}},
			"prompt_cache_policy": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Prompt cache handling after the swap: PROMPT_CACHE_POLICY_PRESERVE (default) or PROMPT_CACHE_POLICY_RESET_ALL.", Default: stringdefault.StaticString("PROMPT_CACHE_POLICY_PRESERVE"), PlanModifiers: immutable, Validators: []validator.String{stringvalidator.OneOf("PROMPT_CACHE_POLICY_PRESERVE", "PROMPT_CACHE_POLICY_RESET_ALL")}},
			"incremental_snapshot_metadata": schema.SingleNestedAttribute{
				Optional: true, MarkdownDescription: "Required for incremental snapshots; omitted for full snapshots.", PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"previous_snapshot_identity": schema.StringAttribute{Required: true, MarkdownDescription: "Identity of the predecessor snapshot.", Validators: []validator.String{stringvalidator.LengthBetween(1, 128)}},
					"compression_format":         schema.StringAttribute{Required: true, MarkdownDescription: "Incremental snapshot compression: COMPRESSION_FORMAT_ZSTD.", Validators: []validator.String{stringvalidator.OneOf("COMPRESSION_FORMAT_ZSTD")}},
					"checksum_format":            schema.StringAttribute{Required: true, MarkdownDescription: "Snapshot integrity checksum: CHECKSUM_FORMAT_ADLER32.", Validators: []validator.String{stringvalidator.OneOf("CHECKSUM_FORMAT_ADLER32")}},
				},
			},
			"organization_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Organization owning the operation."},
			"state":           schema.StringAttribute{Computed: true, MarkdownDescription: "Aggregate HotLoad state. Acceptance does not imply completion."},
			"created_at":      schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time in RFC3339."},
			"updated_at":      schema.StringAttribute{Computed: true, MarkdownDescription: "Last update time in RFC3339."},
			"finished_at":     schema.StringAttribute{Computed: true, MarkdownDescription: "Terminal time in RFC3339, or null while unfinished."},
			"canceled_at":     schema.StringAttribute{Computed: true, MarkdownDescription: "Cancellation time in RFC3339, or null."},
			"snapshot_chain": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Resolved snapshot chain, oldest first.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"identity":           schema.StringAttribute{Computed: true, MarkdownDescription: "Snapshot identity."},
				"type":               schema.StringAttribute{Computed: true, MarkdownDescription: "Snapshot type."},
				"compression_format": schema.StringAttribute{Computed: true, MarkdownDescription: "Compression format."},
				"checksum_format":    schema.StringAttribute{Computed: true, MarkdownDescription: "Checksum format."},
				"hot_load_id":        schema.StringAttribute{Computed: true, MarkdownDescription: "Operation that introduced this snapshot."},
			}}},
			"conditions": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Detailed operation conditions and error messages.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"type":             schema.StringAttribute{Computed: true, MarkdownDescription: "Condition type."},
				"status":           schema.StringAttribute{Computed: true, MarkdownDescription: "Condition status."},
				"last_update_time": schema.StringAttribute{Computed: true, MarkdownDescription: "Last condition update in RFC3339."},
				"reason":           schema.StringAttribute{Computed: true, MarkdownDescription: "Machine-readable condition reason."},
				"message":          schema.StringAttribute{Computed: true, MarkdownDescription: "Condition details."},
			}}},
		},
	}
}

func (r *InferenceHotLoadResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected resource configuration", fmt.Sprintf("Expected *coreweave.Client, got %T", req.ProviderData))
		return
	}
	r.client = client.Inference
}

func (r *InferenceHotLoadResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var snapshotType types.String
	var metadata types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("type"), &snapshotType)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("incremental_snapshot_metadata"), &metadata)...)
	if resp.Diagnostics.HasError() || snapshotType.IsUnknown() || snapshotType.IsNull() || metadata.IsUnknown() {
		return
	}
	if snapshotType.ValueString() == "SNAPSHOT_TYPE_INCREMENTAL" && metadata.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("incremental_snapshot_metadata"), "Missing incremental metadata", "Incremental snapshots require predecessor identity, compression format and checksum format.")
	}
	if snapshotType.ValueString() == "SNAPSHOT_TYPE_FULL" && !metadata.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("incremental_snapshot_metadata"), "Unexpected incremental metadata", "Full snapshots must omit incremental_snapshot_metadata.")
	}
}

func (m *InferenceHotLoadResourceModel) request() *v1.CreateHotLoadRequest {
	in := &v1.CreateHotLoadRequest{
		DeploymentId: m.DeploymentID.ValueString(), Identity: m.Identity.ValueString(),
		Type:              v1.SnapshotType(v1.SnapshotType_value[m.Type.ValueString()]),
		PromptCachePolicy: v1.PromptCachePolicy(v1.PromptCachePolicy_value[m.PromptCachePolicy.ValueString()]),
	}
	if meta := m.IncrementalSnapshotMetadata; meta != nil {
		in.IncrementalSnapshotMetadata = &v1.IncrementalSnapshotMetadata{
			PreviousSnapshotIdentity: meta.PreviousSnapshotIdentity.ValueString(),
			CompressionFormat:        v1.CompressionFormat(v1.CompressionFormat_value[meta.CompressionFormat.ValueString()]),
			ChecksumFormat:           v1.ChecksumFormat(v1.ChecksumFormat_value[meta.ChecksumFormat.ValueString()]),
		}
	}
	return in
}

func (r *InferenceHotLoadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m InferenceHotLoadResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := m.request()
	id, err := uuid.GenerateUUID()
	if err != nil {
		resp.Diagnostics.AddError("Generating hot-load ID failed", err.Error())
		return
	}
	in.HotLoadId = id
	created, err := r.client.CreateHotLoad(ctx, connect.NewRequest(in))
	var operation *v1.HotLoad
	switch {
	case err == nil:
		operation = created.Msg.GetHotLoad()
	case connect.CodeOf(err) == connect.CodeAlreadyExists:
		fetched, fetchErr := r.client.GetHotLoad(ctx, connect.NewRequest(&v1.GetHotLoadRequest{Id: id}))
		if fetchErr != nil {
			resp.Diagnostics.AddError("Reading accepted hot-load failed", fmt.Sprintf("Operation %s: %v", id, fetchErr))
			return
		}
		operation = fetched.Msg.GetHotLoad()
		spec := operation.GetSpec()
		if spec.GetId() != id || spec.GetDeploymentId() != in.GetDeploymentId() || spec.GetIdentity() != in.GetIdentity() || spec.GetType() != in.GetType() || spec.GetPromptCachePolicy() != in.GetPromptCachePolicy() || !proto.Equal(spec.GetIncrementalSnapshotMetadata(), in.GetIncrementalSnapshotMetadata()) {
			resp.Diagnostics.AddError("Hot-load ID conflict", fmt.Sprintf("Operation %s exists with different inputs; it was not adopted.", id))
			return
		}
	default:
		resp.Diagnostics.AddError("Creating hot-load failed", fmt.Sprintf("Operation %s: %v. If the request was accepted, inspect and import this operation ID before retrying.", id, err))
		return
	}
	resp.Diagnostics.Append(m.setFromHotLoad(operation)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func hotLoadTimestamp(ts *timestamppb.Timestamp) types.String {
	if ts == nil {
		return types.StringNull()
	}
	return types.StringValue(ts.AsTime().Format(time.RFC3339))
}

func (m *InferenceHotLoadResourceModel) setFromHotLoad(h *v1.HotLoad) (diagnostics diag.Diagnostics) {
	spec, status := h.GetSpec(), h.GetStatus()
	m.ID = types.StringValue(spec.GetId())
	m.DeploymentID = types.StringValue(spec.GetDeploymentId())
	m.OrganizationID = types.StringValue(spec.GetOrganizationId())
	m.Identity = types.StringValue(spec.GetIdentity())
	m.Type = types.StringValue(spec.GetType().String())
	m.PromptCachePolicy = types.StringValue(spec.GetPromptCachePolicy().String())
	m.IncrementalSnapshotMetadata = nil
	if meta := spec.GetIncrementalSnapshotMetadata(); meta != nil {
		m.IncrementalSnapshotMetadata = &IncrementalSnapshotModel{
			PreviousSnapshotIdentity: types.StringValue(meta.GetPreviousSnapshotIdentity()),
			CompressionFormat:        types.StringValue(meta.GetCompressionFormat().String()), ChecksumFormat: types.StringValue(meta.GetChecksumFormat().String()),
		}
	}
	m.State = types.StringValue(status.GetState().String())
	m.CreatedAt, m.UpdatedAt = hotLoadTimestamp(status.GetCreatedAt()), hotLoadTimestamp(status.GetUpdatedAt())
	m.FinishedAt, m.CanceledAt = hotLoadTimestamp(status.GetFinishedAt()), hotLoadTimestamp(status.GetCanceledAt())
	chain := make([]attr.Value, 0, len(spec.GetSnapshotChain()))
	for _, s := range spec.GetSnapshotChain() {
		value, diags := types.ObjectValue(snapshotChainTypes, map[string]attr.Value{
			"identity": types.StringValue(s.GetIdentity()), "type": types.StringValue(s.GetType().String()),
			"compression_format": types.StringValue(s.GetCompressionFormat().String()),
			"checksum_format":    types.StringValue(s.GetChecksumFormat().String()), "hot_load_id": types.StringValue(s.GetHotLoadId()),
		})
		diagnostics.Append(diags...)
		chain = append(chain, value)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: snapshotChainTypes}, chain)
	diagnostics.Append(diags...)
	m.SnapshotChain = list
	conditions, diags := conditionsListFromStatus(status.GetConditions())
	diagnostics.Append(diags...)
	m.Conditions = conditions
	return diagnostics
}

func (r *InferenceHotLoadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m InferenceHotLoadResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fetched, err := r.client.GetHotLoad(ctx, connect.NewRequest(&v1.GetHotLoadRequest{Id: m.ID.ValueString()}))
	if coreweave.IsNotFoundError(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading hot-load failed", err.Error())
		return
	}
	resp.Diagnostics.Append(m.setFromHotLoad(fetched.Msg.GetHotLoad())...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *InferenceHotLoadResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Hot-load operations are immutable", "Changing operation inputs requires replacement.")
}

func (r *InferenceHotLoadResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m InferenceHotLoadResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fetched, err := r.client.GetHotLoad(ctx, connect.NewRequest(&v1.GetHotLoadRequest{Id: m.ID.ValueString()}))
	if coreweave.IsNotFoundError(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading hot-load before cancellation failed", err.Error())
		return
	}
	state := fetched.Msg.GetHotLoad().GetStatus().GetState()
	switch state {
	case v1.HotLoadState_HOT_LOAD_STATE_PENDING, v1.HotLoadState_HOT_LOAD_STATE_IN_PROGRESS:
		_, err = r.client.CancelHotLoad(ctx, connect.NewRequest(&v1.CancelHotLoadRequest{Id: m.ID.ValueString()}))
		if err != nil && !coreweave.IsNotFoundError(err) {
			resp.Diagnostics.AddError("Canceling hot-load failed", err.Error())
		}
	case v1.HotLoadState_HOT_LOAD_STATE_COMPLETED, v1.HotLoadState_HOT_LOAD_STATE_FAILED, v1.HotLoadState_HOT_LOAD_STATE_CANCELED:
		return
	case v1.HotLoadState_HOT_LOAD_STATE_UNSPECIFIED:
		resp.Diagnostics.AddError("Unknown hot-load state", "Cannot determine whether the operation needs cancellation; retaining it in state.")
	default:
		resp.Diagnostics.AddError("Unknown hot-load state", fmt.Sprintf("Unsupported hot-load state %d; retaining it in state.", state))
	}
}

func (r *InferenceHotLoadResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
