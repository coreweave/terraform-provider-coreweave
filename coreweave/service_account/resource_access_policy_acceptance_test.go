package serviceaccount_test

import (
	"fmt"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccIAMAccessPolicyResource(t *testing.T) {
	accessPolicyID := acctest.RandomWithPrefix("tf-acc-ap")
	var firstUID string
	configWith := func(description string) string {
		return fmt.Sprintf(`
data "coreweave_caller_identity" "current" {}

resource "coreweave_iam_access_policy" "test" {
  organization_id  = data.coreweave_caller_identity.current.organization_id
  access_policy_id = %q
  description      = %q

  rules = [{
    actions = ["read_auth"]
  }]
}
`, accessPolicyID, description)
	}
	updated := configWith("Terraform acceptance test updated")

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configWith("Terraform acceptance test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(accessPolicyResourceAddress, "access_policy_id", accessPolicyID),
					resource.TestCheckResourceAttr(accessPolicyResourceAddress, "description", "Terraform acceptance test"),
					resource.TestCheckResourceAttr(accessPolicyResourceAddress, "policy_type", "POLICY_TYPE_CUSTOM"),
					resource.TestCheckResourceAttr(accessPolicyResourceAddress, "active", "true"),
					resource.TestCheckResourceAttr(accessPolicyResourceAddress, "rules.#", "1"),
					resource.TestCheckResourceAttrSet(accessPolicyResourceAddress, "uid"),
					func(state *terraform.State) error {
						firstUID = state.RootModule().Resources[accessPolicyResourceAddress].Primary.Attributes["uid"]
						return nil
					},
				),
			},
			{
				ResourceName:                         accessPolicyResourceAddress,
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_policy_id",
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					return state.RootModule().Resources[accessPolicyResourceAddress].Primary.Attributes["name"], nil
				},
			},
			{
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(accessPolicyResourceAddress, plancheck.ResourceActionUpdate),
				}},
				Check: func(state *terraform.State) error {
					attributes := state.RootModule().Resources[accessPolicyResourceAddress].Primary.Attributes
					if attributes["description"] != "Terraform acceptance test updated" {
						return fmt.Errorf("description not updated: %q", attributes["description"])
					}
					if attributes["uid"] != firstUID {
						return fmt.Errorf("UID changed during in-place update: %q -> %q", firstUID, attributes["uid"])
					}
					return nil
				},
			},
			{
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				}},
			},
		},
	})
}
