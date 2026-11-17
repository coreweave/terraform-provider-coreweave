package serviceaccount

import (
	"testing"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
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
