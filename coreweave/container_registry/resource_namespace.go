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
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// NamespaceResource manages the Container Registry Namespace resource.
type NamespaceResource struct{ client client.RegistryServiceClient }

var (
	_ resource.Resource                   = &NamespaceResource{}
	_ resource.ResourceWithImportState    = &NamespaceResource{}
	_ resource.ResourceWithValidateConfig = &NamespaceResource{}
)

// NewNamespaceResource constructs a Namespace resource.
func NewNamespaceResource() resource.Resource {
	return &NamespaceResource{}
}

// Metadata sets the resource's static Terraform type name.
func (r *NamespaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_registry_namespace"
}

// namespaceApply preserves quota presence, bootstrap atomicity, and explicit force deletion.
func (r *NamespaceResource) namespaceApply(ctx context.Context, a, old *NamespaceResourceModel, destroy bool, p privateData) (bool, error) {
	creating := old == nil
	mutating := creating || !a.StorageQuotaBytes.Equal(old.StorageQuotaBytes)
	name := namespaceResourceName(a.Name.ValueString())
	if !creating {
		name = old.ID.ValueString()
		a.Name = old.Name
		a.ID = old.ID
	}
	key, e := newIdempotencyKey()
	if e != nil {
		return false, e
	}
	if destroy {
		return r.namespaceDelete(ctx, a, name, key, p)
	}
	if !known(a.Name) || !known(a.Zone) || !known(a.StorageQuotaBytes) || !known(a.InitialAccessConfiguration) {
		return !creating, fmt.Errorf("namespace inputs must be known at apply")
	}
	n := &api.RegistryNamespace{Zone: a.Zone.ValueString()}
	if !a.StorageQuotaBytes.IsNull() {
		q := a.StorageQuotaBytes.ValueInt64()
		if q < 0 {
			return !creating, fmt.Errorf("negative quota")
		}
		u := uint64(q)
		n.StorageQuotaBytes = &u
	}
	var owned bool
	if creating {
		owned, e = r.namespaceCreate(ctx, a, n, name, key, p)
	} else {
		owned = true
		e = r.namespaceUpdate(ctx, a, old, n, name, key, p)
	}
	if e != nil {
		return owned, e
	}

	if e = r.read(ctx, a); e != nil {
		return true, e
	}
	if mutating && a.Status.ValueString() != "STATE_ACTIVE" {
		return true, fmt.Errorf("namespace %s is %s after completion", name, a.Status.ValueString())
	}
	return true, saveRecovery(ctx, p, nil)
}

// namespaceDelete confirms absence after the deletion operation completes.
func (r *NamespaceResource) namespaceDelete(ctx context.Context, a *NamespaceResourceModel, name, key string, p privateData) (bool, error) {
	q := &api.DeleteRegistryNamespaceRequest{Name: name, Force: a.ForceDestroy.ValueBool(), IdempotencyKey: key}
	rec := &recovery{Action: actionDelete}
	if e := saveRecovery(ctx, p, rec); e != nil {
		return true, e
	}
	res, e := r.client.DeleteRegistryNamespace(ctx, connect.NewRequest(q))
	if coreweave.IsNotFoundError(e) {
		return true, saveRecovery(ctx, p, nil)
	}
	if e != nil {
		return true, e
	}
	rec.Operation = res.Msg.Name
	if err := saveRecovery(ctx, p, rec); err != nil {
		return true, err
	}
	if e = waitOperation(ctx, r.client, res.Msg, &emptypb.Empty{}); e != nil {
		return true, e
	}
	err := poll(ctx, "namespace deletion "+name, func(ctx context.Context) (bool, error) {
		_, err := getNamespace(ctx, r.client, name, false)
		if coreweave.IsNotFoundError(err) {
			return true, nil
		}
		return false, err
	})
	if err != nil {
		return true, err
	}
	return true, saveRecovery(ctx, p, nil)
}

// namespaceCreate submits atomic bootstrap and retains ownership after acceptance.
func (r *NamespaceResource) namespaceCreate(ctx context.Context, a *NamespaceResourceModel, n *api.RegistryNamespace, name, key string, p privateData) (bool, error) {
	q := &api.CreateRegistryNamespaceRequest{RegistryNamespaceId: a.Name.ValueString(), RegistryNamespace: n, IdempotencyKey: key}
	if b := a.InitialAccessConfiguration; !b.IsNull() {
		var e error
		var bootstrap AccessConfigurationModel
		if err := conversionError(b.As(ctx, &bootstrap, basetypes.ObjectAsOptions{})); err != nil {
			return false, err
		}
		var diags diag.Diagnostics
		q.RegistryAccessConfiguration, diags = accessInput(ctx, bootstrap.PolicySets, bootstrap.RequestIPACL, path.Root("initial_access_configuration"))
		e = conversionError(diags)
		if e != nil {
			return false, e
		}
	}
	var diags diag.Diagnostics
	validateProto(q, &diags)
	if diags.HasError() {
		return false, conversionError(diags)
	}
	rec := &recovery{Action: actionCreate}
	if err := saveRecovery(ctx, p, rec); err != nil {
		return false, err
	}
	res, e := r.client.CreateRegistryNamespace(ctx, connect.NewRequest(q))
	if e != nil {
		return false, fmt.Errorf("create %s: %w; if the response was lost, inspect server operations and explicitly import the verified namespace; an existing name is not proof of ownership", name, e)
	}
	a.ID = types.StringValue(name)
	rec.Operation = res.Msg.Name
	if err := saveRecovery(ctx, p, rec); err != nil {
		return true, err
	}
	result := new(api.RegistryNamespace)
	e = waitOperation(ctx, r.client, res.Msg, result)
	if e != nil {
		if last, readErr := getNamespace(ctx, r.client, name, false); readErr == nil {
			_ = a.Set(ctx, last)
		}
		return true, e
	}
	if result.Name != name {
		return true, fmt.Errorf("create operation returned unexpected namespace %s", result.Name)
	}
	return true, nil
}

// namespaceUpdate fences mutable quota changes with the planned etag.
func (r *NamespaceResource) namespaceUpdate(ctx context.Context, a, old *NamespaceResourceModel, n *api.RegistryNamespace, name, key string, p privateData) error {
	if a.StorageQuotaBytes.Equal(old.StorageQuotaBytes) {
		return nil
	}
	current, e := getNamespace(ctx, r.client, name, false)
	if e != nil {
		return e
	}
	if current.Etag != old.Etag.ValueString() {
		return fmt.Errorf("namespace %s etag changed (current %s); refresh and replan", name, current.Etag)
	}
	n.Name = name
	n.Zone = ""
	n.Etag = old.Etag.ValueString()
	q := &api.UpdateRegistryNamespaceRequest{RegistryNamespace: n, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"storage_quota_bytes"}}, IdempotencyKey: key}
	rec := &recovery{Action: actionUpdate}
	if err := saveRecovery(ctx, p, rec); err != nil {
		return err
	}
	res, e := r.client.UpdateRegistryNamespace(ctx, connect.NewRequest(q))
	if e != nil {
		return e
	}
	rec.Operation = res.Msg.Name
	if err := saveRecovery(ctx, p, rec); err != nil {
		return err
	}
	result := new(api.RegistryNamespace)
	if e = waitOperation(ctx, r.client, res.Msg, result); e != nil {
		return e
	}
	if result.Name != name {
		return fmt.Errorf("update returned unexpected namespace %s", result.Name)
	}
	return nil
}

// NamespaceResourceModel is the typed Terraform model, preserving null and unknown values.
type NamespaceResourceModel struct {
	ID                         types.String   `tfsdk:"id"`
	Name                       types.String   `tfsdk:"name"`
	DNSName                    types.String   `tfsdk:"dns_name"`
	OrgID                      types.String   `tfsdk:"org_id"`
	Status                     types.String   `tfsdk:"status"`
	Etag                       types.String   `tfsdk:"etag"`
	CreatedAt                  types.String   `tfsdk:"created_at"`
	UpdatedAt                  types.String   `tfsdk:"updated_at"`
	Zone                       types.String   `tfsdk:"zone"`
	StorageQuotaBytes          types.Int64    `tfsdk:"storage_quota_bytes"`
	CreatedBy                  types.Object   `tfsdk:"created_by"`
	UpdatedBy                  types.Object   `tfsdk:"updated_by"`
	AccessMode                 types.Object   `tfsdk:"access_mode"`
	ContentStatus              types.Object   `tfsdk:"content_status"`
	ForceDestroy               types.Bool     `tfsdk:"force_destroy"`
	InitialAccessConfiguration types.Object   `tfsdk:"initial_access_configuration"`
	Timeouts                   timeouts.Value `tfsdk:"timeouts"`
}

// Schema defines the namespace ownership contract.
func (r *NamespaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Manages a Container Registry namespace and its storage quota. Initial access configuration is historical create-only input. The resource never automatically adopts an existing namespace.\n\nDestroy refuses to delete stored content unless `force_destroy` was applied beforehand. Action deadlines default to 20 minutes and are independent of HTTP attempt timeouts.\n\nAn interrupted create can leave a tainted resource. Inspect the remote namespace and explicitly import it when private recovery state is unavailable.", Attributes: namespaceAttributes()}
}

// Configure reuses the provider authenticated Registry API client.
func (r *NamespaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *NamespaceResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var a NamespaceResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &a)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if known(a.Zone) && !a.Zone.IsNull() {
		if err := validateProtoField(&api.RegistryNamespace{Zone: a.Zone.ValueString()}, "zone"); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("zone"), "Invalid namespace zone", err.Error())
		}
	}
	if b := a.InitialAccessConfiguration; !b.IsNull() && !b.IsUnknown() {
		var bootstrap AccessConfigurationModel
		diags := b.As(ctx, &bootstrap, basetypes.ObjectAsOptions{})
		resp.Diagnostics.Append(diags...)
		if diags.HasError() {
			return
		}
		policies, acl := bootstrap.PolicySets, bootstrap.RequestIPACL
		if nullEntry(policies) {
			return
		}
		if known(b) {
			_, diags := accessInput(ctx, policies, acl, path.Root("initial_access_configuration"))
			resp.Diagnostics.Append(diags...)
		} else {
			validateAccessBounds(ctx, policies, acl, &resp.Diagnostics)
		}
	}
}

// Create records ownership only after accepted work or a verified singleton handoff.
func (r *NamespaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var a NamespaceResourceModel
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
func (r *NamespaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var a NamespaceResourceModel
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
		pending, recoveryErr := protectPendingCreate(c, r.client, resp.Private)
		if recoveryErr != nil {
			report(ctx, recoveryErr, &resp.Diagnostics)
			return
		}
		if pending {
			report(ctx, fmt.Errorf("pending namespace create is not visible yet; retaining state for a later refresh"), &resp.Diagnostics)
			return
		}
		resp.State.RemoveResource(ctx)
		return
	}
	if err == nil {
		err = observeRecovery(c, r.client, 0, true, resp.Private)
		resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
	}
	report(ctx, err, &resp.Diagnostics)
}

// Update preserves the planned etag and requires a new plan after recovering earlier work.
func (r *NamespaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var a, old NamespaceResourceModel
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
	recovered, err := r.resume(c, resp.Private)
	if err == nil && recovered {
		err = r.read(c, &old)
		if err == nil {
			err = fmt.Errorf("recovered an earlier mutation; refresh and replan before changing content")
		}
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
func (r *NamespaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var a NamespaceResourceModel
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
	_, parentErr := getNamespace(c, r.client, a.ID.ValueString(), false)
	if coreweave.IsNotFoundError(parentErr) {
		pending, recoveryErr := protectPendingCreate(c, r.client, resp.Private)
		if recoveryErr != nil {
			report(ctx, recoveryErr, &resp.Diagnostics)
			return
		}
		if pending {
			report(ctx, fmt.Errorf("pending namespace create is not visible yet; retaining state until its outcome is known"), &resp.Diagnostics)
			return
		}
		resp.State.RemoveResource(ctx)
		return
	}
	if parentErr != nil {
		report(ctx, parentErr, &resp.Diagnostics)
		return
	}
	_, err := r.resume(c, resp.Private)
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
func (r *NamespaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !validNamespace(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a namespace name, such as example-images")
		return
	}
	a := NamespaceResourceModel{}
	a.CreatedBy = types.ObjectNull(namespaceTypes()["created_by"].(types.ObjectType).AttrTypes)
	a.UpdatedBy = types.ObjectNull(namespaceTypes()["updated_by"].(types.ObjectType).AttrTypes)
	a.AccessMode = types.ObjectNull(namespaceTypes()["access_mode"].(types.ObjectType).AttrTypes)
	a.ContentStatus = types.ObjectNull(namespaceTypes()["content_status"].(types.ObjectType).AttrTypes)
	a.InitialAccessConfiguration = types.ObjectNull(namespaceTypes()["initial_access_configuration"].(types.ObjectType).AttrTypes)
	a.Timeouts = nullResourceTimeouts()
	a.ID = types.StringValue(namespaceResourceName(req.ID))
	a.Name = types.StringValue(req.ID)
	a.ForceDestroy = types.BoolValue(false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
}

// read refreshes observations owned by this resource.
func (r *NamespaceResource) read(ctx context.Context, a *NamespaceResourceModel) error {
	n, err := getNamespace(ctx, r.client, a.ID.ValueString(), false)
	if err != nil {
		return err
	}
	return conversionError(a.Set(ctx, n))
}

// apply submits one concrete mutation and clears only terminal recovery evidence.
func (r *NamespaceResource) apply(ctx context.Context, a, old *NamespaceResourceModel, destroy bool, p privateData) (bool, error) {
	owned, err := r.namespaceApply(ctx, a, old, destroy, p)
	return owned, finishRecovery(ctx, p, err)
}

// cleanUnknown preserves valid partial state after a failed mutation.
func (a *NamespaceResourceModel) cleanUnknown() {
	if a.ID.IsUnknown() {
		a.ID = types.StringNull()
	}
	if a.Name.IsUnknown() {
		a.Name = types.StringNull()
	}
	if a.DNSName.IsUnknown() {
		a.DNSName = types.StringNull()
	}
	if a.OrgID.IsUnknown() {
		a.OrgID = types.StringNull()
	}
	if a.Status.IsUnknown() {
		a.Status = types.StringNull()
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
	if a.Zone.IsUnknown() {
		a.Zone = types.StringNull()
	}
	if a.StorageQuotaBytes.IsUnknown() {
		a.StorageQuotaBytes = types.Int64Null()
	}
	if a.CreatedBy.IsUnknown() {
		a.CreatedBy = types.ObjectNull(namespaceTypes()["created_by"].(types.ObjectType).AttrTypes)
	}
	if a.UpdatedBy.IsUnknown() {
		a.UpdatedBy = types.ObjectNull(namespaceTypes()["updated_by"].(types.ObjectType).AttrTypes)
	}
	if a.AccessMode.IsUnknown() {
		a.AccessMode = types.ObjectNull(namespaceTypes()["access_mode"].(types.ObjectType).AttrTypes)
	}
	if a.ContentStatus.IsUnknown() {
		a.ContentStatus = types.ObjectNull(namespaceTypes()["content_status"].(types.ObjectType).AttrTypes)
	}
	if a.ForceDestroy.IsUnknown() {
		a.ForceDestroy = types.BoolNull()
	}
	if a.InitialAccessConfiguration.IsUnknown() {
		a.InitialAccessConfiguration = types.ObjectNull(namespaceTypes()["initial_access_configuration"].(types.ObjectType).AttrTypes)
	}
}

// Set copies server observations while preserving local resource settings.
func (a *NamespaceResourceModel) Set(ctx context.Context, n *api.RegistryNamespace) diag.Diagnostics {
	a.ID = types.StringValue(n.Name)
	a.Name = types.StringValue(strings.TrimPrefix(n.Name, "namespaces/"))
	a.Zone = types.StringValue(n.Zone)
	a.OrgID = types.StringValue(n.OwnerOrg)
	a.DNSName = types.StringValue(n.DnsName)
	a.Status = types.StringValue(n.State.String())
	a.Etag = types.StringValue(n.Etag)
	a.CreatedAt = timestamp(n.CreateTime)
	a.UpdatedAt = timestamp(n.UpdateTime)
	a.StorageQuotaBytes = types.Int64Null()
	if n.StorageQuotaBytes != nil {
		if *n.StorageQuotaBytes > uint64(1<<63-1) {
			return diag.Diagnostics{diag.NewAttributeErrorDiagnostic(path.Root("storage_quota_bytes"), "Quota cannot be represented", "Namespace quota exceeds Terraform int64 range.")}
		}
		a.StorageQuotaBytes = types.Int64Value(int64(*n.StorageQuotaBytes))
	}
	var diags diag.Diagnostics
	a.CreatedBy, a.UpdatedBy, a.AccessMode, a.ContentStatus, diags = namespaceObjects(ctx, n)
	return diags
}
