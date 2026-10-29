package networking

import (
	"testing"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-nettypes/cidrtypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

func TestHostPrefixesAppendOnly(t *testing.T) {
	primary := testHostPrefixValue(t, "primary", "PRIMARY", "10.194.192.0/18")
	routed := testHostPrefixValue(t, "routed", "ROUTED", "192.168.0.0/18")
	attached := testHostPrefixValue(t, "attached", "ATTACHED", "172.20.0.0/18")
	initial := types.SetValueMust(hostPrefixObjectType, []attr.Value{primary, routed})
	unknownAttributes := routed.Attributes()
	unknownAttributes["name"] = types.StringUnknown()
	unknownElement := types.ObjectValueMust(hostPrefixObjectType.AttrTypes, unknownAttributes)
	knownRaw := tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{}}, map[string]tftypes.Value{})
	nullRaw := tftypes.NewValue(knownRaw.Type(), nil)
	for _, tc := range []struct {
		name                                string
		plan                                types.Set
		omitted, create, destroy, wantError bool
	}{
		{name: "unchanged", plan: initial},
		{name: "reordered", plan: types.SetValueMust(hostPrefixObjectType, []attr.Value{routed, primary})},
		{name: "addition", plan: types.SetValueMust(hostPrefixObjectType, []attr.Value{attached, primary, routed})},
		{name: "removal", plan: types.SetValueMust(hostPrefixObjectType, []attr.Value{primary}), wantError: true},
		{name: "replacement", plan: types.SetValueMust(hostPrefixObjectType, []attr.Value{primary, attached}), wantError: true},
		{name: "creation", plan: types.SetValueMust(hostPrefixObjectType, []attr.Value{attached}), create: true},
		{name: "destruction", plan: types.SetNull(hostPrefixObjectType), destroy: true},
		{name: "omitted computed", plan: types.SetUnknown(hostPrefixObjectType), omitted: true},
		{name: "unknown set", plan: types.SetUnknown(hostPrefixObjectType)},
		{name: "unknown nested value", plan: types.SetValueMust(hostPrefixObjectType, []attr.Value{primary, unknownElement})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := planmodifier.SetRequest{
				Path: path.Root("host_prefixes"), ConfigValue: tc.plan, PlanValue: tc.plan, StateValue: initial,
				State: tfsdk.State{Raw: knownRaw}, Plan: tfsdk.Plan{Raw: knownRaw},
			}
			if tc.create {
				req.State.Raw = nullRaw
			}
			if tc.destroy {
				req.Plan.Raw = nullRaw
			}
			if tc.omitted {
				req.ConfigValue = types.SetNull(hostPrefixObjectType)
			}
			resp := planmodifier.SetResponse{PlanValue: req.PlanValue}
			hostPrefixesAppendOnly().PlanModifySet(t.Context(), req, &resp)
			require.Equal(t, tc.wantError, resp.Diagnostics.HasError(), resp.Diagnostics)
			require.False(t, resp.RequiresReplace, "host-prefix changes must never request VPC replacement")
			require.True(t, req.PlanValue.Equal(resp.PlanValue), "modifier must preserve the proposed value")
		})
	}
}

func TestToUpdateRequestHostPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prefixes types.Set
		count    int
	}{
		{"full desired set", types.SetValueMust(hostPrefixObjectType, []attr.Value{
			testHostPrefixValue(t, "primary", "PRIMARY", "10.194.192.0/18"),
			testHostPrefixValue(t, "routed", "ROUTED", "192.168.0.0/18"),
		}), 2},
		{"omitted", types.SetNull(hostPrefixObjectType), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := VpcResourceModel{Id: types.StringValue("test-vpc"), HostPrefixes: tc.prefixes}
			req, diagnostics := model.ToUpdateRequest(t.Context())
			require.False(t, diagnostics.HasError(), diagnostics)
			require.Len(t, req.HostPrefixes, tc.count)
			require.Nil(t, req.UpdateMask, "empty mask uses the API's populated-field semantics")
			if tc.count > 0 {
				require.Equal(t, "primary", req.HostPrefixes[0].Name)
				require.Equal(t, networkingv1beta1.HostPrefix_ROUTED, req.HostPrefixes[1].Type)
				require.Equal(t, []string{"192.168.0.0/18"}, req.HostPrefixes[1].Prefixes)
				require.Equal(t, int32(31), req.HostPrefixes[1].Ipam.PrefixLength)
			}
		})
	}
}

func testHostPrefixValue(t *testing.T, name, kind, cidr string) types.Object {
	t.Helper()
	model := HostPrefixResourceModel{
		Name: types.StringValue(name), Type: types.StringValue(kind),
		Prefixes: []cidrtypes.IPPrefix{cidrtypes.NewIPPrefixValue(cidr)},
	}
	if kind != "PRIMARY" {
		model.IPAM = &IPAMPolicyResourceModel{PrefixLength: types.Int32Value(31), GatewayAddressPolicy: types.StringValue("UNSPECIFIED")}
	}
	value, diagnostics := types.ObjectValueFrom(t.Context(), hostPrefixObjectType.AttrTypes, model)
	require.False(t, diagnostics.HasError(), diagnostics)
	return value
}
