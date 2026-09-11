package sandbox

import (
	"encoding/json"
	"testing"

	sandboxv1 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestManagedRunnerSchemaCoversV1Contract(t *testing.T) {
	t.Parallel()
	var response resource.SchemaResponse
	NewManagedRunnerResource().Schema(t.Context(), resource.SchemaRequest{}, &response)
	require.False(t, response.Schema.ValidateImplementation(t.Context()).HasError())
	assert.NotContains(t, response.Schema.Attributes, "profile_bindings")
	assert.NotContains(t, response.Schema.Attributes, "active_profile_spec_version")
	checkMessageSchema(t, (&sandboxv1.ManagedRunnerSpec{}).ProtoReflect().Descriptor(), runnerSpecAttribute().Attributes)
	checkMessageSchema(t, (&sandboxv1.Policy{}).ProtoReflect().Descriptor(), policyAttribute().Attributes)
}

func checkMessageSchema(t *testing.T, descriptor protoreflect.MessageDescriptor, attributes map[string]schema.Attribute) {
	t.Helper()
	fields := descriptor.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		name := string(field.Name())
		if name == "allow_privileged_profile_annotations" {
			assert.NotContains(t, attributes, name, "legacy profile switches are intentionally excluded")
			continue
		}
		attribute, ok := attributes[name]
		require.True(t, ok, "missing %s.%s", descriptor.FullName(), name)
		switch nested := attribute.(type) {
		case schema.SingleNestedAttribute:
			checkMessageSchema(t, field.Message(), nested.Attributes)
		case schema.ListNestedAttribute:
			checkMessageSchema(t, field.Message(), nested.NestedObject.Attributes)
		}
	}
	for name := range attributes {
		require.NotNil(t, fields.ByName(protoreflect.Name(name)), "unexpected field %s.%s", descriptor.FullName(), name)
	}
}

func policyObject(t *testing.T, policy *sandboxv1.Policy) types.Object {
	t.Helper()
	typ := policyAttribute().GetType().(types.ObjectType)
	value, err := objectFromProto(t.Context(), policy, types.ObjectNull(typ.AttrTypes), typ)
	require.NoError(t, err)
	return value
}

func TestPolicyPresenceRoundTrip(t *testing.T) {
	t.Parallel()
	for name, policy := range map[string]*sandboxv1.Policy{
		"empty posture":               {},
		"empty runtime base":          {Base: &structpb.Struct{Fields: map[string]*structpb.Value{}}},
		"uncapped GPUs":               {Constraints: &sandboxv1.PolicyConstraints{Resources: &sandboxv1.ResourceConstraints{}}},
		"zero GPUs":                   {Constraints: &sandboxv1.PolicyConstraints{Resources: &sandboxv1.ResourceConstraints{MaxGpuCount: proto.Int64(0)}}},
		"large GPU integer":           {Constraints: &sandboxv1.PolicyConstraints{Resources: &sandboxv1.ResourceConstraints{MaxGpuCount: proto.Int64(9007199254740993)}}},
		"deny all public sources":     {ControlPlaneAccess: &sandboxv1.ControlPlaneAccessPolicy{SourceIpAllowlist: &sandboxv1.SourceIpAllowlist{}}},
		"unrestricted public sources": {ControlPlaneAccess: &sandboxv1.ControlPlaneAccessPolicy{}},
	} {
		t.Run(name, func(t *testing.T) {
			value := policyObject(t, policy)
			var actual sandboxv1.Policy
			require.NoError(t, objectToProto(value, &actual))
			assert.True(t, proto.Equal(policy, &actual), "want %v, got %v", policy, &actual)
		})
	}
}

func TestDisabledDataPlaneRoundTrip(t *testing.T) {
	t.Parallel()
	spec := &sandboxv1.ManagedRunnerSpec{DataPlane: &sandboxv1.RunnerDataPlaneConfig{Exposure: &sandboxv1.RunnerDataPlaneConfig_Disabled{Disabled: &sandboxv1.RunnerDataPlaneDisabledConfig{}}}}
	typ := runnerSpecAttribute().GetType().(types.ObjectType)
	value, err := objectFromProto(t.Context(), spec, types.ObjectNull(typ.AttrTypes), typ)
	require.NoError(t, err)
	var actual sandboxv1.ManagedRunnerSpec
	require.NoError(t, objectToProto(value, &actual))
	assert.True(t, proto.Equal(spec, &actual))
}

func TestUpdateMaskAndPolicyEtag(t *testing.T) {
	t.Parallel()
	oldSpec := &sandboxv1.ManagedRunnerSpec{ReleaseChannel: sandboxv1.ReleaseChannel_RELEASE_CHANNEL_STABLE, Volumes: &sandboxv1.RunnerVolumesConfig{Enabled: true}}
	typ := runnerSpecAttribute().GetType().(types.ObjectType)
	oldValue, err := objectFromProto(t.Context(), oldSpec, types.ObjectNull(typ.AttrTypes), typ)
	require.NoError(t, err)
	previous := managedRunnerModel{ID: types.StringValue("runner"), RunnerID: types.StringValue("runner"), Spec: oldValue, Policy: policyObject(t, &sandboxv1.Policy{}), Etag: types.StringValue("etag-7"), DisplayName: types.StringValue("old"), RunnerGroupID: types.StringValue("group")}
	plan := previous
	plan.Policy = policyObject(t, &sandboxv1.Policy{Constraints: &sandboxv1.PolicyConstraints{Resources: &sandboxv1.ResourceConstraints{MaxGpuCount: proto.Int64(0)}}})
	attrs := oldValue.Attributes()
	attrs["volumes"] = types.ObjectNull(attrs["volumes"].(types.Object).AttributeTypes(t.Context()))
	plan.Spec = types.ObjectValueMust(typ.AttrTypes, attrs)
	plan.DisplayName = types.StringNull()
	request, err := plan.updateRequest(&previous)
	require.NoError(t, err)
	assert.Equal(t, []string{"display_name", "policy", "spec.volumes"}, request.UpdateMask.Paths)
	assert.Equal(t, "etag-7", request.ManagedRunner.Etag)
	assert.Empty(t, request.ManagedRunner.DisplayName)
	assert.Nil(t, request.ManagedRunner.Spec.Volumes)
	assert.Equal(t, int64(0), *request.ManagedRunner.Policy.Constraints.Resources.MaxGpuCount)
	assert.NotContains(t, request.UpdateMask.Paths, "identity.runner_id")
	previous.Etag = types.StringNull()
	_, err = plan.updateRequest(&previous)
	require.ErrorContains(t, err, "etag")
}

func TestNoOpUpdateAndUnknownSpec(t *testing.T) {
	t.Parallel()
	model := managedRunnerModel{ID: types.StringValue("runner"), RunnerID: types.StringValue("runner"), Policy: policyObject(t, &sandboxv1.Policy{})}
	model.Spec = types.ObjectUnknown(runnerSpecAttribute().GetType().(types.ObjectType).AttrTypes)
	request, err := model.updateRequest(&model)
	require.NoError(t, err)
	assert.Empty(t, request.UpdateMask.Paths)
	assert.Empty(t, request.ManagedRunner.Etag)
}

func TestReadRejectsIncompleteV1Server(t *testing.T) {
	t.Parallel()
	var model managedRunnerModel
	err := model.setRunner(t.Context(), &sandboxv1.ManagedRunner{Identity: &sandboxv1.RunnerIdentity{RunnerId: "runner"}, Policy: &sandboxv1.Policy{}})
	require.ErrorContains(t, err, "policy-only")
}

func TestReadReportsPolicyDrift(t *testing.T) {
	t.Parallel()
	before := policyObject(t, &sandboxv1.Policy{Constraints: &sandboxv1.PolicyConstraints{Resources: &sandboxv1.ResourceConstraints{MaxGpuCount: proto.Int64(0)}}})
	after := &sandboxv1.Policy{Constraints: &sandboxv1.PolicyConstraints{Resources: &sandboxv1.ResourceConstraints{}}}
	value, err := objectFromProto(t.Context(), after, before, policyAttribute().GetType().(types.ObjectType))
	require.NoError(t, err)
	assert.False(t, value.Equal(before), "removing a zero-GPU cap must be detected as drift")
	var actual sandboxv1.Policy
	require.NoError(t, objectToProto(value, &actual))
	assert.Nil(t, actual.Constraints.Resources.MaxGpuCount)
}

func TestNoOpRefreshPreservesPlanDuringConcurrentDrift(t *testing.T) {
	t.Parallel()
	model := managedRunnerModel{
		RunnerID:    types.StringValue("runner"),
		DisplayName: types.StringValue("planned"),
		Policy:      policyObject(t, &sandboxv1.Policy{DisplayName: "planned policy"}),
		Spec:        types.ObjectUnknown(runnerSpecAttribute().GetType().(types.ObjectType).AttrTypes),
	}
	policy := model.Policy
	runner := &sandboxv1.ManagedRunner{
		Identity:    &sandboxv1.RunnerIdentity{RunnerId: "runner"},
		DisplayName: "concurrent change",
		Policy:      &sandboxv1.Policy{DisplayName: "concurrent policy change"},
		Spec:        &sandboxv1.ManagedRunnerSpec{ReleaseChannel: sandboxv1.ReleaseChannel_RELEASE_CHANNEL_STABLE},
		Etag:        "new-etag",
	}
	require.NoError(t, model.setNoOpRunner(t.Context(), runner))
	assert.Equal(t, "planned", model.DisplayName.ValueString())
	assert.True(t, model.Policy.Equal(policy))
	assert.Equal(t, "new-etag", model.Etag.ValueString())
	assert.False(t, model.Spec.IsUnknown())
	// A subsequent refresh still observes and reports the concurrent change.
	require.NoError(t, model.setRunner(t.Context(), runner))
	assert.Equal(t, "concurrent change", model.DisplayName.ValueString())
	assert.False(t, model.Policy.Equal(policy))
}

func TestPolicyValidation(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"missing destination":     `{"constraints":{"network":{"allowed_egress":[{}]}}}`,
		"DNS default":             `{"constraints":{"network":{"default_egress":[{"dns_name":"example.com"}]}}}`,
		"invalid port range":      `{"constraints":{"network":{"allowed_egress":[{"any":true,"ports":[{"port":100,"end_port":90}]}]}}}`,
		"redundant source prefix": `{"control_plane_access":{"source_ip_allowlist":{"cidrs":["10.0.0.0/8","10.1.0.0/16"]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var policy sandboxv1.Policy
			require.NoError(t, protojson.Unmarshal([]byte(raw), &policy))
			require.Error(t, validatePolicy(&policy))
		})
	}
}

func TestExplicitFalseAndEmptyCollectionsRoundTrip(t *testing.T) {
	t.Parallel()
	typ := runnerSpecAttribute().GetType().(types.ObjectType)
	attributes := make(map[string]attr.Value, len(typ.AttrTypes))
	for name, attrType := range typ.AttrTypes {
		var err error
		attributes[name], err = nullValue(t.Context(), attrType)
		require.NoError(t, err)
	}
	attributes["enforce_resource_limits"] = types.BoolValue(false)
	prior := types.ObjectValueMust(typ.AttrTypes, attributes)
	value, err := objectFromProto(t.Context(), &sandboxv1.ManagedRunnerSpec{}, prior, typ)
	require.NoError(t, err)
	assert.Equal(t, types.BoolValue(false), value.Attributes()["enforce_resource_limits"])
	// ProtoJSON int64s are strings; ordinary JSON numbers must stay exact too.
	number, err := jsonToValue(t.Context(), json.Number("9007199254740993"), types.Int64Unknown(), types.Int64Type)
	require.NoError(t, err)
	assert.Equal(t, types.Int64Value(9007199254740993), number)
}
