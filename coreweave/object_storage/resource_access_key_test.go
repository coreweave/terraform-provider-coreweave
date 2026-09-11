package objectstorage_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Creates only these two test keys and revokes each by its own ID. The second
// key demonstrates that replacing the first leaves the same principal's other
// credentials usable. No principal-wide APIs or sweepers are involved.
func TestAccAccessKeyResource(t *testing.T) {
	var client *coreweave.Client
	var otherID, firstID string
	config := func(duration int, attribute string) string {
		return fmt.Sprintf(`
resource "coreweave_object_storage_access_key" "test" {
 duration_seconds = %d
 attributes = { purpose = %q }
}
resource "coreweave_object_storage_access_key" "other" {
 duration_seconds = 3600
 attributes = { purpose = "tf-acc-access-key-other" }
}
`, duration, attribute)
	}
	checkKeys := func(s *terraform.State) error {
		managed := s.RootModule().Resources[accessKeyAddress]
		other := s.RootModule().Resources[accessKeyTypeName+".other"]
		if managed == nil || other == nil {
			return fmt.Errorf("expected both test keys in state")
		}
		if otherID != "" && other.Primary.ID != otherID {
			return fmt.Errorf("replacement changed the other key ID")
		}
		otherID = other.Primary.ID
		info, err := client.GetAccessKeyInfo(context.Background(), connect.NewRequest(&cwobjectv1.GetAccessKeyInfoRequest{AccessKeyId: otherID}))
		if err != nil {
			return err
		}
		if info.Msg.GetInfo().GetStatus() != "ACTIVE" {
			return fmt.Errorf("other key is no longer active")
		}
		if firstID != "" && firstID != managed.Primary.ID {
			if err := checkAccessKeyRevoked(client, firstID); err != nil {
				return err
			}
		}
		firstID = managed.Primary.ID
		return nil
	}
	resource.ParallelTest(t, resource.TestCase{
		PreCheck: func() {
			if os.Getenv("COREWEAVE_API_ENDPOINT") == "" || os.Getenv("COREWEAVE_API_TOKEN") == "" {
				t.Fatal("set COREWEAVE_API_ENDPOINT and COREWEAVE_API_TOKEN for the authorized nonproduction environment")
			}
			var err error
			client, err = provider.BuildClient(context.Background(), provider.CoreweaveProviderModel{}, "test", "test")
			if err != nil {
				t.Fatal(err)
			}
		},
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			for _, r := range s.RootModule().Resources {
				if r.Type != accessKeyTypeName {
					continue
				}
				if err := checkAccessKeyRevoked(client, r.Primary.ID); err != nil {
					return err
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config(0, "tf-acc-access-key"), Check: resource.ComposeTestCheckFunc(checkKeys, resource.TestCheckResourceAttr(accessKeyAddress, "status", "ACTIVE"), resource.TestCheckResourceAttrSet(accessKeyAddress, "secret_key"), resource.TestCheckNoResourceAttr(accessKeyAddress, "expiry"))},
			{Config: config(0, "tf-acc-access-key"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{Config: config(3600, "tf-acc-access-key"), Check: resource.ComposeTestCheckFunc(checkKeys, resource.TestCheckResourceAttrSet(accessKeyAddress, "expiry")), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(accessKeyAddress, plancheck.ResourceActionDestroyBeforeCreate)}}},
			{Config: config(3600, "tf-acc-access-key-replaced"), Check: checkKeys},
			{ResourceName: accessKeyAddress, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"secret_key", "duration_seconds"}},
		},
	})
}

// Revocation retains metadata with DELETED status until the service removes it.
func checkAccessKeyRevoked(client *coreweave.Client, id string) error {
	response, err := client.GetAccessKeyInfo(context.Background(), connect.NewRequest(&cwobjectv1.GetAccessKeyInfoRequest{AccessKeyId: id}))
	if coreweave.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read revoked test key %s: %w", id, err)
	}
	info := response.Msg.GetInfo()
	if info.GetAccessKeyId() != id || info.GetStatus() != "DELETED" {
		return fmt.Errorf("test key %s was not revoked: API status %q", id, info.GetStatus())
	}
	return nil
}
