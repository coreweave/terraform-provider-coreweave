package sandbox

import (
	"encoding/json"
	"testing"

	sandboxv1 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyNetworkAliasesRoundTrip(t *testing.T) {
	for _, input := range []string{
		`{"constraints":{"network":{"deny_dns":false,"allowed_egress":[{"dns_name":"*.example.com","dns_name_except":["private.example.com"]}]}}}`,
		`{"constraints":{"network":{"deny_dns":true,"allowed_egress":[{"dns_name":"*.example.com","https_hostname_except":["private.example.com"]}]}}}`,
		`{"constraints":{"network":{"deny_https_hostname_rules":false,"dns_egress":"DNS_EGRESS_MODE_DENY","allowed_egress":[{"https_hostname":"*.example.com","https_hostname_except":["private.example.com"]}]}}}`,
	} {
		t.Run(input, func(t *testing.T) {
			var raw map[string]any
			require.NoError(t, json.Unmarshal([]byte(input), &raw))
			typ := policyAttribute().GetType().(types.ObjectType)
			value, err := jsonToValue(t.Context(), raw, nil, typ)
			require.NoError(t, err)
			previous := value.(types.Object)
			// jsonToValue normally treats an omitted proto3 zero as null. Here
			// the input is user configuration, so retain explicitly set false.
			attrs := previous.Attributes()
			constraints := attrs["constraints"].(types.Object)
			constraintAttrs := constraints.Attributes()
			network := constraintAttrs["network"].(types.Object)
			networkAttrs := network.Attributes()
			for key, v := range raw["constraints"].(map[string]any)["network"].(map[string]any) {
				if b, ok := v.(bool); ok {
					networkAttrs[key] = types.BoolValue(b)
				}
			}
			constraintAttrs["network"] = types.ObjectValueMust(network.AttributeTypes(t.Context()), networkAttrs)
			attrs["constraints"] = types.ObjectValueMust(constraints.AttributeTypes(t.Context()), constraintAttrs)
			previous = types.ObjectValueMust(typ.AttrTypes, attrs)
			var policy sandboxv1.Policy
			require.NoError(t, objectToProto(previous, &policy))
			assert.Equal(t, "*.example.com", policy.GetConstraints().GetNetwork().GetAllowedEgress()[0].GetHttpsHostname())
			assert.Equal(t, []string{"private.example.com"}, policy.GetConstraints().GetNetwork().GetAllowedEgress()[0].GetHttpsHostnameExcept())
			refreshed, err := objectFromProto(t.Context(), &policy, previous, typ)
			require.NoError(t, err)
			assert.True(t, previous.Equal(refreshed), "configured spelling and explicit false must survive refresh: %s", refreshed)
			imported, err := objectFromProto(t.Context(), &policy, types.ObjectNull(typ.AttrTypes), typ)
			require.NoError(t, err)
			encoded, err := valueToJSON(imported)
			require.NoError(t, err)
			bytes, err := json.Marshal(encoded)
			require.NoError(t, err)
			assert.Contains(t, string(bytes), "https_hostname")
			assert.NotContains(t, string(bytes), "dns_name")
			assert.NotContains(t, string(bytes), "deny_dns")
		})
	}
}

func TestPolicyNetworkAliasConflicts(t *testing.T) {
	for _, network := range []string{
		`{"deny_dns":true,"deny_https_hostname_rules":true}`,
		`{"allowed_egress":[{"dns_name":"example.com","https_hostname":"example.com"}]}`,
		`{"allowed_egress":[{"https_hostname":"*","dns_name_except":["one.example.com"],"https_hostname_except":["two.example.com"]}]}`,
	} {
		var raw map[string]any
		require.NoError(t, json.Unmarshal([]byte(`{"constraints":{"network":`+network+`}}`), &raw))
		typ := policyAttribute().GetType().(types.ObjectType)
		value, err := jsonToValue(t.Context(), raw, types.ObjectUnknown(typ.AttrTypes), typ)
		require.NoError(t, err)
		var policy sandboxv1.Policy
		require.ErrorContains(t, objectToProto(value.(types.Object), &policy), "cannot set both")
	}
}

func TestPolicyAliasLeavesOpaqueBaseUntouched(t *testing.T) {
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"base":{"spec":{"nodeSelector":{"dns_name":"literal","deny_dns":"literal"}}},"constraints":{"network":{"allowed_egress":[{"dns_name":"example.com"}]}}}`), &raw))
	typ := policyAttribute().GetType().(types.ObjectType)
	value, err := jsonToValue(t.Context(), raw, nil, typ)
	require.NoError(t, err)
	var policy sandboxv1.Policy
	require.NoError(t, objectToProto(value.(types.Object), &policy))
	assert.Equal(t, map[string]any{"dns_name": "literal", "deny_dns": "literal"}, policy.GetBase().AsMap()["spec"].(map[string]any)["nodeSelector"])
}
