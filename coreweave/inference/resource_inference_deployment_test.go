package inference_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// --- Unit tests for pure helper functions ---

func TestInferenceDeploymentResource_Schema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schemaReq := fwresource.SchemaRequest{}
	schemaResp := &fwresource.SchemaResponse{}

	inference.NewInferenceDeploymentResource().Schema(ctx, schemaReq, schemaResp)

	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema returned errors: %v", schemaResp.Diagnostics)
	}
}

func TestSetFromDeployment_NullPreservation(t *testing.T) {
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
			Status: inferencev1.DeploymentStatus_STATUS_RUNNING,
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
	if !m.Traffic.Weight.IsNull() {
		t.Errorf("Traffic.Weight: expected null when API returns 0, got %v", m.Traffic.Weight)
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

func TestToCreateRequest_OptionalFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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

func TestToUpdateRequest_Fields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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

func inferenceDeploymentConfig(name string) string {
	return fmt.Sprintf(`
data "coreweave_inference_gateway_parameters" "gw_params" {}
data "coreweave_inference_parameters" "params" {}

resource "coreweave_inference_gateway" "test" {
  name  = "%s-gw"
  zones = [data.coreweave_inference_gateway_parameters.gw_params.zones[0]]

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
  name        = %q
  gateway_ids = [coreweave_inference_gateway.test.id]

  runtime = {
    engine = "vllm"
  }

  resources = {
    instance_type = data.coreweave_inference_parameters.params.instance_types[0]
    gpu_count     = 1
  }

  model = {
    name   = "meta-llama/Llama-3.1-8B"
    bucket = "test-model-bucket"
    path   = "models/llama-3.1-8b"
  }

  autoscaling = {
    min = 1
    max = 2
  }

  traffic = {}
}
`, name, name)
}

func inferenceDeploymentUpdatedConfig(name string) string {
	return fmt.Sprintf(`
data "coreweave_inference_gateway_parameters" "gw_params" {}
data "coreweave_inference_parameters" "params" {}

resource "coreweave_inference_gateway" "test" {
  name  = "%s-gw"
  zones = [data.coreweave_inference_gateway_parameters.gw_params.zones[0]]

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
  name        = %q
  gateway_ids = [coreweave_inference_gateway.test.id]
  disabled    = true

  runtime = {
    engine = "vllm"
  }

  resources = {
    instance_type = data.coreweave_inference_parameters.params.instance_types[0]
    gpu_count     = 1
  }

  model = {
    name   = "meta-llama/Llama-3.1-8B"
    bucket = "test-model-bucket"
    path   = "models/llama-3.1-8b"
  }

  autoscaling = {
    min = 2
    max = 4
  }

  traffic = {
    weight = 50
  }
}
`, name, name)
}

func TestAccInferenceDeployment(t *testing.T) {
	name := fmt.Sprintf("%sdeploy-%x", AcceptanceTestPrefix, rand.IntN(100000))
	fullResourceName := "coreweave_inference_deployment.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: inferenceDeploymentConfig(name),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("name"), knownvalue.StringExact(name)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status"), knownvalue.StringExact("STATUS_RUNNING")),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("disabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("organization_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("autoscaling").AtMapKey("min"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("autoscaling").AtMapKey("max"), knownvalue.Int64Exact(2)),
				},
			},
			{
				Config: inferenceDeploymentUpdatedConfig(name),
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
}

func TestAccInferenceParameters(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "coreweave_inference_parameters" "test" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						"data.coreweave_inference_parameters.test",
						tfjsonpath.New("gateway_ids"),
						knownvalue.NotNull(),
					),
					statecheck.ExpectKnownValue(
						"data.coreweave_inference_parameters.test",
						tfjsonpath.New("instance_types"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}
