package sandbox

import (
	"context"
	"fmt"
	"reflect"

	sandboxv1beta2 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1beta2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// buildManagedRunnerUpdateRequest constructs an UpdateManagedRunnerRequest containing only the
// sub-trees that differ between plan and state. Field-mask paths track the dirty leaves —
// whole-message paths are used for `managed_spec.maintenance_policy`, `managed_spec.overrides`,
// and `profile_bindings` because those carry replace-set semantics on the server.
//
// Mutable paths per proto: "identity.zone", "identity.runner_group_id", "managed_spec",
// "managed_spec.release_channel", "managed_spec.maintenance_policy", "managed_spec.overrides",
// "profile_bindings". `display_name` and the `*_resource_limits` / privileged flags ride along
// under `managed_spec` when those are dirty.
func buildManagedRunnerUpdateRequest(ctx context.Context, plan, state *ManagedRunnerResourceModel) (*sandboxv1beta2.UpdateManagedRunnerRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	var paths []string

	runnerOut := &sandboxv1beta2.Runner{
		// The id field accepts either UUID or operator runner_id; we always
		// store the UUID in plan.ID after the first refresh.
		Id: plan.ID.ValueString(),
	}

	// --- Identity sub-tree ---
	idOut := &sandboxv1beta2.RunnerIdentity{}
	identityDirty := false
	if !plan.Zone.Equal(state.Zone) {
		idOut.Zone = plan.Zone.ValueString()
		paths = append(paths, "identity.zone")
		identityDirty = true
	}
	if !plan.RunnerGroupID.Equal(state.RunnerGroupID) {
		idOut.RunnerGroupId = plan.RunnerGroupID.ValueString()
		paths = append(paths, "identity.runner_group_id")
		identityDirty = true
	}
	if identityDirty {
		runnerOut.Identity = idOut
	}

	// --- ManagedSpec sub-tree ---
	specOut := &sandboxv1beta2.ManagedRunnerSpec{}
	specDirty := false

	// Skip release_channel when the plan value is unknown — Terraform will fill it
	// in from the prior state via UseStateForUnknown, so there's nothing to update.
	if !plan.ReleaseChannel.IsUnknown() && !plan.ReleaseChannel.Equal(state.ReleaseChannel) {
		v, ok := sandboxv1beta2.ReleaseChannel_value[plan.ReleaseChannel.ValueString()]
		if !ok {
			diags.AddAttributeError(
				path.Root("release_channel"),
				"Invalid release_channel",
				fmt.Sprintf("unknown release channel %q", plan.ReleaseChannel.ValueString()),
			)
		} else {
			specOut.ReleaseChannel = sandboxv1beta2.ReleaseChannel(v)
			paths = append(paths, "managed_spec.release_channel")
			specDirty = true
		}
	}

	if !maintenancePolicyEqual(plan.MaintenancePolicy, state.MaintenancePolicy) {
		mp, d := plan.MaintenancePolicy.toProto()
		diags.Append(d...)
		specOut.MaintenancePolicy = mp
		paths = append(paths, "managed_spec.maintenance_policy")
		specDirty = true
	}

	if !overridesEqual(plan.Overrides, state.Overrides) {
		ov, d := plan.Overrides.toProto(ctx)
		diags.Append(d...)
		specOut.Overrides = ov
		paths = append(paths, "managed_spec.overrides")
		specDirty = true
	}

	// allow_privileged_profile_annotations and enforce_resource_limits live under
	// managed_spec but the proto only documents leaf paths for release_channel /
	// maintenance_policy / overrides. Including the parent `managed_spec` path
	// when these flags change keeps the wire intent unambiguous.
	flagsDirty := false
	if !plan.AllowPrivilegedProfileAnnotations.Equal(state.AllowPrivilegedProfileAnnotations) {
		specOut.AllowPrivilegedProfileAnnotations = plan.AllowPrivilegedProfileAnnotations.ValueBool()
		flagsDirty = true
	}
	if !plan.EnforceResourceLimits.Equal(state.EnforceResourceLimits) {
		specOut.EnforceResourceLimits = plan.EnforceResourceLimits.ValueBool()
		flagsDirty = true
	}
	if flagsDirty {
		paths = append(paths, "managed_spec")
		specDirty = true
	}

	if specDirty {
		runnerOut.ManagedSpec = specOut
	}

	// --- DisplayName ---
	if !plan.DisplayName.Equal(state.DisplayName) {
		runnerOut.DisplayName = plan.DisplayName.ValueString()
		paths = append(paths, "display_name")
	}

	// --- Profile bindings ---
	if !bindingsEqual(plan.ProfileBindings, state.ProfileBindings) {
		runnerOut.ProfileBindings = plan.toBindings()
		paths = append(paths, "profile_bindings")
	}

	if diags.HasError() {
		return nil, diags
	}

	return &sandboxv1beta2.UpdateManagedRunnerRequest{
		Runner:     runnerOut,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
	}, diags
}

// maintenancePolicyEqual compares two policy models structurally. nil-safe.
// Timestamps are compared semantically (parsed) rather than byte-for-byte so a
// user writing "2026-01-01T00:00:00Z" doesn't appear to drift against a server
// response of "2026-01-01T00:00:00.000000000Z" or similar.
func maintenancePolicyEqual(a, b *MaintenancePolicyModel) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a.Windows) != len(b.Windows) || len(a.Exclusions) != len(b.Exclusions) {
		return false
	}
	for i := range a.Windows {
		if !a.Windows[i].Cron.Equal(b.Windows[i].Cron) || !a.Windows[i].DurationSeconds.Equal(b.Windows[i].DurationSeconds) {
			return false
		}
	}
	for i := range a.Exclusions {
		if !timestampStringsEqual(a.Exclusions[i].StartTime, b.Exclusions[i].StartTime) ||
			!timestampStringsEqual(a.Exclusions[i].EndTime, b.Exclusions[i].EndTime) ||
			!a.Exclusions[i].Reason.Equal(b.Exclusions[i].Reason) {
			return false
		}
	}
	return true
}

// overridesEqual compares two override models field-by-field using each field's
// Equal method. The model is small enough that explicit comparison is cheaper
// to reason about than reflection.
func overridesEqual(a, b *RunnerOverridesModel) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if !a.NodeSelector.Equal(b.NodeSelector) ||
		!a.Annotations.Equal(b.Annotations) ||
		!a.Labels.Equal(b.Labels) ||
		!a.Env.Equal(b.Env) ||
		!a.Args.Equal(b.Args) ||
		!a.CPURuntimeClass.Equal(b.CPURuntimeClass) ||
		!a.GPURuntimeClass.Equal(b.GPURuntimeClass) {
		return false
	}
	if !runnerResourcesEqual(a.Resources, b.Resources) {
		return false
	}
	if !runnerScalingEqual(a.Scaling, b.Scaling) {
		return false
	}
	if len(a.Tolerations) != len(b.Tolerations) {
		return false
	}
	for i := range a.Tolerations {
		if !a.Tolerations[i].Key.Equal(b.Tolerations[i].Key) ||
			!a.Tolerations[i].Operator.Equal(b.Tolerations[i].Operator) ||
			!a.Tolerations[i].Value.Equal(b.Tolerations[i].Value) ||
			!a.Tolerations[i].Effect.Equal(b.Tolerations[i].Effect) {
			return false
		}
	}
	return true
}

func runnerResourcesEqual(a, b *RunnerResourceRequirementsModel) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.CPURequest.Equal(b.CPURequest) &&
		a.MemoryRequest.Equal(b.MemoryRequest) &&
		a.CPULimit.Equal(b.CPULimit) &&
		a.MemoryLimit.Equal(b.MemoryLimit)
}

func runnerScalingEqual(a, b *RunnerScalingConfigModel) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Replicas.Equal(b.Replicas) &&
		a.AutoscalingEnabled.Equal(b.AutoscalingEnabled) &&
		a.MinReplicas.Equal(b.MinReplicas) &&
		a.MaxReplicas.Equal(b.MaxReplicas)
}

// bindingsEqual compares two profile-binding lists order-independently, keyed
// by profile_template_id. Replicates the server's matching semantics so a
// reorder in plan doesn't appear as a diff.
//
// Duplicate profile_template_ids are caught earlier by validateProfileBindings;
// if validation is ever bypassed, we treat duplicates as a diff rather than
// silently merging them via map overwrite.
func bindingsEqual(a, b []ProfileBindingModel) bool {
	if len(a) != len(b) {
		return false
	}
	bByKey := make(map[string]ProfileBindingModel, len(b))
	for _, x := range b {
		key := x.ProfileTemplateID.ValueString()
		if _, dup := bByKey[key]; dup {
			return false
		}
		bByKey[key] = x
	}
	for _, x := range a {
		other, ok := bByKey[x.ProfileTemplateID.ValueString()]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(x, other) {
			return false
		}
	}
	return true
}
