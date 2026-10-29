package networking

import (
	"testing"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

func TestVpcPrefixTypeRoundTrip(t *testing.T) {
	t.Parallel()
	for name, value := range networkingv1beta1.Prefix_Type_value {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			model := VpcPrefixResourceModel{Name: types.StringValue("prefix"), Value: types.StringValue("10.0.0.0/24"), Type: types.StringValue(name)}
			prefix, diags := model.ToProto()
			require.False(t, diags.HasError(), "%v", diags)
			require.NotNil(t, prefix.Type)
			require.Equal(t, networkingv1beta1.Prefix_Type(value), prefix.GetType())
			var state VpcPrefixResourceModel
			state.Set(prefix)
			if value == 0 {
				require.True(t, state.Type.IsNull())
			} else {
				require.Equal(t, model, state)
			}
			vpc := VpcResourceModel{VpcPrefixes: []VpcPrefixResourceModel{model}}
			create, diags := vpc.ToCreateRequest(t.Context())
			require.False(t, diags.HasError(), "%v", diags)
			require.Equal(t, prefix, create.VpcPrefixes[0])
			update, diags := vpc.ToUpdateRequest(t.Context())
			require.False(t, diags.HasError(), "%v", diags)
			require.Equal(t, prefix, update.VpcPrefixes[0])
			require.Contains(t, MustRenderVpcResource(t.Context(), "test", &vpc), `type`)
		})
	}
}

// TestVpcPrefixTypeModifyPlan checks in-place update decisions without decoding unrelated unknown values.
func TestVpcPrefixTypeModifyPlan(t *testing.T) {
	t.Parallel()
	r := &VpcResource{}
	var schemaResponse resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schemaResponse)
	require.False(t, schemaResponse.Diagnostics.HasError())
	model := VpcResourceModel{
		Id: types.StringValue("vpc"), Name: types.StringValue("vpc"), Zone: types.StringValue("US-LAB-01A"),
		HostPrefixes: types.SetNull(hostPrefixObjectType),
		VpcPrefixes:  []VpcPrefixResourceModel{{Name: types.StringValue("private"), Value: types.StringValue("10.0.0.0/24"), Type: types.StringValue("PRIVATE_ACCESS")}},
	}
	state := tfsdk.State{Schema: schemaResponse.Schema}
	diags := state.Set(t.Context(), &model)
	require.False(t, diags.HasError(), "%v", diags)
	prefixType := types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "value": types.StringType, "type": types.StringType}}
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, plan *tfsdk.Plan)
	}{
		{name: "unchanged"},
		{name: "changed type", change: func(t *testing.T, plan *tfsdk.Plan) {
			t.Helper()
			changed := model
			changed.VpcPrefixes = append([]VpcPrefixResourceModel(nil), model.VpcPrefixes...)
			changed.VpcPrefixes[0].Type = types.StringValue("SERVICE")
			diags := plan.Set(t.Context(), &changed)
			require.False(t, diags.HasError(), "%v", diags)
		}},
		{name: "new prefix", change: func(t *testing.T, plan *tfsdk.Plan) {
			t.Helper()
			added := model
			added.VpcPrefixes = append(append([]VpcPrefixResourceModel(nil), model.VpcPrefixes...), VpcPrefixResourceModel{Name: types.StringValue("pods"), Value: types.StringValue("10.1.0.0/24"), Type: types.StringValue("POD")})
			diags := plan.Set(t.Context(), &added)
			require.False(t, diags.HasError(), "%v", diags)
		}},
		{name: "unknown name", change: func(t *testing.T, plan *tfsdk.Plan) {
			t.Helper()
			changed := model
			changed.VpcPrefixes = append([]VpcPrefixResourceModel(nil), model.VpcPrefixes...)
			changed.VpcPrefixes[0].Name = types.StringUnknown()
			diags := plan.Set(t.Context(), &changed)
			require.False(t, diags.HasError(), "%v", diags)
		}},
		{name: "unknown collection", change: func(t *testing.T, plan *tfsdk.Plan) {
			t.Helper()
			diags := plan.SetAttribute(t.Context(), path.Root("vpc_prefixes"), types.SetUnknown(prefixType))
			require.False(t, diags.HasError(), "%v", diags)
		}},
		{name: "unknown element", change: func(t *testing.T, plan *tfsdk.Plan) {
			t.Helper()
			diags := plan.SetAttribute(t.Context(), path.Root("vpc_prefixes"), types.SetValueMust(prefixType, []attr.Value{types.ObjectUnknown(prefixType.AttrTypes)}))
			require.False(t, diags.HasError(), "%v", diags)
		}},
		{name: "unrelated unknown dhcp", change: func(t *testing.T, plan *tfsdk.Plan) {
			t.Helper()
			var dhcp types.Object
			diags := plan.GetAttribute(t.Context(), path.Root("dhcp"), &dhcp)
			require.False(t, diags.HasError(), "%v", diags)
			diags = plan.SetAttribute(t.Context(), path.Root("dhcp"), types.ObjectUnknown(dhcp.AttributeTypes(t.Context())))
			require.False(t, diags.HasError(), "%v", diags)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := tfsdk.Plan{Schema: schemaResponse.Schema, Raw: state.Raw.Copy()}
			if tc.change != nil {
				tc.change(t, &plan)
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(t.Context(), resource.ModifyPlanRequest{State: state, Plan: plan, Config: tfsdk.Config{Schema: schemaResponse.Schema, Raw: plan.Raw.Copy()}}, &resp)
			require.False(t, resp.Diagnostics.HasError(), "%v", resp.Diagnostics)
			require.Empty(t, resp.RequiresReplace)
		})
	}
}

// TestVpcPrefixTypeConfiguredUnknownPlan distinguishes omitted computed types from explicit unknowns.
func TestVpcPrefixTypeConfiguredUnknownPlan(t *testing.T) {
	const (
		existingPrefixName = "private"
		newPrefixName      = "new"
	)
	t.Parallel()
	r := &VpcResource{}
	var schemaResponse resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schemaResponse)
	require.False(t, schemaResponse.Diagnostics.HasError())
	for _, tc := range []struct {
		name       string
		configured types.String
		prefixName string
		untyped    bool
		create     bool
	}{
		{name: "omitted type", configured: types.StringNull(), prefixName: existingPrefixName},
		{name: "explicit unknown type", configured: types.StringUnknown(), prefixName: existingPrefixName},
		{name: "new prefix with unknown type", configured: types.StringUnknown(), prefixName: newPrefixName},
		{name: "new untyped prefix", configured: types.StringNull(), prefixName: newPrefixName},
		{name: "create untyped prefix", configured: types.StringNull(), prefixName: newPrefixName, create: true},
		{name: "untyped omission", configured: types.StringNull(), prefixName: existingPrefixName, untyped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			model := VpcResourceModel{
				HostPrefixes: types.SetNull(hostPrefixObjectType),
				VpcPrefixes:  []VpcPrefixResourceModel{{Name: types.StringValue("private"), Value: types.StringValue("10.0.0.0/24"), Type: types.StringValue("PRIVATE_ACCESS")}},
			}
			if tc.untyped {
				model.VpcPrefixes[0].Type = types.StringNull()
			}
			priorType := model.VpcPrefixes[0].Type
			if tc.prefixName != existingPrefixName {
				priorType = types.StringNull()
			}
			state := tfsdk.State{Schema: schemaResponse.Schema}
			diags := state.Set(t.Context(), &model)
			require.False(t, diags.HasError(), "%v", diags)
			if tc.create {
				state.Raw = tftypes.NewValue(state.Raw.Type(), nil)
			}
			model.VpcPrefixes[0].Name = types.StringValue(tc.prefixName)
			model.VpcPrefixes[0].Type = tc.configured
			config := tfsdk.State{Schema: schemaResponse.Schema}
			diags = config.Set(t.Context(), &model)
			require.False(t, diags.HasError(), "%v", diags)
			model.VpcPrefixes[0].Type = types.StringUnknown()
			plan := tfsdk.Plan{Schema: schemaResponse.Schema}
			diags = plan.Set(t.Context(), &model)
			require.False(t, diags.HasError(), "%v", diags)
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(t.Context(), resource.ModifyPlanRequest{State: state, Plan: plan, Config: tfsdk.Config{Schema: schemaResponse.Schema, Raw: config.Raw}}, &resp)
			require.False(t, resp.Diagnostics.HasError(), "%v", resp.Diagnostics)
			require.Empty(t, resp.RequiresReplace)
			var prefixes types.Set
			diags = resp.Plan.GetAttribute(t.Context(), path.Root("vpc_prefixes"), &prefixes)
			require.False(t, diags.HasError(), "%v", diags)
			plannedType := prefixes.Elements()[0].(types.Object).Attributes()["type"]
			if tc.configured.IsNull() {
				require.True(t, plannedType.Equal(priorType))
			} else {
				require.True(t, plannedType.IsUnknown(), "explicit unknown types must remain available for API validation at apply")
			}
		})
	}
}

func TestVpcPrefixTypePresence(t *testing.T) {
	t.Parallel()
	for _, value := range []types.String{types.StringNull(), types.StringUnknown()} {
		model := VpcPrefixResourceModel{Type: value}
		prefix, diags := model.ToProto()
		require.False(t, diags.HasError())
		require.Nil(t, prefix.Type, "omission must preserve an existing prefix type on updates")
		model.Set(prefix)
		require.True(t, model.Type.IsNull())
	}
	model := VpcResourceModel{VpcPrefixes: []VpcPrefixResourceModel{{Type: types.StringValue("invalid")}}}
	_, createDiags := model.ToCreateRequest(t.Context())
	require.True(t, createDiags.HasError())
	_, updateDiags := model.ToUpdateRequest(t.Context())
	require.True(t, updateDiags.HasError())
}

// TestVpcPrefixTypeAPIEvolution preserves unknown numeric values and normalizes the zero enum.
func TestVpcPrefixTypeAPIEvolution(t *testing.T) {
	t.Parallel()
	for _, value := range []int32{0, 2, 99} {
		var prefix VpcPrefixResourceModel
		prefix.Set(&networkingv1beta1.Prefix{Name: "future", Type: networkingv1beta1.Prefix_Type(value).Enum()})
		if value == 0 {
			require.True(t, prefix.Type.IsNull())
		}
		request, diags := (&VpcResourceModel{VpcPrefixes: []VpcPrefixResourceModel{prefix}}).ToUpdateRequest(t.Context())
		require.False(t, diags.HasError(), "%v", diags)
		require.Equal(t, value, int32(request.VpcPrefixes[0].GetType()))
	}
}

// TestVpcPrefixNames rejects duplicate identities even when their configured types differ.
func TestVpcPrefixNames(t *testing.T) {
	t.Parallel()
	objectType := types.ObjectType{AttrTypes: map[string]attr.Type{"name": types.StringType, "value": types.StringType, "type": types.StringType}}
	prefixes, diags := types.SetValueFrom(t.Context(), objectType, []VpcPrefixResourceModel{
		{Name: types.StringValue("same"), Value: types.StringValue("10.0.0.0/24"), Type: types.StringNull()},
		{Name: types.StringValue("same"), Value: types.StringValue("10.1.0.0/24"), Type: types.StringValue("PRIVATE_ACCESS")},
	})
	require.False(t, diags.HasError())
	require.True(t, validateVpcPrefixNames(prefixes).HasError())
}
