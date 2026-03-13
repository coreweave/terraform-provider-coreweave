package inference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
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

func TestCapacityClassRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input    string
		proto    inferencev1.DeploymentAutoscaling_CapacityClass
		expected string
	}{
		{"RESERVED", inferencev1.DeploymentAutoscaling_CAPACITY_CLASS_RESERVED, "RESERVED"},
		{"ON_DEMAND", inferencev1.DeploymentAutoscaling_CAPACITY_CLASS_ON_DEMAND, "ON_DEMAND"},
		{"unknown", inferencev1.DeploymentAutoscaling_CAPACITY_CLASS_UNSPECIFIED, ""},
	}

	for _, tc := range cases {
		got := inference.CapacityClassFromString(tc.input)
		if got != tc.proto {
			t.Errorf("CapacityClassFromString(%q): got %v, want %v", tc.input, got, tc.proto)
		}
		str := inference.CapacityClassToString(tc.proto)
		if str != tc.expected {
			t.Errorf("CapacityClassToString(%v): got %q, want %q", tc.proto, str, tc.expected)
		}
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
		Runtime: inference.RuntimeModel{
			Version:      types.StringNull(),
			EngineConfig: types.MapNull(types.StringType),
		},
		Autoscaling: inference.AutoscalingModel{
			Priority:        types.Int64Null(),
			CapacityClasses: types.ListNull(types.StringType),
			Concurrency:     types.Int64Null(),
		},
		Traffic: inference.TrafficModel{
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
	gwIds, _ := types.ListValueFrom(ctx, types.StringType, []string{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"})

	m := &inference.InferenceDeploymentResourceModel{
		Name:       types.StringValue("my-llm"),
		GatewayIds: gwIds,
		Disabled:   types.BoolValue(false),
		Runtime: inference.RuntimeModel{
			Engine:       types.StringValue("vllm"),
			Version:      types.StringNull(),
			EngineConfig: types.MapNull(types.StringType),
		},
		Resources: inference.ResourcesModel{
			InstanceType: types.StringValue("H100_80GB_SXM5"),
			GpuCount:     types.Int64Value(1),
		},
		Model: inference.DeploymentModelConfig{
			Name:   types.StringValue("meta-llama/Llama-3.1-8B"),
			Bucket: types.StringValue("my-bucket"),
			Path:   types.StringValue("models/llama"),
		},
		Autoscaling: inference.AutoscalingModel{
			Min:             types.Int64Value(1),
			Max:             types.Int64Value(4),
			Priority:        types.Int64Null(),
			CapacityClasses: types.ListNull(types.StringType),
			Concurrency:     types.Int64Null(),
		},
		Traffic: inference.TrafficModel{
			Weight: types.Int64Null(),
		},
	}

	req := inference.ToCreateRequest(ctx, m)

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

// --- Acceptance tests ---

const accTestPrefix = "test-acc-inference-"

func init() {
	resource.AddTestSweepers("coreweave_inference_deployment", &resource.Sweeper{
		Name: "coreweave_inference_deployment",
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.ListDeployments(ctx, connect.NewRequest(&inferencev1.ListDeploymentsRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list deployments: %w", err)
			}

			for _, d := range listResp.Msg.GetItems() {
				name := d.GetSpec().GetName()
				if !strings.HasPrefix(name, accTestPrefix) {
					continue
				}
				if testutil.SweepDryRun() {
					fmt.Printf("[DRY RUN] would delete inference deployment: %s\n", name)
					continue
				}

				id := d.GetSpec().GetId()
				fmt.Printf("sweeping inference deployment: %s (%s)\n", name, id)

				_, err := client.DeleteDeployment(ctx, connect.NewRequest(&inferencev1.DeleteDeploymentRequest{Id: id}))
				if err != nil {
					return fmt.Errorf("failed to delete deployment %s: %w", name, err)
				}

				if err := testutil.WaitForDelete(ctx, 20*time.Minute, 10*time.Second,
					client.GetDeployment,
					&inferencev1.GetDeploymentRequest{Id: id},
				); err != nil {
					return fmt.Errorf("timed out waiting for deployment %s to be deleted: %w", name, err)
				}
			}

			return nil
		},
	})
}

func inferenceDeploymentConfig(name, gatewayID string) string {
	return fmt.Sprintf(`
resource "coreweave_inference_deployment" "test" {
  name        = %q
  gateway_ids = [%q]

  runtime = {
    engine = "vllm"
  }

  resources = {
    instance_type = "H100_80GB_SXM5"
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
`, name, gatewayID)
}

func inferenceDeploymentUpdatedConfig(name, gatewayID string) string {
	return fmt.Sprintf(`
resource "coreweave_inference_deployment" "test" {
  name        = %q
  gateway_ids = [%q]
  disabled    = true

  runtime = {
    engine = "vllm"
  }

  resources = {
    instance_type = "H100_80GB_SXM5"
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
`, name, gatewayID)
}

func getTestClient(t *testing.T) *coreweave.Client {
	t.Helper()
	cfg := provider.CoreweaveProviderModel{}
	client, err := provider.BuildClient(context.Background(), cfg, "test", "test")
	if err != nil {
		t.Skipf("skipping: could not build client: %v", err)
	}
	return client
}

func getTestGatewayID(t *testing.T) string {
	t.Helper()
	client := getTestClient(t)

	resp, err := client.GetDeploymentParameters(context.Background(), connect.NewRequest(&inferencev1.GetDeploymentParametersRequest{}))
	if err != nil {
		t.Skipf("skipping: could not get deployment parameters: %v", err)
	}
	if len(resp.Msg.GetGatewayIds()) == 0 {
		t.Skip("skipping: no gateway IDs available")
	}
	return resp.Msg.GetGatewayIds()[0]
}

func deleteInferenceDeployment(ctx context.Context, client *coreweave.Client, id string) error {
	_, err := client.DeleteDeployment(ctx, connect.NewRequest(&inferencev1.DeleteDeploymentRequest{Id: id}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil
		}
		return fmt.Errorf("failed to delete deployment %s: %w", id, err)
	}

	return testutil.WaitForDelete(ctx, 20*time.Minute, 10*time.Second,
		client.GetDeployment,
		&inferencev1.GetDeploymentRequest{Id: id},
	)
}

func TestAcc_InferenceDeployment_basic(t *testing.T) {
	gatewayID := getTestGatewayID(t)
	name := accTestPrefix + "basic"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: inferenceDeploymentConfig(name, gatewayID),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("name"), knownvalue.StringExact(name)),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("status"), knownvalue.StringExact("STATUS_RUNNING")),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("disabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("organization_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("created_at"), knownvalue.NotNull()),
				},
			},
		},
	})
}

func TestAcc_InferenceDeployment_update(t *testing.T) {
	gatewayID := getTestGatewayID(t)
	name := accTestPrefix + "update"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: inferenceDeploymentConfig(name, gatewayID),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("autoscaling").AtMapKey("min"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("autoscaling").AtMapKey("max"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("disabled"), knownvalue.Bool(false)),
				},
			},
			{
				Config: inferenceDeploymentUpdatedConfig(name, gatewayID),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("autoscaling").AtMapKey("min"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("autoscaling").AtMapKey("max"), knownvalue.Int64Exact(4)),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("disabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("coreweave_inference_deployment.test", tfjsonpath.New("traffic").AtMapKey("weight"), knownvalue.Int64Exact(50)),
				},
			},
		},
	})
}

func TestAcc_InferenceDeployment_import(t *testing.T) {
	gatewayID := getTestGatewayID(t)
	name := accTestPrefix + "import"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: inferenceDeploymentConfig(name, gatewayID),
			},
			{
				ResourceName:      "coreweave_inference_deployment.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAcc_InferenceDeployment_disappeared(t *testing.T) {
	gatewayID := getTestGatewayID(t)
	name := accTestPrefix + "disappeared"
	client := getTestClient(t)

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: inferenceDeploymentConfig(name, gatewayID),
				Check:  resource.TestCheckResourceAttrSet("coreweave_inference_deployment.test", "id"),
			},
			{
				// Find the deployment by name and delete it out-of-band, then verify plan shows recreation.
				PreConfig: func() {
					ctx := context.Background()
					listResp, err := client.ListDeployments(ctx, connect.NewRequest(&inferencev1.ListDeploymentsRequest{}))
					if err != nil {
						t.Fatalf("failed to list deployments: %v", err)
					}
					for _, d := range listResp.Msg.GetItems() {
						if d.GetSpec().GetName() == name {
							if delErr := deleteInferenceDeployment(ctx, client, d.GetSpec().GetId()); delErr != nil {
								t.Fatalf("failed to delete deployment: %v", delErr)
							}
							return
						}
					}
					t.Fatalf("deployment %q not found in list", name)
				},
				Config:             inferenceDeploymentConfig(name, gatewayID),
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAcc_InferenceParameters_basic(t *testing.T) {
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
