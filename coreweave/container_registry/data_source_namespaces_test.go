package containerregistry_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// TestAccNamespacesDataSource verifies the live discovery contract independently.
func TestAccNamespacesDataSource(t *testing.T) {
	name, zone := acceptanceNamespace(t)
	config := namespaceConfig(name, zone, "null") + `
 data "coreweave_container_registry_namespaces" "test" { depends_on = [coreweave_container_registry_namespace.test] }
 `
	resource.ParallelTest(t, resource.TestCase{ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: checkNamespaceDestroyed(t, name), Steps: []resource.TestStep{{Config: config, ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue("data.coreweave_container_registry_namespaces.test", tfjsonpath.New("namespaces"), knownvalue.SetPartial([]knownvalue.Check{knownvalue.ObjectPartial(map[string]knownvalue.Check{"name": knownvalue.StringExact("namespaces/" + name), "zone": knownvalue.StringExact(zone)})}))}}}})
}
