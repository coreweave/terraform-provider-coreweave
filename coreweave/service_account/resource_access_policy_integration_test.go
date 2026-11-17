package serviceaccount_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"buf.build/gen/go/coreweave/directory/connectrpc/go/v2/coreweave/directory/v1alpha/directoryv1alphaconnect"
	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const accessPolicyResourceAddress = "coreweave_iam_access_policy.test"

type accessPolicyTestServer struct {
	directoryv1alphaconnect.UnimplementedDirectoryServiceHandler

	mu                    sync.Mutex
	policies              map[string]*directoryv1alpha.AccessPolicy
	nextPolicyID          int
	nextRuleID            int
	invalidCreateResponse bool
	alreadyExistsOnCreate bool
}

func newAccessPolicyTestServer() *accessPolicyTestServer {
	return &accessPolicyTestServer{policies: make(map[string]*directoryv1alpha.AccessPolicy)}
}

func cloneAccessPolicy(policy *directoryv1alpha.AccessPolicy) *directoryv1alpha.AccessPolicy {
	return proto.Clone(policy).(*directoryv1alpha.AccessPolicy)
}

func (s *accessPolicyTestServer) assignRuleIDs(rules []*directoryv1alpha.AccessPolicyRule) []*directoryv1alpha.AccessPolicyRule {
	result := make([]*directoryv1alpha.AccessPolicyRule, 0, len(rules))
	for _, input := range rules {
		s.nextRuleID++
		rule := cloneAccessPolicyRule(input)
		rule.Id = fmt.Sprintf("33333333-3333-4333-8333-%012d", s.nextRuleID)
		result = append(result, rule)
	}
	return result
}

func cloneAccessPolicyRule(rule *directoryv1alpha.AccessPolicyRule) *directoryv1alpha.AccessPolicyRule {
	return proto.Clone(rule).(*directoryv1alpha.AccessPolicyRule)
}

func (s *accessPolicyTestServer) CreateAccessPolicy(_ context.Context, request *directoryv1alpha.CreateAccessPolicyRequest) (*directoryv1alpha.AccessPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.GetParent() != "organizations/a1b2c3" || request.GetAccessPolicy() == nil || request.GetAccessPolicyId() == "" {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid access policy create request")
	}
	if s.alreadyExistsOnCreate {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "access policy already exists")
	}
	s.nextPolicyID++
	uid := fmt.Sprintf("22222222-2222-4222-8222-%012d", s.nextPolicyID)
	name := request.GetParent() + "/accessPolicies/" + request.GetAccessPolicyId()
	if _, exists := s.policies[name]; exists {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "access policy already exists")
	}
	now := timestamppb.Now()
	input := request.GetAccessPolicy()
	policy := &directoryv1alpha.AccessPolicy{
		Name: name, Uid: uid, OrgId: "a1b2c3", Description: input.GetDescription(),
		PolicyType: directoryv1alpha.PolicyType_POLICY_TYPE_CUSTOM, Active: true,
		Rules: s.assignRuleIDs(input.GetRules()), CreateTime: now, UpdateTime: now,
	}
	s.policies[name] = policy
	if s.invalidCreateResponse {
		s.invalidCreateResponse = false
		return &directoryv1alpha.AccessPolicy{}, nil
	}
	return cloneAccessPolicy(policy), nil
}

func (s *accessPolicyTestServer) GetAccessPolicy(_ context.Context, request *directoryv1alpha.GetAccessPolicyRequest) (*directoryv1alpha.AccessPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, exists := s.policies[request.GetName()]
	if !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "access policy not found")
	}
	return cloneAccessPolicy(policy), nil
}

func (s *accessPolicyTestServer) UpdateAccessPolicy(_ context.Context, request *directoryv1alpha.UpdateAccessPolicyRequest) (*directoryv1alpha.AccessPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input := request.GetAccessPolicy()
	if input == nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "access policy is required")
	}
	policy, exists := s.policies[input.GetName()]
	if !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "access policy not found")
	}
	for _, field := range request.GetUpdateMask().GetPaths() {
		switch field {
		case "description":
			policy.Description = input.GetDescription()
		case "rules":
			policy.Rules = s.assignRuleIDs(input.GetRules())
		}
	}
	policy.UpdateTime = timestamppb.New(policy.GetUpdateTime().AsTime().Add(time.Minute))
	return cloneAccessPolicy(policy), nil
}

func (s *accessPolicyTestServer) DeleteAccessPolicy(_ context.Context, request *directoryv1alpha.DeleteAccessPolicyRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.policies[request.GetName()]; !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "access policy not found")
	}
	delete(s.policies, request.GetName())
	return &emptypb.Empty{}, nil
}

func (s *accessPolicyTestServer) checkEmpty(*terraform.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.policies) != 0 {
		return fmt.Errorf("expected all access policies deleted, found %d", len(s.policies))
	}
	return nil
}

func (s *accessPolicyTestServer) removeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies = make(map[string]*directoryv1alpha.AccessPolicy)
}

func (s *accessPolicyTestServer) returnInvalidCreateResponseOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidCreateResponse = true
}

func (s *accessPolicyTestServer) returnAlreadyExistsOnCreate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alreadyExistsOnCreate = true
}

func (s *accessPolicyTestServer) checkRule(expectedActions, expectedServiceAccounts []string) tfresource.TestCheckFunc {
	return func(*terraform.State) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.policies) != 1 {
			return fmt.Errorf("expected one access policy, found %d", len(s.policies))
		}
		var policy *directoryv1alpha.AccessPolicy
		for _, candidate := range s.policies {
			policy = candidate
		}
		if len(policy.GetRules()) != 1 {
			return fmt.Errorf("expected one rule, found %d", len(policy.GetRules()))
		}
		rule := policy.GetRules()[0]
		for _, expected := range expectedActions {
			if !slices.Contains(rule.GetActions(), expected) {
				return fmt.Errorf("expected action %q in %v", expected, rule.GetActions())
			}
		}
		for _, expected := range expectedServiceAccounts {
			if !slices.Contains(rule.GetServiceAccounts(), expected) {
				return fmt.Errorf("expected service account %q in %v", expected, rule.GetServiceAccounts())
			}
		}
		return nil
	}
}

func startAccessPolicyTestServer(t *testing.T) (*accessPolicyTestServer, *httptest.Server) {
	t.Helper()
	service := newAccessPolicyTestServer()
	rpcServer := connect.NewServer()
	directoryv1alphaconnect.RegisterDirectoryServiceHandler(rpcServer, service)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	return service, server
}

func accessPolicyTestConfig(endpoint, accessPolicyID, description string, actions, serviceAccounts []string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_iam_access_policy" "test" {
  organization_id  = "a1b2c3"
  access_policy_id = %q
  description      = %q

  rules = [{
    actions = [%s]
    users = [
      "organizations/a1b2c3/users/user-1",
    ]
    groups = [
      "organizations/a1b2c3/groups/group-1",
    ]
    service_accounts = [%s]
  }]
}
`, endpoint, accessPolicyID, description, quotedValues(actions), quotedValues(serviceAccounts))
}

func quotedValues(values []string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += ", "
		}
		result += fmt.Sprintf("%q", value)
	}
	return result
}

func TestAccessPolicyResourceLifecycle(t *testing.T) {
	service, server := startAccessPolicyTestServer(t)
	initialServiceAccounts := []string{"organizations/a1b2c3/serviceAccounts/ci-deployer"}
	updatedServiceAccounts := []string{
		"organizations/a1b2c3/serviceAccounts/ci-deployer",
		"organizations/a1b2c3/serviceAccounts/release-bot",
	}
	initial := accessPolicyTestConfig(server.URL, "automation", "Initial policy", []string{"read_auth"}, initialServiceAccounts)
	updated := accessPolicyTestConfig(server.URL, "automation", "Updated policy", []string{"read_auth", "write_auth"}, updatedServiceAccounts)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: initial,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "access_policy_id", "automation"),
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "name", "organizations/a1b2c3/accessPolicies/automation"),
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "uid", "22222222-2222-4222-8222-000000000001"),
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "policy_type", "POLICY_TYPE_CUSTOM"),
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "active", "true"),
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "rules.0.service_accounts.#", "1"),
					service.checkRule([]string{"read_auth"}, initialServiceAccounts),
				),
			},
			{
				ResourceName:                         accessPolicyResourceAddress,
				ImportState:                          true,
				ImportStateId:                        "organizations/a1b2c3/accessPolicies/automation",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_policy_id",
			},
			{
				Config: updated,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "access_policy_id", "automation"),
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "description", "Updated policy"),
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "rules.0.actions.#", "2"),
					tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "rules.0.service_accounts.#", "2"),
					service.checkRule([]string{"read_auth", "write_auth"}, updatedServiceAccounts),
				),
			},
		},
	})
}

func TestAccessPolicyResourceNotFoundRemovesState(t *testing.T) {
	service, server := startAccessPolicyTestServer(t)
	config := accessPolicyTestConfig(server.URL, "remote-deletion", "", []string{"read_auth"}, []string{"organizations/a1b2c3/serviceAccounts/ci-deployer"})

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{Config: config},
			{
				Config:             config,
				PreConfig:          service.removeAll,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccessPolicyResourceRecoversInvalidCreateResponse(t *testing.T) {
	service, server := startAccessPolicyTestServer(t)
	service.returnInvalidCreateResponseOnce()
	config := accessPolicyTestConfig(server.URL, "create-recovery", "Recovery test", []string{"read_auth"}, nil)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{{
			Config: config,
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "name", "organizations/a1b2c3/accessPolicies/create-recovery"),
				tfresource.TestCheckResourceAttr(accessPolicyResourceAddress, "uid", "22222222-2222-4222-8222-000000000001"),
			),
		}},
	})
}

func TestAccessPolicyResourceAlreadyExistsSuggestsImport(t *testing.T) {
	service, server := startAccessPolicyTestServer(t)
	service.returnAlreadyExistsOnCreate()
	config := accessPolicyTestConfig(server.URL, "existing-policy", "Already exists", []string{"read_auth"}, nil)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{{
			Config:      config,
			ExpectError: regexp.MustCompile(`terraform import\s+<resource-address>\s+organizations/a1b2c3/accessPolicies/existing-policy`),
		}},
	})
}
