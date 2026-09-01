package workloadfederation

import (
	"context"
	"fmt"

	controlplanev1beta1 "buf.build/gen/go/coreweave/workload-federation/protocolbuffers/go/coreweave/workload_federation/control_plane/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var (
	_ datasource.DataSource                     = &OIDCConfigDataSource{}
	_ datasource.DataSourceWithConfigure        = &OIDCConfigDataSource{}
	_ datasource.DataSourceWithConfigValidators = &OIDCConfigDataSource{}
)

// OIDCConfigDataSource looks up an existing workload federation OIDC config.
type OIDCConfigDataSource struct {
	client *coreweave.Client
}

// OIDCConfigDataSourceModel is shared with the resource so additions to the
// remote representation cannot silently be omitted from data source state.
type OIDCConfigDataSourceModel = OIDCConfigResourceModel

func NewOIDCConfigDataSource() datasource.DataSource {
	return &OIDCConfigDataSource{}
}

func (d *OIDCConfigDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workload_federation_oidc_config"
}

func (d *OIDCConfigDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an existing workload federation OIDC configuration by its stable UID or exact name. Name lookup fails when no configuration or more than one configuration has that name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The server-assigned stable UID of the OIDC configuration.",
			},
			"uid": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The server-assigned stable UID to look up. Exactly one of `uid` or `name` must be configured.",
				Validators: []validator.String{
					stringvalidator.UTF8LengthAtLeast(1),
				},
			},
			"organization_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier of the CoreWeave organization that owns the OIDC configuration.",
			},
			"name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The exact human-readable name to look up. Exactly one of `uid` or `name` must be configured.",
				Validators: []validator.String{
					stringvalidator.UTF8LengthAtLeast(1),
				},
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The optional human-readable description of the OIDC configuration.",
			},
			"issuer_url": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The issuer URL of the external OIDC provider.",
			},
			"audience": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The expected audience claim in tokens from the external OIDC provider.",
			},
			"active": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether tokens from this OIDC provider are accepted for workload identity federation.",
			},
			"deactivated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 timestamp at which the OIDC configuration was deactivated, or null while active.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 timestamp at which the OIDC configuration was created.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The RFC 3339 timestamp at which the OIDC configuration was last updated.",
			},
		},
	}
}

func (d *OIDCConfigDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("uid"),
			path.MatchRoot("name"),
		),
	}
}

func (d *OIDCConfigDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *OIDCConfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data OIDCConfigDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var config *controlplanev1beta1.OIDCConfig
	if !data.UID.IsNull() && !data.UID.IsUnknown() {
		readResp, err := d.client.GetOIDCConfig(ctx, connect.NewRequest(&controlplanev1beta1.GetOIDCConfigRequest{Uid: data.UID.ValueString()}))
		if err != nil {
			if coreweave.IsNotFoundError(err) {
				resp.Diagnostics.AddError(
					"OIDC Configuration Not Found",
					fmt.Sprintf("No workload federation OIDC configuration has the UID %q.", data.UID.ValueString()),
				)
				return
			}
			coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
			return
		}
		config = readResp.Msg.GetConfig()
	} else {
		listResp, err := d.client.ListOIDCConfigs(ctx, connect.NewRequest(&controlplanev1beta1.ListOIDCConfigsRequest{}))
		if err != nil {
			coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
			return
		}
		config, err = selectOIDCConfigByName(listResp.Msg.GetConfigs(), data.Name.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("OIDC Configuration Lookup Failed", err.Error())
			return
		}
	}

	if config == nil {
		resp.Diagnostics.AddError("Invalid OIDC Configuration Response", "The CoreWeave API returned an empty OIDC configuration. Please report this issue to CoreWeave support.")
		return
	}

	if err := data.SetFromProto(config); err != nil {
		resp.Diagnostics.AddError("Invalid OIDC Configuration Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func selectOIDCConfigByName(configs []*controlplanev1beta1.OIDCConfig, name string) (*controlplanev1beta1.OIDCConfig, error) {
	var match *controlplanev1beta1.OIDCConfig
	for _, config := range configs {
		if config == nil || config.GetName() != name {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("more than one workload federation OIDC configuration has the exact name %q; use uid to select one unambiguously", name)
		}
		match = config
	}
	if match == nil {
		return nil, fmt.Errorf("no workload federation OIDC configuration has the exact name %q", name)
	}
	return match, nil
}
