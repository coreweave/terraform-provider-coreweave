package cks

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// kubeAPIAccessCIDRFixture returns count distinct, canonical network prefixes.
// It alternates IPv4 /24 and IPv6 /48 networks so both families appear at every size.
func kubeAPIAccessCIDRFixture(count int) []string {
	cidrs := make([]string, count)
	for i := range cidrs {
		n := i / 2
		if i%2 == 0 {
			cidrs[i] = fmt.Sprintf("10.%d.%d.0/24", n/256, n%256)
		} else {
			cidrs[i] = fmt.Sprintf("2001:db8:%x::/48", n+1)
		}
	}
	return cidrs
}

const (
	testCACertPEM = "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ0FfRklSU1Q=\n-----END CERTIFICATE-----\n"
	testCAPEM     = testCACertPEM + "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ0FfU0VDT05E\n-----END CERTIFICATE-----\n"
	testEndpoint  = "https://kube-api.example.test:6443"
)

func TestKubeAPIAccessCIDRFixture(t *testing.T) {
	t.Parallel()
	cidrs := kubeAPIAccessCIDRFixture(1025)
	require.Len(t, cidrs, 1025)
	seen := map[netip.Prefix]bool{}
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		require.NoError(t, err, cidr)
		require.Equal(t, prefix.Masked(), prefix, "fixture %s must not set host bits", cidr)
		require.False(t, seen[prefix], "fixture %s must be distinct", cidr)
		seen[prefix] = true
	}
}

func TestKubeAPIAccessFromProtoPreservesPresence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		config *cksv1beta1.KubeAPIAccess
	}{
		{name: "absent outer"},
		{name: "empty outer", config: &cksv1beta1.KubeAPIAccess{}},
		{name: "empty public", config: testAccess(nil, nil)},
		{name: "allow only", config: testAccess([]string{"203.0.113.0/24", "2001:db8::/32"}, nil)},
		{name: "deny only", config: testAccess(nil, []string{"203.0.113.10/32"})},
		{name: "combined", config: testAccess([]string{"0.0.0.0/0", "::/0"}, []string{"203.0.113.0/24", "2001:db8::/32"})},
		{name: "verbatim spelling", config: testAccess([]string{"2001:0DB8::/32"}, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := kubeAPIAccessFromProto(tc.config)
			expected := testAccessObject(tc.config)
			require.True(t, expected.Equal(actual), "want %v, got %v", expected, actual)
			if tc.config != nil && tc.config.Public != nil {
				public := actual.Attributes()["public"].(types.Object)
				for _, name := range []string{"allow_cidrs", "deny_cidrs"} {
					require.False(t, public.Attributes()[name].IsNull(), "%s must normalize to an empty set inside a present public object", name)
				}
			}
		})
	}
}

func TestKubeAPIAccessFromProtoDeduplicatesCIDRs(t *testing.T) {
	t.Parallel()
	actual := kubeAPIAccessFromProto(testAccess(
		[]string{"203.0.113.0/24", "2001:db8::/32", "203.0.113.0/24"},
		[]string{"203.0.113.10/32", "203.0.113.10/32"},
	))
	expected := testAccessObject(testAccess([]string{"203.0.113.0/24", "2001:db8::/32"}, []string{"203.0.113.10/32"}))
	require.True(t, expected.Equal(actual), "want %v, got %v", expected, actual)
	public := actual.Attributes()["public"].(types.Object)
	require.Len(t, public.Attributes()["allow_cidrs"].(types.Set).Elements(), 2)
	require.Len(t, public.Attributes()["deny_cidrs"].(types.Set).Elements(), 1)
}

func TestKubeAPIAccessToProtoPreservesPresence(t *testing.T) {
	t.Parallel()
	null := types.SetNull(types.StringType)
	for _, tc := range []struct {
		name  string
		value types.Object
		want  *cksv1beta1.KubeAPIAccess
	}{
		{name: "null outer", value: types.ObjectNull(kubeAPIAccessAttrTypes)},
		{name: "unknown outer", value: types.ObjectUnknown(kubeAPIAccessAttrTypes)},
		{name: "null public", value: testOuter(types.ObjectNull(publicKubeAPIAccessAttrTypes)), want: &cksv1beta1.KubeAPIAccess{}},
		{name: "null sets", value: testOuter(testPublic(null, null)), want: testAccess(nil, nil)},
		{name: "empty sets", value: testOuter(testPublic(testCIDRSet(), testCIDRSet())), want: testAccess(nil, nil)},
		{name: "allow only", value: testOuter(testPublic(testCIDRSet("203.0.113.0/24"), null)), want: testAccess([]string{"203.0.113.0/24"}, nil)},
		{name: "deny only", value: testOuter(testPublic(null, testCIDRSet("203.0.113.10/32"))), want: testAccess(nil, []string{"203.0.113.10/32"})},
		{
			name:  "sorted serialization",
			value: testOuter(testPublic(testCIDRSet("2001:db8::/32", "203.0.113.0/24", "192.0.2.0/24"), testCIDRSet("::/0", "0.0.0.0/0"))),
			want:  testAccess([]string{"192.0.2.0/24", "2001:db8::/32", "203.0.113.0/24"}, []string{"0.0.0.0/0", "::/0"}),
		},
		{name: "verbatim spelling", value: testOuter(testPublic(testCIDRSet("2001:0DB8::/32"), null)), want: testAccess([]string{"2001:0DB8::/32"}, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := kubeAPIAccessToProto(tc.value)
			requireAccessProto(t, tc.want, actual)
			if actual != nil {
				// Deterministic request serialization, independent of set iteration order.
				require.True(t, slices.IsSorted(actual.GetPublic().GetAllowCidrs()))
				require.True(t, slices.IsSorted(actual.GetPublic().GetDenyCidrs()))
			}
		})
	}
}

func TestKubeAPIAccessProtoRoundTrip(t *testing.T) {
	t.Parallel()
	for _, config := range []*cksv1beta1.KubeAPIAccess{
		nil,
		{},
		testAccess(nil, nil),
		testAccess([]string{"203.0.113.0/24"}, nil),
		testAccess(nil, []string{"2001:db8::/32"}),
		testAccess([]string{"203.0.113.0/24", "2001:db8::/32"}, []string{"203.0.113.10/32"}),
	} {
		requireAccessProto(t, config, kubeAPIAccessToProto(kubeAPIAccessFromProto(config)))
	}
}

func TestKubeAPIAccessStatusFromProto(t *testing.T) {
	t.Parallel()
	ca := types.StringValue(base64.StdEncoding.EncodeToString([]byte(testCAPEM)))
	for _, tc := range []struct {
		name   string
		status *cksv1beta1.KubeAPIAccessStatus
		want   types.Object
	}{
		{name: "absent status", want: types.ObjectNull(kubeAPIAccessStatusAttrTypes)},
		{name: "empty status", status: &cksv1beta1.KubeAPIAccessStatus{}, want: testStatus(types.ObjectNull(publicKubeAPIAccessStatusAttrTypes), types.StringNull())},
		{name: "empty CA bytes", status: &cksv1beta1.KubeAPIAccessStatus{CaData: []byte{}}, want: testStatus(types.ObjectNull(publicKubeAPIAccessStatusAttrTypes), types.StringNull())},
		{name: "CA only", status: &cksv1beta1.KubeAPIAccessStatus{CaData: []byte(testCAPEM)}, want: testStatus(types.ObjectNull(publicKubeAPIAccessStatusAttrTypes), ca)},
		{
			name:   "enum zero without endpoint",
			status: &cksv1beta1.KubeAPIAccessStatus{Public: &cksv1beta1.PublicKubeAPIAccessStatus{}},
			want:   testStatus(testPublicStatus(types.StringNull(), "STATUS_UNSPECIFIED"), types.StringNull()),
		},
		{
			name:   "status without endpoint",
			status: &cksv1beta1.KubeAPIAccessStatus{Public: testPublicStatusProto(nil, cksv1beta1.PublicKubeAPIAccessStatus_STATUS_PENDING)},
			want:   testStatus(testPublicStatus(types.StringNull(), "STATUS_PENDING"), types.StringNull()),
		},
		{
			name:   "endpoint and CA",
			status: &cksv1beta1.KubeAPIAccessStatus{Public: testPublicStatusProto(proto.String(testEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_ACCEPTED), CaData: []byte(testCAPEM)},
			want:   testStatus(testPublicStatus(types.StringValue(testEndpoint), "STATUS_ACCEPTED"), ca),
		},
		{
			// Future enum values must surface rather than fail the read.
			name:   "unrecognized enum",
			status: &cksv1beta1.KubeAPIAccessStatus{Public: testPublicStatusProto(nil, cksv1beta1.PublicKubeAPIAccessStatus_Status(42))},
			want:   testStatus(testPublicStatus(types.StringNull(), "42"), types.StringNull()),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := kubeAPIAccessStatusFromProto(tc.status)
			require.True(t, tc.want.Equal(actual), "want %v, got %v", tc.want, actual)
		})
	}
	for value, name := range cksv1beta1.PublicKubeAPIAccessStatus_Status_name {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status := &cksv1beta1.KubeAPIAccessStatus{Public: testPublicStatusProto(proto.String(testEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_Status(value))}
			want := testStatus(testPublicStatus(types.StringValue(testEndpoint), name), types.StringNull())
			actual := kubeAPIAccessStatusFromProto(status)
			require.True(t, want.Equal(actual), "want %v, got %v", want, actual)
		})
	}
}

func TestKubeAPIAccessStatusCAEncodedOnce(t *testing.T) {
	t.Parallel()
	// Bytes 0xfb 0xff 0xfe encode to "+//+", proving standard (not URL) encoding with padding.
	for _, raw := range [][]byte{[]byte(testCAPEM), append([]byte(testCAPEM), 0xfb, 0xff, 0xfe, 0x00)} {
		status := kubeAPIAccessStatusFromProto(&cksv1beta1.KubeAPIAccessStatus{CaData: raw})
		require.False(t, status.IsNull())
		encoded := status.Attributes()["ca_data"].(types.String)
		require.False(t, encoded.IsNull())
		require.Equal(t, base64.StdEncoding.EncodeToString(raw), encoded.ValueString())
		decoded, err := base64.StdEncoding.DecodeString(encoded.ValueString())
		require.NoError(t, err)
		require.Equal(t, raw, decoded, "a single base64decode must yield the API bytes exactly")
	}
}

func TestClusterSetReplacesKubeAPIAccessObservations(t *testing.T) {
	t.Parallel()
	model := kubeAPIAccessRequestModel(testAccess([]string{"203.0.113.0/24"}, nil))
	model.KubeAPIAccessStatus = testStatus(testPublicStatus(types.StringValue(testEndpoint), "STATUS_ACCEPTED"), types.StringValue("c3RhbGU="))

	// The API dropped public access and endpoint observations but still reports a CA.
	model.Set(&cksv1beta1.Cluster{Id: "test-cluster", KubeApiAccessStatus: &cksv1beta1.KubeAPIAccessStatus{CaData: []byte(testCAPEM)}})
	require.True(t, model.KubeAPIAccess.IsNull(), "absent API configuration must clear stale state")
	wantStatus := testStatus(types.ObjectNull(publicKubeAPIAccessStatusAttrTypes), types.StringValue(base64.StdEncoding.EncodeToString([]byte(testCAPEM))))
	require.True(t, wantStatus.Equal(model.KubeAPIAccessStatus), "got %v", model.KubeAPIAccessStatus)

	model.Set(&cksv1beta1.Cluster{Id: "test-cluster", KubeApiAccess: &cksv1beta1.KubeAPIAccess{}})
	require.True(t, testAccessObject(&cksv1beta1.KubeAPIAccess{}).Equal(model.KubeAPIAccess), "got %v", model.KubeAPIAccess)
	require.True(t, model.KubeAPIAccessStatus.IsNull(), "absent API status must clear stale state")
}

func TestKubeAPIAccessCreateRequest(t *testing.T) {
	t.Parallel()
	for _, config := range []*cksv1beta1.KubeAPIAccess{
		nil,
		{},
		testAccess(nil, nil),
		testAccess([]string{"203.0.113.0/24"}, nil),
		testAccess(nil, []string{"203.0.113.10/32"}),
		testAccess([]string{"203.0.113.0/24", "2001:db8::/32"}, []string{"203.0.113.10/32"}),
	} {
		model := kubeAPIAccessRequestModel(config)
		request := model.ToCreateRequest(t.Context())
		requireAccessProto(t, config, request.KubeApiAccess)
		require.False(t, request.Public, "direct public access must not enable legacy public ingress")
	}
}

func TestKubeAPIAccessUpdateRequests(t *testing.T) {
	t.Parallel()
	allow := []string{"203.0.113.0/24", "2001:db8::/32"}
	deny := []string{"203.0.113.10/32", "2001:db8::10/128"}
	initial := testAccess(allow, deny)
	for _, tc := range []struct {
		name    string
		from    *cksv1beta1.KubeAPIAccess
		desired *cksv1beta1.KubeAPIAccess
	}{
		{name: "clear outer", from: initial},
		{name: "remove public", from: initial, desired: &cksv1beta1.KubeAPIAccess{}},
		{name: "clear both sets while enabled", from: initial, desired: testAccess(nil, nil)},
		{name: "clear allow keeps deny", from: initial, desired: testAccess(nil, deny)},
		{name: "clear deny keeps allow", from: initial, desired: testAccess(allow, nil)},
		{name: "replace allow", from: initial, desired: testAccess([]string{"192.0.2.0/24"}, deny)},
		{name: "explicit empty outer from null", desired: &cksv1beta1.KubeAPIAccess{}},
		{name: "enable all from null", desired: testAccess(nil, nil)},
		{name: "deny-only enable uses parent", from: &cksv1beta1.KubeAPIAccess{}, desired: testAccess(nil, []string{"198.51.100.0/24"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := kubeAPIAccessRequestModel(tc.from)
			plan := kubeAPIAccessRequestModel(tc.desired)
			request := buildUpdateRequest(t.Context(), &plan, &state)
			require.Equal(t, []string{"kube_api_access"}, request.UpdateMask.Paths)
			requireAccessProto(t, tc.desired, request.KubeApiAccess)
			require.Empty(t, request.Version)
			require.Nil(t, request.Network)
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(plan, state *ClusterResourceModel)
		paths  []string
	}{
		{name: "no change", mutate: func(_, _ *ClusterResourceModel) {}},
		{name: "version only", mutate: func(plan, _ *ClusterResourceModel) { plan.Version = types.StringValue("v1.34") }, paths: []string{"version"}},
		{name: "legacy public only", mutate: func(plan, _ *ClusterResourceModel) { plan.Public = types.BoolValue(true) }, paths: []string{"public"}},
		{name: "audit policy only", mutate: func(plan, _ *ClusterResourceModel) { plan.AuditPolicy = types.StringValue("dGVzdA==") }, paths: []string{"audit_policy"}},
		{
			name: "API CIDR order",
			mutate: func(_, state *ClusterResourceModel) {
				reversed := func(cidrs []string) types.Set {
					cidrs = slices.Clone(cidrs)
					slices.Reverse(cidrs)
					return testCIDRSet(cidrs...)
				}
				state.KubeAPIAccess = testOuter(testPublic(reversed(allow), reversed(deny)))
			},
		},
		{
			name: "observed status only",
			mutate: func(plan, state *ClusterResourceModel) {
				plan.KubeAPIAccessStatus = types.ObjectUnknown(kubeAPIAccessStatusAttrTypes)
				state.KubeAPIAccessStatus = testStatus(testPublicStatus(types.StringValue(testEndpoint), "STATUS_ACCEPTED"), types.StringNull())
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := kubeAPIAccessRequestModel(initial)
			plan := kubeAPIAccessRequestModel(initial)
			tc.mutate(&plan, &state)
			request := buildUpdateRequest(t.Context(), &plan, &state)
			require.Equal(t, tc.paths, request.UpdateMask.Paths)
			require.Nil(t, request.KubeApiAccess, "unrelated updates must omit the access body")
		})
	}
}

func TestKubeAPIAccessUpgradeRestriction(t *testing.T) {
	t.Parallel()
	policy := testAccess([]string{"203.0.113.0/24"}, nil)
	for _, tc := range []struct {
		name          string
		from, to      *cksv1beta1.KubeAPIAccess
		versionChange bool
		invalid       bool
	}{
		{name: "set during upgrade", to: policy, versionChange: true, invalid: true},
		{name: "explicit empty during upgrade", to: &cksv1beta1.KubeAPIAccess{}, versionChange: true, invalid: true},
		{name: "change during upgrade", from: policy, to: testAccess(nil, []string{"192.0.2.0/24"}), versionChange: true, invalid: true},
		{name: "clear during upgrade", from: policy, versionChange: true, invalid: true},
		{name: "upgrade with unchanged policy", from: policy, to: policy, versionChange: true},
		{name: "change without upgrade", from: policy, to: testAccess(nil, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := kubeAPIAccessRequestModel(tc.from)
			plan := kubeAPIAccessRequestModel(tc.to)
			plan.KubeAPIAccessStatus = types.ObjectUnknown(kubeAPIAccessStatusAttrTypes)
			if tc.versionChange {
				plan.Version = types.StringValue("v1.34")
			}
			diagnostics := validateKubeAPIAccessUpgrade(&plan, &state)
			require.Equal(t, tc.invalid, diagnostics.HasError(), "%v", diagnostics)
			if tc.invalid {
				requireDiagnosticPath(t, diagnostics, path.Root("kube_api_access"))
			}
		})
	}
}

func TestKubeAPIAccessUnknownsDeferUntilApply(t *testing.T) {
	t.Parallel()
	valid := testCIDRSet("203.0.113.0/24")
	for _, tc := range []struct {
		name  string
		value types.Object
	}{
		{name: "outer", value: types.ObjectUnknown(kubeAPIAccessAttrTypes)},
		{name: "public", value: testOuter(types.ObjectUnknown(publicKubeAPIAccessAttrTypes))},
		{name: "allow set", value: testOuter(testPublic(types.SetUnknown(types.StringType), valid))},
		{name: "deny set", value: testOuter(testPublic(valid, types.SetUnknown(types.StringType)))},
		{name: "allow member", value: testOuter(testPublic(types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}), valid))},
		{name: "deny member", value: testOuter(testPublic(valid, types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()})))},
		{
			// An unknown member may resolve to a duplicate, so the count cannot be enforced yet.
			name:  "unknown member at limit",
			value: testOuter(testPublic(testCIDRSetWith(kubeAPIAccessCIDRFixture(1024), types.StringUnknown()), valid)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			response := validator.ObjectResponse{}
			kubeAPIAccessValidator{}.ValidateObject(t.Context(), validator.ObjectRequest{
				ConfigValue: tc.value, Path: path.Root("kube_api_access"),
			}, &response)
			require.False(t, response.Diagnostics.HasError(), "unknown values must defer during config validation: %v", response.Diagnostics)
			require.True(t, validateKubeAPIAccessForApply(t.Context(), tc.value).HasError(), "unknown values must be rejected before any RPC")
		})
	}
}

func TestKubeAPIAccessValidation(t *testing.T) {
	t.Parallel()
	limit := kubeAPIAccessCIDRFixture(1025)
	empty := testCIDRSet()
	null := types.SetNull(types.StringType)
	for _, tc := range []struct {
		name  string
		value types.Object
	}{
		{name: "null outer", value: types.ObjectNull(kubeAPIAccessAttrTypes)},
		{name: "null public", value: testOuter(types.ObjectNull(publicKubeAPIAccessAttrTypes))},
		{name: "null sets", value: testOuter(testPublic(null, null))},
		{name: "empty sets", value: testOuter(testPublic(empty, empty))},
		{name: "dual stack", value: testOuter(testPublic(testCIDRSet("203.0.113.0/24", "2001:db8::/32"), testCIDRSet("203.0.113.10/32", "2001:db8::10/128")))},
		{name: "all sources", value: testOuter(testPublic(testCIDRSet("0.0.0.0/0", "::/0"), empty))},
		{name: "complete denial", value: testOuter(testPublic(testCIDRSet("0.0.0.0/0", "::/0"), testCIDRSet("0.0.0.0/0", "::/0")))},
		{name: "deny only", value: testOuter(testPublic(null, testCIDRSet("192.0.2.0/24")))},
		{name: "verbatim IPv6 spelling", value: testOuter(testPublic(testCIDRSet("2001:0DB8::/32"), empty))},
		{name: "1024 allow", value: testOuter(testPublic(testCIDRSet(limit[:1024]...), empty))},
		{name: "1024 deny", value: testOuter(testPublic(empty, testCIDRSet(limit[:1024]...)))},
		{name: "1024 each", value: testOuter(testPublic(testCIDRSet(limit[:1024]...), testCIDRSet(limit[1:]...)))},
	} {
		t.Run("valid/"+tc.name, func(t *testing.T) {
			t.Parallel()
			require.False(t, validateKubeAPIAccessConfig(t, tc.value).HasError())
			require.False(t, validateKubeAPIAccessForApply(t.Context(), tc.value).HasError())
		})
	}

	invalidCIDRs := []struct {
		name  string
		value types.String
	}{
		{name: "malformed", value: types.StringValue("not-a-cidr")},
		{name: "empty string", value: types.StringValue("")},
		{name: "IPv4 missing mask", value: types.StringValue("203.0.113.1")},
		{name: "IPv6 missing mask", value: types.StringValue("2001:db8::1")},
		{name: "IPv4 host bits", value: types.StringValue("203.0.113.7/24")},
		{name: "IPv6 host bits", value: types.StringValue("2001:db8::1/32")},
		{name: "IPv4 leading zero octet", value: types.StringValue("010.0.0.0/8")},
		{name: "leading zero prefix length", value: types.StringValue("10.0.0.0/08")},
		{name: "IPv4 prefix too long", value: types.StringValue("203.0.113.0/33")},
		{name: "IPv6 prefix too long", value: types.StringValue("2001:db8::/129")},
		{name: "IPv6 zone", value: types.StringValue("fe80::%eth0/64")},
		{name: "surrounding whitespace", value: types.StringValue(" 203.0.113.0/24")},
		{name: "null element", value: types.StringNull()},
	}
	for _, field := range []string{"allow_cidrs", "deny_cidrs"} {
		fieldPath := path.Root("kube_api_access").AtName("public").AtName(field)
		for _, tc := range invalidCIDRs {
			t.Run(fmt.Sprintf("invalid/%s/%s", field, tc.name), func(t *testing.T) {
				t.Parallel()
				value := testOuterWithField(field, types.SetValueMust(types.StringType, []attr.Value{types.StringValue("192.0.2.0/24"), tc.value}))
				for _, diagnostics := range []diag.Diagnostics{validateKubeAPIAccessConfig(t, value), validateKubeAPIAccessForApply(t.Context(), value)} {
					require.True(t, diagnostics.HasError(), "%s must be rejected", tc.value)
					requireDiagnosticPath(t, diagnostics, fieldPath)
				}
			})
		}
		t.Run(fmt.Sprintf("invalid/%s/1025 entries", field), func(t *testing.T) {
			t.Parallel()
			value := testOuterWithField(field, testCIDRSet(limit...))
			for _, diagnostics := range []diag.Diagnostics{validateKubeAPIAccessConfig(t, value), validateKubeAPIAccessForApply(t.Context(), value)} {
				require.True(t, diagnostics.HasError())
				requireDiagnosticPath(t, diagnostics, fieldPath)
				require.Contains(t, diagnostics[0].Detail(), "1024")
			}
		})
	}
}

func validateKubeAPIAccessConfig(t *testing.T, value types.Object) diag.Diagnostics {
	t.Helper()
	response := validator.ObjectResponse{}
	kubeAPIAccessValidator{}.ValidateObject(t.Context(), validator.ObjectRequest{ConfigValue: value, Path: path.Root("kube_api_access")}, &response)
	return response.Diagnostics
}

// requireDiagnosticPath asserts that an error targets want or one of its descendants.
func requireDiagnosticPath(t *testing.T, diagnostics diag.Diagnostics, want path.Path) {
	t.Helper()
	for _, d := range diagnostics.Errors() {
		if withPath, ok := d.(diag.DiagnosticWithPath); ok && strings.HasPrefix(withPath.Path().String(), want.String()) {
			return
		}
	}
	require.Failf(t, "missing diagnostic path", "no error targets %s: %v", want, diagnostics)
}

func requireAccessProto(t *testing.T, want, got *cksv1beta1.KubeAPIAccess) {
	t.Helper()
	if want == nil {
		require.Nil(t, got)
		return
	}
	require.NotNil(t, got, "present configuration must be sent")
	require.Equal(t, want.Public == nil, got.Public == nil, "public presence changed: want %v, got %v", want, got)
	require.ElementsMatch(t, want.GetPublic().GetAllowCidrs(), got.GetPublic().GetAllowCidrs())
	require.ElementsMatch(t, want.GetPublic().GetDenyCidrs(), got.GetPublic().GetDenyCidrs())
}

func testAccess(allow, deny []string) *cksv1beta1.KubeAPIAccess {
	return &cksv1beta1.KubeAPIAccess{Public: &cksv1beta1.PublicKubeAPIAccess{AllowCidrs: allow, DenyCidrs: deny}}
}

func testPublicStatusProto(endpoint *string, status cksv1beta1.PublicKubeAPIAccessStatus_Status) *cksv1beta1.PublicKubeAPIAccessStatus {
	return &cksv1beta1.PublicKubeAPIAccessStatus{Endpoint: endpoint, Status: status}
}

func testCIDRSet(cidrs ...string) types.Set {
	return testCIDRSetWith(cidrs)
}

func testCIDRSetWith(cidrs []string, extra ...attr.Value) types.Set {
	values := make([]attr.Value, 0, len(cidrs)+len(extra))
	for _, cidr := range cidrs {
		values = append(values, types.StringValue(cidr))
	}
	return types.SetValueMust(types.StringType, append(values, extra...))
}

func testPublic(allow, deny types.Set) types.Object {
	return types.ObjectValueMust(publicKubeAPIAccessAttrTypes, map[string]attr.Value{"allow_cidrs": allow, "deny_cidrs": deny})
}

func testOuter(public types.Object) types.Object {
	return types.ObjectValueMust(kubeAPIAccessAttrTypes, map[string]attr.Value{"public": public})
}

// testOuterWithField builds an enabled policy with value in field and an empty opposite set.
func testOuterWithField(field string, value types.Set) types.Object {
	if field == "allow_cidrs" {
		return testOuter(testPublic(value, testCIDRSet()))
	}
	return testOuter(testPublic(testCIDRSet(), value))
}

// testAccessObject is an independent oracle for the expected Terraform value of an API configuration.
func testAccessObject(config *cksv1beta1.KubeAPIAccess) types.Object {
	if config == nil {
		return types.ObjectNull(kubeAPIAccessAttrTypes)
	}
	if config.Public == nil {
		return testOuter(types.ObjectNull(publicKubeAPIAccessAttrTypes))
	}
	return testOuter(testPublic(testCIDRSet(config.Public.AllowCidrs...), testCIDRSet(config.Public.DenyCidrs...)))
}

func testStatus(public types.Object, ca types.String) types.Object {
	return types.ObjectValueMust(kubeAPIAccessStatusAttrTypes, map[string]attr.Value{"public": public, "ca_data": ca})
}

func testPublicStatus(endpoint types.String, status string) types.Object {
	return types.ObjectValueMust(publicKubeAPIAccessStatusAttrTypes, map[string]attr.Value{"endpoint": endpoint, "status": types.StringValue(status)})
}

func kubeAPIAccessRequestModel(config *cksv1beta1.KubeAPIAccess) ClusterResourceModel {
	return ClusterResourceModel{
		Id: types.StringValue("test-cluster"), Version: types.StringValue("v1.33"), Public: types.BoolValue(false),
		InternalLBCidrNames: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("lb")}),
		NodePortRange:       types.ObjectNull(map[string]attr.Type{"start": types.Int32Type, "end": types.Int32Type}),
		AuditPolicy:         types.StringNull(), AdditionalServerSans: types.SetNull(types.StringType),
		Kubelet:             jsontypes.NewNormalizedNull(),
		KubeAPIAccess:       testAccessObject(config),
		KubeAPIAccessStatus: types.ObjectNull(kubeAPIAccessStatusAttrTypes),
	}
}
