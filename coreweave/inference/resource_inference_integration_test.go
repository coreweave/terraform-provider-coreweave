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

func inferenceIntegrationConfig(name, preferredZone, preferredInstance string) string {
	modelName := inferenceModelName()
	modelBucket := inferenceModelBucket()
	modelPath := inferenceModelPath()

	return fmt.Sprintf(`
data "coreweave_inference_capacity_claim_parameters" "cc_params" {}

locals {
  zones_map           = data.coreweave_inference_capacity_claim_parameters.cc_params.zone_instance_types
  preferred_zone      = %q
  available_zones     = sort(keys(local.zones_map))
  zone                = local.preferred_zone != "" ? local.preferred_zone : sort(tolist(local.available_zones))[0]
  preferred_instance  = %q
  available_instances = local.zones_map[local.zone].instance_types
  instance            = local.preferred_instance != "" ? local.preferred_instance : sort(tolist(local.available_instances))[0]

  # Runtime version is required on create; source it from deployment parameters.
  # Restrict to plain MAJOR.MINOR.PATCH for now — pre-release/build-metadata
  # versions are excluded pending #319. Revert this commit once #319 lands.
  available_runtime_versions = try(data.coreweave_inference_deployment_parameters.deploy_params.runtime_versions["vllm"].versions, [])
  runtime_version            = try([for v in local.available_runtime_versions : v if can(regex("^[0-9]+[.][0-9]+[.][0-9]+$", v))][0], "")
}

resource "coreweave_inference_capacity_claim" "test" {
  name = "%s-cc"

  resources = {
    instance_type  = local.instance
    instance_count = 1
    capacity_type  = "CAPACITY_TYPE_MANAGED"
    zones          = [local.zone]
  }

  lifecycle {
    precondition {
      condition     = local.preferred_zone == "" || contains(local.available_zones, local.preferred_zone)
      error_message = "TEST_ACC_INFERENCE_ZONE=\"${local.preferred_zone}\" is not present in capacity claim parameters; available: ${jsonencode(local.available_zones)}"
    }
    precondition {
      condition     = local.preferred_instance == "" || contains(local.available_instances, local.preferred_instance)
      error_message = "INFR_INSTANCE_ID=\"${local.preferred_instance}\" is not present in capacity claim parameters for zone ${local.zone}; available: ${jsonencode(local.available_instances)}"
    }
  }
}

resource "coreweave_inference_gateway" "test" {
  name  = "%s-gw"
  zones = [local.zone]

  auth = {
    coreweave = {}
  }

  routing = {
    body_based = {
      api_type = "API_TYPE_OPENAI"
    }
  }
}

data "coreweave_inference_deployment_parameters" "deploy_params" {
  depends_on = [coreweave_inference_gateway.test]
}

resource "coreweave_inference_deployment" "test" {
  name        = "%s-deploy"
  gateway_ids = [coreweave_inference_gateway.test.id]

  runtime = {
    engine  = "vllm"
    version = local.runtime_version
  }

  resources = {
    instance_type = coreweave_inference_capacity_claim.test.resources.instance_type
    gpu_count     = 1
  }

  model = {
    name   = %q
    bucket = %q
    path   = %q
  }

  autoscaling = {
    min              = 1
    max              = 1
    priority         = 100
    capacity_classes = ["CAPACITY_CLASS_RESERVED"]
  }

  lifecycle {
    precondition {
      condition     = local.runtime_version != ""
      error_message = "No x.y.z vllm runtime version available (pre-release/build-metadata excluded pending #319); available: ${jsonencode(local.available_runtime_versions)}"
    }
  }

  depends_on = [coreweave_inference_capacity_claim.test]
}
`, preferredZone, preferredInstance, name, name, name, modelName, modelBucket, modelPath)
}

// TestInferenceReservedCapacity exercises the full reserved-capacity chain —
// coreweave_inference_capacity_claim, coreweave_inference_gateway, and
// coreweave_inference_deployment — in a single config. The per-resource acceptance
// tests cover each resource in isolation; this one verifies they compose correctly:
// the deployment shares an instance_type with the capacity claim and schedules
// against it via capacity_classes = ["CAPACITY_CLASS_RESERVED"]. depends_on
// sequences create and destroy so the deployment tears down before the claim it
// references.
func TestInferenceReservedCapacity(t *testing.T) {
	t.Run("lifecycle", func(t *testing.T) {
		name := fmt.Sprintf("%sint-%x", AcceptanceTestPrefix, rand.IntN(100000))
		ccResource := "coreweave_inference_capacity_claim.test"
		gwResource := "coreweave_inference_gateway.test"
		depResource := "coreweave_inference_deployment.test"
		preferredZone := preferredInferenceZone()
		preferredInstance := preferredInferenceInstanceType()

		// Inference acceptance tests run sequentially via resource.Test (not
		// resource.ParallelTest) because the staging environment has limited
		// per-zone capacity and parallel runs cause allocation failures.
		//nolint:forbidigo // sequential per-zone capacity constraint, see comment above
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: inferenceIntegrationConfig(name, preferredZone, preferredInstance),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(ccResource, tfjsonpath.New("id"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(ccResource, tfjsonpath.New("status"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(ccResource, tfjsonpath.New("allocated_instances"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(ccResource, tfjsonpath.New("resources").AtMapKey("capacity_type"), knownvalue.StringExact("CAPACITY_TYPE_MANAGED")),
						statecheck.ExpectKnownValue(gwResource, tfjsonpath.New("id"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(depResource, tfjsonpath.New("status"), knownvalue.StringExact("STATUS_READY")),
						statecheck.ExpectKnownValue(depResource, tfjsonpath.New("autoscaling").AtMapKey("capacity_classes"), knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("CAPACITY_CLASS_RESERVED")})),
						statecheck.ExpectKnownValue(depResource, tfjsonpath.New("autoscaling").AtMapKey("priority"), knownvalue.Int64Exact(100)),
						statecheck.ExpectKnownValue(depResource, tfjsonpath.New("traffic").AtMapKey("weight"), knownvalue.Int64Exact(0)),
					},
				},
			},
		})
	})
}
