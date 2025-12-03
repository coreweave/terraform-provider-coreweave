package telecaster_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const (
	AcceptanceTestPrefix = "tf-acc-tc-"
)

func with[T any](value *T, fn func(*T)) *T {
	v := *value
	fn(&v)
	return &v
}

// slugify creates a test resource slug with the acceptance test prefix and random suffix
func slugify(name string, randomInt int) string {
	return fmt.Sprintf("%s%s-%d", AcceptanceTestPrefix, name, randomInt)
}

func dieIfDiagnostics(t *testing.T, diagnostics diag.Diagnostics) {
	t.Helper()
	if diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", diagnostics)
	}
}

type testStepOption func(*resource.TestStep)

func testStepOptionPlanOnly(v bool) testStepOption {
	return func(step *resource.TestStep) {
		step.PlanOnly = v
	}
}

func testStepOptionExpectNonEmptyPlan(v bool) testStepOption {
	return func(step *resource.TestStep) {
		step.ExpectNonEmptyPlan = v
	}
}
