package containerregistry_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// TestAccNamespaceQuota verifies import, explicit zero, updates, and clearing the ceiling.
func TestAccNamespaceQuota(t *testing.T) {
	name, zone := acceptanceNamespace(t)
	address := "coreweave_container_registry_namespace.test"
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             checkNamespaceDestroyed(t, name),
		Steps: []resource.TestStep{
			{Config: namespaceConfig(name, zone, "null"), ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(address, tfjsonpath.New("force_destroy"), knownvalue.Bool(false)), statecheck.ExpectKnownValue(address, tfjsonpath.New("storage_quota_bytes"), knownvalue.Null())}},
			{ResourceName: address, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"content_status", "access_mode", "update_time", "updated_by"}},
			{Config: namespaceConfig(name, zone, "0"), ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(address, tfjsonpath.New("storage_quota_bytes"), knownvalue.Int64Exact(0))}},
			{Config: namespaceConfig(name, zone, "1048576"), ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(address, tfjsonpath.New("storage_quota_bytes"), knownvalue.Int64Exact(1048576))}},
			{Config: namespaceConfig(name, zone, "null"), ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(address, tfjsonpath.New("storage_quota_bytes"), knownvalue.Null())}},
		}})
}
