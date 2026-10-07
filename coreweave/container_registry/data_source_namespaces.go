package containerregistry

import (
	"context"
	"fmt"
	"time"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/coreweave/registry/v1alpha1/registryv1alpha1connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/datasource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// NamespacesDataSource reads Container Registry namespaces metadata.
type NamespacesDataSource struct{ client client.RegistryServiceClient }

var (
	_ datasource.DataSource = &NamespacesDataSource{}
)

// NewNamespacesDataSource constructs a namespaces data source.
func NewNamespacesDataSource() datasource.DataSource {
	return &NamespacesDataSource{}
}

// Metadata sets the data source's static Terraform type name.
func (d *NamespacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_registry_namespaces"
}

// NamespacesDataSourceModel is the typed Terraform model, preserving null and unknown values.
type NamespacesDataSourceModel struct {
	Namespaces types.List     `tfsdk:"namespaces"`
	Timeouts   timeouts.Value `tfsdk:"timeouts"`
}

// Schema defines the namespaces discovery contract.
func (d *NamespacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Lists namespaces visible to the authenticated organization, sorted by name.", Attributes: namespacesDataAttributes()}
}

// Configure shares the provider authenticated Registry API client.
func (d *NamespacesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// namespacesDataAttributes defines the complete namespace discovery result.
func namespacesDataAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"namespaces": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Namespaces visible to the authenticated organization, sorted by name.", NestedObject: schema.NestedAttributeObject{Attributes: observedNamespaceAttributes()}},
		"timeouts":   timeouts.Attributes(context.Background()),
	}
}

// Read retrieves complete discovery results within the configured Framework timeout.
func (d *NamespacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var a NamespacesDataSourceModel
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
	items, err := listNamespaces(c, d.client)
	if err != nil {
		report(ctx, err, &resp.Diagnostics)
		return
	}
	elementType := namespacesDataAttributes()["namespaces"].GetType().(types.ListType).ElemType
	values := make([]attr.Value, 0, len(items))
	for _, item := range items {
		var model NamespaceDataSourceModel
		resp.Diagnostics.Append(model.Set(ctx, item)...)
		if resp.Diagnostics.HasError() {
			return
		}
		// Nested list entries expose observations only, not the data source's read timeout.
		value, diags := types.ObjectValueFrom(ctx, elementType.(types.ObjectType).AttrTypes, &NamespaceObservationModel{
			ID:                model.ID,
			Name:              model.Name,
			DNSName:           model.DNSName,
			OwnerOrg:          model.OwnerOrg,
			State:             model.State,
			Etag:              model.Etag,
			CreateTime:        model.CreateTime,
			UpdateTime:        model.UpdateTime,
			NamespaceID:       model.NamespaceID,
			Zone:              model.Zone,
			StorageQuotaBytes: model.StorageQuotaBytes,
			CreatedBy:         model.CreatedBy,
			UpdatedBy:         model.UpdatedBy,
			AccessMode:        model.AccessMode,
			ContentStatus:     model.ContentStatus,
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		values = append(values, value)
	}
	a.Namespaces = types.ListValueMust(elementType, values)
	resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
}
