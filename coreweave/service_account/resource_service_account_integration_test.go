package serviceaccount_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	serviceAccountAddress  = "coreweave_service_account.test"
	directoryProcedureRoot = "/coreweave.directory.v1alpha.InternalDirectoryService/"
)

// These wire types are deliberately independent from the production client.
// They make the fake enforce the field names and shapes in the API contract.
type createRequestWire struct {
	ServiceAccount *struct {
		DisplayName *string `json:"displayName"`
	} `json:"serviceAccount"`
}

type updateRequestWire struct {
	ServiceAccount *struct {
		Name        string  `json:"name"`
		DisplayName *string `json:"displayName"`
	} `json:"serviceAccount"`
	UpdateMask string `json:"updateMask"`
}

type nameRequestWire struct {
	Name string `json:"name"`
}

type serviceAccountWire struct {
	Name           string  `json:"name"`
	UID            string  `json:"uid"`
	OrganizationID string  `json:"orgId"`
	Creator        string  `json:"creator"`
	DisplayName    *string `json:"displayName,omitempty"`
	SystemManaged  bool    `json:"systemManaged,omitempty"`
	Active         bool    `json:"active,omitempty"`
	CreateTime     string  `json:"createTime"`
	UpdateTime     string  `json:"updateTime"`
}

type serviceAccountTestServer struct {
	mu                 sync.Mutex
	nextID             int
	accounts           map[string]*serviceAccountWire
	calls              []string
	failNextDeactivate bool
	failNextDelete     bool
}

func newServiceAccountTestServer() *serviceAccountTestServer {
	return &serviceAccountTestServer{accounts: make(map[string]*serviceAccountWire)}
}

func (s *serviceAccountTestServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.Header.Get("Connect-Protocol-Version") != "1" || request.Header.Get("Authorization") != "Bearer test-token" {
		writer.Header().Set("Content-Type", "application/json")
		s.writeError(writer, http.StatusBadRequest, "invalid_argument", "invalid request metadata")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	writer.Header().Set("Content-Type", "application/json")
	s.calls = append(s.calls, request.URL.Path)
	switch request.URL.Path {
	case directoryProcedureRoot + "CreateServiceAccount":
		var payload createRequestWire
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.ServiceAccount == nil {
			s.writeError(writer, http.StatusBadRequest, "invalid_argument", "serviceAccount is required")
			return
		}
		s.nextID++
		uid := fmt.Sprintf("sa-test-%d", s.nextID)
		name := "serviceAccounts/" + uid
		now := time.Now().UTC().Format(time.RFC3339)
		displayName := payload.ServiceAccount.DisplayName
		if displayName == nil {
			generated := "Generated service account"
			displayName = &generated
		}
		account := &serviceAccountWire{
			Name:           name,
			UID:            uid,
			OrganizationID: "org-test",
			Creator:        "users/u-test",
			DisplayName:    displayName,
			Active:         true,
			CreateTime:     now,
			UpdateTime:     now,
		}
		s.accounts[name] = account
		s.writeJSON(writer, account)
	case directoryProcedureRoot + "GetServiceAccount":
		var payload nameRequestWire
		if json.NewDecoder(request.Body).Decode(&payload) != nil {
			s.writeError(writer, http.StatusBadRequest, "invalid_argument", "name is required")
			return
		}
		s.writeAccount(writer, payload.Name)
	case directoryProcedureRoot + "UpdateServiceAccount":
		var payload updateRequestWire
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.ServiceAccount == nil || payload.UpdateMask != "displayName" {
			s.writeError(writer, http.StatusBadRequest, "invalid_argument", "displayName update is required")
			return
		}
		account, ok := s.accounts[payload.ServiceAccount.Name]
		if !ok {
			s.writeError(writer, http.StatusNotFound, "not_found", "service account not found")
			return
		}
		account.DisplayName = payload.ServiceAccount.DisplayName
		account.UpdateTime = time.Now().UTC().Format(time.RFC3339)
		s.writeJSON(writer, account)
	case directoryProcedureRoot + "ActivateServiceAccount":
		s.setActive(writer, request, true)
	case directoryProcedureRoot + "DeactivateServiceAccount":
		if s.failNextDeactivate {
			s.failNextDeactivate = false
			s.writeError(writer, http.StatusForbidden, "permission_denied", "deactivation unavailable")
			return
		}
		s.setActive(writer, request, false)
	case directoryProcedureRoot + "DeleteServiceAccount":
		if s.failNextDelete {
			s.failNextDelete = false
			s.writeError(writer, http.StatusForbidden, "permission_denied", "deletion unavailable")
			return
		}
		var payload nameRequestWire
		if json.NewDecoder(request.Body).Decode(&payload) != nil {
			s.writeError(writer, http.StatusBadRequest, "invalid_argument", "name is required")
			return
		}
		if _, ok := s.accounts[payload.Name]; !ok {
			s.writeError(writer, http.StatusNotFound, "not_found", "service account not found")
			return
		}
		delete(s.accounts, payload.Name)
		s.writeJSON(writer, struct{}{})
	default:
		s.writeError(writer, http.StatusNotFound, "unimplemented", "unknown procedure")
	}
}

func (s *serviceAccountTestServer) setActive(writer http.ResponseWriter, request *http.Request, active bool) {
	var payload nameRequestWire
	if json.NewDecoder(request.Body).Decode(&payload) != nil {
		s.writeError(writer, http.StatusBadRequest, "invalid_argument", "name is required")
		return
	}
	account, ok := s.accounts[payload.Name]
	if !ok {
		s.writeError(writer, http.StatusNotFound, "not_found", "service account not found")
		return
	}
	account.Active = active
	account.UpdateTime = time.Now().UTC().Format(time.RFC3339)
	s.writeJSON(writer, account)
}

func (s *serviceAccountTestServer) writeAccount(writer http.ResponseWriter, name string) {
	account, ok := s.accounts[name]
	if !ok {
		s.writeError(writer, http.StatusNotFound, "not_found", "service account not found")
		return
	}
	s.writeJSON(writer, account)
}

func (s *serviceAccountTestServer) writeJSON(writer http.ResponseWriter, value any) {
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		panic(err)
	}
}

func (s *serviceAccountTestServer) writeError(writer http.ResponseWriter, status int, code, message string) {
	writer.WriteHeader(status)
	s.writeJSON(writer, map[string]string{"code": code, "message": message})
}

func (s *serviceAccountTestServer) removeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.accounts)
}

func (s *serviceAccountTestServer) deactivateAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.accounts {
		account.Active = false
		account.UpdateTime = time.Now().UTC().Format(time.RFC3339)
	}
}

func (s *serviceAccountTestServer) failInitialDeactivation(deleteFails bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNextDeactivate = true
	s.failNextDelete = deleteFails
}

func (s *serviceAccountTestServer) checkLastCallIsNotGet(*terraform.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return fmt.Errorf("expected at least one API call")
	}
	if s.calls[len(s.calls)-1] == directoryProcedureRoot+"GetServiceAccount" {
		return fmt.Errorf("update ended with a read-after-write GetServiceAccount call")
	}
	return nil
}

func (s *serviceAccountTestServer) checkEmpty(*terraform.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.accounts) != 0 {
		return fmt.Errorf("expected all service accounts to be deleted, found %d", len(s.accounts))
	}
	return nil
}

func startServiceAccountTestServer(t *testing.T) (*serviceAccountTestServer, *httptest.Server) {
	t.Helper()
	service := newServiceAccountTestServer()
	server := httptest.NewServer(service)
	t.Cleanup(server.Close)
	// Environment values override provider-block values. Pin both so tests can
	// never use credentials or an endpoint exported by the developer.
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	return service, server
}

func serviceAccountConfig(endpoint, displayName string, active *bool) string {
	activeAttribute := ""
	if active != nil {
		activeAttribute = fmt.Sprintf("  active       = %t\n", *active)
	}
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_service_account" "test" {
  display_name = %q
%s}
`, endpoint, displayName, activeAttribute)
}

func serviceAccountConfigWithoutDisplayName(endpoint string, active bool) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_service_account" "test" {
  active = %t
}
`, endpoint, active)
}

func TestServiceAccountResourceLifecycle(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	active := true
	inactive := false

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: serviceAccountConfig(server.URL, "Terraform automation", &inactive),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttrSet(serviceAccountAddress, "id"),
					tfresource.TestCheckResourceAttrSet(serviceAccountAddress, "name"),
					tfresource.TestCheckResourceAttrSet(serviceAccountAddress, "uid"),
					tfresource.TestCheckResourceAttr(serviceAccountAddress, "organization_id", "org-test"),
					tfresource.TestCheckResourceAttr(serviceAccountAddress, "creator", "users/u-test"),
					tfresource.TestCheckResourceAttr(serviceAccountAddress, "display_name", "Terraform automation"),
					tfresource.TestCheckResourceAttr(serviceAccountAddress, "system_managed", "false"),
					tfresource.TestCheckResourceAttr(serviceAccountAddress, "active", "false"),
					tfresource.TestCheckResourceAttrSet(serviceAccountAddress, "created_at"),
					tfresource.TestCheckResourceAttrSet(serviceAccountAddress, "updated_at"),
				),
			},
			{
				ResourceName:      serviceAccountAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: serviceAccountConfig(server.URL, "Deployment automation", &active),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(serviceAccountAddress, "display_name", "Deployment automation"),
					tfresource.TestCheckResourceAttr(serviceAccountAddress, "active", "true"),
					service.checkLastCallIsNotGet,
				),
			},
			{
				Config: serviceAccountConfig(server.URL, "Deployment automation", &inactive),
				Check:  tfresource.TestCheckResourceAttr(serviceAccountAddress, "active", "false"),
			},
			{
				Config: serviceAccountConfig(server.URL, "", &inactive),
				Check:  tfresource.TestCheckResourceAttr(serviceAccountAddress, "display_name", ""),
			},
		},
	})
}

func TestServiceAccountResourceAcceptsServerDefaultDisplayName(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	config := serviceAccountConfigWithoutDisplayName(server.URL, true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: config,
				Check:  tfresource.TestCheckResourceAttr(serviceAccountAddress, "display_name", "Generated service account"),
			},
			{
				Config: config,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(serviceAccountAddress, plancheck.ResourceActionNoop),
				}},
			},
		},
	})
}

func TestServiceAccountResourceCleansUpFailedInitialDeactivation(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	service.failInitialDeactivation(false)
	inactive := false

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{{
			Config:      serviceAccountConfig(server.URL, "Inactive account", &inactive),
			ExpectError: regexp.MustCompile("Unable to Set Initial Service Account Activation State"),
		}},
	})
}

func TestServiceAccountResourceRetainsStateWhenInitialDeactivationCleanupFails(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	service.failInitialDeactivation(true)
	t.Cleanup(service.removeAll)
	inactive := false

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{{
			Config:      serviceAccountConfig(server.URL, "Inactive account", &inactive),
			ExpectError: regexp.MustCompile(`(?s)also could not.*delete the new account`),
		}},
	})
}

func TestServiceAccountResourcePreservesOmittedActiveAfterRemoteDeactivation(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	config := serviceAccountConfig(server.URL, "Preserve deactivation", nil)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{Config: config, Check: tfresource.TestCheckResourceAttr(serviceAccountAddress, "active", "true")},
			{
				Config:    config,
				PreConfig: service.deactivateAll,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(serviceAccountAddress, plancheck.ResourceActionNoop),
				}},
				Check: tfresource.TestCheckResourceAttr(serviceAccountAddress, "active", "false"),
			},
		},
	})
}

func TestServiceAccountResourceNotFoundRemovesState(t *testing.T) {
	service, server := startServiceAccountTestServer(t)
	active := true
	config := serviceAccountConfig(server.URL, "Deleted remotely", &active)

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
