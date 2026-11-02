package inference

import (
	"context"
	"slices"
	"testing"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type paramOpts = inferencev1.DeploymentRuntimeParameters_RuntimeEngineOptions_VersionOptions_ParameterOptions

func TestRuntimeEngineOptionsValue(t *testing.T) {
	t.Parallel()

	in := map[string]*inferencev1.DeploymentRuntimeParameters_RuntimeEngineOptions{
		"vllm": {
			Versions: map[string]*inferencev1.DeploymentRuntimeParameters_RuntimeEngineOptions_VersionOptions{
				"0.16.0": {
					Lifecycle: inferencev1.DeploymentRuntimeParameters_LIFECYCLE_DEPRECATED,
				},
				"0.17.1": {
					Lifecycle:     inferencev1.DeploymentRuntimeParameters_LIFECYCLE_STABLE,
					ConfigOptions: map[string]*paramOpts{"max-model-len": {}, "dtype": {}},
					EnvOptions:    map[string]*paramOpts{"VLLM_LOGGING_LEVEL": {}},
				},
			},
		},
	}

	got, diags := runtimeEngineOptionsValue(in)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	type versionModel struct {
		Lifecycle     string   `tfsdk:"lifecycle"`
		ConfigOptions []string `tfsdk:"config_options"`
		EnvOptions    []string `tfsdk:"env_options"`
	}
	type engineModel struct {
		Versions map[string]versionModel `tfsdk:"versions"`
	}
	var engines map[string]engineModel
	if d := got.ElementsAs(context.Background(), &engines, false); d.HasError() {
		t.Fatalf("decoding result: %v", d)
	}

	vllm, ok := engines["vllm"]
	if !ok || len(engines) != 1 {
		t.Fatalf("engines = %v, want only vllm", engines)
	}

	deprecated := vllm.Versions["0.16.0"]
	if deprecated.Lifecycle != "LIFECYCLE_DEPRECATED" {
		t.Errorf("0.16.0 lifecycle = %q, want LIFECYCLE_DEPRECATED", deprecated.Lifecycle)
	}
	if len(deprecated.ConfigOptions) != 0 || len(deprecated.EnvOptions) != 0 {
		t.Errorf("0.16.0 options = %v / %v, want empty", deprecated.ConfigOptions, deprecated.EnvOptions)
	}

	stable := vllm.Versions["0.17.1"]
	if stable.Lifecycle != "LIFECYCLE_STABLE" {
		t.Errorf("0.17.1 lifecycle = %q, want LIFECYCLE_STABLE", stable.Lifecycle)
	}
	// Option names are map keys server-side; they must come back sorted so the
	// list is stable across reads and does not produce spurious diffs.
	if want := []string{"dtype", "max-model-len"}; !slices.Equal(stable.ConfigOptions, want) {
		t.Errorf("0.17.1 config_options = %v, want %v", stable.ConfigOptions, want)
	}
	if want := []string{"VLLM_LOGGING_LEVEL"}; !slices.Equal(stable.EnvOptions, want) {
		t.Errorf("0.17.1 env_options = %v, want %v", stable.EnvOptions, want)
	}
}

func TestRuntimeEngineOptionsValue_Empty(t *testing.T) {
	t.Parallel()

	got, diags := runtimeEngineOptionsValue(nil)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if got.IsNull() || got.IsUnknown() || len(got.Elements()) != 0 {
		t.Fatalf("got %v, want a known empty map", got)
	}
	if want := (types.ObjectType{AttrTypes: runtimeEngineOptionsAttrTypes}); !got.ElementType(context.Background()).Equal(want) {
		t.Fatalf("element type = %v, want %v", got.ElementType(context.Background()), want)
	}
}

