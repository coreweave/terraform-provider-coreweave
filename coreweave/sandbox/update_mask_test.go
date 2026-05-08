package sandbox

import (
	"context"
	"sort"
	"testing"

	sandboxv1beta2 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1beta2"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func expectMaskPaths(t *testing.T, got []string, want ...string) {
	t.Helper()
	gotCopy := append([]string(nil), got...)
	wantCopy := append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	if len(gotCopy) != len(wantCopy) {
		t.Fatalf("expected mask paths %v, got %v", wantCopy, gotCopy)
	}
	for i := range gotCopy {
		if gotCopy[i] != wantCopy[i] {
			t.Fatalf("expected mask paths %v, got %v", wantCopy, gotCopy)
		}
	}
}

func baseRunnerModel() ManagedRunnerResourceModel {
	return ManagedRunnerResourceModel{
		ID:                                types.StringValue("runner-uuid"),
		RunnerID:                          types.StringValue("prod-east-managed"),
		Zone:                              types.StringValue("US-EAST-04A"),
		ClusterID:                         types.StringValue("cluster-uuid"),
		ClusterName:                       types.StringNull(),
		RunnerGroupID:                     types.StringNull(),
		DisplayName:                       types.StringNull(),
		ReleaseChannel:                    types.StringValue(sandboxv1beta2.ReleaseChannel_RELEASE_CHANNEL_STABLE.String()),
		AllowPrivilegedProfileAnnotations: types.BoolValue(false),
		EnforceResourceLimits:             types.BoolValue(false),
		ProfileBindings: []ProfileBindingModel{
			{
				ProfileTemplateID: types.StringValue("pt-1"),
				ProfileName:       types.StringValue("default"),
				IsDefault:         types.BoolValue(true),
			},
		},
	}
}

func TestBuildManagedRunnerUpdateRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("no changes produces empty mask", func(t *testing.T) {
		state := baseRunnerModel()
		plan := baseRunnerModel()
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths())
		if req.GetRunner().GetIdentity() != nil {
			t.Errorf("expected nil identity on no-op, got %+v", req.GetRunner().GetIdentity())
		}
		if req.GetRunner().GetManagedSpec() != nil {
			t.Errorf("expected nil managed_spec on no-op")
		}
		if req.GetRunner().GetProfileBindings() != nil {
			t.Errorf("expected nil profile_bindings on no-op")
		}
	})

	t.Run("zone change emits identity.zone only", func(t *testing.T) {
		state := baseRunnerModel()
		plan := baseRunnerModel()
		plan.Zone = types.StringValue("US-WEST-01A")
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "identity.zone")
		if req.GetRunner().GetIdentity().GetZone() != "US-WEST-01A" {
			t.Errorf("expected zone US-WEST-01A, got %q", req.GetRunner().GetIdentity().GetZone())
		}
		if req.GetRunner().GetManagedSpec() != nil {
			t.Errorf("expected nil managed_spec when only zone changed")
		}
	})

	t.Run("clearing runner_group_id emits the path even though value is empty", func(t *testing.T) {
		state := baseRunnerModel()
		state.RunnerGroupID = types.StringValue("group-1")
		plan := baseRunnerModel() // RunnerGroupID is null in base
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "identity.runner_group_id")
		if req.GetRunner().GetIdentity().GetRunnerGroupId() != "" {
			t.Errorf("expected cleared runner_group_id, got %q", req.GetRunner().GetIdentity().GetRunnerGroupId())
		}
	})

	t.Run("release_channel change emits leaf path", func(t *testing.T) {
		state := baseRunnerModel()
		plan := baseRunnerModel()
		plan.ReleaseChannel = types.StringValue(sandboxv1beta2.ReleaseChannel_RELEASE_CHANNEL_RAPID.String())
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "managed_spec.release_channel")
		if req.GetRunner().GetManagedSpec().GetReleaseChannel() != sandboxv1beta2.ReleaseChannel_RELEASE_CHANNEL_RAPID {
			t.Errorf("expected RAPID channel on the wire")
		}
	})

	t.Run("flag change emits parent managed_spec path", func(t *testing.T) {
		state := baseRunnerModel()
		plan := baseRunnerModel()
		plan.EnforceResourceLimits = types.BoolValue(true)
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "managed_spec")
		if !req.GetRunner().GetManagedSpec().GetEnforceResourceLimits() {
			t.Errorf("expected enforce_resource_limits=true")
		}
	})

	t.Run("maintenance_policy change emits whole-policy path", func(t *testing.T) {
		state := baseRunnerModel()
		plan := baseRunnerModel()
		plan.MaintenancePolicy = &MaintenancePolicyModel{
			Windows: []MaintenanceWindowModel{
				{Cron: types.StringValue("0 2 * * SAT"), DurationSeconds: types.Int32Value(7200)},
			},
		}
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "managed_spec.maintenance_policy")
		if got := req.GetRunner().GetManagedSpec().GetMaintenancePolicy(); got == nil || len(got.GetWindows()) != 1 {
			t.Errorf("expected one maintenance window on the wire, got %+v", got)
		}
	})

	t.Run("overrides change emits whole-overrides path", func(t *testing.T) {
		state := baseRunnerModel()
		plan := baseRunnerModel()
		plan.Overrides = &RunnerOverridesModel{
			NodeSelector: types.MapValueMust(types.StringType, map[string]attr.Value{
				"workload-class": types.StringValue("general"),
			}),
		}
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "managed_spec.overrides")
		if got := req.GetRunner().GetManagedSpec().GetOverrides(); got == nil || got.GetNodeSelector()["workload-class"] != "general" {
			t.Errorf("expected overrides.node_selector on the wire, got %+v", got)
		}
	})

	t.Run("profile_bindings replace emits the path", func(t *testing.T) {
		state := baseRunnerModel()
		plan := baseRunnerModel()
		plan.ProfileBindings = []ProfileBindingModel{
			{
				ProfileTemplateID: types.StringValue("pt-1"),
				ProfileName:       types.StringValue("default"),
				IsDefault:         types.BoolValue(true),
			},
			{
				ProfileTemplateID: types.StringValue("pt-2"),
				ProfileName:       types.StringValue("gpu"),
				IsDefault:         types.BoolValue(false),
			},
		}
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "profile_bindings")
		if got := req.GetRunner().GetProfileBindings(); len(got) != 2 {
			t.Errorf("expected 2 bindings, got %d", len(got))
		}
	})

	t.Run("default-binding swap is detected", func(t *testing.T) {
		state := baseRunnerModel()
		state.ProfileBindings = []ProfileBindingModel{
			{ProfileTemplateID: types.StringValue("pt-1"), IsDefault: types.BoolValue(true)},
			{ProfileTemplateID: types.StringValue("pt-2"), IsDefault: types.BoolValue(false)},
		}
		plan := baseRunnerModel()
		plan.ProfileBindings = []ProfileBindingModel{
			{ProfileTemplateID: types.StringValue("pt-1"), IsDefault: types.BoolValue(false)},
			{ProfileTemplateID: types.StringValue("pt-2"), IsDefault: types.BoolValue(true)},
		}
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "profile_bindings")
	})

	t.Run("profile_bindings reorder is not a diff", func(t *testing.T) {
		state := baseRunnerModel()
		state.ProfileBindings = []ProfileBindingModel{
			{ProfileTemplateID: types.StringValue("pt-1"), IsDefault: types.BoolValue(true)},
			{ProfileTemplateID: types.StringValue("pt-2"), IsDefault: types.BoolValue(false)},
		}
		plan := baseRunnerModel()
		plan.ProfileBindings = []ProfileBindingModel{
			{ProfileTemplateID: types.StringValue("pt-2"), IsDefault: types.BoolValue(false)},
			{ProfileTemplateID: types.StringValue("pt-1"), IsDefault: types.BoolValue(true)},
		}
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths())
	})

	t.Run("multiple unrelated changes emit all paths", func(t *testing.T) {
		state := baseRunnerModel()
		plan := baseRunnerModel()
		plan.Zone = types.StringValue("US-WEST-01A")
		plan.ReleaseChannel = types.StringValue(sandboxv1beta2.ReleaseChannel_RELEASE_CHANNEL_RAPID.String())
		plan.DisplayName = types.StringValue("Renamed")
		req, diags := buildManagedRunnerUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(),
			"identity.zone",
			"managed_spec.release_channel",
			"display_name",
		)
	})
}

func baseProfileTemplateModel() ProfileTemplateResourceModel {
	return ProfileTemplateResourceModel{
		ID:          types.StringValue("pt-uuid"),
		DisplayName: types.StringValue("default-cpu"),
		Description: types.StringValue("default"),
		Spec: &ProfileSpecModel{
			ContainerImage: types.StringValue("ghcr.io/coreweave/sandbox-runtime:v1"),
			RuntimeClass:   types.StringValue("kata-qemu"),
			InstanceTypes:  types.ListNull(types.StringType),
			NodeSelector:   types.MapNull(types.StringType),
			Tags:           types.ListNull(types.StringType),
		},
		Labels: types.MapNull(types.StringType),
	}
}

func TestBuildProfileTemplateUpdateRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("no change produces empty mask", func(t *testing.T) {
		state := baseProfileTemplateModel()
		plan := baseProfileTemplateModel()
		req, diags := buildProfileTemplateUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths())
	})

	t.Run("description change", func(t *testing.T) {
		state := baseProfileTemplateModel()
		plan := baseProfileTemplateModel()
		plan.Description = types.StringValue("updated")
		req, diags := buildProfileTemplateUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "description")
		if req.GetProfileTemplate().GetDescription() != "updated" {
			t.Errorf("expected description=updated, got %q", req.GetProfileTemplate().GetDescription())
		}
	})

	t.Run("spec change", func(t *testing.T) {
		state := baseProfileTemplateModel()
		plan := baseProfileTemplateModel()
		plan.Spec.ContainerImage = types.StringValue("ghcr.io/coreweave/sandbox-runtime:v2")
		req, diags := buildProfileTemplateUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "spec")
	})

	t.Run("labels change", func(t *testing.T) {
		state := baseProfileTemplateModel()
		plan := baseProfileTemplateModel()
		plan.Labels = types.MapValueMust(types.StringType, map[string]attr.Value{
			"team": types.StringValue("platform"),
		})
		req, diags := buildProfileTemplateUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "labels")
	})

	t.Run("multiple field change", func(t *testing.T) {
		state := baseProfileTemplateModel()
		plan := baseProfileTemplateModel()
		plan.Description = types.StringValue("new")
		plan.Spec.ContainerImage = types.StringValue("new-image")
		plan.Labels = types.MapValueMust(types.StringType, map[string]attr.Value{"k": types.StringValue("v")})
		req, diags := buildProfileTemplateUpdateRequest(ctx, &plan, &state)
		if diags.HasError() {
			t.Fatalf("unexpected diags: %v", diags)
		}
		expectMaskPaths(t, req.GetUpdateMask().GetPaths(), "description", "spec", "labels")
	})
}
