package cks_test

import (
	"regexp"
	"testing"

	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
)

func TestPrivateAccessTerraformLifecycle(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	steps := []tfresource.TestStep{{Config: kubeAPIClusterConfig("", "v1.33", "")}}
	for _, access := range []string{
		`private_access = { prefix_name_ipv4 = "private-v4" }`,
		`private_access = {
 prefix_name_ipv4 = "private-v4"
 prefix_name_ipv6 = "private-v6"
 }`,
		`private_access = { prefix_name_ipv6 = "private-v6" }`,
		"",
	} {
		// Each transition adds, changes, or removes immutable private access.
		config := kubeAPIClusterConfig("", "v1.33", access)
		steps = append(steps, tfresource.TestStep{
			Config: config,
			ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(kubeAPIClusterAddress, plancheck.ResourceActionDestroyBeforeCreate),
			}},
		}, tfresource.TestStep{Config: config, PlanOnly: true}, tfresource.TestStep{
			ResourceName: kubeAPIClusterAddress, ImportState: true, ImportStateVerify: true,
		})
	}
	runKubeAPIAccessTest(t, service, steps...)
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.creates, 5)
	require.Nil(t, service.creates[0].Network.PrivateAccess)
	require.Equal(t, "private-v4", service.creates[1].Network.PrivateAccess.PrefixNameIpv4)
	require.Equal(t, "private-v6", service.creates[2].Network.PrivateAccess.PrefixNameIpv6)
	require.Empty(t, service.creates[3].Network.PrivateAccess.PrefixNameIpv4)
	require.Nil(t, service.creates[4].Network.PrivateAccess)
	require.Zero(t, service.updateCalls)
}

func TestPrivateAccessTerraformReadback(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	checks := make([]statecheck.StateCheck, 0, 2)
	for _, address := range []string{kubeAPIClusterAddress, kubeAPIDataSourceAddress} {
		checks = append(checks, statecheck.ExpectKnownValue(address, tfjsonpath.New("private_access"), knownvalue.ObjectExact(map[string]knownvalue.Check{
			"prefix_name_ipv4": knownvalue.StringExact("private-v4"),
			"prefix_name_ipv6": knownvalue.Null(),
		})))
	}
	config := kubeAPIClusterConfig("", "v1.33", `private_access = { prefix_name_ipv4 = "private-v4" }`)
	runKubeAPIAccessTest(t, service, tfresource.TestStep{Config: config, ConfigStateChecks: checks}, tfresource.TestStep{
		Config: kubeAPIClusterConfig("", "v1.34", `private_access = { prefix_name_ipv4 = "private-v4" }`), ConfigStateChecks: checks,
		ConfigPlanChecks: expectUpdate(),
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.updates, 1)
	require.Nil(t, service.updates[0].Network)
}

func TestPrivateAccessTerraformValidation(t *testing.T) {
	for _, value := range []string{`{}`, `{ prefix_name_ipv4 = "" }`, `{ prefix_name_ipv6 = "" }`} {
		t.Run(value, func(t *testing.T) {
			service := startKubeAPIAccessServer(t)
			runKubeAPIAccessTest(t, service, tfresource.TestStep{
				Config:      kubeAPIClusterConfig("", "v1.33", "private_access = "+value),
				ExpectError: regexp.MustCompile(`Invalid Attribute (Combination|Value Length)`),
			})
		})
	}
}
