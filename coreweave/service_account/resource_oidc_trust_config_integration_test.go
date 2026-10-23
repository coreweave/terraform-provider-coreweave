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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const oidcTrustConfigResourceAddress = "coreweave_service_account_oidc_trust_config.test"

type oidcTrustConfigTestServer struct {
	directoryv1alphaconnect.UnimplementedDirectoryServiceHandler

	mu      sync.Mutex
	configs map[string]*directoryv1alpha.ServiceAccountOidcTrustConfig

	emptyCreateResponse      bool
	emptyCreateResponseUID   bool
	emptyUpdateResponse      bool
	notFoundOnNextUpdateCall bool
}

func newOIDCTrustConfigTestServer() *oidcTrustConfigTestServer {
	return &oidcTrustConfigTestServer{configs: make(map[string]*directoryv1alpha.ServiceAccountOidcTrustConfig)}
}

func cloneOIDCTrustConfig(config *directoryv1alpha.ServiceAccountOidcTrustConfig) *directoryv1alpha.ServiceAccountOidcTrustConfig {
	return proto.Clone(config).(*directoryv1alpha.ServiceAccountOidcTrustConfig)
}

func (s *oidcTrustConfigTestServer) CreateServiceAccountOidcTrustConfig(_ context.Context, request *directoryv1alpha.CreateServiceAccountOidcTrustConfigRequest) (*directoryv1alpha.ServiceAccountOidcTrustConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := request.GetParent() + "/oidcTrustConfigs/" + request.GetServiceAccountOidcTrustConfigId()
	if _, exists := s.configs[name]; exists {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "OIDC trust config exists")
	}
	now := timestamppb.Now()
	input := request.GetServiceAccountOidcTrustConfig()
	config := &directoryv1alpha.ServiceAccountOidcTrustConfig{
		Name: name, Uid: proto.String("74931f1b-b5cc-48d4-9a97-24073d43fd82"), Description: proto.String(input.GetDescription()),
		Cel: input.Cel, WorkloadFederationOidcConfigUid: input.WorkloadFederationOidcConfigUid,
		CreateTime: now, UpdateTime: now,
	}
	s.configs[name] = config
	if s.emptyCreateResponse {
		s.emptyCreateResponse = false
		return nil, nil
	}
	response := cloneOIDCTrustConfig(config)
	if s.emptyCreateResponseUID {
		s.emptyCreateResponseUID = false
		response.Uid = nil
	}
	return response, nil
}

func (s *oidcTrustConfigTestServer) GetServiceAccountOidcTrustConfig(_ context.Context, request *directoryv1alpha.GetServiceAccountOidcTrustConfigRequest) (*directoryv1alpha.ServiceAccountOidcTrustConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, exists := s.configs[request.GetName()]
	if !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "OIDC trust config not found")
	}
	return cloneOIDCTrustConfig(config), nil
}

func (s *oidcTrustConfigTestServer) UpdateServiceAccountOidcTrustConfig(_ context.Context, request *directoryv1alpha.UpdateServiceAccountOidcTrustConfigRequest) (*directoryv1alpha.ServiceAccountOidcTrustConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := request.GetServiceAccountOidcTrustConfig()
	if s.notFoundOnNextUpdateCall {
		s.notFoundOnNextUpdateCall = false
		return nil, connect.Errorf(connect.CodeNotFound, "OIDC trust config not found")
	}
	config, exists := s.configs[input.GetName()]
	if !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "OIDC trust config not found")
	}
	for _, field := range request.GetUpdateMask().GetPaths() {
		switch field {
		case "description":
			config.Description = proto.String(input.GetDescription())
		case "cel":
			config.Cel = input.Cel
		case "workload_federation_oidc_config_uid":
			config.WorkloadFederationOidcConfigUid = input.WorkloadFederationOidcConfigUid
		}
	}
	config.UpdateTime = timestamppb.Now()
	if s.emptyUpdateResponse {
		s.emptyUpdateResponse = false
		return nil, nil
	}
	return cloneOIDCTrustConfig(config), nil
}

func (s *oidcTrustConfigTestServer) DeleteServiceAccountOidcTrustConfig(_ context.Context, request *directoryv1alpha.DeleteServiceAccountOidcTrustConfigRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.configs[request.GetName()]; !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "OIDC trust config not found")
	}
	delete(s.configs, request.GetName())
	return &emptypb.Empty{}, nil
}

func (s *oidcTrustConfigTestServer) checkEmpty(*terraform.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.configs) != 0 {
		return fmt.Errorf("expected all OIDC trust configs deleted, found %d", len(s.configs))
	}
	return nil
}

func (s *oidcTrustConfigTestServer) removeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs = make(map[string]*directoryv1alpha.ServiceAccountOidcTrustConfig)
}

func (s *oidcTrustConfigTestServer) returnEmptyCreateResponse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyCreateResponse = true
}

func (s *oidcTrustConfigTestServer) returnEmptyCreateResponseUID() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyCreateResponseUID = true
}

func (s *oidcTrustConfigTestServer) returnEmptyUpdateResponse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyUpdateResponse = true
}

func (s *oidcTrustConfigTestServer) returnNotFoundOnNextUpdate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notFoundOnNextUpdateCall = true
}

func startOIDCTrustConfigTestServer(t *testing.T) (*oidcTrustConfigTestServer, *httptest.Server) {
	t.Helper()
	service := newOIDCTrustConfigTestServer()
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

func oidcTrustConfigTestConfig(endpoint, description, cel, oidcConfigID string) string {
	descriptionAttribute := ""
	if description != "" {
		descriptionAttribute = fmt.Sprintf("  description = %q\n", description)
	}
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_service_account_oidc_trust_config" "test" {
  id                                 = "main-repo"
  organization_id                    = "a1b2c3"
  service_account_id                 = "ci-agent"
  workload_federation_oidc_config_id = %q
%s  cel                                = %q
}
`, endpoint, oidcConfigID, descriptionAttribute, cel)
}

func oidcTrustConfigTestConfigWithExplicitDescription(endpoint, description string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_service_account_oidc_trust_config" "test" {
  id                                 = "main-repo"
  organization_id                    = "a1b2c3"
  service_account_id                 = "ci-agent"
  description                        = %q
  cel                                = "token.repository == 'example/repo'"
  workload_federation_oidc_config_id = "8d39f431-715c-4c6c-916a-849292f97920"
}
`, endpoint, description)
}

func oidcTrustConfigTestConfigWithReplacedDescription(endpoint, replacement string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "terraform_data" "description" {
  input            = "stable description"
  triggers_replace = %q
}

resource "coreweave_service_account_oidc_trust_config" "test" {
  id                                 = "main-repo"
  organization_id                    = "a1b2c3"
  service_account_id                 = "ci-agent"
  description                        = terraform_data.description.output
  cel                                = "token.repository == 'example/repo'"
  workload_federation_oidc_config_id = "8d39f431-715c-4c6c-916a-849292f97920"
}
`, endpoint, replacement)
}

func oidcTrustConfigTestConfigWithIdentity(endpoint, id, organizationID, serviceAccountID, description, cel, oidcConfigID string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_service_account_oidc_trust_config" "test" {
  id                                 = %q
  organization_id                    = %q
  service_account_id                 = %q
  description                        = %q
  cel                                = %q
  workload_federation_oidc_config_id = %q
}
`, endpoint, id, organizationID, serviceAccountID, description, cel, oidcConfigID)
}

func TestOIDCTrustConfigResourceLifecycle(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	initial := oidcTrustConfigTestConfig(server.URL, "main branch", `token.ref == "refs/heads/main"`, "8d39f431-715c-4c6c-916a-849292f97920")
	updated := oidcTrustConfigTestConfig(server.URL, "", `token.repository == "example/repo"`, "e3bb1892-f89e-4258-9495-427ed5ace8c7")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: initial,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "name", "organizations/a1b2c3/serviceAccounts/ci-agent/oidcTrustConfigs/main-repo"),
					tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "description", "main branch"),
					tfresource.TestCheckResourceAttrSet(oidcTrustConfigResourceAddress, "uid"),
					tfresource.TestCheckResourceAttrSet(oidcTrustConfigResourceAddress, "created_at"),
					tfresource.TestCheckResourceAttrSet(oidcTrustConfigResourceAddress, "updated_at"),
				),
			},
			{
				ResourceName:      oidcTrustConfigResourceAddress,
				ImportState:       true,
				ImportStateId:     "organizations/a1b2c3/serviceAccounts/ci-agent/oidcTrustConfigs/main-repo",
				ImportStateVerify: true,
			},
			{
				Config: updated,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckNoResourceAttr(oidcTrustConfigResourceAddress, "description"),
					tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "cel", `token.repository == "example/repo"`),
					tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "workload_federation_oidc_config_id", "e3bb1892-f89e-4258-9495-427ed5ace8c7"),
				),
			},
		},
	})
}

func TestOIDCTrustConfigResourceNotFoundRemovesState(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	config := oidcTrustConfigTestConfig(server.URL, "remote deletion", "true", "8d39f431-715c-4c6c-916a-849292f97920")

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

func TestOIDCTrustConfigResourceResolvedNoOpUpdate(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: oidcTrustConfigTestConfigWithReplacedDescription(server.URL, "initial"),
				Check:  tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "description", "stable description"),
			},
			{
				Config: oidcTrustConfigTestConfigWithReplacedDescription(server.URL, "replacement"),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "description", "stable description"),
					tfresource.TestCheckResourceAttrSet(oidcTrustConfigResourceAddress, "uid"),
					tfresource.TestCheckResourceAttrSet(oidcTrustConfigResourceAddress, "created_at"),
				),
			},
		},
	})
}

func TestOIDCTrustConfigResourceRejectsEmptyCreateResponse(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	service.returnEmptyCreateResponse()
	t.Cleanup(service.removeAll)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{{
			Config:      oidcTrustConfigTestConfig(server.URL, "empty response", "true", "8d39f431-715c-4c6c-916a-849292f97920"),
			ExpectError: regexp.MustCompile("expected"),
		}},
	})
}

func TestOIDCTrustConfigResourceRejectsCreateResponseWithoutUID(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	service.returnEmptyCreateResponseUID()
	t.Cleanup(service.removeAll)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{{
			Config:      oidcTrustConfigTestConfig(server.URL, "missing UID", "true", "8d39f431-715c-4c6c-916a-849292f97920"),
			ExpectError: regexp.MustCompile("OIDC trust configuration UID is missing"),
		}},
	})
}

func TestOIDCTrustConfigResourceUpdateNotFoundRetainsState(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	initial := oidcTrustConfigTestConfig(server.URL, "initial", "true", "8d39f431-715c-4c6c-916a-849292f97920")
	updated := oidcTrustConfigTestConfig(server.URL, "updated", "token.repository == 'example/repo'", "8d39f431-715c-4c6c-916a-849292f97920")

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
				Check:  tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "description", "updated"),
			},
		},
	})
}

func TestOIDCTrustConfigResourceRecoversEmptyUpdateResponse(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	initial := oidcTrustConfigTestConfig(server.URL, "initial", "true", "8d39f431-715c-4c6c-916a-849292f97920")
	updated := oidcTrustConfigTestConfig(server.URL, "updated", "token.repository == 'example/repo'", "8d39f431-715c-4c6c-916a-849292f97920")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{Config: initial},
			{
				Config:    updated,
				PreConfig: service.returnEmptyUpdateResponse,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "description", "updated"),
					tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "cel", "token.repository == 'example/repo'"),
				),
			},
		},
	})
}

func TestOIDCTrustConfigResourcePreservesExplicitEmptyDescription(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	config := oidcTrustConfigTestConfigWithExplicitDescription(server.URL, "")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{{
			Config: config,
			Check:  tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "description", ""),
		}},
	})
}

func TestOIDCTrustConfigResourcePreservesOmittedDescription(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	config := oidcTrustConfigTestConfig(server.URL, "", "true", "8d39f431-715c-4c6c-916a-849292f97920")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: config,
				Check:  tfresource.TestCheckNoResourceAttr(oidcTrustConfigResourceAddress, "description"),
			},
			{
				Config:   config,
				PlanOnly: true,
			},
		},
	})
}

func TestOIDCTrustConfigResourceAcceptsUnicodeByCodePointLength(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	description := strings.Repeat("界", 400)
	cel := strings.Repeat("界", 400)
	config := oidcTrustConfigTestConfig(server.URL, description, cel, "8d39f431-715c-4c6c-916a-849292f97920")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{{
			Config: config,
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "description", description),
				tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "cel", cel),
			),
		}},
	})
}

func TestOIDCTrustConfigResourceRejectsInvalidInputs(t *testing.T) {
	tests := map[string]struct {
		id, organizationID, serviceAccountID, description, cel, oidcConfigID string
		errorPattern                                                         string
	}{
		"uppercase config ID": {
			id: "Main-repo", organizationID: "a1b2c3", serviceAccountID: "ci-agent", cel: "true", oidcConfigID: "8d39f431-715c-4c6c-916a-849292f97920",
			errorPattern: "must start with a lowercase letter",
		},
		"short organization ID": {
			id: "main-repo", organizationID: "abc12", serviceAccountID: "ci-agent", cel: "true", oidcConfigID: "8d39f431-715c-4c6c-916a-849292f97920",
			errorPattern: "must contain exactly six",
		},
		"uppercase service account ID": {
			id: "main-repo", organizationID: "a1b2c3", serviceAccountID: "CI-agent", cel: "true", oidcConfigID: "8d39f431-715c-4c6c-916a-849292f97920",
			errorPattern: "must start with a lowercase letter",
		},
		"empty CEL": {
			id: "main-repo", organizationID: "a1b2c3", serviceAccountID: "ci-agent", cel: "", oidcConfigID: "8d39f431-715c-4c6c-916a-849292f97920",
			errorPattern: "between 1 and 4096",
		},
		"invalid OIDC config UID": {
			id: "main-repo", organizationID: "a1b2c3", serviceAccountID: "ci-agent", cel: "true", oidcConfigID: "not-a-uuid",
			errorPattern: "must be a UUID",
		},
		"description over 2048 code points": {
			id: "main-repo", organizationID: "a1b2c3", serviceAccountID: "ci-agent", description: strings.Repeat("界", 2049), cel: "true", oidcConfigID: "8d39f431-715c-4c6c-916a-849292f97920",
			errorPattern: "at most 2048",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			service, server := startOIDCTrustConfigTestServer(t)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				CheckDestroy:             service.checkEmpty,
				Steps: []tfresource.TestStep{{
					Config:      oidcTrustConfigTestConfigWithIdentity(server.URL, test.id, test.organizationID, test.serviceAccountID, test.description, test.cel, test.oidcConfigID),
					ExpectError: regexp.MustCompile(test.errorPattern),
				}},
			})
		})
	}
}
