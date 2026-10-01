package inference_test

import (
	"testing"

	v1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestDeploymentHotLoadSchema(t *testing.T) {
	var resp resource.SchemaResponse
	inference.NewInferenceDeploymentResource().Schema(t.Context(), resource.SchemaRequest{}, &resp)
	h, ok := resp.Schema.Attributes["hot_load"].(schema.SingleNestedAttribute)
	if !ok || !h.Optional || len(h.PlanModifiers) == 0 {
		t.Fatalf("hot_load must be optional and require replacement: %#v", h)
	}
}

func TestDeploymentHotLoadReplacement(t *testing.T) {
	var schemaResp resource.SchemaResponse
	inference.NewInferenceDeploymentResource().Schema(t.Context(), resource.SchemaRequest{}, &schemaResp)
	h := schemaResp.Schema.Attributes["hot_load"].(schema.SingleNestedAttribute)
	objectTypes := map[string]attr.Type{"bucket": types.StringType, "path_prefix": types.StringType, "transition_mode": types.StringType}
	value := func(bucket string) types.Object {
		v, diags := types.ObjectValue(objectTypes, map[string]attr.Value{"bucket": types.StringValue(bucket), "path_prefix": types.StringNull(), "transition_mode": types.StringValue("TRANSITION_MODE_ASYNC")})
		if diags.HasError() {
			t.Fatal(diags)
		}
		return v
	}
	null, first, second := types.ObjectNull(objectTypes), value("first"), value("second")
	for name, tc := range map[string]struct {
		before, after types.Object
		replace       bool
	}{
		"add":       {before: null, after: first, replace: true},
		"remove":    {before: first, after: null, replace: true},
		"change":    {before: first, after: second, replace: true},
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
			if !m.HotLoad.PathPrefix.Equal(prefix) {
				t.Fatalf("prefix changed: %v", m.HotLoad.PathPrefix)
			}
			imported := &inference.InferenceDeploymentResourceModel{}
			inference.SetFromDeployment(imported, &v1.Deployment{Spec: &v1.DeploymentSpec{HotLoad: created.GetHotLoad()}}, false)
			if imported.HotLoad == nil || imported.HotLoad.Bucket.ValueString() != bucket {
				t.Fatal("import lost hot_load")
			}
			inference.SetFromDeployment(m, &v1.Deployment{Spec: &v1.DeploymentSpec{}}, false)
			if m.HotLoad != nil {
				t.Fatal("absent hot_load was not cleared")
			}
		})
	}
}
