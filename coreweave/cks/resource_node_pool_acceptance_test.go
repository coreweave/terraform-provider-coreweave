package cks_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/coreweave/cks"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const nodePoolInstanceTypeEnv = "COREWEAVE_TEST_CKS_INSTANCE_TYPE"

func TestNodePoolResource(t *testing.T) {
	resources := generateResourceNames(t, "node-pool")
	instanceType := os.Getenv(nodePoolInstanceTypeEnv)
	if instanceType == "" {
		instanceType = "gd-8xh100ib-i128"
	}

	vpc := defaultVpc(resources.VPCName, testutil.AcceptanceTestZone)
	cluster := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", resources.ResourceName)),
		Name:                types.StringValue(resources.ClusterName),
		Zone:                types.StringValue(testutil.AcceptanceTestZone),
		Version:             types.StringValue(testutil.AcceptanceTestKubeVersion),
		Public:              types.BoolValue(true),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
	}
	baseConfig := strings.Join([]string{
		networking.MustRenderVpcResource(context.Background(), resources.ResourceName, vpc),
		cks.MustRenderClusterResource(context.Background(), resources.ResourceName, cluster),
	}, "\n")
	nodePoolName := AcceptanceTestPrefix + "node-pool"
	resourceName := "coreweave_cks_node_pool.test"

	nodePoolConfig := func(instanceType string, updated, spot bool) string {
		optional := ""
		if updated {
			optional = `
  autoscaling   = true
  min_nodes     = 0
  max_nodes     = 2
  labels = {
    managed-by = "terraform"
  }
  annotations = {
    "example.com/test" = "acceptance"
  }
  taints = [{
    key    = "dedicated"
    value  = "gpu"
    effect = "NoSchedule"
  }]
  update_strategy = "Always"`
		}
		if spot {
			optional += `
  compute_class = "spot"`
		}
		return baseConfig + fmt.Sprintf(`
resource "coreweave_cks_node_pool" "test" {
  cluster_id   = coreweave_cks_cluster.%s.id
  name          = %q
  instance_type = %q
  target_nodes  = 0%s
}
`, resources.ResourceName, nodePoolName, instanceType, optional)
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
			if os.Getenv("TF_ACC") != "" && os.Getenv(nodePoolInstanceTypeEnv) == "" {
				t.Logf("%s is unset; using %s", nodePoolInstanceTypeEnv, instanceType)
			}
		},
		Steps: []resource.TestStep{
			{
				Config: nodePoolConfig(instanceType, false, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", nodePoolName),
					resource.TestCheckResourceAttr(resourceName, "instance_type", instanceType),
					resource.TestCheckResourceAttr(resourceName, "target_nodes", "0"),
					resource.TestCheckResourceAttr(resourceName, "compute_class", "default"),
					resource.TestCheckResourceAttr(resourceName, "autoscaling", "false"),
				),
			},
			{
				Config: nodePoolConfig(instanceType, true, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "compute_class", "default"),
					resource.TestCheckResourceAttr(resourceName, "autoscaling", "true"),
					resource.TestCheckResourceAttr(resourceName, "min_nodes", "0"),
					resource.TestCheckResourceAttr(resourceName, "max_nodes", "2"),
					resource.TestCheckResourceAttr(resourceName, "labels.managed-by", "terraform"),
					resource.TestCheckResourceAttr(resourceName, "update_strategy", "Always"),
				),
			},
			{
				Config:             nodePoolConfig(instanceType, true, true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPreRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
				}},
			},
			{
				Config:             nodePoolConfig(instanceType+"-replacement-check", true, false),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPreRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
				}},
			},
			{
				ResourceName: resourceName,
				ImportState:  true,
				ImportStateIdFunc: func(state *terraform.State) (string, error) {
					resourceState := state.RootModule().Resources[resourceName]
					return resourceState.Primary.Attributes["cluster_id"] + "/" + nodePoolName, nil
				},
				ImportStateVerify: true,
			},
		},
	})
}
