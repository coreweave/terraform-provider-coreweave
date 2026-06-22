package inference_test

import (
	"fmt"
	"math/rand/v2"
	"testing"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	fwschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// --- Unit tests for pure helper functions ---

func TestInferenceDeployment_Schema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaReq := fwresource.SchemaRequest{}
	schemaResp := &fwresource.SchemaResponse{}

	inference.NewInferenceDeploymentResource().Schema(ctx, schemaReq, schemaResp)

	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema returned errors: %v", schemaResp.Diagnostics)
	}

	trafficAttr, ok := schemaResp.Schema.Attributes["traffic"].(fwschema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("traffic: expected SingleNestedAttribute, got %T", schemaResp.Schema.Attributes["traffic"])
	}
	if !trafficAttr.Optional {
		t.Error("traffic: expected optional")
	}
	if !trafficAttr.Computed {
		t.Error("traffic: expected computed")
	}
	if trafficAttr.Default == nil {
		t.Error("traffic: expected default")
	}

	weightAttr, ok := trafficAttr.Attributes["weight"].(fwschema.Int64Attribute)
	if !ok {
		t.Fatalf("traffic.weight: expected Int64Attribute, got %T", trafficAttr.Attributes["weight"])
	}
	if !weightAttr.Optional {
		t.Error("traffic.weight: expected optional")
	}
	if !weightAttr.Computed {
		t.Error("traffic.weight: expected computed")
	}
}

func TestInferenceDeployment_SetFromDeployment_NullPreservation(t *testing.T) {
	t.Parallel()

	// Build a minimal proto deployment with no optional fields set.
	d := &inferencev1.Deployment{
		Spec: &inferencev1.DeploymentSpec{
			Id:   "test-id",
			Name: "my-llm",
			Runtime: &inferencev1.DeploymentRuntime{
				Engine:  "vllm",
				Version: "",
			},
			Resources: &inferencev1.DeploymentResources{
				InstanceType: "H100_80GB_SXM5",
				GpuCount:     1,
			},
			Model: &inferencev1.DeploymentModel{
				Name:   "meta-llama/Llama-3.1-8B",
				Bucket: "my-bucket",
				Path:   "models/llama",
			},
			Autoscaling: &inferencev1.DeploymentAutoscaling{
				Min: 1,
				Max: 4,
			},
			Traffic: &inferencev1.DeploymentTraffic{},
		},
		Status: &inferencev1.DeploymentStatus{
			Status: inferencev1.Status_STATUS_READY,
		},
	}

	// Model starts with all optional fields null (simulating plan where user didn't configure them).
	m := &inference.InferenceDeploymentResourceModel{
		Runtime: &inference.RuntimeModel{
			Version:      types.StringNull(),
			EngineConfig: types.MapNull(types.StringType),
		},
		Autoscaling: &inference.AutoscalingModel{
			Priority:        types.Int64Null(),
			CapacityClasses: types.ListNull(types.StringType),
			Concurrency:     types.Int64Null(),
		},
		Traffic: &inference.TrafficModel{
			Weight: types.Int64Null(),
		},
	}

	inference.SetFromDeployment(m, d)

	// Optional fields with default values should remain null.
	if !m.Runtime.Version.IsNull() {
		t.Errorf("Runtime.Version: expected null when API returns empty, got %v", m.Runtime.Version)
	}
	if !m.Runtime.EngineConfig.IsNull() {
		t.Errorf("Runtime.EngineConfig: expected null when API returns empty map, got %v", m.Runtime.EngineConfig)
	}
	if !m.Autoscaling.Priority.IsNull() {
		t.Errorf("Autoscaling.Priority: expected null when API returns 0, got %v", m.Autoscaling.Priority)
	}
	if !m.Autoscaling.Concurrency.IsNull() {
		t.Errorf("Autoscaling.Concurrency: expected null when API returns 0, got %v", m.Autoscaling.Concurrency)
	}
	if !m.Autoscaling.CapacityClasses.IsNull() {
		t.Errorf("Autoscaling.CapacityClasses: expected null when API returns empty, got %v", m.Autoscaling.CapacityClasses)
	}
	m.Runtime.Version = types.StringUnknown()
	inference.SetFromDeployment(m, d)
	if !m.Runtime.Version.IsNull() {
		t.Errorf("Runtime.Version: expected null when plan is unknown and API returns empty, got %v", m.Runtime.Version)
	}

	// traffic weight is computed: the API value is always populated into state.
	if m.Traffic.Weight.ValueInt64() != 0 {
		t.Errorf("Traffic.Weight: expected 0 from API response, got %v", m.Traffic.Weight)
	}

	// Required fields should always be populated.
	if m.ID.ValueString() != "test-id" {
		t.Errorf("ID: expected 'test-id', got %q", m.ID.ValueString())
	}
	if m.Resources.GpuCount.ValueInt64() != 1 {
		t.Errorf("GpuCount: expected 1, got %d", m.Resources.GpuCount.ValueInt64())
	}
	if m.Autoscaling.Min.ValueInt64() != 1 {
		t.Errorf("Autoscaling.Min: expected 1, got %d", m.Autoscaling.Min.ValueInt64())
	}
}

func TestInferenceDeployment_ToCreateRequest_OptionalFields(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	gwIds := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")})

	m := &inference.InferenceDeploymentResourceModel{
		Name:       types.StringValue("my-llm"),
		GatewayIds: gwIds,
		Disabled:   types.BoolValue(false),
		Runtime: &inference.RuntimeModel{
			Engine:       types.StringValue("vllm"),
			Version:      types.StringNull(),
			EngineConfig: types.MapNull(types.StringType),
		},
		Resources: &inference.ResourcesModel{
			InstanceType: types.StringValue("H100_80GB_SXM5"),
			GpuCount:     types.Int64Value(1),
		},
		Model: &inference.DeploymentModelConfig{
			Name:   types.StringValue("meta-llama/Llama-3.1-8B"),
			Bucket: types.StringValue("my-bucket"),
			Path:   types.StringValue("models/llama"),
		},
		Autoscaling: &inference.AutoscalingModel{
			Min:             types.Int64Value(1),
			Max:             types.Int64Value(4),
			Priority:        types.Int64Null(),
			CapacityClasses: types.ListNull(types.StringType),
			Concurrency:     types.Int64Null(),
		},
		Traffic: &inference.TrafficModel{
			Weight: types.Int64Null(),
		},
	}

	req, diags := inference.ToCreateRequest(ctx, m)
	if diags.HasError() {
		t.Fatalf("ToCreateRequest returned errors: %v", diags)
	}

	if req.Name != "my-llm" {
		t.Errorf("Name: got %q, want 'my-llm'", req.Name)
	}
	if req.Runtime.Version != "" {
		t.Errorf("Runtime.Version: expected empty when null, got %q", req.Runtime.Version)
	}
	if len(req.Runtime.EngineConfig) != 0 {
		t.Errorf("Runtime.EngineConfig: expected empty map, got %v", req.Runtime.EngineConfig)
	}
	if req.Autoscaling.Priority != 0 {
		t.Errorf("Autoscaling.Priority: expected 0 when null, got %d", req.Autoscaling.Priority)
	}
	if req.Traffic.Weight != 0 {
		t.Errorf("Traffic.Weight: expected 0 when null, got %d", req.Traffic.Weight)
	}
	if req.Resources.GpuCount != 1 {
		t.Errorf("GpuCount: expected 1, got %d", req.Resources.GpuCount)
	}
}

func TestInferenceDeployment_ToUpdateRequest_Fields(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	gwIds := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")})
	ccList, _ := types.ListValueFrom(ctx, types.StringType, []string{"CAPACITY_CLASS_RESERVED"})

	m := &inference.InferenceDeploymentResourceModel{
		ID:         types.StringValue("deploy-123"),
		Name:       types.StringValue("my-llm"),
		GatewayIds: gwIds,
		Disabled:   types.BoolValue(true),
		Runtime: &inference.RuntimeModel{
			Engine:       types.StringValue("vllm"),
			Version:      types.StringValue("1.0.0"),
			EngineConfig: types.MapNull(types.StringType),
		},
		Resources: &inference.ResourcesModel{
			InstanceType: types.StringValue("H100_80GB_SXM5"),
			GpuCount:     types.Int64Value(2),
		},
		Model: &inference.DeploymentModelConfig{
			Name:   types.StringValue("meta-llama/Llama-3.1-8B"),
			Bucket: types.StringValue("my-bucket"),
			Path:   types.StringValue("models/llama"),
		},
		Autoscaling: &inference.AutoscalingModel{
			Min:             types.Int64Value(2),
			Max:             types.Int64Value(8),
			Priority:        types.Int64Value(100),
			CapacityClasses: ccList,
			Concurrency:     types.Int64Value(4),
		},
		Traffic: &inference.TrafficModel{
			Weight: types.Int64Value(50),
		},
	}

	req, diags := inference.ToUpdateRequest(ctx, m)
	if diags.HasError() {
		t.Fatalf("ToUpdateRequest returned errors: %v", diags)
	}

	if req.Id != "deploy-123" {
		t.Errorf("Id: got %q, want 'deploy-123'", req.Id)
	}
	if req.Name != "my-llm" {
		t.Errorf("Name: got %q, want 'my-llm'", req.Name)
	}
	if !req.Disabled {
		t.Error("Disabled: expected true")
	}
	if req.Runtime.Version != "1.0.0" {
		t.Errorf("Runtime.Version: got %q, want '1.0.0'", req.Runtime.Version)
	}
	if req.Resources.GpuCount != 2 {
		t.Errorf("GpuCount: expected 2, got %d", req.Resources.GpuCount)
	}
	if req.Autoscaling.Min != 2 {
		t.Errorf("Autoscaling.Min: expected 2, got %d", req.Autoscaling.Min)
	}
	if req.Autoscaling.Max != 8 {
		t.Errorf("Autoscaling.Max: expected 8, got %d", req.Autoscaling.Max)
	}
	if req.Autoscaling.Priority != 100 {
		t.Errorf("Autoscaling.Priority: expected 100, got %d", req.Autoscaling.Priority)
	}
	if req.Autoscaling.Concurrency != 4 {
		t.Errorf("Autoscaling.Concurrency: expected 4, got %d", req.Autoscaling.Concurrency)
	}
	if len(req.Autoscaling.CapacityClasses) != 1 {
		t.Fatalf("Autoscaling.CapacityClasses: expected 1 element, got %d", len(req.Autoscaling.CapacityClasses))
	}
	if req.Traffic.Weight != 50 {
		t.Errorf("Traffic.Weight: expected 50, got %d", req.Traffic.Weight)
	}
}

// --- Acceptance tests ---

func inferenceDeploymentConfig(name, preferredZone, preferredInstance string) string {
	modelName := inferenceModelName()
	modelBucket := inferenceModelBucket()
	modelPath := inferenceModelPath()

	return fmt.Sprintf(`
data "coreweave_inference_gateway_parameters" "gw_params" {}

# Read deployment parameters AFTER the gateway is created. The
# GetDeploymentParameters response is scoped by the org's existing gateways and
# returns an empty instance_types list when no gateway exists yet, which would
# fail the precondition below at plan time. depends_on defers the read until
# after the gateway resource exists, and Terraform re-evaluates the precondition
# at apply time once available_instances is known.
data "coreweave_inference_deployment_parameters" "deploy_params" {
  depends_on = [coreweave_inference_gateway.test]
}

locals {
  preferred_zone      = %q
  available_zones     = data.coreweave_inference_gateway_parameters.gw_params.zones
  zone                = local.preferred_zone != "" ? local.preferred_zone : sort(tolist(local.available_zones))[0]
  preferred_instance  = %q
  available_instances = data.coreweave_inference_deployment_parameters.deploy_params.instance_types
  instance            = local.preferred_instance != "" ? local.preferred_instance : sort(tolist(local.available_instances))[0]

  # Runtime version is required on create; source it from deployment parameters.
  # Restrict to plain MAJOR.MINOR.PATCH for now — pre-release/build-metadata
  # versions are excluded pending #319. Revert this commit once #319 lands.
  available_runtime_versions = try(data.coreweave_inference_deployment_parameters.deploy_params.runtime_versions["vllm"].versions, [])
  runtime_version            = try([for v in local.available_runtime_versions : v if can(regex("^[0-9]+[.][0-9]+[.][0-9]+$", v))][0], "")
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

  lifecycle {
    precondition {
      condition     = local.preferred_zone == "" || contains(local.available_zones, local.preferred_zone)
      error_message = "TEST_ACC_INFERENCE_ZONE=\"${local.preferred_zone}\" is not present in gateway parameters; available: ${jsonencode(local.available_zones)}"
    }
  }
}

resource "coreweave_inference_deployment" "test" {
  name        = %q
  gateway_ids = [coreweave_inference_gateway.test.id]

  runtime = {
    engine  = "vllm"
    version = local.runtime_version
  }

  resources = {
    instance_type = local.instance
    gpu_count     = 1
  }

  model = {
    name   = %q
    bucket = %q
    path   = %q
  }

  autoscaling = {
    min = 1
    max = 2
  }

  traffic = {}

  lifecycle {
    precondition {
      condition     = local.preferred_instance == "" || contains(local.available_instances, local.preferred_instance)
      error_message = "INFR_INSTANCE_ID=\"${local.preferred_instance}\" is not present in inference parameters; available: ${jsonencode(local.available_instances)}"
    }
    precondition {
      condition     = local.runtime_version != ""
      error_message = "No x.y.z vllm runtime version available (pre-release/build-metadata excluded pending #319); available: ${jsonencode(local.available_runtime_versions)}"
    }
  }
}
`, preferredZone, preferredInstance, name, name, modelName, modelBucket, modelPath)
}

func inferenceDeploymentUpdatedConfig(name, preferredZone, preferredInstance string) string {
	modelName := inferenceModelName()
	modelBucket := inferenceModelBucket()
	modelPath := inferenceModelPath()

	return fmt.Sprintf(`
data "coreweave_inference_gateway_parameters" "gw_params" {}

# Read deployment parameters AFTER the gateway is created. The
# GetDeploymentParameters response is scoped by the org's existing gateways and
# returns an empty instance_types list when no gateway exists yet, which would
# fail the precondition below at plan time. depends_on defers the read until
# after the gateway resource exists, and Terraform re-evaluates the precondition
# at apply time once available_instances is known.
data "coreweave_inference_deployment_parameters" "deploy_params" {
  depends_on = [coreweave_inference_gateway.test]
}

locals {
  preferred_zone      = %q
  available_zones     = data.coreweave_inference_gateway_parameters.gw_params.zones
  zone                = local.preferred_zone != "" ? local.preferred_zone : sort(tolist(local.available_zones))[0]
  preferred_instance  = %q
  available_instances = data.coreweave_inference_deployment_parameters.deploy_params.instance_types
  instance            = local.preferred_instance != "" ? local.preferred_instance : sort(tolist(local.available_instances))[0]

  # Runtime version is required on create; source it from deployment parameters.
  # Restrict to plain MAJOR.MINOR.PATCH for now — pre-release/build-metadata
  # versions are excluded pending #319. Revert this commit once #319 lands.
  available_runtime_versions = try(data.coreweave_inference_deployment_parameters.deploy_params.runtime_versions["vllm"].versions, [])
  runtime_version            = try([for v in local.available_runtime_versions : v if can(regex("^[0-9]+[.][0-9]+[.][0-9]+$", v))][0], "")
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

  lifecycle {
    precondition {
      condition     = local.preferred_zone == "" || contains(local.available_zones, local.preferred_zone)
      error_message = "TEST_ACC_INFERENCE_ZONE=\"${local.preferred_zone}\" is not present in gateway parameters; available: ${jsonencode(local.available_zones)}"
    }
  }
}

resource "coreweave_inference_deployment" "test" {
  name        = %q
  gateway_ids = [coreweave_inference_gateway.test.id]
  disabled    = true

  runtime = {
    engine  = "vllm"
    version = local.runtime_version
  }

  resources = {
    instance_type = local.instance
    gpu_count     = 1
  }

  model = {
    name   = %q
    bucket = %q
    path   = %q
  }

  autoscaling = {
    min = 2
    max = 4
  }

  traffic = {
    weight = 50
  }

  lifecycle {
    precondition {
      condition     = local.preferred_instance == "" || contains(local.available_instances, local.preferred_instance)
      error_message = "INFR_INSTANCE_ID=\"${local.preferred_instance}\" is not present in inference parameters; available: ${jsonencode(local.available_instances)}"
    }
    precondition {
      condition     = local.runtime_version != ""
      error_message = "No x.y.z vllm runtime version available (pre-release/build-metadata excluded pending #319); available: ${jsonencode(local.available_runtime_versions)}"
    }
  }
}
`, preferredZone, preferredInstance, name, name, modelName, modelBucket, modelPath)
}

func TestInferenceDeployment(t *testing.T) {
	t.Run("lifecycle", func(t *testing.T) {
		name := fmt.Sprintf("%sdeploy-%x", AcceptanceTestPrefix, rand.IntN(100000))
		fullResourceName := "coreweave_inference_deployment.test"
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
					Config: inferenceDeploymentConfig(name, preferredZone, preferredInstance),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("name"), knownvalue.StringExact(name)),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status"), knownvalue.StringExact("STATUS_READY")),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("disabled"), knownvalue.Bool(false)),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("organization_id"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("autoscaling").AtMapKey("min"), knownvalue.Int64Exact(1)),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("autoscaling").AtMapKey("max"), knownvalue.Int64Exact(2)),
					},
				},
				{
					Config: inferenceDeploymentUpdatedConfig(name, preferredZone, preferredInstance),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("autoscaling").AtMapKey("min"), knownvalue.Int64Exact(2)),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("autoscaling").AtMapKey("max"), knownvalue.Int64Exact(4)),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("disabled"), knownvalue.Bool(true)),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("traffic").AtMapKey("weight"), knownvalue.Int64Exact(50)),
					},
				},
				{
					ResourceName:      fullResourceName,
					ImportState:       true,
					ImportStateVerify: true,
				},
			},
		})
	})
}

func TestInferenceDeploymentParameters(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		resource.ParallelTest(t, resource.TestCase{
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: `data "coreweave_inference_deployment_parameters" "test" {}`,
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(
							"data.coreweave_inference_deployment_parameters.test",
							tfjsonpath.New("gateway_ids"),
							knownvalue.NotNull(),
						),
						statecheck.ExpectKnownValue(
							"data.coreweave_inference_deployment_parameters.test",
							tfjsonpath.New("instance_types"),
							knownvalue.NotNull(),
						),
					},
				},
			},
		})
	})
}
