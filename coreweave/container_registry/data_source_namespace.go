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
	"github.com/hashicorp/terraform-plugin-framework-timeouts/datasource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// NamespaceDataSource reads Container Registry namespace metadata.
type NamespaceDataSource struct{ client client.RegistryServiceClient }

var (
	_ datasource.DataSource = &NamespaceDataSource{}
)

// NewNamespaceDataSource constructs a namespace data source.
func NewNamespaceDataSource() datasource.DataSource {
	return &NamespaceDataSource{}
}

// Metadata sets the data source's static Terraform type name.
func (d *NamespaceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_registry_namespace"
}

// NamespaceDataSourceModel is the typed Terraform model, preserving null and unknown values.
type NamespaceDataSourceModel struct {
	ID                types.String   `tfsdk:"id"`
	Name              types.String   `tfsdk:"name"`
	DNSName           types.String   `tfsdk:"dns_name"`
	OrgID             types.String   `tfsdk:"org_id"`
	Status            types.String   `tfsdk:"status"`
	Etag              types.String   `tfsdk:"etag"`
	CreatedAt         types.String   `tfsdk:"created_at"`
	UpdatedAt         types.String   `tfsdk:"updated_at"`
	NamespaceID       types.String   `tfsdk:"namespace_id"`
	Zone              types.String   `tfsdk:"zone"`
	StorageQuotaBytes types.Int64    `tfsdk:"storage_quota_bytes"`
	CreatedBy         types.Object   `tfsdk:"created_by"`
	UpdatedBy         types.Object   `tfsdk:"updated_by"`
	AccessMode        types.Object   `tfsdk:"access_mode"`
	ContentStatus     types.Object   `tfsdk:"content_status"`
	Timeouts          timeouts.Value `tfsdk:"timeouts"`
}

// Schema defines the namespace discovery contract.
func (d *NamespaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Reads the current metadata for a Container Registry namespace.", Attributes: namespaceDataAttributes()}
}

// Configure shares the provider authenticated Registry API client.
func (d *NamespaceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*coreweave.Client)
	if !ok || c.ContainerRegistry == nil {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *coreweave.Client with a configured Container Registry client, got: %T. Please report this issue to the provider developers.", req.ProviderData))
		return
	}
	d.client = c.ContainerRegistry
}

// namespaceDataAttributes defines the canonical lookup input and observations.
func namespaceDataAttributes() map[string]schema.Attribute {
	a := observedNamespaceAttributes()
	a["name"] = schema.StringAttribute{Required: true, MarkdownDescription: "Canonical namespace name, such as namespaces/example-images.", Validators: []validator.String{parentNameValidator{}}}
	a["timeouts"] = timeouts.Attributes(context.Background())
	return a
}

// Read retrieves complete discovery results within the configured Framework timeout.
func (d *NamespaceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var a NamespaceDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &a)...)
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
	if !known(a.Name) {
		report(ctx, fmt.Errorf("name must be known"), &resp.Diagnostics)
		return
	}
	res, err := d.client.GetRegistryNamespace(c, connect.NewRequest(&api.GetRegistryNamespaceRequest{Name: a.Name.ValueString()}))
	if err == nil {
		resp.Diagnostics.Append(a.Set(ctx, res.Msg)...)
	}
	if err != nil || resp.Diagnostics.HasError() {
		report(ctx, err, &resp.Diagnostics)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
}

// Set populates namespace discovery observations without touching local timeouts.
func (a *NamespaceDataSourceModel) Set(ctx context.Context, n *api.RegistryNamespace) diag.Diagnostics {
	a.ID = types.StringValue(n.Name)
	a.Name = types.StringValue(n.Name)
	a.NamespaceID = types.StringValue(strings.TrimPrefix(n.Name, "namespaces/"))
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

// NamespaceObservationModel describes each namespace list entry.
type NamespaceObservationModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	DNSName           types.String `tfsdk:"dns_name"`
	OrgID             types.String `tfsdk:"org_id"`
	Status            types.String `tfsdk:"status"`
	Etag              types.String `tfsdk:"etag"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
	NamespaceID       types.String `tfsdk:"namespace_id"`
	Zone              types.String `tfsdk:"zone"`
	StorageQuotaBytes types.Int64  `tfsdk:"storage_quota_bytes"`
	CreatedBy         types.Object `tfsdk:"created_by"`
	UpdatedBy         types.Object `tfsdk:"updated_by"`
	AccessMode        types.Object `tfsdk:"access_mode"`
	ContentStatus     types.Object `tfsdk:"content_status"`
}
