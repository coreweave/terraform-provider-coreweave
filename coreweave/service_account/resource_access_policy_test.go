package serviceaccount

import (
	"testing"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAccessPolicySchema(t *testing.T) {
	t.Parallel()
	response := &frameworkresource.SchemaResponse{}
	NewAccessPolicyResource().Schema(t.Context(), frameworkresource.SchemaRequest{}, response)
	require.False(t, response.Diagnostics.HasError(), response.Diagnostics.Errors())
	diagnostics := response.Schema.ValidateImplementation(t.Context())
	require.False(t, diagnostics.HasError(), diagnostics.Errors())

	assert.True(t, response.Schema.Attributes["organization_id"].IsRequired())
	assert.True(t, response.Schema.Attributes["access_policy_id"].IsRequired())
	assert.True(t, response.Schema.Attributes["name"].IsComputed())
	assert.True(t, response.Schema.Attributes["uid"].IsComputed())
	assert.True(t, response.Schema.Attributes["description"].IsOptional())
	assert.True(t, response.Schema.Attributes["rules"].IsOptional())

	// active can be changed outside Terraform, so an update must not plan the
	// prior state value for it.
	active := response.Schema.Attributes["active"].(resourceschema.BoolAttribute)
	assert.True(t, active.IsComputed())
	assert.Empty(t, active.PlanModifiers)
}

func TestAccessPolicyModelRoundTrip(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.November, 13, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	policy := &directoryv1alpha.AccessPolicy{
		Name:        "organizations/a1b2c3/accessPolicies/automation",
		Uid:         "22222222-2222-4222-8222-222222222222",
		OrgId:       "a1b2c3",
		Description: "Deployment permissions",
		PolicyType:  directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM,
		Active:      true,
		Rules: []*directoryv1alpha.AccessPolicyRule{{
			Id:              "33333333-3333-4333-8333-333333333333",
			Actions:         []string{"read_auth", "write_auth"},
			Users:           []string{"organizations/a1b2c3/users/user-1"},
			Groups:          []string{"organizations/a1b2c3/groups/group-1"},
			ServiceAccounts: []string{"organizations/a1b2c3/serviceAccounts/ci-deployer"},
		}},
		CreateTime: timestamppb.New(createdAt),
		UpdateTime: timestamppb.New(updatedAt),
	}

	var model AccessPolicyResourceModel
	require.NoError(t, model.setFromProto(policy))
	assert.Equal(t, "a1b2c3", model.OrganizationID.ValueString())
	assert.Equal(t, "automation", model.AccessPolicyID.ValueString())
	assert.Equal(t, policy.GetName(), model.Name.ValueString())
	assert.Equal(t, policy.GetUid(), model.UID.ValueString())
	assert.Equal(t, "POLICY_TYPE_CUSTOM", model.PolicyType.ValueString())
	require.Len(t, model.Rules, 1)

	roundTrip, diagnostics := model.policyProto(t.Context(), model.Name.ValueString())
	require.False(t, diagnostics.HasError(), diagnostics.Errors())
	assert.Equal(t, policy.GetName(), roundTrip.GetName())
	assert.ElementsMatch(t, policy.GetRules()[0].GetActions(), roundTrip.GetRules()[0].GetActions())
	assert.ElementsMatch(t, policy.GetRules()[0].GetUsers(), roundTrip.GetRules()[0].GetUsers())
	assert.ElementsMatch(t, policy.GetRules()[0].GetGroups(), roundTrip.GetRules()[0].GetGroups())
	assert.ElementsMatch(t, policy.GetRules()[0].GetServiceAccounts(), roundTrip.GetRules()[0].GetServiceAccounts())
}

func TestAccessPolicyModelDeduplicatesRemoteSetsAndRules(t *testing.T) {
	t.Parallel()
	rule := &directoryv1alpha.AccessPolicyRule{
		Id:              "33333333-3333-4333-8333-333333333333",
		Actions:         []string{"read_auth", "read_auth"},
		Users:           []string{"organizations/a1b2c3/users/user-1", "organizations/a1b2c3/users/user-1"},
		Groups:          []string{"organizations/a1b2c3/groups/group-1", "organizations/a1b2c3/groups/group-1"},
		ServiceAccounts: []string{"organizations/a1b2c3/serviceAccounts/ci-deployer", "organizations/a1b2c3/serviceAccounts/ci-deployer"},
	}
	duplicateRule := &directoryv1alpha.AccessPolicyRule{
		Id:              "44444444-4444-4444-8444-444444444444",
		Actions:         append([]string(nil), rule.GetActions()...),
		Users:           append([]string(nil), rule.GetUsers()...),
		Groups:          append([]string(nil), rule.GetGroups()...),
		ServiceAccounts: append([]string(nil), rule.GetServiceAccounts()...),
	}
	policy := &directoryv1alpha.AccessPolicy{
		Name:       "organizations/a1b2c3/accessPolicies/automation",
		Uid:        "22222222-2222-4222-8222-222222222222",
		OrgId:      "a1b2c3",
		PolicyType: directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM,
		Rules:      []*directoryv1alpha.AccessPolicyRule{rule, duplicateRule},
	}

	var model AccessPolicyResourceModel
	require.NoError(t, model.setFromProto(policy))
	require.Len(t, model.Rules, 1)
	assert.Len(t, model.Rules[0].Actions.Elements(), 1)
	assert.Len(t, model.Rules[0].Users.Elements(), 1)
	assert.Len(t, model.Rules[0].Groups.Elements(), 1)
	assert.Len(t, model.Rules[0].ServiceAccounts.Elements(), 1)
}

func TestAccessPolicyModelRejectsUnmanageableResponses(t *testing.T) {
	t.Parallel()
	var model AccessPolicyResourceModel
	assert.EqualError(t, model.setFromProto(nil), "access policy is missing")
	assert.Error(t, model.setFromProto(&directoryv1alpha.AccessPolicy{}))
	assert.ErrorContains(t, model.setFromProto(&directoryv1alpha.AccessPolicy{
		Name: "organizations/a1b2c3/accessPolicies/automation", Uid: "22222222-2222-7222-8222-222222222222", OrgId: "a1b2c3",
		PolicyType: directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM,
	}), "not a UUIDv4")
	assert.ErrorContains(t, model.setFromProto(&directoryv1alpha.AccessPolicy{
		Name:       "organizations/a1b2c3/accessPolicies/automation",
		Uid:        "22222222-2222-4222-8222-222222222222",
		OrgId:      "a1b2c3",
		PolicyType: directoryv1alpha.PolicyType_POLICY_TYPE_DEFAULT_ADMIN,
	}), "only custom access policies")
}

func TestAccessPolicyModelDoesNotRequireRuleIDFormat(t *testing.T) {
	t.Parallel()
	policy := &directoryv1alpha.AccessPolicy{
		Name: "organizations/a1b2c3/accessPolicies/automation", Uid: "22222222-2222-4222-8222-222222222222", OrgId: "a1b2c3",
		PolicyType: directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM,
		Rules:      []*directoryv1alpha.AccessPolicyRule{{Id: "opaque-rule-id", Actions: []string{"read_auth"}}},
	}
	var model AccessPolicyResourceModel
	require.NoError(t, model.setFromProto(policy))
}

func TestAccessPolicyRulesEqualIgnoresServerIDs(t *testing.T) {
	t.Parallel()
	plan := []AccessPolicyRuleResourceModel{{
		Actions: stringSet([]string{"read_auth"}), Users: stringSet(nil), Groups: stringSet(nil),
		ServiceAccounts: stringSet([]string{"organizations/a1b2c3/serviceAccounts/ci"}),
	}, {
		Actions: stringSet([]string{"write_auth"}), Users: stringSet(nil), Groups: stringSet(nil), ServiceAccounts: stringSet(nil),
	}}
	state := []AccessPolicyRuleResourceModel{{
		Actions: stringSet([]string{"write_auth"}), Users: stringSet(nil), Groups: stringSet(nil), ServiceAccounts: stringSet(nil),
	}, {
		Actions: stringSet([]string{"read_auth"}), Users: stringSet(nil), Groups: stringSet(nil),
		ServiceAccounts: stringSet([]string{"organizations/a1b2c3/serviceAccounts/ci"}),
	}}
	assert.True(t, accessPolicyRulesEqual(plan, state))
	plan[0].ServiceAccounts = stringSet([]string{"organizations/a1b2c3/serviceAccounts/other"})
	assert.False(t, accessPolicyRulesEqual(plan, state))
}

func TestAccessPolicyFillUnknownRejectsConcurrentRuleChanges(t *testing.T) {
	t.Parallel()
	remote := &directoryv1alpha.AccessPolicy{
		Name:       "organizations/a1b2c3/accessPolicies/automation",
		Uid:        "22222222-2222-4222-8222-222222222222",
		OrgId:      "a1b2c3",
		PolicyType: directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM,
		Rules: []*directoryv1alpha.AccessPolicyRule{{
			Actions: []string{"write_auth"},
		}},
	}
	plannedActions := stringSet([]string{"read_auth"})
	model := AccessPolicyResourceModel{
		Rules: []AccessPolicyRuleResourceModel{{
			Actions: plannedActions, Users: stringSet(nil), Groups: stringSet(nil), ServiceAccounts: stringSet(nil),
		}},
	}

	err := model.fillUnknownFromProto(remote)
	require.ErrorContains(t, err, "rules changed concurrently")
	assert.True(t, model.Rules[0].Actions.Equal(plannedActions), "known planned rules must not be replaced by remote values")

	remote.Rules = append(remote.Rules, &directoryv1alpha.AccessPolicyRule{Actions: []string{"read_auth"}})
	err = model.fillUnknownFromProto(remote)
	require.ErrorContains(t, err, "planned 1 rules, but the API returned 2")
	assert.True(t, model.Rules[0].Actions.Equal(plannedActions), "known planned rules must not be replaced when rule counts differ")
}

func TestAccessPolicyFillUnknownFromProtoPreservesKnownValues(t *testing.T) {
	t.Parallel()
	remoteCreatedAt := time.Date(2026, time.November, 20, 9, 0, 0, 0, time.UTC)
	remoteUpdatedAt := remoteCreatedAt.Add(time.Hour)
	remote := &directoryv1alpha.AccessPolicy{
		Name:        "organizations/a1b2c3/accessPolicies/automation",
		Uid:         "99999999-9999-4999-8999-999999999999",
		OrgId:       "a1b2c3",
		Description: "Remote description",
		PolicyType:  directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM,
		Active:      false,
		Rules:       []*directoryv1alpha.AccessPolicyRule{{Actions: []string{"read_auth"}}},
		CreateTime:  timestamppb.New(remoteCreatedAt),
		UpdateTime:  timestamppb.New(remoteUpdatedAt),
	}
	model := AccessPolicyResourceModel{
		OrganizationID: types.StringValue("planned-org"),
		AccessPolicyID: types.StringValue("planned-id"),
		Name:           types.StringValue("organizations/planned-org/accessPolicies/planned-id"),
		UID:            types.StringValue("22222222-2222-4222-8222-222222222222"),
		Description:    types.StringValue("Planned description"),
		PolicyType:     types.StringValue("PLANNED_POLICY_TYPE"),
		Active:         types.BoolUnknown(),
		Rules: []AccessPolicyRuleResourceModel{{
			Actions: stringSet([]string{"read_auth"}), Users: stringSet(nil), Groups: stringSet(nil), ServiceAccounts: stringSet(nil),
		}},
		CreatedAt: types.StringValue("2026-11-13T10:00:00Z"),
		UpdatedAt: types.StringUnknown(),
	}

	require.NoError(t, model.fillUnknownFromProto(remote))

	assert.Equal(t, "planned-org", model.OrganizationID.ValueString())
	assert.Equal(t, "planned-id", model.AccessPolicyID.ValueString())
	assert.Equal(t, "organizations/planned-org/accessPolicies/planned-id", model.Name.ValueString())
	assert.Equal(t, "22222222-2222-4222-8222-222222222222", model.UID.ValueString())
	assert.Equal(t, "Planned description", model.Description.ValueString())
	assert.Equal(t, "PLANNED_POLICY_TYPE", model.PolicyType.ValueString())
	assert.Equal(t, "2026-11-13T10:00:00Z", model.CreatedAt.ValueString())

	require.False(t, model.Active.IsUnknown())
	assert.False(t, model.Active.ValueBool(), "unknown active must resolve to the remote value")
	assert.Equal(t, timestampValue(remote.GetUpdateTime()), model.UpdatedAt, "unknown updated_at must resolve to the remote value")
}

func TestAccessPolicyFillUnknownFromProtoResolvesUnknownValues(t *testing.T) {
	t.Parallel()
	remote := &directoryv1alpha.AccessPolicy{
		Name:        "organizations/a1b2c3/accessPolicies/automation",
		Uid:         "22222222-2222-4222-8222-222222222222",
		OrgId:       "a1b2c3",
		Description: "Remote description",
		PolicyType:  directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM,
		Active:      true,
		CreateTime:  timestamppb.New(time.Date(2026, time.November, 13, 10, 0, 0, 0, time.UTC)),
		UpdateTime:  timestamppb.New(time.Date(2026, time.November, 13, 10, 1, 0, 0, time.UTC)),
	}
	model := AccessPolicyResourceModel{
		OrganizationID: types.StringUnknown(),
		AccessPolicyID: types.StringUnknown(),
		Name:           types.StringUnknown(),
		UID:            types.StringUnknown(),
		Description:    types.StringUnknown(),
		PolicyType:     types.StringUnknown(),
		Active:         types.BoolUnknown(),
		Rules:          []AccessPolicyRuleResourceModel{},
		CreatedAt:      types.StringUnknown(),
		UpdatedAt:      types.StringUnknown(),
	}

	require.NoError(t, model.fillUnknownFromProto(remote))

	var expected AccessPolicyResourceModel
	require.NoError(t, expected.setFromProto(remote))
	assert.Equal(t, expected, model)
}

func TestParseAccessPolicyName(t *testing.T) {
	t.Parallel()
	organizationID, accessPolicyID, err := parseAccessPolicyName("organizations/a1b2c3/accessPolicies/automation")
	require.NoError(t, err)
	assert.Equal(t, "a1b2c3", organizationID)
	assert.Equal(t, "automation", accessPolicyID)
	_, _, err = parseAccessPolicyName("accessPolicies/automation")
	assert.Error(t, err)
	_, _, err = parseAccessPolicyName("organizations/a1b2c3/accessPolicies/22222222-2222-4222-8222-222222222222")
	assert.Error(t, err)
}
