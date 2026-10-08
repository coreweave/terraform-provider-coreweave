package containerregistry_test

import (
	"maps"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

// TestPolicyValidationReportsEachFailureOnce exercises semantic errors through the real Framework server.
func TestPolicyValidationReportsEachFailureOnce(t *testing.T) {
	for _, tc := range []struct {
		kind, attribute, summary string
		nullEntry                bool
	}{
		{"lifecycle_policy", "rules", "Missing lifecycle condition", false},
		{"lifecycle_policy", "rules", "Invalid lifecycle rule", true},
		{"access_configuration", "policy_sets", "Invalid policy", true},
		{"access_configuration", "request_ip_acl", "Invalid IP ACL", false},
	} {
		t.Run(tc.summary, func(t *testing.T) {
			h := newHarness(t, tc.kind)
			var values map[string]tftypes.Value
			require.NoError(t, h.config(nil, false).As(&values))
			typ := values[tc.attribute].Type()
			switch typ := typ.(type) {
			case tftypes.Map:
				value := tftypes.NewValue(typ.ElementType, nil)
				if !tc.nullEntry {
					rule := nullObject(typ.ElementType)
					rule["regex"] = tftypes.NewValue(tftypes.String, ".*")
					value = tftypes.NewValue(typ.ElementType, rule)
				}
				values[tc.attribute] = tftypes.NewValue(typ, map[string]tftypes.Value{"test": value})
			case tftypes.Object:
				acl := nullObject(typ)
				for _, key := range []string{"allow_cidrs", "deny_cidrs"} {
					acl[key] = tftypes.NewValue(acl[key].Type(), []tftypes.Value{})
				}
				values[tc.attribute] = tftypes.NewValue(typ, acl)
			}
			result, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: h.name, Config: h.dv(tftypes.NewValue(h.typ, values))})
			require.NoError(t, err)
			if tc.nullEntry {
				require.NotEmpty(t, result.Diagnostics)
				for _, diagnostic := range result.Diagnostics {
					require.Contains(t, diagnostic.Summary, "Missing Configuration")
				}
			} else {
				require.Len(t, result.Diagnostics, 1)
				require.Equal(t, tc.summary, result.Diagnostics[0].Summary)
			}
			require.Empty(t, h.fake.creates)
		})
	}
}

// TestNestedNullAccessRuleUsesFrameworkDiagnostic avoids duplicating the required-field error.
func TestNestedNullAccessRuleUsesFrameworkDiagnostic(t *testing.T) {
	h := newHarness(t, "access_configuration")
	var values map[string]tftypes.Value
	require.NoError(t, h.withContent(h.config(nil, false)).As(&values))
	pt := values["policy_sets"].Type().(tftypes.Map)
	var policies map[string]tftypes.Value
	require.NoError(t, values["policy_sets"].As(&policies))
	var policy map[string]tftypes.Value
	require.NoError(t, policies["readers"].As(&policy))
	rt := policy["rules"].Type().(tftypes.Map)
	policy["rules"] = tftypes.NewValue(rt, map[string]tftypes.Value{"pull": tftypes.NewValue(rt.ElementType, nil)})
	values["policy_sets"] = tftypes.NewValue(pt, map[string]tftypes.Value{"readers": tftypes.NewValue(pt.ElementType, policy)})
	result, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: h.name, Config: h.dv(tftypes.NewValue(h.typ, values))})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	require.Contains(t, result.Diagnostics[0].Summary, "Missing Configuration")
}

// TestResolvedInvalidLifecycleInputNeverWrites covers API bounds that were unknown during planning.
func TestResolvedInvalidLifecycleInputNeverWrites(t *testing.T) {
	h := newHarness(t, "lifecycle_policy")
	var values map[string]tftypes.Value
	require.NoError(t, h.withContent(h.config(nil, false)).As(&values))
	mt := values["rules"].Type().(tftypes.Map)
	rule := nullObject(mt.ElementType)
	rule["regex"] = tftypes.NewValue(tftypes.String, ".*")
	rule["keep_newest"] = tftypes.NewValue(tftypes.Number, int64(101))
	values["rules"] = tftypes.NewValue(mt, map[string]tftypes.Value{"retention": tftypes.NewValue(mt.ElementType, rule)})
	unknownRule := maps.Clone(rule)
	unknownRule["keep_newest"] = tftypes.NewValue(tftypes.Number, tftypes.UnknownValue)
	unknown := maps.Clone(values)
	unknown["rules"] = tftypes.NewValue(mt, map[string]tftypes.Value{"retention": tftypes.NewValue(mt.ElementType, unknownRule)})
	validation, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: h.name, Config: h.dv(tftypes.NewValue(h.typ, unknown))})
	require.NoError(t, err)
	noErrors(t, validation.Diagnostics)
	applied := h.apply(tftypes.NewValue(h.typ, values), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, applied.Diagnostics)
	require.Contains(t, applied.Diagnostics[0].Detail, "keep_newest")
	require.Empty(t, h.fake.lifecycleUpdates)
	require.True(t, h.decode(applied.NewState).IsNull())
}

// TestNamespaceKnownValidationSurvivesUnknownZone validates known identifier constraints independently.
func TestNamespaceKnownValidationSurvivesUnknownZone(t *testing.T) {
	h := newHarness(t, "namespace")
	var values map[string]tftypes.Value
	require.NoError(t, h.config(nil, false).As(&values))
	values["name"] = tftypes.NewValue(tftypes.String, "BAD")
	values["zone"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	result, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: h.name, Config: h.dv(tftypes.NewValue(h.typ, values))})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	require.Equal(t, "Invalid namespace identifier", result.Diagnostics[0].Summary)
	require.Empty(t, h.fake.creates)
}

// TestCanonicalPathsAreNotTerraformNames rejects API paths at each namespace input.
func TestCanonicalPathsAreNotTerraformNames(t *testing.T) {
	for _, kind := range []string{"namespace", "access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			var values map[string]tftypes.Value
			require.NoError(t, h.config(nil, false).As(&values))
			field := "namespace"
			if kind == "namespace" {
				field = "name"
			}
			values[field] = tftypes.NewValue(tftypes.String, "namespaces/example-images")
			result, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: h.name, Config: h.dv(tftypes.NewValue(h.typ, values))})
			require.NoError(t, err)
			require.Len(t, result.Diagnostics, 1)
			require.Equal(t, "Invalid namespace identifier", result.Diagnostics[0].Summary)
			require.Empty(t, h.fake.creates)
			require.Empty(t, h.fake.accessUpdates)
			require.Empty(t, h.fake.lifecycleUpdates)
		})
	}
}
