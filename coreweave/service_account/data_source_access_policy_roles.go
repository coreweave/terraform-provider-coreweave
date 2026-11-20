package serviceaccount

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &AccessPolicyRolesDataSource{}

var accessPolicyRoleAttributeTypes = map[string]attr.Type{
	"display_name": types.StringType,
	"actions":      types.SetType{ElemType: types.StringType},
}

type accessPolicyRole struct {
	displayName string
	actions     []string
}

// accessPolicyRoles mirrors ROLE_LABELS and excludes SYSTEM_MANAGED_ACTION_LIST from:
// https://github.com/coreweave/cloud-console/blob/ed763d17cac69985c93e45e0e44d272c0f7c57a1/frontend/src/features/accessPolicies/constants.ts
//
// The action identifiers and implication rules are defined at the same pinned revision:
// https://github.com/coreweave/cloud-console/blob/ed763d17cac69985c93e45e0e44d272c0f7c57a1/cloud_console/data/models/valid_console_action.py
//
// Keep the keys stable: Terraform configurations use them as compatibility identifiers.
var accessPolicyRoles = map[string]accessPolicyRole{
	"access_request_approver":       {displayName: "Access Request Approver", actions: []string{"decide_teleport_request"}},
	"access_token_admin":            {displayName: "Access Token Admin", actions: []string{"access_token_admin"}},
	"access_token_user":             {displayName: "Access Token User", actions: []string{"write_access_tokens"}},
	"access_token_viewer":           {displayName: "Access Token Viewer", actions: []string{"read_access_tokens"}},
	"arca_apps_admin":               {displayName: "Arca Apps Admin", actions: []string{"admin_arca"}},
	"arca_apps_editor":              {displayName: "Arca Apps Editor", actions: []string{"write_arca"}},
	"arca_apps_viewer":              {displayName: "Arca Apps Viewer", actions: []string{"read_arca"}},
	"baremetal_admin":               {displayName: "Baremetal Admin", actions: []string{"write_baremetal"}},
	"baremetal_node_console_admin":  {displayName: "Baremetal Node Console Admin", actions: []string{"bmaas_node_console_write"}},
	"baremetal_node_console_viewer": {displayName: "Baremetal Node Console Viewer", actions: []string{"bmaas_node_console_read"}},
	"baremetal_viewer":              {displayName: "Baremetal Viewer", actions: []string{"read_baremetal"}},
	"billing_viewer":                {displayName: "Billing Viewer", actions: []string{"read_billing"}},
	"cks_admin":                     {displayName: "CKS Admin", actions: []string{"write_katalog"}},
	"cks_viewer":                    {displayName: "CKS Viewer", actions: []string{"read_katalog"}},
	"iam_admin":                     {displayName: "IAM Admin", actions: []string{"write_auth"}},
	"iam_viewer":                    {displayName: "IAM Viewer", actions: []string{"read_auth"}},
	"inference_admin":               {displayName: "Inference Admin", actions: []string{"write_inference"}},
	"inference_viewer":              {displayName: "Inference Viewer", actions: []string{"read_inference"}},
	"integrations_admin":            {displayName: "Integrations Admin", actions: []string{"write_integrations"}},
	"integrations_viewer":           {displayName: "Integrations Viewer", actions: []string{"read_integrations"}},
	"notifications_admin":           {displayName: "Alerts Admin", actions: []string{"write_notifications"}},
	"notifications_viewer":          {displayName: "Alerts Viewer", actions: []string{"read_notifications"}},
	"object_storage_admin":          {displayName: "Object Storage Admin", actions: []string{"object_storage_admin"}},
	"object_storage_catalog_viewer": {displayName: "Object Storage Catalog Viewer", actions: []string{"read_caios_catalog"}},
	"observability_viewer":          {displayName: "Observability Viewer", actions: []string{"read_cluster_metrics"}},
	"grafana_dashboard_editor":      {displayName: "Grafana Dashboard Editor", actions: []string{"write_o11y_dashboards"}},
	"grafana_dashboard_viewer":      {displayName: "Grafana Dashboard Viewer", actions: []string{"read_o11y_dashboards"}},
	"registry_namespace_admin":      {displayName: "Registry Namespace Admin", actions: []string{"admin_registry_namespaces"}},
	"registry_namespace_content_admin": {
		displayName: "Registry Namespace Content Admin",
		actions:     []string{"admin_registry_content"},
	},
	"registry_namespace_content_viewer": {
		displayName: "Registry Namespace Content Viewer",
		actions:     []string{"read_registry_content"},
	},
	"registry_namespace_content_writer": {
		displayName: "Registry Namespace Content Writer",
		actions:     []string{"write_registry_content"},
	},
	"registry_namespace_viewer": {displayName: "Registry Namespace Viewer", actions: []string{"read_registry_namespaces"}},
	"registry_namespace_writer": {displayName: "Registry Namespace Writer", actions: []string{"write_registry_namespaces"}},
	"sandbox_admin":             {displayName: "Sandbox Admin", actions: []string{"sandbox_admin"}},
	"sandbox_user":              {displayName: "Sandbox User", actions: []string{"sandbox_user"}},
	"support_viewer":            {displayName: "Support Viewer", actions: []string{"read_freshdesk_support"}},
	"telemetry_relay_admin":     {displayName: "Telemetry Relay Admin", actions: []string{"write_telemetry_relay"}},
	"telemetry_relay_reader":    {displayName: "Telemetry Relay Reader", actions: []string{"read_telemetry_relay"}},
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
			"Returns the provider-versioned catalog of customer-assignable IAM access policy roles. Role keys are stable Terraform identifiers; each role resolves to the CoreWeave console actions accepted by `coreweave_iam_access_policy`. System-managed actions are excluded.",
		Attributes: map[string]schema.Attribute{
			"roles": schema.MapNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Customer-assignable roles keyed by stable Terraform identifiers.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The customer-facing role name.",
					},
					"actions": schema.SetAttribute{
						Computed:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Console action identifiers granted by the role. Pass this value to a `coreweave_iam_access_policy` rule's `actions` attribute.",
					},
				}},
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
		roleValues[key] = types.ObjectValueMust(accessPolicyRoleAttributeTypes, map[string]attr.Value{
			"display_name": types.StringValue(role.displayName),
			"actions":      types.SetValueMust(types.StringType, actions),
		})
	}

	data := AccessPolicyRolesDataSourceModel{
		Roles: types.MapValueMust(types.ObjectType{AttrTypes: accessPolicyRoleAttributeTypes}, roleValues),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
