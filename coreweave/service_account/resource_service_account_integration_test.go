package serviceaccount_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"buf.build/gen/go/coreweave/directory/connectrpc/go/v2/coreweave/directory/v1alpha/directoryv1alphaconnect"
	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const serviceAccountResourceAddress = "coreweave_service_account.test"

type serviceAccountTestServer struct {
	directoryv1alphaconnect.UnimplementedDirectoryServiceHandler

	mu       sync.Mutex
	accounts map[string]*directoryv1alpha.ServiceAccount

	emptyCreateResponse      bool
	emptyCreateResponseUID   bool
	emptyUpdateResponse      bool
	notFoundOnNextUpdateCall bool
}

func newServiceAccountTestServer() *serviceAccountTestServer {
	return &serviceAccountTestServer{accounts: make(map[string]*directoryv1alpha.ServiceAccount)}
}

func cloneServiceAccount(account *directoryv1alpha.ServiceAccount) *directoryv1alpha.ServiceAccount {
	return proto.Clone(account).(*directoryv1alpha.ServiceAccount)
}

func (s *serviceAccountTestServer) CreateServiceAccount(_ context.Context, request *directoryv1alpha.CreateServiceAccountRequest) (*directoryv1alpha.ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := request.GetParent() + "/serviceAccounts/" + request.GetServiceAccountId()
	if _, exists := s.accounts[name]; exists {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "service account exists")
	}
	now := timestamppb.Now()
	account := &directoryv1alpha.ServiceAccount{
		Name: name, Uid: proto.String("c4f985e4-a2f0-44f8-937e-4fc7a4a67d06"), OrgId: proto.String("a1b2c3"),
		Creator: proto.String("organizations/a1b2c3/users/test-user"), DisplayName: request.GetServiceAccount().DisplayName,
		SystemManaged: proto.Bool(false), Active: proto.Bool(true), CreateTime: now, UpdateTime: now,
	}
	s.accounts[name] = account
	if s.emptyCreateResponse {
		s.emptyCreateResponse = false
		return nil, nil
	}
	response := cloneServiceAccount(account)
	if s.emptyCreateResponseUID {
		s.emptyCreateResponseUID = false
		response.Uid = nil
	}
	return response, nil
}

func (s *serviceAccountTestServer) GetServiceAccount(_ context.Context, request *directoryv1alpha.GetServiceAccountRequest) (*directoryv1alpha.ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	account, exists := s.accounts[request.GetName()]
	if !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "service account not found")
	}
	return cloneServiceAccount(account), nil
}

func (s *serviceAccountTestServer) UpdateServiceAccount(_ context.Context, request *directoryv1alpha.UpdateServiceAccountRequest) (*directoryv1alpha.ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.notFoundOnNextUpdateCall {
		s.notFoundOnNextUpdateCall = false
		return nil, connect.Errorf(connect.CodeNotFound, "service account not found")
	}
	account, exists := s.accounts[request.GetServiceAccount().GetName()]
	if !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "service account not found")
	}
	for _, field := range request.GetUpdateMask().GetPaths() {
		if field == "display_name" {
			account.DisplayName = request.GetServiceAccount().DisplayName
		}
	}
	account.UpdateTime = timestamppb.Now()
	if s.emptyUpdateResponse {
		s.emptyUpdateResponse = false
		return nil, nil
	}
	return cloneServiceAccount(account), nil
}

func (s *serviceAccountTestServer) DeleteServiceAccount(_ context.Context, request *directoryv1alpha.DeleteServiceAccountRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.accounts[request.GetName()]; !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "service account not found")
	}
	delete(s.accounts, request.GetName())
	return &emptypb.Empty{}, nil
}

func (s *serviceAccountTestServer) checkEmpty(*terraform.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.accounts) != 0 {
		return fmt.Errorf("expected all service accounts deleted, found %d", len(s.accounts))
	}
	return nil
}

func (s *serviceAccountTestServer) removeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts = make(map[string]*directoryv1alpha.ServiceAccount)
}

func (s *serviceAccountTestServer) deactivateAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.accounts {
		account.Active = proto.Bool(false)
		account.UpdateTime = timestamppb.Now()
	}
}

func (s *serviceAccountTestServer) returnEmptyCreateResponse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyCreateResponse = true
}

func (s *serviceAccountTestServer) returnEmptyCreateResponseUID() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyCreateResponseUID = true
}

func (s *serviceAccountTestServer) returnEmptyUpdateResponse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyUpdateResponse = true
}

func (s *serviceAccountTestServer) returnNotFoundOnNextUpdate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notFoundOnNextUpdateCall = true
}

func startServiceAccountTestServer(t *testing.T) (*serviceAccountTestServer, *httptest.Server) {
	t.Helper()
	service := newServiceAccountTestServer()
	rpcServer := connect.NewServer()
	directoryv1alphaconnect.RegisterDirectoryServiceHandler(rpcServer, service)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	// Provider environment variables take precedence over the test provider
	// block, so force these hermetic tests to use the local fake service.
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	return service, server
}

func serviceAccountTestConfig(endpoint, displayName string) string {
	displayNameAttribute := ""
	if displayName != "" {
		displayNameAttribute = fmt.Sprintf("  display_name = %q\n", displayName)
	}
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_service_account" "test" {
  id              = "ci-agent"
  organization_id = "a1b2c3"
%s
}
`, endpoint, displayNameAttribute)
}

func serviceAccountTestConfigWithExplicitDisplayName(endpoint, displayName string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_service_account" "test" {
  id              = "ci-agent"
  organization_id = "a1b2c3"
  display_name    = %q
}
`, endpoint, displayName)
}

func serviceAccountTestConfigWithReplacedDisplayName(endpoint, replacement string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "terraform_data" "display_name" {
  input            = "stable display name"
  triggers_replace = %q
}

resource "coreweave_service_account" "test" {
  id              = "ci-agent"
  organization_id = "a1b2c3"
  display_name    = terraform_data.display_name.output
}
`, endpoint, replacement)
}

func serviceAccountTestConfigWithIdentity(endpoint, id, organizationID, displayName string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_service_account" "test" {
  id              = %q
  organization_id = %q
  display_name    = %q
}
`, endpoint, id, organizationID, displayName)
}

func TestServiceAccountResourceLifecycle(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	initial := serviceAccountTestConfig(server.URL, "CI agent")
	updated := serviceAccountTestConfig(server.URL, "")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: initial,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "name", "organizations/a1b2c3/serviceAccounts/ci-agent"),
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "active", "true"),
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "display_name", "CI agent"),
					tfresource.TestCheckResourceAttrSet(serviceAccountResourceAddress, "uid"),
					tfresource.TestCheckResourceAttrSet(serviceAccountResourceAddress, "created_at"),
					tfresource.TestCheckResourceAttrSet(serviceAccountResourceAddress, "updated_at"),
				),
			},
			{
				ResourceName:      serviceAccountResourceAddress,
				ImportState:       true,
				ImportStateId:     "organizations/a1b2c3/serviceAccounts/ci-agent",
				ImportStateVerify: true,
			},
			{
				Config: updated,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "active", "true"),
					tfresource.TestCheckNoResourceAttr(serviceAccountResourceAddress, "display_name"),
				),
			},
		},
	})
}

func TestServiceAccountResourceNotFoundRemovesState(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	config := serviceAccountTestConfig(server.URL, "Not found")

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

func TestServiceAccountResourceResolvedNoOpUpdate(t *testing.T) {
	service, server := startServiceAccountTestServer(t)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: serviceAccountTestConfigWithReplacedDisplayName(server.URL, "initial"),
				Check:  tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "display_name", "stable display name"),
			},
			{
				Config: serviceAccountTestConfigWithReplacedDisplayName(server.URL, "replacement"),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "display_name", "stable display name"),
					tfresource.TestCheckResourceAttrSet(serviceAccountResourceAddress, "uid"),
					tfresource.TestCheckResourceAttrSet(serviceAccountResourceAddress, "created_at"),
					tfresource.TestCheckResourceAttrSet(serviceAccountResourceAddress, "updated_at"),
				),
			},
		},
	})
}

func TestServiceAccountResourceRejectsEmptyCreateResponse(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	service.returnEmptyCreateResponse()
	t.Cleanup(service.removeAll)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{{
			Config:      serviceAccountTestConfig(server.URL, "Empty create response"),
			ExpectError: regexp.MustCompile("service account name is missing"),
		}},
	})
}

func TestServiceAccountResourceRejectsCreateResponseWithoutUID(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	service.returnEmptyCreateResponseUID()
	t.Cleanup(service.removeAll)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{{
			Config:      serviceAccountTestConfig(server.URL, "Missing UID"),
			ExpectError: regexp.MustCompile("service account UID is missing"),
		}},
	})
}

func TestServiceAccountResourceUpdateNotFoundRetainsState(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	initial := serviceAccountTestConfig(server.URL, "Initial")
	updated := serviceAccountTestConfig(server.URL, "Updated")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{Config: initial},
			{
				Config:      updated,
				PreConfig:   service.returnNotFoundOnNextUpdate,
				ExpectError: regexp.MustCompile("[Nn]ot [Ff]ound|not found"),
			},
			{
				Config: updated,
				Check:  tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "display_name", "Updated"),
			},
		},
	})
}

func TestServiceAccountResourceReportsRemoteDeactivationWithoutUpdate(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	config := serviceAccountTestConfig(server.URL, "Remote state")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: config,
				Check:  tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "active", "true"),
			},
			{
				Config:    config,
				PreConfig: service.deactivateAll,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(serviceAccountResourceAddress, plancheck.ResourceActionNoop),
				}},
				Check: tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "active", "false"),
			},
		},
	})
}

func TestServiceAccountResourceRecoversEmptyUpdateResponse(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	initial := serviceAccountTestConfig(server.URL, "Initial")
	updated := serviceAccountTestConfig(server.URL, "Updated")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{Config: initial},
			{
				Config:    updated,
				PreConfig: service.returnEmptyUpdateResponse,
				Check:     tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "display_name", "Updated"),
			},
		},
	})
}

func TestServiceAccountResourcePreservesExplicitEmptyDisplayName(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	config := serviceAccountTestConfigWithExplicitDisplayName(server.URL, "")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{{
			Config: config,
			Check:  tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "display_name", ""),
		}},
	})
}

func TestServiceAccountResourceAcceptsUnicodeByCodePointLength(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	displayName := strings.Repeat("界", 63)
	config := serviceAccountTestConfig(server.URL, displayName)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{{
			Config: config,
			Check:  tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "display_name", displayName),
		}},
	})
}

func TestServiceAccountResourceRejectsInvalidInputs(t *testing.T) {
	tests := map[string]struct {
		id             string
		organizationID string
		displayName    string
		errorPattern   string
	}{
		"uppercase resource ID": {
			id: "CI-agent", organizationID: "a1b2c3", displayName: "CI agent",
			errorPattern: "must start with a lowercase letter",
		},
		"short organization ID": {
			id: "ci-agent", organizationID: "abc12", displayName: "CI agent",
			errorPattern: "must contain exactly six",
		},
		"display name over 63 code points": {
			id: "ci-agent", organizationID: "a1b2c3", displayName: strings.Repeat("界", 64),
			errorPattern: "at most 63",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			service, server := startServiceAccountTestServer(t)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				CheckDestroy:             service.checkEmpty,
				Steps: []tfresource.TestStep{{
					Config:      serviceAccountTestConfigWithIdentity(server.URL, test.id, test.organizationID, test.displayName),
					ExpectError: regexp.MustCompile(test.errorPattern),
				}},
			})
		})
	}
}
