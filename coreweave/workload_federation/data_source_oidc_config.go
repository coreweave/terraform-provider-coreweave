package workloadfederation

import (
	"context"
	"fmt"

	controlplanev1beta1 "buf.build/gen/go/coreweave/workload-federation/protocolbuffers/go/coreweave/workload_federation/control_plane/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

type oidcConfigSelectorValidator struct{}

func (oidcConfigSelectorValidator) Description(context.Context) string {
	return "exactly one selector must be configured: uid, or issuer_url together with audience"
}

func (v oidcConfigSelectorValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (oidcConfigSelectorValidator) ValidateDataSource(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var uid, issuerURL, audience types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("uid"), &uid)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("issuer_url"), &issuerURL)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("audience"), &audience)...)
	if resp.Diagnostics.HasError() || uid.IsUnknown() || issuerURL.IsUnknown() || audience.IsUnknown() {
		return
	}

	uidConfigured := !uid.IsNull()
	issuerURLConfigured := !issuerURL.IsNull()
	audienceConfigured := !audience.IsNull()
	if (uidConfigured && !issuerURLConfigured && !audienceConfigured) ||
		(!uidConfigured && issuerURLConfigured && audienceConfigured) {
		return
	}

	resp.Diagnostics.AddError(
		"Invalid OIDC Configuration Selector",
		"Configure exactly one selector: `uid`, or both `issuer_url` and `audience`. The organization is determined by the provider's authenticated context.",
	)
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
		MarkdownDescription: "Looks up an existing workload federation OIDC configuration by its stable UID or unique issuer URL and audience pair. The organization is determined by the provider's authenticated context.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The server-assigned stable UID of the OIDC configuration.",
			},
			"uid": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The server-assigned stable UID to look up. Configure either `uid`, or both `issuer_url` and `audience`.",
				Validators: []validator.String{
					stringvalidator.UTF8LengthAtLeast(1),
				},
			},
			"organization_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The unique identifier of the CoreWeave organization that owns the OIDC configuration.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The human-readable name of the OIDC configuration.",
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The optional human-readable description of the OIDC configuration.",
			},
			"issuer_url": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The issuer URL to look up. Must be configured together with `audience` when `uid` is omitted.",
				Validators: []validator.String{
					stringvalidator.UTF8LengthAtMost(1024),
					oidcIssuerURLValidator{},
				},
			},
			"audience": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The audience to look up. Must be configured together with `issuer_url` when `uid` is omitted.",
				Validators: []validator.String{
					stringvalidator.UTF8LengthBetween(1, 1024),
				},
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
		oidcConfigSelectorValidator{},
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
		config, err = selectOIDCConfigByIssuerAndAudience(listResp.Msg.GetConfigs(), data.IssuerURL.ValueString(), data.Audience.ValueString())
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

func selectOIDCConfigByIssuerAndAudience(configs []*controlplanev1beta1.OIDCConfig, issuerURL, audience string) (*controlplanev1beta1.OIDCConfig, error) {
	var match *controlplanev1beta1.OIDCConfig
	for _, config := range configs {
		if config == nil || config.GetIssuerUrl() != issuerURL || config.GetAudience() != audience {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("more than one workload federation OIDC configuration in the authenticated organization has issuer URL %q and audience %q; use uid to select one unambiguously", issuerURL, audience)
		}
		match = config
	}
	if match == nil {
		return nil, fmt.Errorf("no workload federation OIDC configuration in the authenticated organization has issuer URL %q and audience %q", issuerURL, audience)
	}
	return match, nil
}
