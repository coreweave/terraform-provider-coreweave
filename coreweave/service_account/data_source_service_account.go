package serviceaccount

import (
	"context"
	"fmt"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var (
	_ datasource.DataSource              = &ServiceAccountDataSource{}
	_ datasource.DataSourceWithConfigure = &ServiceAccountDataSource{}
)

// ServiceAccountDataSource looks up an existing CoreWeave service account.
type ServiceAccountDataSource struct {
	client *coreweave.Client
}

// ServiceAccountDataSourceModel aliases the resource model so additions to the
// remote representation cannot silently be omitted from data source state.
type ServiceAccountDataSourceModel = ServiceAccountResourceModel

func NewServiceAccountDataSource() datasource.DataSource {
	return &ServiceAccountDataSource{}
}

func (d *ServiceAccountDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_iam_service_account"
}

func (d *ServiceAccountDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "> **Private beta:** This data source is available only to organizations enrolled in the private beta and may change before general availability. It is gated by an organization-level feature flag. Contact your CoreWeave representative or CoreWeave Support to request access for your organization.\n\nLooks up an existing CoreWeave service account by its organization and caller-chosen ID.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The caller-chosen service account ID used as the final segment of the resource name.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(resourceIDPattern, "must start with a lowercase letter, contain only lowercase letters, digits, or hyphens, end with a letter or digit, and be at most 63 characters"),
				},
			},
			"organization_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The six-character ID of the CoreWeave organization that owns the service account.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(organizationIDPattern, "must contain exactly six lowercase letters or digits"),
				},
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The canonical resource name.",
			},
			"uid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The server-generated unique identifier.",
			},
			"creator": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The canonical resource name of the user that created the service account.",
			},
			"display_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The optional human-readable display name.",
			},
			"system_managed": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether CoreWeave or an organization administrator manages this service account.",
			},
			"active": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the service account may authenticate.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 creation timestamp.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 last-update timestamp.",
			},
		},
	}
}

func (d *ServiceAccountDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *coreweave.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *ServiceAccountDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ServiceAccountDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := serviceAccountName(data.OrganizationID.ValueString(), data.ID.ValueString())
	account, err := d.client.Directory.GetServiceAccount(ctx, &directoryv1alpha.GetServiceAccountRequest{Name: &name})
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.Diagnostics.AddError("Service Account Not Found", fmt.Sprintf("No service account exists with the name %q.", name))
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := data.setFromProto(account); err != nil {
		resp.Diagnostics.AddError("Invalid Service Account Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
