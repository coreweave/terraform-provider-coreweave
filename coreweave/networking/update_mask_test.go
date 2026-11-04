package networking

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
)

// TestBuildUpdateMask verifies that the update mask names only the fields that
// changed between prior state and plan. The search_domains path is the one that
// matters for routing: the VPC API router rejects an update that masks it when
// the VPC still lives on the global backend, so an update that does not change
// search domains must not name that path.
func TestBuildUpdateMask(t *testing.T) {
	t.Parallel()

	stringList := func(vals ...string) types.List {
		elems := make([]attr.Value, len(vals))
		for i, v := range vals {
			elems[i] = types.StringValue(v)
		}
		return types.ListValueMust(types.StringType, elems)
	}
	stringSet := func(vals ...string) types.Set {
		elems := make([]attr.Value, len(vals))
		for i, v := range vals {
			elems[i] = types.StringValue(v)
		}
		return types.SetValueMust(types.StringType, elems)
	}
	withDNS := func(servers types.Set, searchDomains types.List) *VpcResourceModel {
		return &VpcResourceModel{Dhcp: &VpcDhcpResourceModel{Dns: &VpcDhcpDnsResourceModel{
			Servers:       servers,
			SearchDomains: searchDomains,
		}}}
	}
	withPrefixes := func(names ...string) *VpcResourceModel {
		prefixes := make([]VpcPrefixResourceModel, len(names))
		for i, n := range names {
			prefixes[i] = VpcPrefixResourceModel{
				Name:  types.StringValue(n),
				Value: types.StringValue("10.0.0.0/24"),
				Type:  types.StringNull(),
			}
		}
		return &VpcResourceModel{VpcPrefixes: prefixes}
	}

	for _, tc := range []struct {
		name        string
		plan, state *VpcResourceModel
		want        []string
	}{
		{
			name:  "nothing changed",
			plan:  &VpcResourceModel{},
			state: &VpcResourceModel{},
			want:  nil,
		},
		{
			name:  "search domains added",
			plan:  withDNS(types.SetNull(types.StringType), stringList("corp.example.com")),
			state: &VpcResourceModel{},
			want:  []string{fieldPathDNSSearchDomains},
		},
		{
			name:  "search domains cleared",
			plan:  &VpcResourceModel{},
			state: withDNS(types.SetNull(types.StringType), stringList("corp.example.com")),
			want:  []string{fieldPathDNSSearchDomains},
		},
		{
			// The global-backend safety case: an unrelated change must not drag
			// search_domains into the mask, or the router rejects the update.
			name:  "unrelated change leaves search domains unmasked",
			plan:  withPrefixes("a", "b"),
			state: withPrefixes("a"),
			want:  []string{fieldPathVPCPrefixes},
		},
		{
			name:  "dns servers only",
			plan:  withDNS(stringSet("1.1.1.1"), types.ListNull(types.StringType)),
			state: withDNS(types.SetNull(types.StringType), types.ListNull(types.StringType)),
			want:  []string{fieldPathDNSServers},
		},
		{
			name:  "ingress only",
			plan:  &VpcResourceModel{Ingress: &VpcIngressResourceModel{DisablePublicServices: types.BoolValue(true)}},
			state: &VpcResourceModel{Ingress: &VpcIngressResourceModel{DisablePublicServices: types.BoolValue(false)}},
			want:  []string{fieldPathIngress},
		},
		{
			name:  "egress only",
			plan:  &VpcResourceModel{Egress: &VpcEgressResourceModel{DisablePublicAccess: types.BoolValue(true)}},
			state: &VpcResourceModel{Egress: &VpcEgressResourceModel{DisablePublicAccess: types.BoolValue(false)}},
			want:  []string{fieldPathEgress},
		},
		{
			name: "multiple fields changed",
			plan: &VpcResourceModel{
				VpcPrefixes: withPrefixes("a").VpcPrefixes,
				Dhcp:        withDNS(types.SetNull(types.StringType), stringList("corp.example.com")).Dhcp,
			},
			state: &VpcResourceModel{},
			want:  []string{fieldPathVPCPrefixes, fieldPathDNSSearchDomains},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildUpdateMask(tc.plan, tc.state).GetPaths()
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, buildUpdateMask(tc.state, tc.state).GetPaths(), fieldPathDNSSearchDomains,
				"a no-op update (state vs itself) must never mask search domains")
		})
	}
}
