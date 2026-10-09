package observability_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	fwdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/stretchr/testify/require"
)

const (
	AcceptanceTestPrefix = "tf-acc-tc-"

	// Endpoint URLs used by acceptance tests. They do not need to resolve:
	// forwarding endpoints are database-only records server-side and
	// CheckEndpoint performs no connectivity test. They do need to be public
	// https, because buf.validate requires the https:// scheme and the service
	// rejects targets resolving to loopback, private, or link-local addresses.
	testHTTPSEndpointURL      = "https://telemetry.example.com/ingest"
	testPrometheusEndpointURL = "https://prometheus.example.com/api/v1/write"

	// Endpoint slugs are capped at 32 characters by buf.validate; the pipeline
	// matrix generates names from stream and endpoint type and has to shorten
	// them to fit.
	endpointSlugMaxTestLength = 32

	// Lab zones used by the zone_slugs subtests. They must be lowercase: the
	// API canonicalises, so an uppercase value would never match on read-back.
	testLabZoneSlug       = "us-east-04a"
	testSecondLabZoneSlug = "us-west-01a"

	// A self-signed CA, used only to exercise the tls attribute's round-trip.
	testCACertificateData = "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJURENDQWZPZ0F3SUJBZ0lVZmRLdDdHWU9hRDZuL2pvb3A3OEVoT3Y3YkFvd0NnWUlLb1pJemowRUF3SXcKSERFYU1CZ0dBMVVFQXd3UlkyOXlaWGRsWVhabFkyRXRjbTl2ZEMwd0hoY05NalF4TVRFek1EQTBNVEExV2hjTgpNalV4TVRFek1EQTBNVEExV2pBY01Sb3dHQVlEVlFRRERCRmpiM0psZDJWaGRtVmpZUzF5YjI5ME1Ea1ZNQk1HCkJ5cUdTTTQ5QWdFR0NDcUdTTTQ5QXdFSEEwSUFCUElIdUMyQklIdlFyUlV0bjdodFFnY1NGRDlDbEs0U3BLN0sKaEhWaS9RQm9naVREMC9yMWRqRkViYmZHOW9DTzFodHpXWjd4aE1CRUY4NFJ2TlhtdWNlamdZWXdnWU13RGdZRApWUjBQQVFIL0JBUURBZ0VHTUJJR0ExVWRFd0VCL3dRSU1BWUJBZjhDQVFBd0hRWURWUjBPQkJZRUZOckZjS1dJClVOcWdXcWNxWk5FSVRzOVJuZGh4TUI4R0ExVWRJd1FZTUJhQUZOckZjS1dJVU5xZ1dxY3FaTkVJVHM5Um5kaHgKTUJrR0ExVWRFUVFTTUJDQ0RuZGxkR2h2YjJ0ekxuTjJZekFLQmdncWhrak9QUVFEQWdOSEFEQkVBaUJlM3NsYQpTWjc5bmxQeWJlYVY4NXp5VW9VQ1hVWjNvTnhjN1lZc3N0WDFuZ0lnSUhYQ0xEZUZWKzF2Mlk1RzdwN3N0VTRCClA0VTlScHlyVzhMWnhRdWhFYjQ9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K"
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

// testClient builds an API client for the out-of-band operations a few
// acceptance steps need (deleting a resource behind Terraform's back to prove
// Read removes it from state).
func testClient(t *testing.T) *coreweave.Client {
	t.Helper()

	testutil.SetEnvDefaults()
	client, err := provider.BuildClient(t.Context(), provider.CoreweaveProviderModel{}, "", "")
	require.NoError(t, err, "failed to build API client")
	return client
}

// attributeCapture reads one root-level string attribute out of state and
// hands it to run. Credential rotation is only observable as a change in
// credentials_updated_at, so a step has to remember what the previous step
// saw; the stock knownvalue checks cannot express "different from last time".
type attributeCapture struct {
	resourceAddress string
	attribute       string
	run             func(value string) error
}

func (c attributeCapture) CheckState(_ context.Context, req statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	if req.State == nil || req.State.Values == nil || req.State.Values.RootModule == nil {
		resp.Error = fmt.Errorf("no state to read %s from", c.attribute)
		return
	}

	for _, res := range req.State.Values.RootModule.Resources {
		if res.Address != c.resourceAddress {
			continue
		}

		raw, ok := res.AttributeValues[c.attribute]
		if !ok {
			resp.Error = fmt.Errorf("%s has no attribute %q", c.resourceAddress, c.attribute)
			return
		}
		value, ok := raw.(string)
		if !ok {
			resp.Error = fmt.Errorf("%s.%s is %T, want string", c.resourceAddress, c.attribute, raw)
			return
		}

		resp.Error = c.run(value)
		return
	}

	resp.Error = fmt.Errorf("resource %s not found in state", c.resourceAddress)
}

// credentialsUpdatedAtAttribute is the only observable record that credentials
// were replaced, so every rotation assertion is a comparison of this value
// across two steps.
const credentialsUpdatedAtAttribute = "credentials_updated_at"

// extractCredentialsUpdatedAt stores credentials_updated_at into out, for a
// later step to compare against.
func extractCredentialsUpdatedAt(resourceAddress string, out *string) statecheck.StateCheck {
	return attributeCapture{
		resourceAddress: resourceAddress,
		attribute:       credentialsUpdatedAtAttribute,
		run: func(value string) error {
			*out = value
			return nil
		},
	}
}

// comparison defers an assertion over two previously captured values until
// state-check time, by which point both pointers are populated.
type comparison struct {
	assert func() error
}

func (c comparison) CheckState(_ context.Context, _ statecheck.CheckStateRequest, resp *statecheck.CheckStateResponse) {
	resp.Error = c.assert()
}

// expectUnchanged asserts the attribute holds the value a previous step
// captured. Pair it with extractAttribute earlier in the same slice.
func expectUnchanged(attribute string, before, after *string) statecheck.StateCheck {
	return comparison{func() error {
		if *before != *after {
			return fmt.Errorf("%s changed from %q to %q, expected it to stay put", attribute, *before, *after)
		}
		return nil
	}}
}

// expectChanged asserts the attribute moved since a previous step.
func expectChanged(attribute string, before, after *string) statecheck.StateCheck {
	return comparison{func() error {
		if *before == *after {
			return fmt.Errorf("%s is still %q, expected it to change", attribute, *before)
		}
		return nil
	}}
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

// datasourceName returns the datasource name for the given datasource using datasource+provider metadata.
// This is useful for programmatically constructing datasource names that are definitionally correct.
func datasourceName(datasource fwdatasource.DataSource) string {
	providerMetadataResp := new(fwprovider.MetadataResponse)
	new(provider.CoreweaveProvider).Metadata(context.Background(), fwprovider.MetadataRequest{}, providerMetadataResp)

	metadataResp := new(fwdatasource.MetadataResponse)
	datasource.Metadata(context.Background(), fwdatasource.MetadataRequest{ProviderTypeName: providerMetadataResp.TypeName}, metadataResp)
	return metadataResp.TypeName
}
