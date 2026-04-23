package inference_test

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func inferenceIntegrationConfig(name string) string {
	return fmt.Sprintf(`
data "coreweave_inference_capacity_claim_parameters" "cc_params" {}

locals {
  zones_map = data.coreweave_inference_capacity_claim_parameters.cc_params.zone_instance_types
  zone      = keys(local.zones_map)[0]
  instance  = local.zones_map[local.zone].instance_ids[0]
}

resource "coreweave_inference_capacity_claim" "test" {
  name = "%s-cc"

  resources = {
    instance_id    = local.instance
    instance_count = 1
    capacity_type  = "CAPACITY_TYPE_CUSTOMER"
    zones          = [local.zone]
  }
}

resource "coreweave_inference_gateway" "test" {
  name  = "%s-gw"
  zones = [local.zone]

  auth = {
    core_weave = {}
  }

  routing = {
    body_based = {
      api_type = "API_TYPE_OPENAI"
    }
  }
}

resource "coreweave_inference_deployment" "test" {
  name        = "%s-deploy"
  gateway_ids = [coreweave_inference_gateway.test.id]

  runtime = {
    engine = "vllm"
  }

  resources = {
    instance_type = coreweave_inference_capacity_claim.test.resources.instance_id
    gpu_count     = 1
  }

  model = {
    name   = "meta-llama/Llama-3.1-8B"
    bucket = "test-model-bucket"
    path   = "models/llama-3.1-8b"
  }

  autoscaling = {
    min              = 1
    max              = 1
    priority         = 100
    capacity_classes = ["CAPACITY_CLASS_RESERVED"]
  }

  traffic = {}

  depends_on = [coreweave_inference_capacity_claim.test]
}
`, name, name, name)
}

// TestAccInferenceReservedCapacity exercises the full reserved-capacity chain —
// coreweave_inference_capacity_claim, coreweave_inference_gateway, and
// coreweave_inference_deployment — in a single config. The per-resource acceptance
// tests cover each resource in isolation; this one verifies they compose correctly:
// the deployment shares an instance_id with the capacity claim and schedules against
// it via capacity_classes = ["CAPACITY_CLASS_RESERVED"]. depends_on sequences create and destroy so
// the deployment tears down before the claim it references.
func TestAccInferenceReservedCapacity(t *testing.T) {
	name := fmt.Sprintf("%sint-%x", AcceptanceTestPrefix, rand.IntN(100000))
	ccResource := "coreweave_inference_capacity_claim.test"
	gwResource := "coreweave_inference_gateway.test"
	depResource := "coreweave_inference_deployment.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: inferenceIntegrationConfig(name),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(ccResource, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(ccResource, tfjsonpath.New("status"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(ccResource, tfjsonpath.New("allocated_instances"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(ccResource, tfjsonpath.New("resources").AtMapKey("capacity_type"), knownvalue.StringExact("CAPACITY_TYPE_CUSTOMER")),
					statecheck.ExpectKnownValue(gwResource, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(depResource, tfjsonpath.New("status"), knownvalue.StringExact("STATUS_RUNNING")),
					statecheck.ExpectKnownValue(depResource, tfjsonpath.New("autoscaling").AtMapKey("capacity_classes"), knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("CAPACITY_CLASS_RESERVED")})),
					statecheck.ExpectKnownValue(depResource, tfjsonpath.New("autoscaling").AtMapKey("priority"), knownvalue.Int64Exact(100)),
				},
			},
		},
	})
}
