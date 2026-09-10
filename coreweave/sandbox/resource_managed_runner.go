package sandbox

import (
	"context"
	"fmt"
	"slices"
	"time"

	sandboxv1 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/go-uuid"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	_ resource.Resource                   = &ManagedRunnerResource{}
	_ resource.ResourceWithConfigure      = &ManagedRunnerResource{}
	_ resource.ResourceWithImportState    = &ManagedRunnerResource{}
	_ resource.ResourceWithValidateConfig = &ManagedRunnerResource{}
)

type ManagedRunnerResource struct{ client *coreweave.Client }

type managedRunnerModel struct {
	ID                  types.String         `tfsdk:"id"`
	RunnerID            types.String         `tfsdk:"runner_id"`
	RunnerGroupID       types.String         `tfsdk:"runner_group_id"`
	Zone                types.String         `tfsdk:"zone"`
	ClusterID           types.String         `tfsdk:"cluster_id"`
	ClusterName         types.String         `tfsdk:"cluster_name"`
	DisplayName         types.String         `tfsdk:"display_name"`
	Spec                types.Object         `tfsdk:"spec"`
	Policy              types.Object         `tfsdk:"policy"`
	Etag                types.String         `tfsdk:"etag"`
	InstallStatus       types.String         `tfsdk:"install_status"`
	ConnectionStatus    types.String         `tfsdk:"connection_status"`
	ActiveConfigVersion types.String         `tfsdk:"active_config_version"`
	ActiveRevision      types.Int64          `tfsdk:"active_revision"`
	TargetRevision      types.Int64          `tfsdk:"target_revision"`
	UpdateAvailable     types.Bool           `tfsdk:"update_available"`
	RolloutInProgress   types.Bool           `tfsdk:"rollout_in_progress"`
	DeploymentSpec      jsontypes.Normalized `tfsdk:"deployment_spec"`
	InstallError        jsontypes.Normalized `tfsdk:"install_error"`
	DataPlaneStatus     jsontypes.Normalized `tfsdk:"data_plane_status"`
	CreateTime          types.String         `tfsdk:"create_time"`
	UpdateTime          types.String         `tfsdk:"update_time"`
	LastHeartbeatTime   types.String         `tfsdk:"last_heartbeat_time"`
}

func NewManagedRunnerResource() resource.Resource { return &ManagedRunnerResource{} }

func (r *ManagedRunnerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sandbox_managed_runner"
}

func (r *ManagedRunnerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	identity := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Required: true, MarkdownDescription: description, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
	}
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: description}
	}
	computedJSON := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, CustomType: jsontypes.NormalizedType{}, MarkdownDescription: description}
	}
	deployment := computedJSON("Resolved deployment configuration as JSON. Read-only; use spec.overrides to change supported settings.")
	deployment.Sensitive = true
	runnerGroup := optionalString("Runner group for scheduling affinity. Defaults to `default`; omitting or setting this attribute to null resets a custom group to `default`.")
	runnerGroup.Computed = true
	runnerGroup.Default = stringdefault.StaticString("default")
	runnerGroup.Validators = []validator.String{stringvalidator.LengthAtLeast(1)}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a CoreWeave Sandbox runner and its required policy through the v1 RunnerManagementService (`/v1/sandbox/managedRunners`). Requires a server implementing full v1 managed-runner CRUD; the policy-only v1 server cannot provision or fully refresh this resource. Creation and updates record the accepted desired configuration; installation and rollout continue asynchronously and are exposed in computed status attributes. Deletion waits up to 30 minutes for the runner to disappear.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Computed: true, MarkdownDescription: "Terraform resource ID, equal to runner_id within the authenticated organization.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"runner_id":             identity("Operator-assigned runner identifier, unique within the authenticated organization. Changing it replaces the runner."),
			"zone":                  identity("Lowercase geographic zone of the runner, for example `us-east-04a`. Use `lower(coreweave_cks_cluster.example.zone)` when referencing a CKS cluster zone. Changing it replaces the runner."),
			"cluster_id":            identity("CKS cluster UUID. Changing it replaces the runner."),
			"runner_group_id":       runnerGroup,
			"cluster_name":          computed("Cluster display name resolved by the server."),
			"display_name":          optionalString("Human-readable runner name."),
			"spec":                  runnerSpecAttribute(),
			"policy":                policyAttribute(),
			"etag":                  computed("Opaque policy revision used for optimistic concurrency. A concurrent policy edit fails the apply; refresh the plan before retrying."),
			"install_status":        computed("Observed installation status."),
			"connection_status":     computed("Observed live connection status."),
			"active_config_version": computed("Configuration version currently running."),
			"active_revision":       schema.Int64Attribute{Computed: true, MarkdownDescription: "Revision currently running."},
			"target_revision":       schema.Int64Attribute{Computed: true, MarkdownDescription: "Desired deployment revision."},
			"update_available":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether a newer version is available on the release channel."},
			"rollout_in_progress":   schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether a newer desired revision is still rolling out."},
			"deployment_spec":       deployment,
			"install_error":         computedJSON("Structured installation failure details as JSON, or null."),
			"data_plane_status":     computedJSON("Observed direct endpoint state, URI, protocols, and certificate expiry as JSON, or null."),
			"create_time":           computed("Creation timestamp (RFC 3339)."),
			"update_time":           computed("Last configuration update timestamp (RFC 3339)."),
			"last_heartbeat_time":   computed("Last runner heartbeat timestamp (RFC 3339)."),
		},
	}
}

func (r *ManagedRunnerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (m *managedRunnerModel) runner() (*sandboxv1.ManagedRunner, error) {
	runner := &sandboxv1.ManagedRunner{
		Identity:    &sandboxv1.RunnerIdentity{RunnerId: m.RunnerID.ValueString(), RunnerGroupId: m.RunnerGroupID.ValueString(), Zone: m.Zone.ValueString(), ClusterId: m.ClusterID.ValueString()},
		DisplayName: m.DisplayName.ValueString(),
		Policy:      &sandboxv1.Policy{},
	}
	if m.Policy.IsNull() {
		return nil, fmt.Errorf("policy is required; use an explicit empty object for the empty posture")
	}
	if err := objectToProto(m.Policy, runner.Policy); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	if err := validatePolicy(runner.Policy); err != nil {
		return nil, err
	}
	if !m.Spec.IsNull() && !m.Spec.IsUnknown() {
		runner.Spec = &sandboxv1.ManagedRunnerSpec{}
		if err := objectToProto(m.Spec, runner.Spec); err != nil {
			return nil, fmt.Errorf("spec: %w", err)
		}
		if err := validateSpec(runner.Spec); err != nil {
			return nil, err
		}
	}
	return runner, nil
}

func (m *managedRunnerModel) updateRequest(previous *managedRunnerModel) (*sandboxv1.UpdateManagedRunnerRequest, error) {
	runner, err := m.runner()
	if err != nil {
		return nil, err
	}
	mask := &fieldmaskpb.FieldMask{}
	if !m.DisplayName.Equal(previous.DisplayName) {
		mask.Paths = append(mask.Paths, "display_name")
	}
	if !m.RunnerGroupID.Equal(previous.RunnerGroupID) {
		mask.Paths = append(mask.Paths, "identity.runner_group_id")
	}
	if !m.Policy.Equal(previous.Policy) {
		oldPolicy := &sandboxv1.Policy{}
		if err := objectToProto(previous.Policy, oldPolicy); err != nil {
			return nil, err
		}
		if !proto.Equal(oldPolicy, runner.Policy) {
			if previous.Etag.IsNull() || previous.Etag.IsUnknown() || previous.Etag.ValueString() == "" {
				return nil, fmt.Errorf("the server did not return a policy etag; refresh against a server supporting v1 policy concurrency before updating")
			}
			mask.Paths = append(mask.Paths, "policy")
			runner.Etag = previous.Etag.ValueString()
		}
	}
	if !m.Spec.IsUnknown() && !m.Spec.Equal(previous.Spec) {
		for name, value := range m.Spec.Attributes() {
			old, ok := previous.Spec.Attributes()[name]
			if !value.IsUnknown() && (!ok || !value.Equal(old)) {
				mask.Paths = append(mask.Paths, "spec."+name)
			}
		}
	}
	slices.Sort(mask.Paths)
	return &sandboxv1.UpdateManagedRunnerRequest{RunnerId: previous.ID.ValueString(), ManagedRunner: runner, UpdateMask: mask}, nil
}

func optionalResponseString(previous types.String, value string) types.String {
	if previous.IsNull() && value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

func responseTimestamp(value *timestamppb.Timestamp) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(value.AsTime().Format(time.RFC3339Nano))
}

func responseJSON(message proto.Message) (jsontypes.Normalized, error) {
	if !message.ProtoReflect().IsValid() {
		return jsontypes.NewNormalizedNull(), nil
	}
	raw, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
	if err != nil {
		return jsontypes.NewNormalizedNull(), err
	}
	return jsontypes.NewNormalizedValue(string(raw)), nil
}

func (m *managedRunnerModel) setRunner(ctx context.Context, runner *sandboxv1.ManagedRunner) error {
	if runner.GetIdentity().GetRunnerId() == "" {
		return fmt.Errorf("the server returned a runner without an identity; import the runner by runner_id before applying again")
	}
	if expected := m.RunnerID.ValueString(); expected != "" && expected != runner.GetIdentity().GetRunnerId() {
		return fmt.Errorf("the server returned a different runner identity")
	}
	if runner.GetPolicy() == nil || runner.GetSpec() == nil {
		return fmt.Errorf("the server returned an incomplete runner: full v1 managed-runner CRUD is required; the policy-only v1 implementation does not return deployment settings")
	}
	var err error
	m.Spec, err = objectFromProto(ctx, runner.Spec, m.Spec, runnerSpecAttribute().GetType().(types.ObjectType))
	if err != nil {
		return fmt.Errorf("spec: %w", err)
	}
	m.Policy, err = objectFromProto(ctx, runner.Policy, m.Policy, policyAttribute().GetType().(types.ObjectType))
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	m.ID = types.StringValue(runner.Identity.RunnerId)
	m.RunnerID = m.ID
	m.RunnerGroupID = optionalResponseString(m.RunnerGroupID, runner.Identity.RunnerGroupId)
	m.Zone = types.StringValue(runner.Identity.Zone)
	m.ClusterID = types.StringValue(runner.Identity.ClusterId)
	m.ClusterName = types.StringValue(runner.Identity.ClusterName)
	m.DisplayName = optionalResponseString(m.DisplayName, runner.DisplayName)
	m.Etag = types.StringValue(runner.Etag)
	m.InstallStatus = types.StringValue(runner.InstallStatus.String())
	m.ConnectionStatus = types.StringValue(runner.ConnectionStatus.String())
	m.ActiveConfigVersion = types.StringValue(runner.ActiveConfigVersion)
	m.ActiveRevision = types.Int64Value(int64(runner.ActiveRevision))
	m.TargetRevision = types.Int64Value(int64(runner.TargetRevision))
	m.UpdateAvailable = types.BoolValue(runner.UpdateAvailable)
	m.RolloutInProgress = types.BoolValue(runner.RolloutInProgress)
	m.CreateTime = responseTimestamp(runner.CreateTime)
	m.UpdateTime = responseTimestamp(runner.UpdateTime)
	m.LastHeartbeatTime = responseTimestamp(runner.LastHeartbeatTime)
	m.DeploymentSpec, err = responseJSON(runner.DeploymentSpec)
	if err != nil {
		return err
	}
	m.InstallError, err = responseJSON(runner.InstallError)
	if err != nil {
		return err
	}
	m.DataPlaneStatus, err = responseJSON(runner.DataPlaneStatus)
	return err
}

// An update can resolve unknown inputs to their previous values. In that case
// no mutation happened: a concurrent remote change must not replace any known
// planned value. Refresh computed outputs now; the next Read reports drift.
func (m *managedRunnerModel) setNoOpRunner(ctx context.Context, runner *sandboxv1.ManagedRunner) error {
	planned := *m
	if err := m.setRunner(ctx, runner); err != nil {
		return err
	}
	m.RunnerID = planned.RunnerID
	m.RunnerGroupID = planned.RunnerGroupID
	m.Zone = planned.Zone
	m.ClusterID = planned.ClusterID
	m.DisplayName = planned.DisplayName
	m.Policy = planned.Policy
	if !planned.Spec.IsUnknown() {
		m.Spec = planned.Spec
	}
	return nil
}

func (r *ManagedRunnerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data managedRunnerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	runner, err := data.runner()
	if err != nil {
		resp.Diagnostics.AddError("Invalid Managed Runner", err.Error())
		return
	}
	requestID, err := uuid.GenerateUUID()
	if err != nil {
		resp.Diagnostics.AddError("Unable to Generate Request ID", err.Error())
		return
	}
	created, err := r.client.SandboxRunnerManagement.CreateManagedRunner(ctx, connect.NewRequest(&sandboxv1.CreateManagedRunnerRequest{ManagedRunner: runner, RequestId: requestID}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := data.setRunner(ctx, created.Msg); err != nil {
		resp.Diagnostics.AddError("Invalid Managed Runner Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ManagedRunnerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data managedRunnerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	result, err := r.client.SandboxRunnerManagement.GetManagedRunner(ctx, connect.NewRequest(&sandboxv1.GetManagedRunnerRequest{RunnerId: data.ID.ValueString()}))
	if coreweave.IsNotFoundError(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := data.setRunner(ctx, result.Msg); err != nil {
		resp.Diagnostics.AddError("Invalid Managed Runner Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ManagedRunnerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, previous managedRunnerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	request, err := data.updateRequest(&previous)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Managed Runner Update", err.Error())
		return
	}
	var result *connect.Response[sandboxv1.ManagedRunner]
	if len(request.UpdateMask.Paths) == 0 {
		result, err = r.client.SandboxRunnerManagement.GetManagedRunner(ctx, connect.NewRequest(&sandboxv1.GetManagedRunnerRequest{RunnerId: previous.ID.ValueString()}))
	} else {
		result, err = r.client.SandboxRunnerManagement.UpdateManagedRunner(ctx, connect.NewRequest(request))
	}
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if len(request.UpdateMask.Paths) == 0 {
		err = data.setNoOpRunner(ctx, result.Msg)
	} else {
		err = data.setRunner(ctx, result.Msg)
	}
	if err != nil {
		resp.Diagnostics.AddError("Invalid Managed Runner Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ManagedRunnerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data managedRunnerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.SandboxRunnerManagement.DeleteManagedRunner(ctx, connect.NewRequest(&sandboxv1.DeleteManagedRunnerRequest{RunnerId: data.ID.ValueString(), AllowMissing: true}))
	if coreweave.IsNotFoundError(err) {
		return
	}
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	// Decommissioning is asynchronous. Keep ownership in state until deletion
	// completes so replacement cannot race cluster uniqueness constraints.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		_, err := r.client.SandboxRunnerManagement.GetManagedRunner(ctx, connect.NewRequest(&sandboxv1.GetManagedRunnerRequest{RunnerId: data.ID.ValueString()}))
		if coreweave.IsNotFoundError(err) {
			return
		}
		if err != nil {
			coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
			return
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Managed Runner Deletion Incomplete", ctx.Err().Error())
			return
		case <-ticker.C:
		}
	}
}

func (r *ManagedRunnerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid Import ID", "Use the runner_id from the authenticated organization.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("runner_id"), req.ID)...)
}
