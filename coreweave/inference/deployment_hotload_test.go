package inference_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v1connect "buf.build/gen/go/coreweave/inference/connectrpc/go/coreweave/inference/v1alpha1/inferencev1alpha1connect"
	v1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDeploymentHotLoadSchema(t *testing.T) {
	var resp resource.SchemaResponse
	inference.NewInferenceDeploymentResource().Schema(t.Context(), resource.SchemaRequest{}, &resp)
	h, ok := resp.Schema.Attributes["hot_load"].(schema.SingleNestedAttribute)
	if !ok || !h.Optional || len(h.PlanModifiers) == 0 {
		t.Fatalf("hot_load must be optional and require replacement: %#v", h)
	}
	prefix := h.Attributes["path_prefix"].(schema.StringAttribute)
	if !prefix.Optional || !prefix.Computed || prefix.Default == nil {
		t.Fatalf("path_prefix must default to an empty string: %#v", prefix)
	}
	var defaultResp defaults.StringResponse
	prefix.Default.DefaultString(t.Context(), defaults.StringRequest{}, &defaultResp)
	if defaultResp.Diagnostics.HasError() || !defaultResp.PlanValue.Equal(types.StringValue("")) {
		t.Fatalf("unexpected prefix default: %v, %v", defaultResp.PlanValue, defaultResp.Diagnostics)
	}
}

type hotLoadImportHandler struct {
	v1connect.UnimplementedDeploymentServiceHandler
	deployment *v1.Deployment
}

func (h *hotLoadImportHandler) CreateDeployment(_ context.Context, req *connect.Request[v1.CreateDeploymentRequest]) (*connect.Response[v1.CreateDeploymentResponse], error) {
	h.deployment = &v1.Deployment{
		Spec: &v1.DeploymentSpec{
			Id: "11111111-1111-4111-8111-111111111111", Name: req.Msg.Name,
			GatewayIds: req.Msg.GatewayIds, Runtime: req.Msg.Runtime,
			Resources: req.Msg.Resources, Model: req.Msg.Model, Autoscaling: req.Msg.Autoscaling,
			Traffic: req.Msg.Traffic, Disabled: req.Msg.Disabled, HotLoad: req.Msg.HotLoad,
		},
		Status: &v1.DeploymentStatus{
			Status:     v1.Status_STATUS_READY,
			CreatedAt:  timestamppb.New(time.Unix(1, 0)),
			UpdatedAt:  timestamppb.New(time.Unix(1, 0)),
			Conditions: []*v1.Condition{{Type: "ResourcesApplied", Status: v1.Condition_STATUS_TRUE}},
		},
	}
	return connect.NewResponse(&v1.CreateDeploymentResponse{Deployment: h.deployment}), nil
}

func (h *hotLoadImportHandler) GetDeployment(_ context.Context, _ *connect.Request[v1.GetDeploymentRequest]) (*connect.Response[v1.GetDeploymentResponse], error) {
	if h.deployment == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("deployment not found"))
	}
	return connect.NewResponse(&v1.GetDeploymentResponse{Deployment: h.deployment}), nil
}

func (h *hotLoadImportHandler) DeleteDeployment(_ context.Context, _ *connect.Request[v1.DeleteDeploymentRequest]) (*connect.Response[v1.DeleteDeploymentResponse], error) {
	h.deployment = nil
	return connect.NewResponse(&v1.DeleteDeploymentResponse{}), nil
}

func (h *hotLoadImportHandler) GetDeploymentParameters(_ context.Context, _ *connect.Request[v1.GetDeploymentParametersRequest]) (*connect.Response[v1.GetDeploymentParametersResponse], error) {
	return connect.NewResponse(&v1.GetDeploymentParametersResponse{}), nil
}

func TestDeploymentHotLoadImportNoop(t *testing.T) {
	for name, prefix := range map[string]string{"omitted": "", "explicit-empty": `path_prefix = ""`, "nonempty": `path_prefix = "runs"`} {
		t.Run(name, func(t *testing.T) {
			h := &hotLoadImportHandler{}
			mux := http.NewServeMux()
			pattern, handler := v1connect.NewDeploymentServiceHandler(h)
			mux.Handle(pattern, handler)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			t.Setenv("COREWEAVE_API_ENDPOINT", srv.URL)
			t.Setenv("COREWEAVE_API_TOKEN", "fake-token")
			config := fmt.Sprintf(`
resource "coreweave_inference_deployment" "test" {
  name = "hotload-import"
  gateway_ids = ["22222222-2222-4222-8222-222222222222"]
  runtime = { engine = "vllm", version = "0.29.0" }
  resources = { instance_type = "gd-8xh100ib-i128", gpu_count = 2 }
  model = { name = "test-model", bucket = "checkpoints", path = "base" }
  autoscaling = { min = 1, max = 1 }
  hot_load = {
    bucket = "checkpoints"
    transition_mode = "TRANSITION_MODE_ASYNC"
    %s
  }
}`, prefix)
			const address = "coreweave_inference_deployment.test"
			//nolint:forbidigo // Local mock lifecycle is sequential and uses t.Setenv.
			testresource.Test(t, testresource.TestCase{
				IsUnitTest: true, ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				Steps: []testresource.TestStep{
					{Config: config},
					// Forget the local state without deleting the mock deployment, then import it persistently.
					{Config: `removed {
  from = coreweave_inference_deployment.test
  lifecycle { destroy = false }
}`},
					{Config: config, ResourceName: address, ImportState: true,
						ImportStateId: "11111111-1111-4111-8111-111111111111", ImportStatePersist: true},
					{Config: config, PlanOnly: true, ConfigPlanChecks: testresource.ConfigPlanChecks{
						PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionNoop)},
					}},
				},
			})
		})
	}
}

func TestDeploymentHotLoadReplacement(t *testing.T) {
	var schemaResp resource.SchemaResponse
	inference.NewInferenceDeploymentResource().Schema(t.Context(), resource.SchemaRequest{}, &schemaResp)
	h := schemaResp.Schema.Attributes["hot_load"].(schema.SingleNestedAttribute)
	objectTypes := map[string]attr.Type{"bucket": types.StringType, "path_prefix": types.StringType, "transition_mode": types.StringType}
	value := func(bucket, prefix string) types.Object {
		v, diags := types.ObjectValue(objectTypes, map[string]attr.Value{"bucket": types.StringValue(bucket), "path_prefix": types.StringValue(prefix), "transition_mode": types.StringValue("TRANSITION_MODE_ASYNC")})
		if diags.HasError() {
			t.Fatal(diags)
		}
		return v
	}
	null, first, second := types.ObjectNull(objectTypes), value("first", ""), value("second", "")
	for name, tc := range map[string]struct {
		before, after types.Object
		replace       bool
	}{
		"add":       {before: null, after: first, replace: true},
		"remove":    {before: first, after: null, replace: true},
		"change":    {before: first, after: second, replace: true},
		"prefix":    {before: first, after: value("first", "runs"), replace: true},
		"unchanged": {before: first, after: first},
	} {
		t.Run(name, func(t *testing.T) {
			// Non-null root values distinguish an update from resource creation/destruction.
			raw, err := first.ToTerraformValue(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			req := planmodifier.ObjectRequest{State: tfsdk.State{Raw: raw}, Plan: tfsdk.Plan{Raw: raw}, StateValue: tc.before, PlanValue: tc.after}
			var resp planmodifier.ObjectResponse
			h.PlanModifiers[0].PlanModifyObject(t.Context(), req, &resp)
			if resp.Diagnostics.HasError() || resp.RequiresReplace != tc.replace {
				t.Fatalf("replace=%v diagnostics=%v", resp.RequiresReplace, resp.Diagnostics)
			}
		})
	}
}

func TestDeploymentHotLoadRoundTrip(t *testing.T) {
	const bucket = "checkpoints"
	for _, prefix := range []types.String{types.StringNull(), types.StringValue(""), types.StringValue("runs")} {
		t.Run(prefix.String(), func(t *testing.T) {
			m := &inference.InferenceDeploymentResourceModel{
				GatewayIds: types.SetNull(types.StringType), Runtime: &inference.RuntimeModel{},
				Resources: &inference.ResourcesModel{}, Model: &inference.DeploymentModelConfig{},
				Autoscaling: &inference.AutoscalingModel{},
				HotLoad: &inference.DeploymentHotLoadModel{
					Bucket: types.StringValue(bucket), PathPrefix: prefix,
					TransitionMode: types.StringValue("TRANSITION_MODE_ASYNC"),
				},
			}
			created, diags := inference.ToCreateRequest(t.Context(), m)
			if diags.HasError() {
				t.Fatal(diags)
			}
			updated, diags := inference.ToUpdateRequest(t.Context(), m)
			if diags.HasError() {
				t.Fatal(diags)
			}
			if created.GetHotLoad().GetBucket() != bucket || updated.GetHotLoad().GetBucket() != bucket {
				t.Fatal("create/update dropped hot_load")
			}
			if diags := inference.SetFromDeployment(m, &v1.Deployment{Spec: &v1.DeploymentSpec{HotLoad: created.GetHotLoad()}}, false); diags.HasError() {
				t.Fatal(diags)
			}
			expected := types.StringValue(prefix.ValueString())
			if !m.HotLoad.PathPrefix.Equal(expected) {
				t.Fatalf("prefix changed: %v", m.HotLoad.PathPrefix)
			}
			imported := &inference.InferenceDeploymentResourceModel{}
			inference.SetFromDeployment(imported, &v1.Deployment{Spec: &v1.DeploymentSpec{HotLoad: created.GetHotLoad()}}, false)
			if imported.HotLoad == nil || imported.HotLoad.Bucket.ValueString() != bucket {
				t.Fatal("import lost hot_load")
			}
			if !imported.HotLoad.PathPrefix.Equal(expected) {
				t.Fatalf("import changed prefix: %v", imported.HotLoad.PathPrefix)
			}
			inference.SetFromDeployment(m, &v1.Deployment{Spec: &v1.DeploymentSpec{}}, false)
			if m.HotLoad != nil {
				t.Fatal("absent hot_load was not cleared")
			}
		})
	}
}
