package containerregistry_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// TestAccNamespaceDataSource verifies the live discovery contract independently.
func TestAccNamespaceDataSource(t *testing.T) {
	name, zone := acceptanceNamespace(t)
	config := namespaceConfig(name, zone, "null") + `
 data "coreweave_container_registry_namespace" "test" { name = coreweave_container_registry_namespace.test.name }
 `
	resource.ParallelTest(t, resource.TestCase{ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: checkNamespaceDestroyed(t, name), Steps: []resource.TestStep{{Config: config, ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue("data.coreweave_container_registry_namespace.test", tfjsonpath.New("namespace_id"), knownvalue.StringExact(name)), statecheck.ExpectKnownValue("data.coreweave_container_registry_namespace.test", tfjsonpath.New("zone"), knownvalue.StringExact(zone))}}}})
}
