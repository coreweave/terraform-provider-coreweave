package cks

import (
	"context"
	"encoding/json"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestNodePoolSchema(t *testing.T) {
	t.Parallel()

	response := &frameworkresource.SchemaResponse{}
	NewNodePoolResource().Schema(context.Background(), frameworkresource.SchemaRequest{}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("Schema diagnostics: %+v", response.Diagnostics)
	}
	if diagnostics := response.Schema.ValidateImplementation(context.Background()); diagnostics.HasError() {
		t.Fatalf("Schema validation diagnostics: %+v", diagnostics)
	}
}

func TestNodePoolManifestFieldParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	labels, diagnostics := types.MapValueFrom(ctx, types.StringType, map[string]string{"team": "platform"})
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	annotations, diagnostics := types.MapValueFrom(ctx, types.StringType, map[string]string{"example.com/owner": "terraform"})
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	model := NodePoolResourceModel{
		Name:           types.StringValue("gpu-workers"),
		InstanceType:   types.StringValue("gd-8xh100ib-i128"),
		TargetNodes:    types.Int64Value(3),
		ComputeClass:   types.StringValue("spot"),
		Autoscaling:    types.BoolValue(true),
		MinNodes:       types.Int64Value(1),
		MaxNodes:       types.Int64Value(5),
		Labels:         labels,
		Annotations:    annotations,
		UpdateStrategy: types.StringValue("Always"),
		Taints: []NodePoolTaintResourceModel{{
			Key: types.StringValue("nvidia.com/gpu"), Value: types.StringValue("true"), Effect: types.StringValue("NoSchedule"),
		}},
	}

	manifest, err := model.manifest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.APIVersion != "compute.coreweave.com/v1alpha1" || manifest.Kind != "NodePool" || manifest.Metadata.Name != "gpu-workers" {
		t.Fatalf("unexpected manifest metadata: %#v", manifest)
	}
	if manifest.Spec.InstanceType != "gd-8xh100ib-i128" || manifest.Spec.TargetNodes != 3 || manifest.Spec.ComputeClass != "spot" || !manifest.Spec.Autoscaling {
		t.Fatalf("unexpected core spec: %#v", manifest.Spec)
	}
	if manifest.Spec.MinNodes == nil || *manifest.Spec.MinNodes != 1 || manifest.Spec.MaxNodes == nil || *manifest.Spec.MaxNodes != 5 {
		t.Fatalf("unexpected autoscaling bounds: %#v", manifest.Spec)
	}
	if manifest.Spec.NodeLabels["team"] != "platform" || manifest.Spec.NodeAnnotations["example.com/owner"] != "terraform" {
		t.Fatalf("unexpected metadata fields: %#v", manifest.Spec)
	}
	if len(manifest.Spec.NodeTaints) != 1 || manifest.Spec.NodeTaints[0].Effect != "NoSchedule" {
		t.Fatalf("unexpected taints: %#v", manifest.Spec.NodeTaints)
	}
	if manifest.Spec.NodeConfigurationUpdateStrategy == nil || manifest.Spec.NodeConfigurationUpdateStrategy.Type != "Always" {
		t.Fatalf("unexpected update strategy: %#v", manifest.Spec.NodeConfigurationUpdateStrategy)
	}
}

func TestNodePoolPatchClearsOptionalFields(t *testing.T) {
	t.Parallel()

	model := NodePoolResourceModel{
		Name:           types.StringValue("gpu-workers"),
		InstanceType:   types.StringValue("gd-8xh100ib-i128"),
		TargetNodes:    types.Int64Value(2),
		ComputeClass:   types.StringValue("default"),
		Autoscaling:    types.BoolValue(false),
		MinNodes:       types.Int64Null(),
		MaxNodes:       types.Int64Null(),
		Labels:         types.MapNull(types.StringType),
		Annotations:    types.MapNull(types.StringType),
		Taints:         nil,
		UpdateStrategy: types.StringValue("OnSpecUpdate"),
	}

	patch, err := model.patchSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"minNodes", "maxNodes", "nodeLabels", "nodeAnnotations", "nodeTaints"} {
		if value, ok := patch[field]; !ok || value != nil {
			t.Fatalf("%s = %#v, want explicit null", field, value)
		}
	}
	for _, field := range []string{"instanceType", "computeClass"} {
		if _, ok := patch[field]; ok {
			t.Fatalf("immutable %s must not be patched", field)
		}
	}
}

func TestNodePoolSetFromJSON(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(nodePoolManifest{
		Metadata: nodePoolMetadata{Name: "gpu-workers"},
		Spec: nodePoolSpec{
			InstanceType:    "gd-8xh100ib-i128",
			TargetNodes:     4,
			Autoscaling:     true,
			NodeLabels:      map[string]string{"team": "platform"},
			NodeAnnotations: map[string]string{},
			NodeTaints:      []nodePoolTaint{{Key: "dedicated", Value: "gpu", Effect: "NoExecute"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := NodePoolResourceModel{Labels: types.MapNull(types.StringType)}
	if err := model.setFromJSON(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if model.Name.ValueString() != "gpu-workers" || model.InstanceType.ValueString() != "gd-8xh100ib-i128" || model.TargetNodes.ValueInt64() != 4 {
		t.Fatalf("unexpected state: %#v", model)
	}
	if model.ComputeClass.ValueString() != "default" || model.UpdateStrategy.ValueString() != "OnSpecUpdate" {
		t.Fatalf("defaults not normalized: %#v", model)
	}
	if len(model.Taints) != 1 || model.Taints[0].Value.ValueString() != "gpu" {
		t.Fatalf("unexpected taints: %#v", model.Taints)
	}
}

func TestNodePoolIdentifiers(t *testing.T) {
	t.Parallel()

	if got := nodePoolID("cluster-id", "gpu-workers"); got != "cluster-id/gpu-workers" {
		t.Fatalf("id = %q", got)
	}
	if got := nodePoolPath("gpu-workers"); got != nodePoolAPIPath+"/gpu-workers" {
		t.Fatalf("path = %q", got)
	}
}

func TestPublicClusterEndpoint(t *testing.T) {
	t.Parallel()

	got := publicClusterEndpoint("ORG-123", "test-cluster", "US-EAST-04A")
	want := "https://org-123-ac58c12a.k8s.us-east-04a.coreweave.com"
	if got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}
