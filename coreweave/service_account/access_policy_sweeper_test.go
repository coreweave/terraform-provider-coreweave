package serviceaccount_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"connectrpc.com/connect/v2"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	accessPolicyResourceType      = "coreweave_iam_access_policy"
	accessPolicySweepTimeout      = 10 * time.Minute
	accessPolicyPageSize          = int32(200)
	testAccessPolicyParent        = "organizations/a1b2c3"
	testAccessPolicyNextPageToken = "next"
)

var acceptanceAccessPolicyIDPattern = regexp.MustCompile(`^tf-acc-ap-[0-9]+$`)

type accessPolicySweepClient interface {
	ListAccessPolicies(context.Context, *directoryv1alpha.ListAccessPoliciesRequest) (*directoryv1alpha.ListAccessPoliciesResponse, error)
	DeleteAccessPolicy(context.Context, *directoryv1alpha.DeleteAccessPolicyRequest) (*emptypb.Empty, error)
}

func listAccessPolicies(ctx context.Context, client accessPolicySweepClient, parent string) ([]*directoryv1alpha.AccessPolicy, error) {
	var policies []*directoryv1alpha.AccessPolicy
	pageToken := ""
	seenTokens := make(map[string]struct{})
	for {
		request := &directoryv1alpha.ListAccessPoliciesRequest{}
		request.SetParent(parent)
		request.SetMaxPageSize(accessPolicyPageSize)
		if pageToken != "" {
			request.SetPageToken(pageToken)
		}
		response, err := client.ListAccessPolicies(ctx, request)
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("Directory API returned an empty access policy list response")
		}
		policies = append(policies, response.GetAccessPolicies()...)
		nextToken := response.GetNextPageToken()
		if nextToken == "" {
			return policies, nil
		}
		if _, exists := seenTokens[nextToken]; exists {
			return nil, fmt.Errorf("Directory API repeated access policy page token %q", nextToken)
		}
		seenTokens[nextToken] = struct{}{}
		pageToken = nextToken
	}
}

func accessPolicySweepTarget(policy *directoryv1alpha.AccessPolicy, parent string) bool {
	if policy == nil || policy.GetPolicyType() != directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM {
		return false
	}
	prefix := parent + "/accessPolicies/"
	name := policy.GetName()
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return false
	}
	return acceptanceAccessPolicyIDPattern.MatchString(name[len(prefix):])
}

func newAccessPolicySweepConfig(client accessPolicySweepClient, parent string) testutil.SweepConfig[*directoryv1alpha.AccessPolicy] {
	return testutil.SweepConfig[*directoryv1alpha.AccessPolicy]{
		ResourceType: accessPolicyResourceType,
		CreatedAt:    (*directoryv1alpha.AccessPolicy).GetCreateTime,
		List: func(ctx context.Context) ([]*directoryv1alpha.AccessPolicy, error) {
			return listAccessPolicies(ctx, client, parent)
		},
		Name: func(policy *directoryv1alpha.AccessPolicy) string {
			return policy.GetName()
		},
		Match: func(policy *directoryv1alpha.AccessPolicy) bool {
			return accessPolicySweepTarget(policy, parent)
		},
		Delete: func(ctx context.Context, policy *directoryv1alpha.AccessPolicy) error {
			name := policy.GetName()
			_, err := client.DeleteAccessPolicy(ctx, &directoryv1alpha.DeleteAccessPolicyRequest{Name: name})
			if coreweave.IsNotFoundError(err) {
				return nil
			}
			return err
		},
	}
}

func sweepAccessPolicies(_ string) error {
	runtime, err := testutil.SweepRuntimeFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), accessPolicySweepTimeout)
	defer cancel()

	testutil.SetEnvDefaults()
	client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
	if err != nil {
		return fmt.Errorf("build client: %w", err)
	}
	identity, err := client.GetCallerIdentity(ctx)
	if err != nil {
		return fmt.Errorf("get caller identity: %w", err)
	}
	parent := "organizations/" + identity.OrganizationID
	return testutil.Sweep(ctx, runtime, newAccessPolicySweepConfig(client.Directory, parent))
}

func init() {
	resource.AddTestSweepers(accessPolicyResourceType, &resource.Sweeper{
		Name:         accessPolicyResourceType,
		Dependencies: []string{},
		F:            sweepAccessPolicies,
	})
}

type accessPolicyListCall struct {
	parent, pageToken string
	pageSize          int32
}

type fakeAccessPolicySweepClient struct {
	pages        map[string]*directoryv1alpha.ListAccessPoliciesResponse
	deleteErrors map[string]error
	listCalls    []accessPolicyListCall
	deleted      []string
}

func (f *fakeAccessPolicySweepClient) ListAccessPolicies(_ context.Context, request *directoryv1alpha.ListAccessPoliciesRequest) (*directoryv1alpha.ListAccessPoliciesResponse, error) {
	f.listCalls = append(f.listCalls, accessPolicyListCall{
		parent: request.GetParent(), pageToken: request.GetPageToken(), pageSize: request.GetMaxPageSize(),
	})
	return f.pages[request.GetPageToken()], nil
}

func (f *fakeAccessPolicySweepClient) DeleteAccessPolicy(_ context.Context, request *directoryv1alpha.DeleteAccessPolicyRequest) (*emptypb.Empty, error) {
	name := request.GetName()
	f.deleted = append(f.deleted, name)
	return &emptypb.Empty{}, f.deleteErrors[name]
}

func testAccessPolicy(name string, policyType directoryv1alpha.PolicyType, createdAt time.Time) *directoryv1alpha.AccessPolicy {
	return &directoryv1alpha.AccessPolicy{Name: name, PolicyType: policyType, CreateTime: timestamppb.New(createdAt)}
}

func TestAccessPolicySweepTarget(t *testing.T) {
	t.Parallel()
	parent := testAccessPolicyParent
	tests := []struct {
		name   string
		policy *directoryv1alpha.AccessPolicy
		want   bool
	}{
		{name: "acceptance policy", policy: testAccessPolicy(parent+"/accessPolicies/tf-acc-ap-123", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now()), want: true},
		{name: "different organization", policy: testAccessPolicy("organizations/z9y8x7/accessPolicies/tf-acc-ap-123", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now())},
		{name: "production policy", policy: testAccessPolicy(parent+"/accessPolicies/deployers", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now())},
		{name: "partial suffix match", policy: testAccessPolicy(parent+"/accessPolicies/tf-acc-ap-123-extra", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now())},
		{name: "default policy", policy: testAccessPolicy(parent+"/accessPolicies/tf-acc-ap-123", directoryv1alpha.PolicyType_POLICY_TYPE_DEFAULT_ADMIN, time.Now())},
		{name: "missing policy"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, accessPolicySweepTarget(test.policy, parent))
		})
	}
}

func TestListAccessPoliciesPaginates(t *testing.T) {
	t.Parallel()
	parent := testAccessPolicyParent
	first := testAccessPolicy(parent+"/accessPolicies/tf-acc-ap-1", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now())
	second := testAccessPolicy(parent+"/accessPolicies/tf-acc-ap-2", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now())
	fake := &fakeAccessPolicySweepClient{pages: map[string]*directoryv1alpha.ListAccessPoliciesResponse{
		"":                            {AccessPolicies: []*directoryv1alpha.AccessPolicy{first}, NextPageToken: testAccessPolicyNextPageToken},
		testAccessPolicyNextPageToken: {AccessPolicies: []*directoryv1alpha.AccessPolicy{second}},
	}}

	policies, err := listAccessPolicies(t.Context(), fake, parent)
	require.NoError(t, err)
	assert.Equal(t, []*directoryv1alpha.AccessPolicy{first, second}, policies)
	assert.Equal(t, []accessPolicyListCall{
		{parent: parent, pageSize: accessPolicyPageSize},
		{parent: parent, pageToken: testAccessPolicyNextPageToken, pageSize: accessPolicyPageSize},
	}, fake.listCalls)
}

func TestListAccessPoliciesRejectsRepeatedPageToken(t *testing.T) {
	t.Parallel()
	fake := &fakeAccessPolicySweepClient{pages: map[string]*directoryv1alpha.ListAccessPoliciesResponse{
		"":                            {NextPageToken: testAccessPolicyNextPageToken},
		testAccessPolicyNextPageToken: {NextPageToken: testAccessPolicyNextPageToken},
	}}
	_, err := listAccessPolicies(t.Context(), fake, testAccessPolicyParent)
	require.ErrorContains(t, err, `repeated access policy page token "next"`)
}

func TestAccessPolicySweepConfigDeletesOnlyStaleAcceptancePolicies(t *testing.T) {
	t.Parallel()
	parent := testAccessPolicyParent
	oldTarget := testAccessPolicy(parent+"/accessPolicies/tf-acc-ap-1", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now().Add(-24*time.Hour))
	youngTarget := testAccessPolicy(parent+"/accessPolicies/tf-acc-ap-2", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now())
	production := testAccessPolicy(parent+"/accessPolicies/deployers", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now().Add(-24*time.Hour))
	fake := &fakeAccessPolicySweepClient{pages: map[string]*directoryv1alpha.ListAccessPoliciesResponse{
		"": {AccessPolicies: []*directoryv1alpha.AccessPolicy{oldTarget, youngTarget, production}},
	}}

	err := testutil.Sweep(t.Context(), testutil.SweepRuntime{Parallelism: 1, MinAge: 2 * time.Hour}, newAccessPolicySweepConfig(fake, parent))
	require.NoError(t, err)
	assert.Equal(t, []string{oldTarget.GetName()}, fake.deleted)
}

func TestAccessPolicySweepDeleteIgnoresNotFound(t *testing.T) {
	t.Parallel()
	parent := testAccessPolicyParent
	policy := testAccessPolicy(parent+"/accessPolicies/tf-acc-ap-1", directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, time.Now().Add(-24*time.Hour))
	fake := &fakeAccessPolicySweepClient{
		pages: map[string]*directoryv1alpha.ListAccessPoliciesResponse{"": {}},
		deleteErrors: map[string]error{
			policy.GetName(): connect.Errorf(connect.CodeNotFound, "already deleted"),
		},
	}
	require.NoError(t, newAccessPolicySweepConfig(fake, parent).Delete(t.Context(), policy))
}
