package inference_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	v1connect "buf.build/gen/go/coreweave/inference/connectrpc/go/v2/coreweave/inference/v1alpha1/inferencev1alpha1connect"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func pdDeploymentConfig(pd string) string {
	return fmt.Sprintf(`
resource "coreweave_inference_deployment" "test" {
  name        = "pd-test"
  gateway_ids = ["22222222-2222-4222-8222-222222222222"]
  runtime     = { engine = "dynamo-vllm", version = "0.31.0" }
  resources   = { instance_type = "gb300-4x-e", gpu_count = 64 }
  model       = { name = "test-model", bucket = "checkpoints", path = "base" }
  autoscaling = { min = 1, max = 2, concurrency = 64 }
  %s
}`, pd)
}

func TestDeploymentPDLifecycle(t *testing.T) {
	mux := http.NewServeMux()
	connectServer := connect.NewServer()
	v1connect.RegisterDeploymentServiceHandler(connectServer, &hotLoadImportHandler{})
	connecthttp.Mount(mux, connectServer, connecthttp.WithReadMaxBytes(0))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("COREWEAVE_API_ENDPOINT", srv.URL)
	t.Setenv("COREWEAVE_API_TOKEN", "fake-token")

	const address = "coreweave_inference_deployment.test"
	withPD := pdDeploymentConfig(`pd = {
    prefill_resources   = { instance_type = "gb300-4x-e", gpu_count = 4 }
    prefill_autoscaling = { min = 1, max = 4, concurrency = 8 }
  }`)
	changedPD := pdDeploymentConfig(`pd = {
    prefill_resources   = { instance_type = "gb300-4x-e", gpu_count = 8 }
    prefill_autoscaling = { min = 2, max = 6 }
  }`)
	updateInPlace := testresource.ConfigPlanChecks{
		PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
	}

	//nolint:forbidigo // Local mock lifecycle is sequential and uses t.Setenv.
	testresource.Test(t, testresource.TestCase{
		IsUnitTest: true, ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []testresource.TestStep{
			{Config: withPD},
			{Config: withPD, ResourceName: address, ImportState: true, ImportStateVerify: true},
			{Config: changedPD, PlanOnly: true, ExpectNonEmptyPlan: true, ConfigPlanChecks: updateInPlace},
			{Config: pdDeploymentConfig(""), PlanOnly: true, ExpectNonEmptyPlan: true, ConfigPlanChecks: updateInPlace},
		},
	})
}
