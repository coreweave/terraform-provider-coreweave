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
	"time"

	"buf.build/gen/go/coreweave/directory/connectrpc/go/v2/coreweave/directory/v1alpha/directoryv1alphaconnect"
	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	serviceaccount "github.com/coreweave/terraform-provider-coreweave/coreweave/service_account"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	frameworkfunction "github.com/hashicorp/terraform-plugin-framework/function"
	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const serviceAccountResourceAddress = "coreweave_iam_service_account.test"

type serviceAccountTestServer struct {
	directoryv1alphaconnect.UnimplementedDirectoryServiceHandler

	mu       sync.Mutex
	accounts map[string]*directoryv1alpha.ServiceAccount
	creates  int

	emptyCreateResponse      bool
	emptyCreateResponseUID   bool
	emptyNextGetResponse     bool
	notFoundGetResponses     int
	emptyUpdateResponse      bool
	notFoundOnNextUpdateCall bool
}

type serviceAccountPlanCheck func()

func (check serviceAccountPlanCheck) CheckPlan(_ context.Context, _ plancheck.CheckPlanRequest, _ *plancheck.CheckPlanResponse) {
	check()
}

type serviceAccountReadBackTestProvider struct {
	frameworkprovider.Provider
	timeout      time.Duration
	initialDelay time.Duration
	maxDelay     time.Duration
}

var _ frameworkprovider.ProviderWithFunctions = &serviceAccountReadBackTestProvider{}

func (p *serviceAccountReadBackTestProvider) Resources(ctx context.Context) []func() frameworkresource.Resource {
	resources := p.Provider.Resources(ctx)
	for index, factory := range resources {
		if _, ok := factory().(*serviceaccount.ServiceAccountResource); ok {
			resources[index] = func() frameworkresource.Resource {
				return serviceaccount.NewServiceAccountResourceWithReadBackPolicy(p.timeout, p.initialDelay, p.maxDelay)
			}
			break
		}
	}
	return resources
}

func (p *serviceAccountReadBackTestProvider) Functions(ctx context.Context) []func() frameworkfunction.Function {
	if providerWithFunctions, ok := p.Provider.(frameworkprovider.ProviderWithFunctions); ok {
		return providerWithFunctions.Functions(ctx)
	}
	return nil
}

func serviceAccountProviderFactoriesWithReadBackPolicy(timeout, initialDelay, maxDelay time.Duration) map[string]func() (tfprotov6.ProviderServer, error) {
	factory := providerserver.NewProtocol6WithError(&serviceAccountReadBackTestProvider{
		Provider:     provider.New("test")(),
		timeout:      timeout,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
	})
	return map[string]func() (tfprotov6.ProviderServer, error){"coreweave": factory}
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
	s.creates++
	account := &directoryv1alpha.ServiceAccount{
		Name: name, Uid: proto.String(fmt.Sprintf("c4f985e4-a2f0-44f8-937e-%012d", s.creates)), OrgId: proto.String("a1b2c3"),
		Creator: proto.String("organizations/a1b2c3/users/test-user"), DisplayName: request.GetServiceAccount().DisplayName,
		SystemManaged: proto.Bool(false), Active: proto.Bool(true), CreateTime: timestamppb.Now(),
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
	if s.notFoundGetResponses > 0 {
		s.notFoundGetResponses--
		return nil, connect.Errorf(connect.CodeNotFound, "service account not yet visible")
	}
	if s.emptyNextGetResponse {
		s.emptyNextGetResponse = false
		return nil, nil
	}

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

func (s *serviceAccountTestServer) checkCreateCount(expected int) tfresource.TestCheckFunc {
	return func(*terraform.State) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.creates != expected {
			return fmt.Errorf("expected %d service account create calls, got %d", expected, s.creates)
		}
		return nil
	}
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

func (s *serviceAccountTestServer) returnEmptyNextGetResponse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyNextGetResponse = true
}

func (s *serviceAccountTestServer) returnNotFoundOnNextGets(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notFoundGetResponses = count
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

resource "coreweave_iam_service_account" "test" {
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

resource "coreweave_iam_service_account" "test" {
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

resource "coreweave_iam_service_account" "test" {
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

resource "coreweave_iam_service_account" "test" {
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
					tfresource.TestCheckNoResourceAttr(serviceAccountResourceAddress, "updated_at"),
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
					tfresource.TestCheckNoResourceAttr(serviceAccountResourceAddress, "updated_at"),
				),
			},
		},
	})
}

func TestServiceAccountResourceRecoversEmptyCreateResponse(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	service.returnEmptyCreateResponse()

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{{
			Config: serviceAccountTestConfig(server.URL, "Empty create response"),
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "name", "organizations/a1b2c3/serviceAccounts/ci-agent"),
				tfresource.TestCheckResourceAttrSet(serviceAccountResourceAddress, "uid"),
			),
		}},
	})
}

func TestServiceAccountResourceRecoversCreateResponseWithoutUID(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	service.returnEmptyCreateResponseUID()

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{{
			Config: serviceAccountTestConfig(server.URL, "Missing UID"),
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "name", "organizations/a1b2c3/serviceAccounts/ci-agent"),
				tfresource.TestCheckResourceAttrSet(serviceAccountResourceAddress, "uid"),
			),
		}},
	})
}

func TestServiceAccountResourceRetriesNotFoundCreateReadBack(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	service.returnEmptyCreateResponse()
	service.returnNotFoundOnNextGets(2)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: serviceAccountProviderFactoriesWithReadBackPolicy(100*time.Millisecond, time.Millisecond, 2*time.Millisecond),
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{{
			Config: serviceAccountTestConfig(server.URL, "Eventually visible"),
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "name", "organizations/a1b2c3/serviceAccounts/ci-agent"),
				tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "uid", "c4f985e4-a2f0-44f8-937e-000000000001"),
				service.checkCreateCount(1),
			),
		}},
	})
}

func TestServiceAccountResourceTaintsWhenCreateReadBackRemainsNotFound(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	service.returnEmptyCreateResponse()
	service.returnNotFoundOnNextGets(1_000)
	config := serviceAccountTestConfig(server.URL, "Create recovery")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: serviceAccountProviderFactoriesWithReadBackPolicy(15*time.Millisecond, time.Millisecond, 2*time.Millisecond),
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)marks.*tainted.*terraform untaint`),
			},
			{
				Config:    config,
				PreConfig: func() { service.returnNotFoundOnNextGets(0) },
				ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(serviceAccountResourceAddress, plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "name", "organizations/a1b2c3/serviceAccounts/ci-agent"),
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "uid", "c4f985e4-a2f0-44f8-937e-000000000002"),
					service.checkCreateCount(2),
				),
			},
		},
	})
}

func TestServiceAccountResourceTaintsWhenCreateReadBackIsInvalid(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	service.returnEmptyCreateResponse()
	service.returnEmptyNextGetResponse()
	config := serviceAccountTestConfig(server.URL, "Invalid read-back")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)marks.*tainted.*terraform untaint`),
			},
			{
				Config: config,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(serviceAccountResourceAddress, plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "name", "organizations/a1b2c3/serviceAccounts/ci-agent"),
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "uid", "c4f985e4-a2f0-44f8-937e-000000000002"),
					service.checkCreateCount(2),
				),
			},
		},
	})
}

func TestServiceAccountResourceAcceptsActivationDriftDuringUpdate(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	initial := serviceAccountTestConfig(server.URL, "Initial")
	updated := serviceAccountTestConfig(server.URL, "Updated")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{Config: initial},
			{
				Config: updated,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					serviceAccountPlanCheck(service.deactivateAll),
				}},
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "display_name", "Updated"),
					tfresource.TestCheckResourceAttr(serviceAccountResourceAddress, "active", "false"),
				),
			},
		},
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
