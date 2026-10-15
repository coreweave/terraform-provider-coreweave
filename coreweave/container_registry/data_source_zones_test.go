package containerregistry_test

import (
	"fmt"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// TestAccZonesDataSource verifies the live discovery contract independently.
func TestAccZonesDataSource(t *testing.T) {
	name, zone := acceptanceNamespace(t)
	config := namespaceConfig(name, zone, "null") + fmt.Sprintf(`
 data "coreweave_container_registry_zones" "test" { zone_names = [%q] }
 `, zone)
	resource.ParallelTest(t, resource.TestCase{ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: checkNamespaceDestroyed(t, name), Steps: []resource.TestStep{{Config: config, ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue("data.coreweave_container_registry_zones.test", tfjsonpath.New("zones"), knownvalue.ListExact([]knownvalue.Check{knownvalue.ObjectPartial(map[string]knownvalue.Check{"zone": knownvalue.StringExact(zone), "available": knownvalue.Bool(true)})}))}}}})
}
