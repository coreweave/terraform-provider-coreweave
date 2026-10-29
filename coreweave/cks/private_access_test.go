package cks

import (
	"testing"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestPrivateAccessRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		access *cksv1beta1.PrivateAccess
	}{
		{name: "absent"},
		{name: "empty", access: &cksv1beta1.PrivateAccess{}},
		{name: "ipv4", access: &cksv1beta1.PrivateAccess{PrefixNameIpv4: "private-v4"}},
		{name: "ipv6", access: &cksv1beta1.PrivateAccess{PrefixNameIpv6: "private-v6"}},
		{name: "dual stack", access: &cksv1beta1.PrivateAccess{PrefixNameIpv4: "private-v4", PrefixNameIpv6: "private-v6"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := ClusterResourceModel{}
			state.Set(&cksv1beta1.Cluster{Network: &cksv1beta1.ClusterNetworkConfig{PrivateAccess: tc.access}})
			request := state.ToCreateRequest(t.Context())
			if tc.access == nil || tc.name == "empty" {
				require.Nil(t, request.Network.PrivateAccess)
				require.True(t, state.PrivateAccess.IsNull())
			} else {
				require.True(t, proto.Equal(tc.access, request.Network.PrivateAccess))
				require.False(t, state.PrivateAccess.IsNull())
				require.Contains(t, MustRenderClusterResource(t.Context(), "test", &state), "private_access")
			}
			// Unrelated updates must never send immutable network private access.
			plan := state
			plan.Version = types.StringValue("v1.35")
			update := buildUpdateRequest(t.Context(), &plan, &state)
			require.Nil(t, update.Network)
			// An omitted network is incomplete data, so retain the previous observation.
			priorAccess := state.PrivateAccess
			state.Set(&cksv1beta1.Cluster{})
			require.True(t, state.PrivateAccess.Equal(priorAccess))
			// A present network without private access explicitly clears the observation.
			state.Set(&cksv1beta1.Cluster{Network: &cksv1beta1.ClusterNetworkConfig{}})
			require.True(t, state.PrivateAccess.IsNull())
		})
	}
	require.Nil(t, privateAccessToProto(types.ObjectUnknown(privateAccessAttrTypes)))
}

// TestPrivateAccessUnknownWithoutNetwork resolves an omitted computed value before saving state.
func TestPrivateAccessUnknownWithoutNetwork(t *testing.T) {
	t.Parallel()
	model := ClusterResourceModel{PrivateAccess: types.ObjectUnknown(privateAccessAttrTypes)}
	model.Set(&cksv1beta1.Cluster{})
	require.True(t, model.PrivateAccess.IsNull())
}
