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

func TestAccIAMServiceAccountResource(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc-sa")
	var firstUID string
	configWith := func(displayName string) string {
		return fmt.Sprintf(`
data "coreweave_caller_identity" "current" {}

resource "coreweave_iam_service_account" "test" {
  id              = %q
  organization_id = data.coreweave_caller_identity.current.organization_id
  display_name    = %q
}
`, id, displayName)
	}
	configUpdated := configWith("tf acc updated")

	// Service account deletion must complete before the same caller-chosen ID
	// can be recreated, so keep this lifecycle test sequential.
	//nolint:forbidigo // sequential test exercises same-ID destroy-before-create
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: configWith("tf acc initial"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(serviceAccountResourceAddress, "display_name", "tf acc initial"),
					resource.TestCheckResourceAttr(serviceAccountResourceAddress, "active", "true"),
					resource.TestCheckResourceAttrSet(serviceAccountResourceAddress, "uid"),
					func(state *terraform.State) error {
						firstUID = state.RootModule().Resources[serviceAccountResourceAddress].Primary.Attributes["uid"]
						return nil
					},
				),
			},
			{
				ResourceName:      serviceAccountResourceAddress,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					return state.RootModule().Resources[serviceAccountResourceAddress].Primary.Attributes["name"], nil
				},
			},
			{
				Config: configUpdated,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(serviceAccountResourceAddress, plancheck.ResourceActionUpdate),
				}},
				Check: func(state *terraform.State) error {
					attributes := state.RootModule().Resources[serviceAccountResourceAddress].Primary.Attributes
					if attributes["display_name"] != "tf acc updated" {
						return fmt.Errorf("display_name not updated: %q", attributes["display_name"])
					}
					if attributes["uid"] != firstUID {
						return fmt.Errorf("UID changed during in-place update: %q -> %q", firstUID, attributes["uid"])
					}
					return nil
				},
			},
			{
				Config: configUpdated,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				}},
			},
			{
				Config: configUpdated,
				Taint:  []string{serviceAccountResourceAddress},
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(serviceAccountResourceAddress, plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: func(state *terraform.State) error {
					if state.RootModule().Resources[serviceAccountResourceAddress].Primary.Attributes["uid"] == firstUID {
						return fmt.Errorf("expected a new UID after replacement")
					}
					return nil
				},
			},
		},
	})
}
