package containerregistry

import (
	"context"
	"fmt"
	"strings"
	"time"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/coreweave/registry/v1alpha1/registryv1alpha1connect"
	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/encoding/protojson"
)

// LifecyclePolicyResource manages the Container Registry LifecyclePolicy resource.
type LifecyclePolicyResource struct{ client client.RegistryServiceClient }

var (
	_ resource.Resource                   = &LifecyclePolicyResource{}
	_ resource.ResourceWithImportState    = &LifecyclePolicyResource{}
	_ resource.ResourceWithValidateConfig = &LifecyclePolicyResource{}
)

// NewLifecyclePolicyResource constructs a LifecyclePolicy resource.
func NewLifecyclePolicyResource() resource.Resource {
	return &LifecyclePolicyResource{}
}

// Metadata sets the resource's static Terraform type name.
func (r *LifecyclePolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_registry_lifecycle_policy"
}

// equalLifecycle compares policy content independently of revision and ordering.
func equalLifecycle(ctx context.Context, a, b *api.RegistryLifecyclePolicy) (bool, error) {
	var av, bv LifecyclePolicyResourceModel
	if err := conversionError(av.Set(ctx, a)); err != nil {
		return false, err
	}
	if err := conversionError(bv.Set(ctx, b)); err != nil {
		return false, err
	}
	return av.Rules.Equal(bv.Rules) && av.Enabled.Equal(bv.Enabled), nil
}

// lifecycleApply submits and verifies the compact acknowledgement.
func (r *LifecyclePolicyResource) lifecycleApply(ctx context.Context, a, old *LifecyclePolicyResourceModel, destroy bool, p privateData, parent string) (bool, error) {
	// Only successful server conversions may advance the update fallback snapshot.
	observe := func(value *api.RegistryLifecyclePolicy) error {
		if err := conversionError(a.Set(ctx, value)); err != nil {
			return err
		}
		if old != nil {
			*old = *a
		}
		return nil
	}
	current, e := r.client.GetRegistryLifecyclePolicy(ctx, connect.NewRequest(&api.GetRegistryLifecyclePolicyRequest{Parent: parent}))
	if e != nil {
		return old != nil, e
	}
	if old != nil && current.Msg.Etag != old.Etag.ValueString() {
		return true, fmt.Errorf("lifecycle etag changed at %s (current %s); refresh and replan", parent, current.Msg.Etag)
	}
	desired := &api.RegistryLifecyclePolicy{}
	if !destroy {
		var diags diag.Diagnostics
		desired, diags = lifecycleInput(ctx, a.Rules, a.Enabled)
		e = conversionError(diags)
		if e != nil {
			return old != nil, e
		}
	}
	revision := current.Msg.Revision
	if err := observe(current.Msg); err != nil {
		return old != nil, err
	}
	equal, err := equalLifecycle(ctx, desired, current.Msg)
	if err != nil {
		return old != nil, err
	}
	if !equal {
		key, e := newIdempotencyKey()
		if e != nil {
			return old != nil, e
		}
		q := &api.UpdateRegistryLifecyclePolicyRequest{Parent: parent, Etag: current.Msg.Etag, RegistryLifecyclePolicy: desired, IdempotencyKey: key}
		rec := &recovery{Action: recoveryLifecycle}
		if err := saveRecovery(ctx, p, rec); err != nil {
			return old != nil, err
		}
		res, e := r.client.UpdateRegistryLifecyclePolicy(ctx, connect.NewRequest(q))
		if e != nil {
			return old != nil || !definitiveRejection(e), e
		}
		rec.Operation = res.Msg.Name
		if err := saveRecovery(ctx, p, rec); err != nil {
			return true, err
		}
		ack := new(api.UpdateRegistryLifecyclePolicyResponse)
		if e = waitOperation(ctx, r.client, res.Msg, ack); e != nil {
			return true, e
		}
		if ack.Name != parent+lifecycleSuffix {
			return true, fmt.Errorf("lifecycle acknowledgement returned unexpected policy name %s", ack.Name)
		}
		revision = ack.AppliedRevision
		rec.Revision = revision
		rec.Operation = ""
		rec.Action = recoveryLifecycleWait
		if err := saveRecovery(ctx, p, rec); err != nil {
			return true, err
		}
	} else {
		if e := saveRecovery(ctx, p, &recovery{Action: recoveryLifecycleWait, Revision: revision}); e != nil {
			return true, e
		}
	}
	result, e := waitLifecycle(ctx, r.client, parent, revision)
	if result != nil {
		if err := observe(result); err != nil {
			return true, err
		}
	}
	if e != nil {
		return true, e
	}
	return true, saveRecovery(ctx, p, nil)
}

// LifecyclePolicyResourceModel is the typed Terraform model, preserving null and unknown values.
type LifecyclePolicyResourceModel struct {
	Namespace       types.String   `tfsdk:"namespace"`
	ID              types.String   `tfsdk:"id"`
	Name            types.String   `tfsdk:"name"`
	Etag            types.String   `tfsdk:"etag"`
	CreateTime      types.String   `tfsdk:"create_time"`
	UpdateTime      types.String   `tfsdk:"update_time"`
	Revision        types.Int64    `tfsdk:"revision"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
	Enabled         types.Bool     `tfsdk:"enabled"`
	Rules           types.Map      `tfsdk:"rules"`
	AppliedRevision types.Int64    `tfsdk:"applied_revision"`
}

// Schema defines the lifecycle_policy ownership contract.
func (r *LifecyclePolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Authoritatively manages the complete Container Registry lifecycle policy. Omitted entries are removed. Use one owner per namespace; avoid concurrent changes from the console, CLI or another Terraform resource.\n\nDestroy disables retention and removes all lifecycle rules. It does not restore an earlier policy. Use `terraform state rm` or a `removed` block with `destroy = false` to relinquish ownership without resetting the policy.\n\nIf the parent is not ACTIVE, destroy warns and relinquishes ownership because the API cannot reset the policy. A retained namespace keeps its policy if it later becomes ACTIVE. Parent references establish deletion ordering.\n\nAction deadlines default to 20 minutes.", Attributes: lifecycleResourceAttributes()}
}

// Configure reuses the provider authenticated Registry API client.
func (r *LifecyclePolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*coreweave.Client)
	if !ok || c.ContainerRegistry == nil {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *coreweave.Client with a configured Container Registry client, got: %T. Please report this issue to the provider developers.", req.ProviderData))
		return
	}
	r.client = c.ContainerRegistry
}

// ValidateConfig checks cross-field policy constraints while deferring unknown values.
func (r *LifecyclePolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var a LifecyclePolicyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &a)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if nullEntry(a.Rules) {
		return
	}
	if known(a.Rules) && known(a.Enabled) {
		_, diags := lifecycleInput(ctx, a.Rules, a.Enabled)
		resp.Diagnostics.Append(diags...)
	} else {
		validateLifecycleConditions(ctx, a.Rules, &resp.Diagnostics)
	}
}

// Create records ownership only after accepted work or a verified singleton handoff.
func (r *LifecyclePolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var a LifecyclePolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &a)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := a.Timeouts.Create(ctx, 20*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owned, err := r.apply(c, &a, nil, false, resp.Private)
	if owned {
		a.cleanUnknown()
		resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
	} else {
		resp.State.RemoveResource(ctx)
	}
	report(ctx, err, &resp.Diagnostics)
}

// Read refreshes observations without waiting for or replaying saved mutations.
func (r *LifecyclePolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var a LifecyclePolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &a)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := a.Timeouts.Read(ctx, 20*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := r.read(c, &a)
	if coreweave.IsNotFoundError(err) {
		_, parentErr := getNamespace(c, r.client, a.Namespace.ValueString(), false)
		if coreweave.IsNotFoundError(parentErr) {
			resp.State.RemoveResource(ctx)
			return
		}
		if parentErr != nil {
			err = parentErr
		} else {
			err = fmt.Errorf("singleton missing under existing parent %s; API inconsistency", a.Namespace.ValueString())
		}
	}
	if err == nil {
		err = observeRecovery(c, r.client, a.Revision.ValueInt64(), !a.AppliedRevision.IsNull() && a.AppliedRevision.ValueInt64() == a.Revision.ValueInt64(), resp.Private)
		resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
	}
	report(ctx, err, &resp.Diagnostics)
}

// Update preserves the planned etag and requires a new plan after recovering earlier work.
func (r *LifecyclePolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var a, old LifecyclePolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &a)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := a.Timeouts.Update(ctx, 20*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	plannedETag := old.Etag.ValueString()
	err := r.resume(c, &old, resp.Private)
	if err == nil && old.Etag.ValueString() != plannedETag {
		err = fmt.Errorf("recovered an earlier mutation; refresh and replan before changing content")
	}
	if err == nil {
		_, err = r.apply(c, &a, &old, false, resp.Private)
	}
	if err == nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
	} else {
		if c.Err() == nil {
			_ = r.read(c, &old)
		}
		old.cleanUnknown()
		resp.Diagnostics.Append(resp.State.Set(ctx, &old)...)
	}
	report(ctx, err, &resp.Diagnostics)
}

// Delete verifies namespace absence or resets the owned singleton before relinquishing state.
func (r *LifecyclePolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var a LifecyclePolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &a)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := a.Timeouts.Delete(ctx, 20*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	parentNamespace, parentErr := getNamespace(c, r.client, a.Namespace.ValueString(), false)
	if coreweave.IsNotFoundError(parentErr) {
		resp.State.RemoveResource(ctx)
		return
	}
	if parentErr != nil {
		report(ctx, parentErr, &resp.Diagnostics)
		return
	}
	if parentNamespace.State != api.RegistryNamespace_STATE_ACTIVE {
		resp.Diagnostics.AddWarning("Policy reset unavailable", "The parent namespace is not ACTIVE, so the API cannot reset its policy. Terraform is relinquishing this singleton to allow namespace cleanup. If the namespace is retained and later becomes ACTIVE, its existing policy remains in effect.")
		resp.State.RemoveResource(ctx)
		return
	}
	err := r.resume(c, &a, resp.Private)
	if err == nil {
		_, err = r.apply(c, &a, &a, true, resp.Private)
	}
	if err == nil {
		resp.State.RemoveResource(ctx)
	} else {
		a.cleanUnknown()
		resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
	}
	report(ctx, err, &resp.Diagnostics)
}

// ImportState accepts canonical names without changing remote content.
func (r *LifecyclePolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parent := req.ID
	if !strings.HasSuffix(parent, lifecycleSuffix) {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a canonical singleton name ending in "+lifecycleSuffix)
		return
	}
	parent = strings.TrimSuffix(parent, lifecycleSuffix)
	if !validParent(parent) {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a canonical namespace resource name")
		return
	}
	a := LifecyclePolicyResourceModel{}
	a.Rules = types.MapNull(lifecycleTypes()["rules"].(types.MapType).ElemType)
	a.Timeouts = nullResourceTimeouts()
	a.ID = types.StringValue(req.ID)
	a.Name = types.StringValue(req.ID)
	a.Namespace = types.StringValue(parent)
	resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
}

// read refreshes observations owned by this resource.
func (r *LifecyclePolicyResource) read(ctx context.Context, a *LifecyclePolicyResourceModel) error {
	res, err := r.client.GetRegistryLifecyclePolicy(ctx, connect.NewRequest(&api.GetRegistryLifecyclePolicyRequest{Parent: a.Namespace.ValueString()}))
	if err != nil {
		return err
	}
	return conversionError(a.Set(ctx, res.Msg))
}

// apply submits one concrete mutation and clears only terminal recovery evidence.
func (r *LifecyclePolicyResource) apply(ctx context.Context, a, old *LifecyclePolicyResourceModel, destroy bool, p privateData) (bool, error) {
	if !known(a.Namespace) {
		return old != nil, fmt.Errorf("namespace must be known at apply")
	}
	parent := a.Namespace.ValueString()
	_, err := getNamespace(ctx, r.client, parent, !destroy)
	if err != nil {
		if destroy && coreweave.IsNotFoundError(err) {
			return true, nil
		}
		return old != nil, err
	}
	owned, err := r.lifecycleApply(ctx, a, old, destroy, p, parent)
	return owned, finishRecovery(ctx, p, err)
}

// cleanUnknown preserves valid partial state after a failed mutation.
func (a *LifecyclePolicyResourceModel) cleanUnknown() {
	if a.Namespace.IsUnknown() {
		a.Namespace = types.StringNull()
	}
	if a.ID.IsUnknown() {
		a.ID = types.StringNull()
	}
	if a.Name.IsUnknown() {
		a.Name = types.StringNull()
	}
	if a.Etag.IsUnknown() {
		a.Etag = types.StringNull()
	}
	if a.CreateTime.IsUnknown() {
		a.CreateTime = types.StringNull()
	}
	if a.UpdateTime.IsUnknown() {
		a.UpdateTime = types.StringNull()
	}
	if a.Revision.IsUnknown() {
		a.Revision = types.Int64Null()
	}
	if a.Enabled.IsUnknown() {
		a.Enabled = types.BoolNull()
	}
	if a.Rules.IsUnknown() {
		a.Rules = types.MapNull(lifecycleTypes()["rules"].(types.MapType).ElemType)
	}
	if a.AppliedRevision.IsUnknown() {
		a.AppliedRevision = types.Int64Null()
	}
}

// Set copies server observations while preserving local resource settings.
func (a *LifecyclePolicyResourceModel) Set(ctx context.Context, p *api.RegistryLifecyclePolicy) diag.Diagnostics {
	rt := lifecycleTypes()["rules"].(types.MapType).ElemType
	rules := make(map[string]LifecycleRuleModel, len(p.Rules))
	for _, rule := range p.Rules {
		model := LifecycleRuleModel{Regex: types.StringValue(rule.Regex), OlderThan: types.StringNull(), KeepNewest: types.Int64Null()}
		if rule.OlderThan != nil {
			data, err := protojson.Marshal(rule.OlderThan)
			if err != nil {
				return diag.Diagnostics{diag.NewErrorDiagnostic("Invalid lifecycle age", err.Error())}
			}
			model.OlderThan = types.StringValue(strings.Trim(string(data), `"`))
		}
		if rule.KeepNewest != nil {
			model.KeepNewest = types.Int64Value(int64(*rule.KeepNewest))
		}
		rules[rule.RuleId] = model
	}
	value, d := types.MapValueFrom(ctx, rt, rules)
	if d.HasError() {
		return d
	}
	a.Rules = value
	a.Enabled = types.BoolValue(p.Enabled)
	a.ID = types.StringValue(p.Name)
	a.Name = types.StringValue(p.Name)
	a.Etag = types.StringValue(p.Etag)
	a.Revision = types.Int64Value(p.Revision)
	a.AppliedRevision = types.Int64Null()
	if p.AppliedRevision != nil {
		a.AppliedRevision = types.Int64Value(*p.AppliedRevision)
	}
	a.CreateTime = timestamp(p.CreateTime)
	a.UpdateTime = timestamp(p.UpdateTime)
	return d
}
