package inference_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"buf.build/gen/go/coreweave/inference/connectrpc/go/coreweave/inference/v1alpha1/inferencev1alpha1connect"
	v1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/go-uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func hotLoadAcceptanceSettings() (map[string]string, error) {
	settings := make(map[string]string)
	for _, key := range []string{"COREWEAVE_API_ENDPOINT", "COREWEAVE_API_TOKEN", "INFR_ZONE", "INFR_INSTANCE_ID", "INFR_HOTLOAD_RUNTIME_VERSION", "INFR_HOTLOAD_BUCKET", "INFR_HOTLOAD_PREFIX", "INFR_HOTLOAD_INITIAL_IDENTITY", "INFR_HOTLOAD_FULL_IDENTITY", "INFR_HOTLOAD_DELTA_IDENTITY"} {
		settings[key] = os.Getenv(key)
		if settings[key] == "" {
			return nil, fmt.Errorf("HotLoad acceptance requires %s", key)
		}
	}
	if settings["COREWEAVE_API_ENDPOINT"] != "https://api.staging.coreweave.com" {
		return nil, fmt.Errorf("HotLoad acceptance requires the staging API endpoint")
	}
	initial := settings["INFR_HOTLOAD_INITIAL_IDENTITY"]
	full := settings["INFR_HOTLOAD_FULL_IDENTITY"]
	delta := settings["INFR_HOTLOAD_DELTA_IDENTITY"]
	if initial == full || initial == delta || full == delta {
		return nil, fmt.Errorf("HotLoad fixtures must have distinct identities")
	}
	return settings, nil
}

func hotLoadAcceptanceConfig(name string, s map[string]string, prefix string) string {
	return fmt.Sprintf(`
resource "coreweave_inference_gateway" "hotload" {
  name = %q
  zones = [%q]
  auth = { coreweave = {} }
  routing = { body_based = { api_type = "API_TYPE_OPENAI" } }
}
resource "coreweave_inference_deployment" "hotload" {
  name = %q
  gateway_ids = [coreweave_inference_gateway.hotload.id]
  runtime = {
    engine = "dynamo-vllm"
    version = %q
    engine_config = { "max-model-len" = "4096", "max-num-seqs" = "4", "gpu-memory-utilization" = "0.85" }
  }
  resources = { instance_type = %q, gpu_count = 1 }
  model = { name = %q, bucket = %q, path = %q }
  autoscaling = { min = 1, max = 1, concurrency = 4 }
  hot_load = { bucket = %q, path_prefix = %q, transition_mode = "TRANSITION_MODE_ASYNC" }
}
`, name+"-gw", s["INFR_ZONE"], name, s["INFR_HOTLOAD_RUNTIME_VERSION"], s["INFR_INSTANCE_ID"], name, s["INFR_HOTLOAD_BUCKET"], s["INFR_HOTLOAD_PREFIX"]+"/"+s["INFR_HOTLOAD_INITIAL_IDENTITY"], s["INFR_HOTLOAD_BUCKET"], prefix)
}

// TestInferenceHotLoadAcceptance is intentionally opt-in: it consumes one GPU
// and requires valid, isolated checkpoints published before the test starts.
func TestInferenceHotLoadAcceptance(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" || os.Getenv("INFR_HOTLOAD_ACCEPTANCE") != "1" {
		t.Skip("requires TF_ACC=1 and INFR_HOTLOAD_ACCEPTANCE=1 with isolated staging fixtures")
	}
	s, err := hotLoadAcceptanceSettings()
	if err != nil {
		t.Fatal(err)
	}
	unique, err := uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	name := AcceptanceTestPrefix + "hotload-" + unique[:8]
	const deployment = "coreweave_inference_deployment.hotload"
	const full = "coreweave_inference_hot_load.full"
	const delta = "coreweave_inference_hot_load.delta"
	base := hotLoadAcceptanceConfig(name, s, s["INFR_HOTLOAD_PREFIX"])
	fullConfig := base + fmt.Sprintf(`
resource "coreweave_inference_hot_load" "full" {
  deployment_id = coreweave_inference_deployment.hotload.id
  identity = %q
  type = "SNAPSHOT_TYPE_FULL"
}
`, s["INFR_HOTLOAD_FULL_IDENTITY"])
	deltaConfig := fullConfig + fmt.Sprintf(`
resource "coreweave_inference_hot_load" "delta" {
  deployment_id = coreweave_inference_deployment.hotload.id
  identity = %q
  type = "SNAPSHOT_TYPE_INCREMENTAL"
  incremental_snapshot_metadata = {
    previous_snapshot_identity = coreweave_inference_hot_load.full.identity
    compression_format = "COMPRESSION_FORMAT_ZSTD"
    checksum_format = "CHECKSUM_FORMAT_ADLER32"
  }
}
`, s["INFR_HOTLOAD_DELTA_IDENTITY"])
	client := inferencev1alpha1connect.NewHotLoadServiceClient(&http.Client{Timeout: time.Minute}, s["COREWEAVE_API_ENDPOINT"])
	deploymentClient := inferencev1alpha1connect.NewDeploymentServiceClient(&http.Client{Timeout: time.Minute}, s["COREWEAVE_API_ENDPOINT"])
	ids := make(map[string]string)
	completed := func(address string) resource.TestCheckFunc {
		return func(state *terraform.State) error {
			id := state.RootModule().Resources[address].Primary.ID
			ids[address] = id
			t.Logf("created %s: %s", address, id)
			return hotLoadAcceptancePoll(func(ctx context.Context) (bool, error) {
				req := connect.NewRequest(&v1.GetHotLoadRequest{Id: id})
				req.Header().Set("Authorization", "Bearer "+s["COREWEAVE_API_TOKEN"])
				resp, err := client.GetHotLoad(ctx, req)
				if err != nil {
					return false, err
				}
				status := resp.Msg.GetHotLoad().GetStatus().GetState()
				switch status {
				case v1.HotLoadState_HOT_LOAD_STATE_COMPLETED:
					return true, nil
				case v1.HotLoadState_HOT_LOAD_STATE_PENDING, v1.HotLoadState_HOT_LOAD_STATE_IN_PROGRESS:
					return false, nil
				case v1.HotLoadState_HOT_LOAD_STATE_UNSPECIFIED, v1.HotLoadState_HOT_LOAD_STATE_FAILED, v1.HotLoadState_HOT_LOAD_STATE_CANCELED:
					return false, fmt.Errorf("operation %s ended in %s", id, status)
				default:
					return false, fmt.Errorf("operation %s has unknown state %d", id, status)
				}
			})
		}
	}
	noop := func(address string) resource.ConfigPlanChecks {
		return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionNoop)}}
	}
	//nolint:forbidigo // Sequential execution limits staging GPU consumption.
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: base, Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(deployment, "hot_load.path_prefix", s["INFR_HOTLOAD_PREFIX"]),
				resource.TestCheckResourceAttr(deployment, "hot_load.bucket", s["INFR_HOTLOAD_BUCKET"]),
				resource.TestCheckResourceAttr(deployment, "hot_load.transition_mode", "TRANSITION_MODE_ASYNC"),
				func(state *terraform.State) error {
					id := state.RootModule().Resources[deployment].Primary.ID
					t.Logf("created %s: %s", deployment, id)
					return hotLoadAcceptancePoll(func(ctx context.Context) (bool, error) {
						req := connect.NewRequest(&v1.GetDeploymentRequest{Id: id})
						req.Header().Set("Authorization", "Bearer "+s["COREWEAVE_API_TOKEN"])
						resp, err := deploymentClient.GetDeployment(ctx, req)
						if err != nil {
							return false, err
						}
						return resp.Msg.GetDeployment().GetStatus().GetStatus().String() == "STATUS_READY", nil
					})
				},
			)},
			{ResourceName: deployment, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"status", "updated_at", "conditions"}},
			{Config: hotLoadAcceptanceConfig(name, s, s["INFR_HOTLOAD_PREFIX"]+"-replacement"), PlanOnly: true, ExpectNonEmptyPlan: true, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(deployment, plancheck.ResourceActionReplace)}}},
			{Config: base, ConfigPlanChecks: noop(deployment)},
			{Config: fullConfig, Check: completed(full)},
			{Config: fullConfig, ConfigPlanChecks: noop(full), Check: resource.TestCheckResourceAttr(full, "state", "HOT_LOAD_STATE_COMPLETED")},
			{ResourceName: full, ImportState: true, ImportStateVerify: true},
			{Config: strings.Replace(fullConfig, fmt.Sprintf("identity = %q", s["INFR_HOTLOAD_FULL_IDENTITY"]), fmt.Sprintf("identity = %q", s["INFR_HOTLOAD_FULL_IDENTITY"]+"-replacement"), 1), PlanOnly: true, ExpectNonEmptyPlan: true, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(full, plancheck.ResourceActionReplace)}}},
			{Config: deltaConfig, Check: completed(delta)},
			{Config: deltaConfig, ConfigPlanChecks: noop(delta), Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(delta, "state", "HOT_LOAD_STATE_COMPLETED"),
				resource.TestCheckResourceAttr(delta, "incremental_snapshot_metadata.previous_snapshot_identity", s["INFR_HOTLOAD_FULL_IDENTITY"]),
				resource.TestCheckResourceAttr(delta, "prompt_cache_policy", "PROMPT_CACHE_POLICY_PRESERVE"),
			)},
			{ResourceName: delta, ImportState: true, ImportStateVerify: true},
			{Config: base, Check: func(_ *terraform.State) error {
				for address, id := range ids {
					req := connect.NewRequest(&v1.GetHotLoadRequest{Id: id})
					req.Header().Set("Authorization", "Bearer "+s["COREWEAVE_API_TOKEN"])
					resp, err := client.GetHotLoad(t.Context(), req)
					if err != nil {
						return err
					}
					if resp.Msg.GetHotLoad().GetStatus().GetState() != v1.HotLoadState_HOT_LOAD_STATE_COMPLETED {
						return fmt.Errorf("destroy changed terminal history for %s", address)
					}
				}
				return nil
			}},
		},
	})
}

func hotLoadAcceptancePoll(check func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		done, err := check(ctx)
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for HotLoad acceptance readiness/completion: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestHotLoadAcceptanceSettings(t *testing.T) {
	keys := []string{"COREWEAVE_API_ENDPOINT", "COREWEAVE_API_TOKEN", "INFR_ZONE", "INFR_INSTANCE_ID", "INFR_HOTLOAD_RUNTIME_VERSION", "INFR_HOTLOAD_BUCKET", "INFR_HOTLOAD_PREFIX", "INFR_HOTLOAD_INITIAL_IDENTITY", "INFR_HOTLOAD_FULL_IDENTITY", "INFR_HOTLOAD_DELTA_IDENTITY"}
	for _, key := range keys {
		t.Setenv(key, key)
	}
	t.Setenv("COREWEAVE_API_ENDPOINT", "https://api.staging.coreweave.com")
	if _, err := hotLoadAcceptanceSettings(); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "")
			if _, err := hotLoadAcceptanceSettings(); err == nil {
				t.Fatalf("missing %s must fail before creating resources", key)
			}
		})
	}
	t.Run("production endpoint", func(t *testing.T) {
		t.Setenv("COREWEAVE_API_ENDPOINT", "https://api.coreweave.com")
		if _, err := hotLoadAcceptanceSettings(); err == nil {
			t.Fatal("production endpoint must be rejected")
		}
	})
	t.Run("duplicate identities", func(t *testing.T) {
		t.Setenv("INFR_HOTLOAD_FULL_IDENTITY", os.Getenv("INFR_HOTLOAD_INITIAL_IDENTITY"))
		if _, err := hotLoadAcceptanceSettings(); err == nil {
			t.Fatal("fixtures must have distinct identities")
		}
	})
}
