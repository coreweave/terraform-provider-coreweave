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
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// AccessConfigurationResource manages the Container Registry AccessConfiguration resource.
type AccessConfigurationResource struct{ client client.RegistryServiceClient }

var (
	_ resource.Resource                   = &AccessConfigurationResource{}
	_ resource.ResourceWithImportState    = &AccessConfigurationResource{}
	_ resource.ResourceWithValidateConfig = &AccessConfigurationResource{}
)

// NewAccessConfigurationResource constructs a AccessConfiguration resource.
func NewAccessConfigurationResource() resource.Resource {
	return &AccessConfigurationResource{}
}

// Metadata sets the resource's static Terraform type name.
func (r *AccessConfigurationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_registry_access_configuration"
}

// equalAccess compares canonical customer-controlled content, excluding rollout metadata.
func equalAccess(ctx context.Context, a, b *api.RegistryAccessConfiguration) (bool, error) {
	var av, bv AccessConfigurationResourceModel
	if err := conversionError(av.Set(ctx, a)); err != nil {
		return false, err
	}
	if err := conversionError(bv.Set(ctx, b)); err != nil {
		return false, err
	}
	return av.PolicySets.Equal(bv.PolicySets) && av.RequestIPACL.Equal(bv.RequestIPACL), nil
}

// accessApply owns an exact access revision without rebasing etags.
func (r *AccessConfigurationResource) accessApply(ctx context.Context, a, old *AccessConfigurationResourceModel, destroy bool, p privateData, parent string) (bool, error) {
	// Only successful server conversions may advance the update fallback snapshot.
	observe := func(value *api.RegistryAccessConfiguration) error {
		if err := conversionError(a.Set(ctx, value)); err != nil {
			return err
		}
		if old != nil {
			*old = *a
		}
		return nil
	}
	current, e := r.client.GetRegistryAccessConfiguration(ctx, connect.NewRequest(&api.GetRegistryAccessConfigurationRequest{Parent: parent}))
	if e != nil {
		return old != nil, e
	}
	if old != nil && current.Msg.Etag != old.Etag.ValueString() {
		return true, fmt.Errorf("access etag changed at %s (current %s); refresh and replan", parent, current.Msg.Etag)
	}
	desired := &api.RegistryAccessConfiguration{}
	if !destroy {
		var diags diag.Diagnostics
		desired, diags = accessInput(ctx, a.PolicySets, a.RequestIPACL, path.Empty())
		e = conversionError(diags)
		if e != nil {
			return old != nil, e
		}
	}
	revision := current.Msg.Revision
	if err := observe(current.Msg); err != nil {
		return old != nil, err
	}
	equal, err := equalAccess(ctx, desired, current.Msg)
	if err != nil {
		return old != nil, err
	}
	if !equal {
		desired.Name = parent + accessSuffix
		q := &api.UpdateRegistryAccessConfigurationRequest{Parent: parent, Etag: current.Msg.Etag, RegistryAccessConfiguration: desired}
		rec := &recovery{Action: recoveryAccess}
		if err := saveRecovery(ctx, p, rec); err != nil {
			return old != nil, err
		}
		res, e := r.client.UpdateRegistryAccessConfiguration(ctx, connect.NewRequest(q))
		if e != nil {
			last, re := r.client.GetRegistryAccessConfiguration(ctx, connect.NewRequest(&api.GetRegistryAccessConfigurationRequest{Parent: parent}))
			if re == nil {
				if err := observe(last.Msg); err != nil {
					return true, err
				}
				if last.Msg.Etag == current.Msg.Etag && last.Msg.Revision == current.Msg.Revision {
					if err := saveRecovery(ctx, p, nil); err != nil {
						return true, err
					}
					return old != nil || !definitiveRejection(e), e
				}
			}
			return true, fmt.Errorf("access update outcome requires refresh and review (an identical concurrent write cannot be attributed safely): %w", e)
		}
		revision = res.Msg.Revision
		rec.Revision = revision
		rec.Action = recoveryAccessAccepted
		if err := saveRecovery(ctx, p, rec); err != nil {
			return true, err
		}
		if err := observe(res.Msg); err != nil {
			return true, err
		}
	} else {
		if e := saveRecovery(ctx, p, &recovery{Action: recoveryAccessAccepted, Revision: revision}); e != nil {
			return true, e
		}
	}
	result, e := waitAccess(ctx, r.client, parent, revision)
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

// AccessConfigurationResourceModel is the typed Terraform model, preserving null and unknown values.
type AccessConfigurationResourceModel struct {
	Namespace         types.String   `tfsdk:"namespace"`
	ID                types.String   `tfsdk:"id"`
	Name              types.String   `tfsdk:"name"`
	Etag              types.String   `tfsdk:"etag"`
	CreatedAt         types.String   `tfsdk:"created_at"`
	UpdatedAt         types.String   `tfsdk:"updated_at"`
	Revision          types.Int64    `tfsdk:"revision"`
	Timeouts          timeouts.Value `tfsdk:"timeouts"`
	PolicySets        types.Map      `tfsdk:"policy_sets"`
	RequestIPACL      types.Object   `tfsdk:"request_ip_acl"`
	AccessConfigState types.String   `tfsdk:"access_config_state"`
}

// Schema defines the access_configuration ownership contract.
func (r *AccessConfigurationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Authoritatively manages the complete Container Registry access configuration. Omitted entries are removed. Use one owner per namespace; avoid concurrent changes from the console, CLI or another Terraform resource.\n\nDestroy resets access to deny-all and removes the IP ACL. It does not restore an earlier policy. Use `terraform state rm` or a `removed` block with `destroy = false` to relinquish ownership without resetting the policy.\n\nIf the parent is not ACTIVE, destroy warns and relinquishes ownership because the API cannot reset the policy. A retained namespace keeps its policy if it later becomes ACTIVE. Parent references establish deletion ordering.\n\nApply waits until the API reports the desired access revision as accepted. Action deadlines default to 20 minutes.", Attributes: accessResourceAttributes()}
}

// Configure reuses the provider authenticated Registry API client.
func (r *AccessConfigurationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *AccessConfigurationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var a AccessConfigurationResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &a)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if nullEntry(a.PolicySets) {
		return
	}
	if known(a.PolicySets) && known(a.RequestIPACL) {
		_, diags := accessInput(ctx, a.PolicySets, a.RequestIPACL, path.Empty())
		resp.Diagnostics.Append(diags...)
	} else {
		validateAccessBounds(ctx, a.PolicySets, a.RequestIPACL, &resp.Diagnostics)
	}
}

// Create records ownership only after accepted work or a verified singleton handoff.
func (r *AccessConfigurationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var a AccessConfigurationResourceModel
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
func (r *AccessConfigurationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var a AccessConfigurationResourceModel
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
		_, parentErr := getNamespace(c, r.client, namespaceResourceName(a.Namespace.ValueString()), false)
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
		err = observeRecovery(c, r.client, a.Revision.ValueInt64(), a.AccessConfigState.ValueString() == "ACCESS_CONFIG_STATE_ACCEPTED", resp.Private)
		resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
	}
	report(ctx, err, &resp.Diagnostics)
}

// Update preserves the planned etag and requires a new plan after recovering earlier work.
func (r *AccessConfigurationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var a, old AccessConfigurationResourceModel
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
func (r *AccessConfigurationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var a AccessConfigurationResourceModel
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
	parentNamespace, parentErr := getNamespace(c, r.client, namespaceResourceName(a.Namespace.ValueString()), false)
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

// ImportState accepts a bare namespace name without changing remote content.
func (r *AccessConfigurationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !validNamespace(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a namespace name, such as example-images")
		return
	}
	a := AccessConfigurationResourceModel{}
	a.PolicySets = types.MapNull(accessTypes()["policy_sets"].(types.MapType).ElemType)
	a.RequestIPACL = types.ObjectNull(accessTypes()["request_ip_acl"].(types.ObjectType).AttrTypes)
	a.Timeouts = nullResourceTimeouts()
	a.ID = types.StringValue(namespaceResourceName(req.ID) + accessSuffix)
	a.Name = a.ID
	a.Namespace = types.StringValue(req.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
}

// read refreshes observations owned by this resource.
func (r *AccessConfigurationResource) read(ctx context.Context, a *AccessConfigurationResourceModel) error {
	res, err := r.client.GetRegistryAccessConfiguration(ctx, connect.NewRequest(&api.GetRegistryAccessConfigurationRequest{Parent: namespaceResourceName(a.Namespace.ValueString())}))
	if err != nil {
		return err
	}
	return conversionError(a.Set(ctx, res.Msg))
}

// apply submits one concrete mutation and clears only terminal recovery evidence.
func (r *AccessConfigurationResource) apply(ctx context.Context, a, old *AccessConfigurationResourceModel, destroy bool, p privateData) (bool, error) {
	if !known(a.Namespace) {
		return old != nil, fmt.Errorf("namespace must be known at apply")
	}
	parent := namespaceResourceName(a.Namespace.ValueString())
	_, err := getNamespace(ctx, r.client, parent, !destroy)
	if err != nil {
		if destroy && coreweave.IsNotFoundError(err) {
			return true, nil
		}
		return old != nil, err
	}
	owned, err := r.accessApply(ctx, a, old, destroy, p, parent)
	return owned, finishRecovery(ctx, p, err)
}

// cleanUnknown preserves valid partial state after a failed mutation.
func (a *AccessConfigurationResourceModel) cleanUnknown() {
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
	if a.CreatedAt.IsUnknown() {
		a.CreatedAt = types.StringNull()
	}
	if a.UpdatedAt.IsUnknown() {
		a.UpdatedAt = types.StringNull()
	}
	if a.Revision.IsUnknown() {
		a.Revision = types.Int64Null()
	}
	if a.PolicySets.IsUnknown() {
		a.PolicySets = types.MapNull(accessTypes()["policy_sets"].(types.MapType).ElemType)
	}
	if a.RequestIPACL.IsUnknown() {
		a.RequestIPACL = types.ObjectNull(accessTypes()["request_ip_acl"].(types.ObjectType).AttrTypes)
	}
	if a.AccessConfigState.IsUnknown() {
		a.AccessConfigState = types.StringNull()
	}
}

// Set copies server observations while preserving local resource settings.
func (a *AccessConfigurationResourceModel) Set(ctx context.Context, p *api.RegistryAccessConfiguration) diag.Diagnostics {
	ts := accessTypes()
	pt := ts["policy_sets"].(types.MapType).ElemType.(types.ObjectType)
	rt := pt.AttrTypes["rules"].(types.MapType).ElemType
	sets := make(map[string]AccessPolicySetModel, len(p.PolicySets))
	var d diag.Diagnostics
	for _, set := range p.PolicySets {
		rules := make(map[string]AccessRuleModel, len(set.Rules))
		for _, rule := range set.Rules {
			rules[rule.Id] = AccessRuleModel{Expression: types.StringValue(rule.Expression)}
		}
		value, diags := types.MapValueFrom(ctx, rt, rules)
		d.Append(diags...)
		sets[set.Id] = AccessPolicySetModel{IdentitySelector: types.StringValue(strings.TrimPrefix(set.IdentitySelector.String(), "REGISTRY_IDENTITY_SELECTOR_")), Rules: value}
	}
	policies, diags := types.MapValueFrom(ctx, pt, sets)
	d.Append(diags...)
	acl := types.ObjectNull(ts["request_ip_acl"].(types.ObjectType).AttrTypes)
	if p.RequestIpAcl != nil {
		allow, diags := types.SetValueFrom(ctx, types.StringType, append([]string{}, p.RequestIpAcl.AllowCidrs...))
		d.Append(diags...)
		deny, diags := types.SetValueFrom(ctx, types.StringType, append([]string{}, p.RequestIpAcl.DenyCidrs...))
		d.Append(diags...)
		acl, diags = types.ObjectValueFrom(ctx, ts["request_ip_acl"].(types.ObjectType).AttrTypes, IPACLModel{AllowCIDRs: allow, DenyCIDRs: deny})
		d.Append(diags...)
	}
	if d.HasError() {
		return d
	}
	a.PolicySets = policies
	a.RequestIPACL = acl
	a.ID = types.StringValue(p.Name)
	a.Name = types.StringValue(p.Name)
	a.Etag = types.StringValue(p.Etag)
	a.Revision = types.Int64Value(p.Revision)
	a.AccessConfigState = types.StringValue(p.AccessConfigState.String())
	a.CreatedAt = timestamp(p.CreateTime)
	a.UpdatedAt = timestamp(p.UpdateTime)
	return d
}
