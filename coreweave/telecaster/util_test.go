package telecaster_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	fwdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
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

// resourceName returns the resource name for the given resource using resource+provider metadata.
// This is useful for programmatically constructing resource names that are definitionally correct.
func resourceName(resource fwresource.Resource) string {
	// note: this may only be done within the test package, to avoid circular imports.
	providerMetadataResp := new(fwprovider.MetadataResponse)
	new(provider.CoreweaveProvider).Metadata(context.Background(), fwprovider.MetadataRequest{}, providerMetadataResp)

	metadataResp := new(fwresource.MetadataResponse)
	resource.Metadata(context.Background(), fwresource.MetadataRequest{ProviderTypeName: providerMetadataResp.TypeName}, metadataResp)
	return metadataResp.TypeName
}

func datasourceName(datasource fwdatasource.DataSource) string {
	providerMetadataResp := new(fwprovider.MetadataResponse)
	new(provider.CoreweaveProvider).Metadata(context.Background(), fwprovider.MetadataRequest{}, providerMetadataResp)

	metadataResp := new(fwdatasource.MetadataResponse)
	datasource.Metadata(context.Background(), fwdatasource.MetadataRequest{ProviderTypeName: providerMetadataResp.TypeName}, metadataResp)
	return metadataResp.TypeName
}
