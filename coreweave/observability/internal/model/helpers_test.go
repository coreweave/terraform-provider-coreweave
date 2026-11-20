package model_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
)

// Shared fixture values. They repeat across the conversion cases, so they are
// named once here rather than inline.
const (
	testHTTPSEndpoint  = "https://example.com/telemetry"
	testZone           = "us-east-04a"
	nilInputReturnsNil = "nil input returns nil"
)

// with copies value, applies fn to the copy, and returns it. Model fixtures are
// shared between subtests that run in parallel, so a case that needs one field
// changed takes a copy rather than mutating the original.
func with[T any](value *T, fn func(*T)) *T {
	v := *value
	fn(&v)
	return &v
}

// zoneSlugSet is a convenience for building the zone_slugs fixture value.
func zoneSlugSet(t *testing.T, zones ...string) types.Set {
	t.Helper()

	value, diags := types.SetValueFrom(t.Context(), types.StringType, zones)
	require.False(t, diags.HasError(), "failed to build zone_slugs: %v", diags)
	return value
}
