package containerregistry_test

import (
	"fmt"
	"testing"

	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// lifecycleConfig declares a retention rule on an empty test namespace.
func lifecycleConfig(name, zone string, enabled bool, keep int) string {
	return namespaceConfig(name, zone, "null") + fmt.Sprintf(`
resource "coreweave_container_registry_lifecycle_policy" "test" {
 namespace = coreweave_container_registry_namespace.test.name
 enabled = %t
 rules = { retention = { regex = ".*", keep_newest = %d, older_than = "86400s" } }
}`, enabled, keep)
}

// TestAccLifecyclePolicy verifies rule updates, enablement, import, and authoritative removal.
func TestAccLifecyclePolicy(t *testing.T) {
	name, zone := acceptanceNamespace(t)
	address := "coreweave_container_registry_lifecycle_policy.test"
	empty := namespaceConfig(name, zone, "null") + `
resource "coreweave_container_registry_lifecycle_policy" "test" {
 namespace = coreweave_container_registry_namespace.test.name
}`
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             checkNamespaceDestroyed(t, name),
		Steps: []resource.TestStep{
			{Config: lifecycleConfig(name, zone, false, 10), ConfigStateChecks: lifecycleStateChecks(address, false, 10)},
			{ResourceName: address, ImportState: true, ImportStateId: name, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"updated_at"}},
			{Config: lifecycleConfig(name, zone, true, 5), ConfigStateChecks: lifecycleStateChecks(address, true, 5)},
			{Config: empty, ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(address, tfjsonpath.New("enabled"), knownvalue.Bool(false)), statecheck.ExpectKnownValue(address, tfjsonpath.New("rules"), knownvalue.MapExact(map[string]knownvalue.Check{}))}},
			{Config: lifecycleConfig(name, zone, true, 5)},
			{Config: namespaceConfig(name, zone, "null"), Check: checkLifecycleReset(t, name)},
		}})
}

// lifecycleStateChecks verifies enablement and both configured retention conditions.
func lifecycleStateChecks(address string, enabled bool, keep int64) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(address, tfjsonpath.New("enabled"), knownvalue.Bool(enabled)),
		statecheck.ExpectKnownValue(address, tfjsonpath.New("rules"), knownvalue.MapExact(map[string]knownvalue.Check{"retention": knownvalue.ObjectExact(map[string]knownvalue.Check{"regex": knownvalue.StringExact(".*"), "keep_newest": knownvalue.Int64Exact(keep), "older_than": knownvalue.StringExact("86400s")})})),
	}
}

// checkLifecycleReset verifies singleton deletion independently of parent namespace deletion.
func checkLifecycleReset(t *testing.T, name string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{}, "", "")
		if err != nil {
			return err
		}
		response, err := client.ContainerRegistry.GetRegistryLifecyclePolicy(t.Context(), &api.GetRegistryLifecyclePolicyRequest{Parent: "namespaces/" + name})
		if err != nil {
			return err
		}
		policy := response
		if policy.Enabled || len(policy.Rules) != 0 || policy.AppliedRevision == nil || *policy.AppliedRevision != policy.Revision {
			return fmt.Errorf("destroy did not acknowledge disabled, empty lifecycle policy")
		}
		return nil
	}
}
