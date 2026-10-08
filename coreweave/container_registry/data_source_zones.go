package containerregistry

import (
	"context"
	"fmt"
	"sort"
	"time"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/coreweave/registry/v1alpha1/registryv1alpha1connect"
	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/datasource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ZonesDataSource reads Container Registry zones metadata.
type ZonesDataSource struct{ client client.RegistryServiceClient }

var (
	_ datasource.DataSource                   = &ZonesDataSource{}
	_ datasource.DataSourceWithValidateConfig = &ZonesDataSource{}
)

// NewZonesDataSource constructs a zones data source.
func NewZonesDataSource() datasource.DataSource {
	return &ZonesDataSource{}
}

// Metadata sets the data source's static Terraform type name.
func (d *ZonesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_registry_zones"
}

// ZonesDataSourceModel is the typed Terraform model, preserving null and unknown values.
type ZonesDataSourceModel struct {
	ZoneNames types.Set      `tfsdk:"zone_names"`
	Zones     types.List     `tfsdk:"zones"`
	Timeouts  timeouts.Value `tfsdk:"timeouts"`
}

// Schema defines the zones discovery contract.
func (d *ZonesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Lists Container Registry zones, sorted by zone name. Zone availability is advisory and does not reserve capacity.", Attributes: zonesDataAttributes()}
}

// Configure shares the provider authenticated Registry API client.
func (d *ZonesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// zonesDataAttributes defines advisory zone availability and optional filters.
func zonesDataAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"zone_names": schema.SetAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "At most 100 zone filters. Null or empty selects all. Case-equivalent duplicates are rejected."},
		"zones": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Matching zones, sorted by zone name. Availability is advisory.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"zone":      schema.StringAttribute{Computed: true, MarkdownDescription: "Upper-case zone identifier."},
			"available": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether new namespaces can currently be provisioned in this zone."},
		}}},
		"timeouts": timeouts.Attributes(context.Background()),
	}
}

// Read retrieves complete discovery results within the configured Framework timeout.
func (d *ZonesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var a ZonesDataSourceModel
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
	filters, err := zoneFilters(a.ZoneNames)
	if err != nil {
		report(ctx, err, &resp.Diagnostics)
		return
	}
	res, err := d.client.ListZones(c, connect.NewRequest(&api.ListZonesRequest{Zones: filters}))
	if err != nil {
		report(ctx, err, &resp.Diagnostics)
		return
	}
	sort.Slice(res.Msg.Zones, func(i, j int) bool { return res.Msg.Zones[i].Zone < res.Msg.Zones[j].Zone })
	elementType := zonesDataAttributes()["zones"].GetType().(types.ListType).ElemType
	values := make([]ZoneObservationModel, len(res.Msg.Zones))
	for i, zone := range res.Msg.Zones {
		values[i] = ZoneObservationModel{Zone: types.StringValue(zone.Zone), Available: types.BoolValue(zone.Available)}
	}
	value, diags := types.ListValueFrom(ctx, elementType, values)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	a.Zones = value
	resp.Diagnostics.Append(resp.State.Set(ctx, &a)...)
}

// ValidateConfig validates known zone filters before making API requests.
func (d *ZonesDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var a ZonesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &a)...)
	if resp.Diagnostics.HasError() || !known(a.ZoneNames) {
		return
	}
	if _, err := zoneFilters(a.ZoneNames); err != nil {
		resp.Diagnostics.AddError("Invalid zone filters", err.Error())
	}
}

// ZoneObservationModel exposes each discovered zone through a concrete nested model.
type ZoneObservationModel struct {
	Zone      types.String `tfsdk:"zone"`
	Available types.Bool   `tfsdk:"available"`
}
