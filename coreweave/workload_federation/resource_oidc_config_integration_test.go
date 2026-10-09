package workloadfederation_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"buf.build/gen/go/coreweave/workload-federation/connectrpc/go/v2/coreweave/workload_federation/control_plane/v1beta1/control_planev1beta1connect"
	controlplanev1beta1 "buf.build/gen/go/coreweave/workload-federation/protocolbuffers/go/coreweave/workload_federation/control_plane/v1beta1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/go-uuid"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const oidcConfigResourceAddress = "coreweave_workload_federation_oidc_config.test"

type oidcConfigTestServer struct {
	control_planev1beta1connect.UnimplementedWFControlPlaneServiceHandler

	mu                       sync.Mutex
	configs                  map[string]*controlplanev1beta1.OIDCConfig
	emptyCreateResponse      bool
	emptyCreateResponseUID   bool
	emptyUpdateResponse      bool
	notFoundOnNextUpdateCall bool
}

func newOIDCConfigTestServer() *oidcConfigTestServer {
	return &oidcConfigTestServer{configs: make(map[string]*controlplanev1beta1.OIDCConfig)}
}

func cloneOIDCConfig(config *controlplanev1beta1.OIDCConfig) *controlplanev1beta1.OIDCConfig {
	return proto.Clone(config).(*controlplanev1beta1.OIDCConfig)
}

func (s *oidcConfigTestServer) GetOIDCConfig(_ context.Context, req *controlplanev1beta1.GetOIDCConfigRequest) (*controlplanev1beta1.GetOIDCConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, ok := s.configs[req.GetUid()]
	if !ok {
		return nil, connect.Errorf(connect.CodeNotFound, "OIDC config not found")
	}
	return &controlplanev1beta1.GetOIDCConfigResponse{Config: cloneOIDCConfig(config)}, nil
}

func (s *oidcConfigTestServer) ListOIDCConfigs(context.Context, *controlplanev1beta1.ListOIDCConfigsRequest) (*controlplanev1beta1.ListOIDCConfigsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	configs := make([]*controlplanev1beta1.OIDCConfig, 0, len(s.configs))
	for _, config := range s.configs {
		configs = append(configs, cloneOIDCConfig(config))
	}
	return &controlplanev1beta1.ListOIDCConfigsResponse{Configs: configs}, nil
}

func (s *oidcConfigTestServer) CreateOIDCConfig(_ context.Context, req *controlplanev1beta1.CreateOIDCConfigRequest) (*controlplanev1beta1.CreateOIDCConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := uuid.GenerateUUID()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	now := timestamppb.Now()
	config := &controlplanev1beta1.OIDCConfig{
		Uid:         id,
		OrgUid:      "test-organization",
		Name:        req.GetName(),
		Description: req.Description,
		IssuerUrl:   req.GetIssuerUrl(),
		Audience:    req.GetAudience(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if req.HasActive() && !req.GetActive() {
		config.DeactivatedAt = now
	}
	s.configs[id] = config
	if s.emptyCreateResponse {
		s.emptyCreateResponse = false
		return &controlplanev1beta1.CreateOIDCConfigResponse{}, nil
	}
	responseConfig := cloneOIDCConfig(config)
	if s.emptyCreateResponseUID {
		s.emptyCreateResponseUID = false
		responseConfig.Uid = ""
	}
	return &controlplanev1beta1.CreateOIDCConfigResponse{Config: responseConfig}, nil
}

func (s *oidcConfigTestServer) UpdateOIDCConfig(_ context.Context, req *controlplanev1beta1.UpdateOIDCConfigRequest) (*controlplanev1beta1.UpdateOIDCConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(req.GetUpdateMask().GetPaths()) == 0 {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "update mask must contain at least one path")
	}
	if s.notFoundOnNextUpdateCall {
		s.notFoundOnNextUpdateCall = false
		return nil, connect.Errorf(connect.CodeNotFound, "OIDC config not found")
	}

	config, ok := s.configs[req.GetUid()]
	if !ok {
		return nil, connect.Errorf(connect.CodeNotFound, "OIDC config not found")
	}
	now := timestamppb.Now()
	for _, field := range req.GetUpdateMask().GetPaths() {
		switch field {
		case "name":
			config.Name = req.GetName()
		case "description":
			config.Description = req.Description
		case "issuer_url":
			config.IssuerUrl = req.GetIssuerUrl()
		case "audience":
			config.Audience = req.GetAudience()
		case "active":
			if req.GetActive() {
				config.DeactivatedAt = nil
			} else {
				config.DeactivatedAt = now
			}
		}
	}
	config.UpdatedAt = now
	if s.emptyUpdateResponse {
		s.emptyUpdateResponse = false
		return &controlplanev1beta1.UpdateOIDCConfigResponse{}, nil
	}
	return &controlplanev1beta1.UpdateOIDCConfigResponse{Config: cloneOIDCConfig(config)}, nil
}

func (s *oidcConfigTestServer) DeleteOIDCConfig(_ context.Context, req *controlplanev1beta1.DeleteOIDCConfigRequest) (*controlplanev1beta1.DeleteOIDCConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.configs[req.GetUid()]; !ok {
		return nil, connect.Errorf(connect.CodeNotFound, "OIDC config not found")
	}
	delete(s.configs, req.GetUid())
	return &controlplanev1beta1.DeleteOIDCConfigResponse{}, nil
}

func (s *oidcConfigTestServer) removeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.configs)
}

func (s *oidcConfigTestServer) seed(configs ...*controlplanev1beta1.OIDCConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.configs)
	for _, config := range configs {
		s.configs[config.GetUid()] = cloneOIDCConfig(config)
	}
}

func (s *oidcConfigTestServer) returnEmptyCreateResponse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyCreateResponse = true
}

func (s *oidcConfigTestServer) returnEmptyCreateResponseUID() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyCreateResponseUID = true
}

func (s *oidcConfigTestServer) returnNotFoundOnNextUpdate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notFoundOnNextUpdateCall = true
}

func (s *oidcConfigTestServer) returnEmptyUpdateResponse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyUpdateResponse = true
}

func (s *oidcConfigTestServer) deactivateAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := timestamppb.Now()
	for _, config := range s.configs {
		config.DeactivatedAt = now
		config.UpdatedAt = now
	}
}

func (s *oidcConfigTestServer) checkEmpty(*terraform.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.configs) != 0 {
		return fmt.Errorf("expected all OIDC configs to be deleted, found %d", len(s.configs))
	}
	return nil
}

func startOIDCConfigTestServer(t *testing.T) (*oidcConfigTestServer, *httptest.Server) {
	t.Helper()
	service := newOIDCConfigTestServer()
	rpcServer := connect.NewServer()
	control_planev1beta1connect.RegisterWFControlPlaneServiceHandler(rpcServer, service)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	// BuildClient gives environment variables precedence over provider-block
	// configuration. Override any developer credentials so these hermetic tests
	// can never send requests outside the local fake service.
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")

	return service, server
}

func oidcConfigTestConfig(endpoint, name, description, issuerURL, audience string, active bool) string {
	descriptionAttribute := ""
	if description != "" {
		descriptionAttribute = fmt.Sprintf("  description = %q\n", description)
	}
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_workload_federation_oidc_config" "test" {
  name       = %q
%s  issuer_url = %q
  audience   = %q
  active     = %t
}
`, endpoint, name, descriptionAttribute, issuerURL, audience, active)
}

func oidcConfigTestConfigWithReplacedAudience(endpoint, replacement string) string {
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "terraform_data" "audience" {
  input            = "stable-audience"
  triggers_replace = %q
}

resource "coreweave_workload_federation_oidc_config" "test" {
  name       = "resolved-no-op"
  issuer_url = "https://resolved-no-op.example.com"
  audience   = terraform_data.audience.output
}
`, endpoint, replacement)
}

func oidcConfigTestConfigWithoutActive(endpoint, name, description, issuerURL, audience string) string {
	descriptionAttribute := ""
	if description != "" {
		descriptionAttribute = fmt.Sprintf("  description = %q\n", description)
	}
	return fmt.Sprintf(`
provider "coreweave" {
  endpoint = %q
  token    = "test-token"
}

resource "coreweave_workload_federation_oidc_config" "test" {
  name       = %q
%s  issuer_url = %q
  audience   = %q
}
`, endpoint, name, descriptionAttribute, issuerURL, audience)
}

func TestOIDCConfigResourceLifecycle(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)

	initialConfig := oidcConfigTestConfig(server.URL, "initial", "initial description", "https://initial.example.com", "initial-audience", true)
	updatedConfig := oidcConfigTestConfig(server.URL, "updated", "updated description", "https://updated.example.com", "updated-audience", false)
	reactivatedConfig := oidcConfigTestConfig(server.URL, "updated", "", "https://updated.example.com", "updated-audience", true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: initialConfig,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttrSet(oidcConfigResourceAddress, "id"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "organization_id", "test-organization"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "name", "initial"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "description", "initial description"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "issuer_url", "https://initial.example.com"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "audience", "initial-audience"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "active", "true"),
					tfresource.TestCheckResourceAttrSet(oidcConfigResourceAddress, "created_at"),
					tfresource.TestCheckResourceAttrSet(oidcConfigResourceAddress, "updated_at"),
				),
			},
			{
				ResourceName:      oidcConfigResourceAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: updatedConfig,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "name", "updated"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "description", "updated description"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "issuer_url", "https://updated.example.com"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "audience", "updated-audience"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "active", "false"),
					tfresource.TestCheckResourceAttrSet(oidcConfigResourceAddress, "deactivated_at"),
				),
			},
			{
				Config: reactivatedConfig,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckNoResourceAttr(oidcConfigResourceAddress, "description"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "active", "true"),
					tfresource.TestCheckNoResourceAttr(oidcConfigResourceAddress, "deactivated_at"),
				),
			},
		},
	})
}

func TestOIDCConfigResourceNotFoundRemovesState(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)
	config := oidcConfigTestConfig(server.URL, "not-found", "", "https://not-found.example.com", "not-found-audience", true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: config,
			},
			{
				Config:             config,
				PreConfig:          service.removeAll,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestOIDCConfigResourceResolvedNoOpUpdate(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: oidcConfigTestConfigWithReplacedAudience(server.URL, "initial"),
				Check: tfresource.TestCheckResourceAttr(
					oidcConfigResourceAddress,
					"audience",
					"stable-audience",
				),
			},
			{
				Config: oidcConfigTestConfigWithReplacedAudience(server.URL, "replacement"),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "audience", "stable-audience"),
					tfresource.TestCheckResourceAttrSet(oidcConfigResourceAddress, "organization_id"),
					tfresource.TestCheckResourceAttrSet(oidcConfigResourceAddress, "created_at"),
					tfresource.TestCheckResourceAttrSet(oidcConfigResourceAddress, "updated_at"),
				),
			},
		},
	})
}

func TestOIDCConfigResourceRejectsEmptyCreateResponse(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)
	service.returnEmptyCreateResponse()
	t.Cleanup(service.removeAll)
	config := oidcConfigTestConfig(server.URL, "create-recovery", "", "https://create-recovery.example.com", "create-recovery-audience", true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile("OIDC Configuration ID Missing After Creation"),
			},
		},
	})
}

func TestOIDCConfigResourceRejectsEmptyCreateResponseUID(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)
	service.returnEmptyCreateResponseUID()
	t.Cleanup(service.removeAll)
	config := oidcConfigTestConfig(server.URL, "create-empty-uid", "", "https://create-empty-uid.example.com", "create-empty-uid-audience", true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile("OIDC Configuration ID Missing After Creation"),
			},
		},
	})
}

func TestOIDCConfigResourceUpdateNotFoundRetainsState(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)
	initialConfig := oidcConfigTestConfig(server.URL, "update-not-found", "", "https://update-not-found.example.com", "initial-audience", true)
	updatedConfig := oidcConfigTestConfig(server.URL, "update-not-found", "", "https://update-not-found.example.com", "updated-audience", true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: initialConfig,
			},
			{
				Config:      updatedConfig,
				PreConfig:   service.returnNotFoundOnNextUpdate,
				ExpectError: regexp.MustCompile("Unable to Update OIDC Configuration"),
			},
			{
				Config: updatedConfig,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttrSet(oidcConfigResourceAddress, "id"),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "audience", "updated-audience"),
				),
			},
		},
	})
}

func TestOIDCConfigResourceAllowsHTTPIssuerForDevelopmentEndpoints(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)
	config := oidcConfigTestConfig(server.URL, "http-issuer", "", "http://localhost:8080", "http-issuer-audience", true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: config,
				Check: tfresource.TestCheckResourceAttr(
					oidcConfigResourceAddress,
					"issuer_url",
					"http://localhost:8080",
				),
			},
		},
	})
}

func TestOIDCConfigResourceRejectsHTTPNonLoopbackIssuer(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)
	config := oidcConfigTestConfig(server.URL, "http-non-loopback", "", "http://issuer.example.com", "http-non-loopback-audience", true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile("Invalid OIDC issuer URL"),
			},
		},
	})
}

func TestOIDCConfigResourcePreservesOmittedActiveAfterRemoteDeactivation(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)
	config := oidcConfigTestConfigWithoutActive(server.URL, "omitted-active", "", "https://omitted-active.example.com", "omitted-active-audience")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: config,
				Check:  tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "active", "true"),
			},
			{
				Config:    config,
				PreConfig: service.deactivateAll,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(oidcConfigResourceAddress, plancheck.ResourceActionNoop),
					},
				},
				Check: tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "active", "false"),
			},
		},
	})
}

func TestOIDCConfigResourceRecoversEmptyUpdateResponse(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)
	initialConfig := oidcConfigTestConfig(server.URL, "empty-update-response", "", "https://empty-update-response.example.com", "initial-audience", true)
	updatedConfig := oidcConfigTestConfig(server.URL, "empty-update-response", "", "https://empty-update-response.example.com", "updated-audience", true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: initialConfig,
			},
			{
				Config:    updatedConfig,
				PreConfig: service.returnEmptyUpdateResponse,
				Check:     tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "audience", "updated-audience"),
			},
		},
	})
}

func TestOIDCConfigResourceAcceptsUnicodeByCodePointLength(t *testing.T) {
	service, server := startOIDCConfigTestServer(t)
	description := strings.Repeat("界", 400)
	audience := strings.Repeat("界", 400)
	config := oidcConfigTestConfig(server.URL, "unicode-length", description, "https://unicode-length.example.com", audience, true)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkEmpty,
		Steps: []tfresource.TestStep{
			{
				Config: config,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "description", description),
					tfresource.TestCheckResourceAttr(oidcConfigResourceAddress, "audience", audience),
				),
			},
		},
	})
}
