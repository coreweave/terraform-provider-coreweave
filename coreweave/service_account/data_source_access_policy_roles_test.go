package serviceaccount_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const accessPolicyRolesDataSourceAddress = "data.coreweave_iam_access_policy_roles.test"

var expectedAccessPolicyRoleKeys = []string{
	"access_request_approver",
	"access_token_admin",
	"access_token_user",
	"access_token_viewer",
	"arca_apps_admin",
	"arca_apps_editor",
	"arca_apps_viewer",
	"baremetal_admin",
	"baremetal_node_console_admin",
	"baremetal_node_console_viewer",
	"baremetal_viewer",
	"billing_viewer",
	"cks_admin",
	"cks_viewer",
	"grafana_dashboard_editor",
	"grafana_dashboard_viewer",
	"iam_admin",
	"iam_viewer",
	"inference_admin",
	"inference_viewer",
	"integrations_admin",
	"integrations_viewer",
	"notifications_admin",
	"notifications_viewer",
	"object_storage_admin",
	"object_storage_catalog_viewer",
	"observability_viewer",
	"registry_namespace_admin",
	"registry_namespace_content_admin",
	"registry_namespace_content_viewer",
	"registry_namespace_content_writer",
	"registry_namespace_viewer",
	"registry_namespace_writer",
	"sandbox_admin",
	"sandbox_user",
	"support_viewer",
	"telemetry_relay_admin",
	"telemetry_relay_reader",
}

func TestAccessPolicyRolesDataSource(t *testing.T) {
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	checks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttr(accessPolicyRolesDataSourceAddress, "roles.%", "38"),
		resource.TestCheckResourceAttr(accessPolicyRolesDataSourceAddress, "roles.access_token_admin.display_name", "Access Token Admin"),
		resource.TestCheckResourceAttr(accessPolicyRolesDataSourceAddress, "roles.access_token_admin.actions.#", "1"),
		resource.TestCheckTypeSetElemAttr(accessPolicyRolesDataSourceAddress, "roles.access_token_admin.actions.*", "access_token_admin"),
		resource.TestCheckResourceAttr(accessPolicyRolesDataSourceAddress, "roles.notifications_admin.display_name", "Alerts Admin"),
		resource.TestCheckResourceAttr(accessPolicyRolesDataSourceAddress, "roles.registry_namespace_content_admin.display_name", "Registry Namespace Content Admin"),
		resource.TestCheckTypeSetElemAttr(accessPolicyRolesDataSourceAddress, "roles.registry_namespace_content_admin.actions.*", "admin_registry_content"),
		resource.TestCheckNoResourceAttr(accessPolicyRolesDataSourceAddress, "roles.scim_api_reader"),
		resource.TestCheckNoResourceAttr(accessPolicyRolesDataSourceAddress, "roles.billing_admin"),
		resource.TestCheckNoResourceAttr(accessPolicyRolesDataSourceAddress, "roles.parent_org_admin"),
	}
	for _, key := range expectedAccessPolicyRoleKeys {
		checks = append(checks, resource.TestCheckResourceAttrSet(accessPolicyRolesDataSourceAddress, "roles."+key+".display_name"))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `data "coreweave_iam_access_policy_roles" "test" {}`,
			Check:  resource.ComposeAggregateTestCheckFunc(checks...),
		}},
	})
}
