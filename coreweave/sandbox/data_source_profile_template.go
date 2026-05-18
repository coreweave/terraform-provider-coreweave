package sandbox

import (
	"context"
	"fmt"

	sandboxv1beta2 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1beta2"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ProfileTemplateDataSource{}

func NewProfileTemplateDataSource() datasource.DataSource {
	return &ProfileTemplateDataSource{}
}

type ProfileTemplateDataSource struct {
	client *coreweave.Client
}

type ProfileTemplateDataSourceModel = ProfileTemplateResourceModel

func (d *ProfileTemplateDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sandbox_profile_template"
}

func (d *ProfileTemplateDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read a CoreWeave Sandbox profile template by ID or display name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Server-assigned UUID, or the user-set `display_name`. UUID lookup is preferred and falls back to display name on no-match.",
			},
			"organization_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Organization that owns this template."},
			"display_name":    schema.StringAttribute{Computed: true, MarkdownDescription: "Human-readable template name."},
			"description":     schema.StringAttribute{Computed: true, MarkdownDescription: "Human-readable description."},
			"spec": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Profile specification.",
				Attributes: map[string]schema.Attribute{
					"container_image": schema.StringAttribute{Computed: true},
					"runtime_class":   schema.StringAttribute{Computed: true},
					"resource_defaults": schema.SingleNestedAttribute{
						Computed: true,
						Attributes: map[string]schema.Attribute{
							"cpu_request":    schema.StringAttribute{Computed: true},
							"memory_request": schema.StringAttribute{Computed: true},
							"cpu_limit":      schema.StringAttribute{Computed: true},
							"memory_limit":   schema.StringAttribute{Computed: true},
						},
					},
					"instance_types":        schema.ListAttribute{Computed: true, ElementType: types.StringType},
					"node_selector":         schema.MapAttribute{Computed: true, ElementType: types.StringType},
					"tags":                  schema.ListAttribute{Computed: true, ElementType: types.StringType},
					"namespace_config_json": schema.StringAttribute{Computed: true},
					"network_config_json":   schema.StringAttribute{Computed: true},
					"pod_template_json":     schema.StringAttribute{Computed: true},
				},
			},
			"labels":     schema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "User-defined labels."},
			"created_at": schema.StringAttribute{Computed: true},
			"updated_at": schema.StringAttribute{Computed: true},
		},
	}
}

func (d *ProfileTemplateDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *coreweave.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *ProfileTemplateDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	data := new(ProfileTemplateDataSourceModel)
	resp.Diagnostics.Append(req.Config.Get(ctx, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	getResp, err := d.client.GetProfileTemplate(ctx, connect.NewRequest(&sandboxv1beta2.GetProfileTemplateRequest{
		Id: data.ID.ValueString(),
	}))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(data.Set(ctx, getResp.Msg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
