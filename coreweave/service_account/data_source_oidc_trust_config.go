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
	_ datasource.DataSource              = &OIDCTrustConfigDataSource{}
	_ datasource.DataSourceWithConfigure = &OIDCTrustConfigDataSource{}
)

// OIDCTrustConfigDataSource looks up an existing service-account OIDC trust configuration.
type OIDCTrustConfigDataSource struct {
	client *coreweave.Client
}

// OIDCTrustConfigDataSourceModel aliases the resource model so additions to
// the remote representation cannot silently be omitted from data source state.
type OIDCTrustConfigDataSourceModel = OIDCTrustConfigResourceModel

func NewOIDCTrustConfigDataSource() datasource.DataSource {
	return &OIDCTrustConfigDataSource{}
}

func (d *OIDCTrustConfigDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_iam_service_account_oidc_trust_config"
}

func (d *OIDCTrustConfigDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	idValidator := []validator.String{stringvalidator.RegexMatches(resourceIDPattern, "must start with a lowercase letter, contain only lowercase letters, digits, or hyphens, end with a letter or digit, and be at most 63 characters")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "> **Private beta:** This data source is available only to organizations enrolled in the private beta and may change before general availability. It is gated by an organization-level feature flag. Contact your CoreWeave representative or CoreWeave Support to request access for your organization.\n\nLooks up an existing OIDC trust configuration belonging to a CoreWeave service account.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				Validators:          idValidator,
				MarkdownDescription: "The caller-chosen trust configuration ID.",
			},
			"organization_id": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(organizationIDPattern, "must contain exactly six lowercase letters or digits"),
				},
				MarkdownDescription: "The six-character ID of the owning CoreWeave organization.",
			},
			"service_account_id": schema.StringAttribute{
				Required:            true,
				Validators:          idValidator,
				MarkdownDescription: "The ID of the service account that owns this trust configuration.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The canonical resource name.",
			},
			"uid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The server-generated unique identifier.",
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The optional description of the trust rule.",
			},
			"cel": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The CEL expression evaluated against verified OIDC token claims exposed through the `token` namespace.",
			},
			"workload_federation_oidc_config_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The UID of the workload federation OIDC configuration used to verify tokens.",
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

func (d *OIDCTrustConfigDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *OIDCTrustConfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data OIDCTrustConfigDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := serviceAccountName(data.OrganizationID.ValueString(), data.ServiceAccountID.ValueString()) + "/oidcTrustConfigs/" + data.ID.ValueString()
	config, err := d.client.Directory.GetServiceAccountOidcTrustConfig(ctx, &directoryv1alpha.GetServiceAccountOidcTrustConfigRequest{Name: &name})
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			resp.Diagnostics.AddError("OIDC Trust Configuration Not Found", fmt.Sprintf("No service-account OIDC trust configuration exists with the name %q.", name))
			return
		}
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}
	if err := data.setFromProto(config); err != nil {
		resp.Diagnostics.AddError("Invalid OIDC Trust Configuration Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
