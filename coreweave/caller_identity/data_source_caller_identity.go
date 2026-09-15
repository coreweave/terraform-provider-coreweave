package calleridentity

import (
	"context"
	"fmt"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &CallerIdentityDataSource{}

const callerIdentityDescription = `Retrieve the organization and principal identifiers associated with the configured CoreWeave credentials.

Comparable identity data sources in other HashiCorp cloud providers include [aws_caller_identity](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/caller_identity), [azurerm_client_config](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/data-sources/client_config), and [google_client_openid_userinfo](https://registry.terraform.io/providers/hashicorp/google/latest/docs/data-sources/client_openid_userinfo). Each data source reflects its provider's identity model and exports different attributes.`

func NewCallerIdentityDataSource() datasource.DataSource {
	return &CallerIdentityDataSource{}
}

type CallerIdentityDataSource struct {
	client *coreweave.Client
}

type CallerIdentityDataSourceModel struct {
	OrganizationID types.String `tfsdk:"organization_id"`
	PrincipalID    types.String `tfsdk:"principal_id"`
}

func (d *CallerIdentityDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_caller_identity"
}

func (d *CallerIdentityDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: callerIdentityDescription,
		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "The unique identifier of the organization associated with the configured CoreWeave credentials.",
				Computed:            true,
			},
			"principal_id": schema.StringAttribute{
				MarkdownDescription: "The unique identifier of the principal associated with the configured CoreWeave credentials.",
				Computed:            true,
			},
		},
	}
}

func (d *CallerIdentityDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CallerIdentityDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	identity, err := d.client.GetCallerIdentity(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Read Caller Identity", err.Error())
		return
	}

	data := CallerIdentityDataSourceModel{
		OrganizationID: types.StringValue(identity.OrganizationID),
		PrincipalID:    types.StringValue(identity.PrincipalID),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
