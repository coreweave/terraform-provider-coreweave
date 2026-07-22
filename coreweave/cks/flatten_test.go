package cks

import (
	"testing"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSetTailscale(t *testing.T) {
	tests := []struct {
		name         string
		tailscale    *cksv1beta1.Tailscale
		wantNil      bool
		wantClientID types.String
		wantDomain   types.String
	}{
		{
			name:      "no tailscale configured",
			tailscale: nil,
			wantNil:   true,
		},
		{
			// The API may keep the Tailscale message populated with only an
			// output-only tailnet_domain after the client_id is cleared; the
			// block is required to carry a client_id, so treat this as unset.
			name:      "empty client_id is treated as unset",
			tailscale: &cksv1beta1.Tailscale{ClientId: "", TailnetDomain: "my-cluster.tailnet.ts.net"},
			wantNil:   true,
		},
		{
			name:         "domain not yet assigned is null",
			tailscale:    &cksv1beta1.Tailscale{ClientId: "client-123"},
			wantClientID: types.StringValue("client-123"),
			wantDomain:   types.StringNull(),
		},
		{
			name:         "domain assigned",
			tailscale:    &cksv1beta1.Tailscale{ClientId: "client-123", TailnetDomain: "my-cluster.tailnet.ts.net"},
			wantClientID: types.StringValue("client-123"),
			wantDomain:   types.StringValue("my-cluster.tailnet.ts.net"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var model ClusterResourceModel
			model.setTailscale(&cksv1beta1.Cluster{Tailscale: tt.tailscale})

			if tt.wantNil {
				if model.Tailscale != nil {
					t.Fatalf("expected Tailscale to be nil, got %+v", model.Tailscale)
				}
				return
			}

			if model.Tailscale == nil {
				t.Fatal("expected Tailscale to be non-nil")
			}
			if !model.Tailscale.ClientID.Equal(tt.wantClientID) {
				t.Errorf("client_id: got %v, want %v", model.Tailscale.ClientID, tt.wantClientID)
			}
			if !model.Tailscale.TailnetDomain.Equal(tt.wantDomain) {
				t.Errorf("tailnet_domain: got %v, want %v", model.Tailscale.TailnetDomain, tt.wantDomain)
			}
		})
	}
}
