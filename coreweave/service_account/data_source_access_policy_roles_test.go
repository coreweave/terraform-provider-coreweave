package serviceaccount_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const accessPolicyRolesDataSourceAddress = "data.coreweave_iam_access_policy_roles.test"

var expectedAccessPolicyRoleKeys = []string{
	"Access Request Approver",
	"Access Token Admin",
	"Access Token User",
	"Access Token Viewer",
	"Alerts Admin",
	"Alerts Viewer",
	"Arca Apps Admin",
	"Arca Apps Editor",
	"Arca Apps Viewer",
	"Baremetal Admin",
	"Baremetal Node Console Admin",
	"Baremetal Node Console Viewer",
	"Baremetal Viewer",
	"Billing Viewer",
	"CKS Admin",
	"CKS Viewer",
	"Grafana Dashboard Editor",
	"Grafana Dashboard Viewer",
	"IAM Admin",
	"IAM Viewer",
	"Inference Admin",
	"Inference Viewer",
	"Integrations Admin",
	"Integrations Viewer",
	"Object Storage Admin",
	"Object Storage Catalog Viewer",
	"Observability Viewer",
	"Registry Namespace Admin",
	"Registry Namespace Content Admin",
	"Registry Namespace Content Viewer",
	"Registry Namespace Content Writer",
	"Registry Namespace Viewer",
	"Registry Namespace Writer",
	"Sandbox Admin",
	"Sandbox User",
	"Support Viewer",
	"Telemetry Relay Admin",
	"Telemetry Relay Reader",
}

func TestAccessPolicyRolesDataSource(t *testing.T) {
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	checks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttr(accessPolicyRolesDataSourceAddress, "roles.%", "38"),
		resource.TestCheckResourceAttr(accessPolicyRolesDataSourceAddress, "roles.Access Token Admin.#", "1"),
		resource.TestCheckTypeSetElemAttr(accessPolicyRolesDataSourceAddress, "roles.Access Token Admin.*", "access_token_admin"),
		resource.TestCheckResourceAttr(accessPolicyRolesDataSourceAddress, "roles.Alerts Admin.#", "1"),
		resource.TestCheckTypeSetElemAttr(accessPolicyRolesDataSourceAddress, "roles.Alerts Admin.*", "write_notifications"),
		resource.TestCheckResourceAttr(accessPolicyRolesDataSourceAddress, "roles.Registry Namespace Content Admin.#", "1"),
		resource.TestCheckTypeSetElemAttr(accessPolicyRolesDataSourceAddress, "roles.Registry Namespace Content Admin.*", "admin_registry_content"),
		resource.TestCheckNoResourceAttr(accessPolicyRolesDataSourceAddress, "roles.SCIM API reader (system-managed)"),
		resource.TestCheckNoResourceAttr(accessPolicyRolesDataSourceAddress, "roles.Billing admin (system-managed)"),
		resource.TestCheckNoResourceAttr(accessPolicyRolesDataSourceAddress, "roles.Parent organization admin (system-managed)"),
	}
	for _, key := range expectedAccessPolicyRoleKeys {
		checks = append(checks, resource.TestCheckResourceAttrSet(accessPolicyRolesDataSourceAddress, "roles."+key+".#"))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `data "coreweave_iam_access_policy_roles" "test" {}`,
			Check:  resource.ComposeAggregateTestCheckFunc(checks...),
		}},
	})
}
