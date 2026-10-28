package networking

import (
	"testing"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
			require.Equal(t, model, state)
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
