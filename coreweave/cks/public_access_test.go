package cks

import (
	"fmt"
	"slices"
	"testing"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestPublicAccessPresenceRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		config *cksv1beta1.PublicAccessConfig
		mode   types.String
	}{
		{name: "absent", mode: types.StringNull()},
		{name: "empty configuration", config: &cksv1beta1.PublicAccessConfig{}, mode: types.StringNull()},
		{name: "explicit unspecified", config: publicAccessConfig(cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum()), mode: types.StringValue("MODE_UNSPECIFIED")},
		{name: "explicit disabled", config: publicAccessConfig(cksv1beta1.PublicAccessConfig_DISABLED.Enum()), mode: types.StringValue("DISABLED")},
		{name: "absent mode with CIDRs", config: publicAccessConfig(nil, "203.0.113.0/24"), mode: types.StringNull()},
		{name: "unspecified retains CIDRs", config: publicAccessConfig(cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum(), "203.0.113.0/24"), mode: types.StringValue("MODE_UNSPECIFIED")},
		{name: "disabled retains CIDRs", config: publicAccessConfig(cksv1beta1.PublicAccessConfig_DISABLED.Enum(), "203.0.113.0/24"), mode: types.StringValue("DISABLED")},
		{name: "TLS dual stack", config: publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24", "2001:db8::/32"), mode: types.StringValue("TLS")},
		{name: "explicit all sources", config: publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "0.0.0.0/0", "::/0"), mode: types.StringValue("TLS")},
		{name: "preserve valid spelling", config: publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.7/24", "2001:0DB8:0000::/32"), mode: types.StringValue("TLS")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value := publicAccessFromProto(tc.config)
			actual := publicAccessToProto(value)
			var expected *cksv1beta1.PublicAccessConfig
			if tc.config != nil {
				expected = proto.Clone(tc.config).(*cksv1beta1.PublicAccessConfig)
				slices.Sort(expected.AllowCidrs)
			}
			require.True(t, proto.Equal(expected, actual), "presence changed: want %v, got %v", expected, actual)
			require.False(t, value.IsUnknown())
			if tc.config == nil {
				require.True(t, value.IsNull())
				return
			}
			require.False(t, value.IsNull())
			require.Equal(t, tc.mode, value.Attributes()["mode"])
			cidrs := value.Attributes()["allow_cidrs"].(types.Set)
			require.False(t, cidrs.IsNull(), "present configuration canonicalizes omitted CIDRs to an empty set")
			expectedCIDRs := make([]attr.Value, len(tc.config.AllowCidrs))
			for i, cidr := range tc.config.AllowCidrs {
				expectedCIDRs[i] = types.StringValue(cidr)
			}
			require.True(t, cidrs.Equal(types.SetValueMust(types.StringType, expectedCIDRs)))
		})
	}
}

func TestPublicAccessEmptyCIDRsPreserveConfiguration(t *testing.T) {
	t.Parallel()
	for _, cidrs := range []types.Set{types.SetNull(types.StringType), types.SetValueMust(types.StringType, []attr.Value{})} {
		for _, tc := range []struct {
			mode types.String
			want *cksv1beta1.PublicAccessConfig_Mode
		}{
			{mode: types.StringNull()},
			{mode: types.StringValue("MODE_UNSPECIFIED"), want: cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum()},
			{mode: types.StringValue("DISABLED"), want: cksv1beta1.PublicAccessConfig_DISABLED.Enum()},
		} {
			value := publicAccessObject(tc.mode, cidrs)
			actual := publicAccessToProto(value)
			require.NotNil(t, actual)
			require.Equal(t, tc.want, actual.Mode)
			require.Empty(t, actual.AllowCidrs)
		}
	}
}

func TestPublicAccessUpdateRequests(t *testing.T) {
	t.Parallel()
	initial := publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24")
	for _, tc := range []struct {
		name    string
		desired *cksv1beta1.PublicAccessConfig
	}{
		{name: "clear entire configuration"},
		{name: "empty disables and clears CIDRs", desired: &cksv1beta1.PublicAccessConfig{}},
		{name: "replace CIDRs", desired: publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "2001:db8::/32")},
		{name: "explicit all sources", desired: publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "0.0.0.0/0", "::/0")},
		{name: "disable retains CIDRs", desired: publicAccessConfig(cksv1beta1.PublicAccessConfig_DISABLED.Enum(), "203.0.113.0/24")},
		{name: "unspecified retains CIDRs", desired: publicAccessConfig(cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum(), "203.0.113.0/24")},
		{name: "disable clears CIDRs", desired: publicAccessConfig(cksv1beta1.PublicAccessConfig_DISABLED.Enum())},
		{name: "clear mode presence", desired: publicAccessConfig(nil, "203.0.113.0/24")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := publicAccessRequestModel(initial)
			plan := publicAccessRequestModel(tc.desired)
			request := buildUpdateRequest(t.Context(), &plan, &state)
			require.Equal(t, []string{"public_access"}, request.UpdateMask.Paths)
			require.True(t, proto.Equal(tc.desired, request.PublicAccess))
			require.Empty(t, request.Version)
			require.Nil(t, request.Network)
		})
	}
	t.Run("version only omits configuration", func(t *testing.T) {
		state := publicAccessRequestModel(initial)
		plan := publicAccessRequestModel(initial)
		plan.Version = types.StringValue("v1.34")
		request := buildUpdateRequest(t.Context(), &plan, &state)
		require.Equal(t, []string{"version"}, request.UpdateMask.Paths)
		require.Nil(t, request.PublicAccess)
	})
	t.Run("legacy public only omits configuration", func(t *testing.T) {
		state := publicAccessRequestModel(initial)
		plan := publicAccessRequestModel(initial)
		plan.Public = types.BoolValue(true)
		request := buildUpdateRequest(t.Context(), &plan, &state)
		require.Equal(t, []string{"public"}, request.UpdateMask.Paths)
		require.True(t, request.Public)
		require.Nil(t, request.PublicAccess)
	})
	t.Run("no change omits configuration", func(t *testing.T) {
		state := publicAccessRequestModel(initial)
		plan := publicAccessRequestModel(initial)
		request := buildUpdateRequest(t.Context(), &plan, &state)
		require.Empty(t, request.UpdateMask.Paths)
		require.Nil(t, request.PublicAccess)
	})
	t.Run("API CIDR order does not cause an update", func(t *testing.T) {
		state := publicAccessRequestModel(publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "2001:db8::/32", "203.0.113.0/24"))
		plan := publicAccessRequestModel(publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24", "2001:db8::/32"))
		request := buildUpdateRequest(t.Context(), &plan, &state)
		require.Empty(t, request.UpdateMask.Paths)
		require.Nil(t, request.PublicAccess)
	})
}

func TestPublicAccessCreateRequest(t *testing.T) {
	t.Parallel()
	for _, config := range []*cksv1beta1.PublicAccessConfig{
		nil,
		{},
		publicAccessConfig(cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum()),
		publicAccessConfig(cksv1beta1.PublicAccessConfig_DISABLED.Enum()),
		publicAccessConfig(nil, "203.0.113.0/24"),
		publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24"),
	} {
		model := publicAccessRequestModel(config)
		request := model.ToCreateRequest(t.Context())
		require.True(t, proto.Equal(config, request.PublicAccess))
		require.False(t, request.Public, "direct public access must not enable legacy public ingress")
	}
}

func TestPublicAccessRequiresKnownApplyValues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value types.Object
	}{
		{name: "configuration", value: types.ObjectUnknown(publicAccessAttrTypes)},
		{name: "mode", value: publicAccessObject(types.StringUnknown(), types.SetValueMust(types.StringType, []attr.Value{}))},
		{name: "CIDR set", value: publicAccessObject(types.StringValue("TLS"), types.SetUnknown(types.StringType))},
		{name: "CIDR element", value: publicAccessObject(types.StringValue("TLS"), types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			response := validator.ObjectResponse{}
			publicAccessValidator{}.ValidateObject(t.Context(), validator.ObjectRequest{
				ConfigValue: tc.value, Path: path.Root("public_access"),
			}, &response)
			require.False(t, response.Diagnostics.HasError(), "unknown values must defer during config validation: %v", response.Diagnostics)
			require.True(t, validatePublicAccessForApply(t.Context(), tc.value).HasError())
		})
	}
}

func TestPublicAccessApplyValidation(t *testing.T) {
	t.Parallel()
	prefixes := make([]attr.Value, 101)
	for i := range prefixes {
		prefixes[i] = types.StringValue(fmt.Sprintf("10.0.%d.0/24", i))
	}
	empty := types.SetValueMust(types.StringType, []attr.Value{})
	validCIDRs := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("203.0.113.0/24")})
	for _, tc := range []struct {
		name    string
		value   types.Object
		invalid bool
	}{
		{name: "absent", value: types.ObjectNull(publicAccessAttrTypes)},
		{name: "empty disabled", value: publicAccessFromProto(&cksv1beta1.PublicAccessConfig{})},
		{name: "explicit unspecified null", value: publicAccessObject(types.StringValue("MODE_UNSPECIFIED"), types.SetNull(types.StringType))},
		{name: "explicit unspecified empty", value: publicAccessObject(types.StringValue("MODE_UNSPECIFIED"), empty)},
		{name: "explicit disabled null", value: publicAccessObject(types.StringValue("DISABLED"), types.SetNull(types.StringType))},
		{name: "explicit disabled empty", value: publicAccessObject(types.StringValue("DISABLED"), empty)},
		{name: "TLS null", value: publicAccessObject(types.StringValue("TLS"), types.SetNull(types.StringType)), invalid: true},
		{name: "TLS empty", value: publicAccessObject(types.StringValue("TLS"), empty), invalid: true},
		{name: "valid spelling", value: publicAccessFromProto(publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.7/24", "2001:0DB8:0000::/32"))},
		{name: "explicit all sources", value: publicAccessFromProto(publicAccessConfig(cksv1beta1.PublicAccessConfig_TLS.Enum(), "0.0.0.0/0", "::/0"))},
		{name: "invalid even disabled", value: publicAccessFromProto(publicAccessConfig(cksv1beta1.PublicAccessConfig_DISABLED.Enum(), "invalid")), invalid: true},
		{name: "invalid absent mode", value: publicAccessFromProto(publicAccessConfig(nil, "203.0.113.1")), invalid: true},
		{name: "null even disabled", value: publicAccessObject(types.StringValue("DISABLED"), types.SetValueMust(types.StringType, []attr.Value{types.StringNull()})), invalid: true},
		{name: "100 TLS", value: publicAccessObject(types.StringValue("TLS"), types.SetValueMust(types.StringType, prefixes[:100]))},
		{name: "100 disabled", value: publicAccessObject(types.StringValue("DISABLED"), types.SetValueMust(types.StringType, prefixes[:100]))},
		{name: "101 TLS", value: publicAccessObject(types.StringValue("TLS"), types.SetValueMust(types.StringType, prefixes)), invalid: true},
		{name: "101 disabled", value: publicAccessObject(types.StringValue("DISABLED"), types.SetValueMust(types.StringType, prefixes)), invalid: true},
		{name: "invalid mode", value: publicAccessObject(types.StringValue("MTLS"), validCIDRs), invalid: true},
		{name: "lowercase mode", value: publicAccessObject(types.StringValue("tls"), validCIDRs), invalid: true},
		{name: "numeric mode", value: publicAccessObject(types.StringValue("2"), validCIDRs), invalid: true},
		{name: "empty mode", value: publicAccessObject(types.StringValue(""), validCIDRs), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diagnostics := validatePublicAccessForApply(t.Context(), tc.value)
			require.Equal(t, tc.invalid, diagnostics.HasError(), "%v", diagnostics)
		})
	}
}

func publicAccessConfig(mode *cksv1beta1.PublicAccessConfig_Mode, cidrs ...string) *cksv1beta1.PublicAccessConfig {
	return &cksv1beta1.PublicAccessConfig{Mode: mode, AllowCidrs: cidrs}
}

func publicAccessObject(mode types.String, cidrs types.Set) types.Object {
	return types.ObjectValueMust(publicAccessAttrTypes, map[string]attr.Value{
		"mode": mode, "allow_cidrs": cidrs,
	})
}

func publicAccessRequestModel(config *cksv1beta1.PublicAccessConfig) ClusterResourceModel {
	return ClusterResourceModel{
		Id: types.StringValue("test-cluster"), Version: types.StringValue("v1.33"), Public: types.BoolValue(false),
		InternalLBCidrNames: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("lb")}),
		NodePortRange:       types.ObjectNull(map[string]attr.Type{"start": types.Int32Type, "end": types.Int32Type}),
		AuditPolicy:         types.StringNull(), AdditionalServerSans: types.SetNull(types.StringType),
		Kubelet: jsontypes.NewNormalizedNull(), PublicAccess: publicAccessFromProto(config),
	}
}
