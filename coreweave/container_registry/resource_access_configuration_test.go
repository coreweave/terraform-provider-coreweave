package containerregistry_test

import (
	"fmt"
	"strings"
	"testing"

	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// accessConfig declares an authoritative policy with configurable CEL content.
func accessConfig(name, zone, expression string) string {
	return namespaceConfig(name, zone, "null") + fmt.Sprintf(`
resource "coreweave_container_registry_access_configuration" "test" {
 namespace = coreweave_container_registry_namespace.test.name
 policy_sets = {
 readers = {
 identity_selector = "WORKLOAD_FEDERATION"
 rules = { pull = { expression = %q } }
 }
 }
}`, expression)
}

// TestAccAccessConfiguration verifies nonempty policy updates, import, and authoritative removal.
func TestAccAccessConfiguration(t *testing.T) {
	name, zone := acceptanceNamespace(t)
	address := "coreweave_container_registry_access_configuration.test"
	empty := namespaceConfig(name, zone, "null") + `
resource "coreweave_container_registry_access_configuration" "test" {
 namespace = coreweave_container_registry_namespace.test.name
}`
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             checkNamespaceDestroyed(t, name),
		Steps: []resource.TestStep{
			{Config: accessConfig(name, zone, "true"), ConfigStateChecks: accessStateChecks(address, "true")},
			{ResourceName: address, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"updated_at"}},
			{Config: accessConfig(name, zone, "false"), ConfigStateChecks: accessStateChecks(address, "false")},

			{Config: empty, ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(address, tfjsonpath.New("policy_sets"), knownvalue.MapExact(map[string]knownvalue.Check{})), statecheck.ExpectKnownValue(address, tfjsonpath.New("request_ip_acl"), knownvalue.Null())}},
			{Config: accessConfig(name, zone, "true")},
			{Config: namespaceConfig(name, zone, "null"), Check: checkAccessReset(t, name)},
		}})
}

// accessStateChecks verifies the complete configured policy and successful delivery.
func accessStateChecks(address, expression string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(address, tfjsonpath.New("policy_sets"), knownvalue.MapExact(map[string]knownvalue.Check{"readers": knownvalue.ObjectExact(map[string]knownvalue.Check{"identity_selector": knownvalue.StringExact("WORKLOAD_FEDERATION"), "rules": knownvalue.MapExact(map[string]knownvalue.Check{"pull": knownvalue.ObjectExact(map[string]knownvalue.Check{"expression": knownvalue.StringExact(expression)})})})})),
		statecheck.ExpectKnownValue(address, tfjsonpath.New("access_config_state"), knownvalue.StringExact("ACCESS_CONFIG_STATE_ACCEPTED")),
	}
}

// TestAccAccessConfigurationIPACL verifies ACL delivery and omission independently of CEL policies.
func TestAccAccessConfigurationIPACL(t *testing.T) {
	name, zone := acceptanceNamespace(t)
	address := "coreweave_container_registry_access_configuration.test"
	base := strings.Replace(accessConfig(name, zone, "false"), " policy_sets = {", ` timeouts = { create = "2m", update = "2m", delete = "2m" }
 policy_sets = {`, 1)
	acl := strings.Replace(base, " policy_sets = {", ` request_ip_acl = { allow_cidrs = ["0.0.0.0/0", "::/0"], deny_cidrs = ["192.0.2.0/24"] }
 policy_sets = {`, 1)
	resource.ParallelTest(t, resource.TestCase{ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: checkNamespaceDestroyed(t, name), Steps: []resource.TestStep{
		{Config: base, ConfigStateChecks: accessStateChecks(address, "false")},
		{Config: acl, ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(address, tfjsonpath.New("request_ip_acl"), knownvalue.ObjectExact(map[string]knownvalue.Check{"allow_cidrs": knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("0.0.0.0/0"), knownvalue.StringExact("::/0")}), "deny_cidrs": knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("192.0.2.0/24")})}))}},
		{Config: base, ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(address, tfjsonpath.New("request_ip_acl"), knownvalue.Null())}},
	}})
}

// checkAccessReset verifies singleton deletion independently of parent namespace deletion.
func checkAccessReset(t *testing.T, name string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{}, "", "")
		if err != nil {
			return err
		}
		response, err := client.ContainerRegistry.GetRegistryAccessConfiguration(t.Context(), connect.NewRequest(&api.GetRegistryAccessConfigurationRequest{Parent: "namespaces/" + name}))
		if err != nil {
			return err
		}
		if len(response.Msg.PolicySets) != 0 || response.Msg.RequestIpAcl != nil || response.Msg.AccessConfigState != api.RegistryAccessConfiguration_ACCESS_CONFIG_STATE_ACCEPTED {
			return fmt.Errorf("destroy did not reset access to accepted deny-all without an ACL")
		}
		return nil
	}
}
