package serviceaccount

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &AccessPolicyRolesDataSource{}

type accessPolicyRole struct {
	actions []string
}

// Role names are public Terraform lookup keys. If a role is renamed, retain its old key as
// a compatibility alias so existing configurations continue to resolve the same actions.
// System-managed actions are deliberately excluded.
var accessPolicyRoles = map[string]accessPolicyRole{
	"Access Request Approver":           {actions: []string{"decide_teleport_request"}},
	"Access Token Admin":                {actions: []string{"access_token_admin"}},
	"Access Token User":                 {actions: []string{"write_access_tokens"}},
	"Access Token Viewer":               {actions: []string{"read_access_tokens"}},
	"Alerts Admin":                      {actions: []string{"write_notifications"}},
	"Alerts Viewer":                     {actions: []string{"read_notifications"}},
	"Arca Apps Admin":                   {actions: []string{"admin_arca"}},
	"Arca Apps Editor":                  {actions: []string{"write_arca"}},
	"Arca Apps Viewer":                  {actions: []string{"read_arca"}},
	"Baremetal Admin":                   {actions: []string{"write_baremetal"}},
	"Baremetal Node Console Admin":      {actions: []string{"bmaas_node_console_write"}},
	"Baremetal Node Console Viewer":     {actions: []string{"bmaas_node_console_read"}},
	"Baremetal Viewer":                  {actions: []string{"read_baremetal"}},
	"Billing Viewer":                    {actions: []string{"read_billing"}},
	"CKS Admin":                         {actions: []string{"write_katalog"}},
	"CKS Viewer":                        {actions: []string{"read_katalog"}},
	"Grafana Dashboard Editor":          {actions: []string{"write_o11y_dashboards"}},
	"Grafana Dashboard Viewer":          {actions: []string{"read_o11y_dashboards"}},
	"IAM Admin":                         {actions: []string{"write_auth"}},
	"IAM Viewer":                        {actions: []string{"read_auth"}},
	"Inference Admin":                   {actions: []string{"write_inference"}},
	"Inference Viewer":                  {actions: []string{"read_inference"}},
	"Integrations Admin":                {actions: []string{"write_integrations"}},
	"Integrations Viewer":               {actions: []string{"read_integrations"}},
	"Object Storage Admin":              {actions: []string{"object_storage_admin"}},
	"Object Storage Catalog Viewer":     {actions: []string{"read_caios_catalog"}},
	"Observability Viewer":              {actions: []string{"read_cluster_metrics"}},
	"Registry Namespace Admin":          {actions: []string{"admin_registry_namespaces"}},
	"Registry Namespace Content Admin":  {actions: []string{"admin_registry_content"}},
	"Registry Namespace Content Viewer": {actions: []string{"read_registry_content"}},
	"Registry Namespace Content Writer": {actions: []string{"write_registry_content"}},
	"Registry Namespace Viewer":         {actions: []string{"read_registry_namespaces"}},
	"Registry Namespace Writer":         {actions: []string{"write_registry_namespaces"}},
	"Sandbox Admin":                     {actions: []string{"sandbox_admin"}},
	"Sandbox User":                      {actions: []string{"sandbox_user"}},
	"Support Viewer":                    {actions: []string{"read_freshdesk_support"}},
	"Telemetry Relay Admin":             {actions: []string{"write_telemetry_relay"}},
	"Telemetry Relay Reader":            {actions: []string{"read_telemetry_relay"}},
}

// AccessPolicyRolesDataSource exposes the static, provider-versioned IAM role catalog.
type AccessPolicyRolesDataSource struct{}

// NewAccessPolicyRolesDataSource constructs an IAM access policy roles data source.
func NewAccessPolicyRolesDataSource() datasource.DataSource {
	return &AccessPolicyRolesDataSource{}
}

type AccessPolicyRolesDataSourceModel struct {
	Roles types.Map `tfsdk:"roles"`
}

func (d *AccessPolicyRolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_iam_access_policy_roles"
}

func (d *AccessPolicyRolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "> **Private beta:** This data source is available only to organizations enrolled in the IAM access policy private beta and may change before general availability.\n\n" +
			"Returns the provider-versioned catalog of customer-assignable IAM access policy roles, keyed by their customer-facing names. Each role resolves directly to the CoreWeave console actions accepted by `coreweave_iam_access_policy`. Keys are case- and space-sensitive. Existing keys remain available as compatibility aliases if a role is renamed. System-managed actions are excluded.",
		Attributes: map[string]schema.Attribute{
			"roles": schema.MapAttribute{
				Computed:            true,
				ElementType:         types.SetType{ElemType: types.StringType},
				MarkdownDescription: "Console action sets keyed by customer-facing role names. Keys are case- and space-sensitive. Pass a selected value directly to a `coreweave_iam_access_policy` rule's `actions` attribute.",
			},
		},
	}
}

func (d *AccessPolicyRolesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	roleValues := make(map[string]attr.Value, len(accessPolicyRoles))
	for key, role := range accessPolicyRoles {
		actions := make([]attr.Value, 0, len(role.actions))
		for _, action := range role.actions {
			actions = append(actions, types.StringValue(action))
		}
		roleValues[key] = types.SetValueMust(types.StringType, actions)
	}

	data := AccessPolicyRolesDataSourceModel{
		Roles: types.MapValueMust(types.SetType{ElemType: types.StringType}, roleValues),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
