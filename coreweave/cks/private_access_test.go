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
		{name: "ipv4", access: &cksv1beta1.PrivateAccess{PrefixNameIpv4: "private-v4"}},
		{name: "ipv6", access: &cksv1beta1.PrivateAccess{PrefixNameIpv6: "private-v6"}},
		{name: "dual stack", access: &cksv1beta1.PrivateAccess{PrefixNameIpv4: "private-v4", PrefixNameIpv6: "private-v6"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := ClusterResourceModel{}
			state.Set(&cksv1beta1.Cluster{Network: &cksv1beta1.ClusterNetworkConfig{PrivateAccess: tc.access}})
			request := state.ToCreateRequest(t.Context())
			require.True(t, proto.Equal(tc.access, request.Network.PrivateAccess))
			if tc.access == nil {
				require.True(t, state.PrivateAccess.IsNull())
			} else {
				require.False(t, state.PrivateAccess.IsNull())
				require.Contains(t, MustRenderClusterResource(t.Context(), "test", &state), "private_access")
			}
			// Unrelated updates must never send immutable network private access.
			plan := state
			plan.Version = types.StringValue("v1.35")
			update := buildUpdateRequest(t.Context(), &plan, &state)
			require.Nil(t, update.Network)
			// Refresh must clear a previously populated value when the API omits network.
			state.Set(&cksv1beta1.Cluster{})
			require.True(t, state.PrivateAccess.IsNull())
		})
	}
	require.Nil(t, privateAccessToProto(types.ObjectUnknown(privateAccessAttrTypes)))
}
