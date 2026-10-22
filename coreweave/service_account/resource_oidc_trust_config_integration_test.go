package serviceaccount_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		Name: name, Uid: proto.String("74931f1b-b5cc-48d4-9a97-24073d43fd82"), Description: input.Description,
		Cel: input.Cel, WorkloadFederationOidcConfigUid: input.WorkloadFederationOidcConfigUid,
		CreateTime: now, UpdateTime: now,
	}
	s.configs[name] = config
	return cloneOIDCTrustConfig(config), nil
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
	config, exists := s.configs[input.GetName()]
	if !exists {
		return nil, connect.Errorf(connect.CodeNotFound, "OIDC trust config not found")
	}
	for _, field := range request.GetUpdateMask().GetPaths() {
		switch field {
		case "description":
			config.Description = input.Description
		case "cel":
			config.Cel = input.Cel
		case "workload_federation_oidc_config_uid":
			config.WorkloadFederationOidcConfigUid = input.WorkloadFederationOidcConfigUid
		}
	}
	config.UpdateTime = timestamppb.Now()
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

func TestOIDCTrustConfigResourceLifecycle(t *testing.T) {
	service, server := startOIDCTrustConfigTestServer(t)
	initial := oidcTrustConfigTestConfig(server.URL, "main branch", `claims.ref == "refs/heads/main"`, "8d39f431-715c-4c6c-916a-849292f97920")
	updated := oidcTrustConfigTestConfig(server.URL, "", `claims.repository == "example/repo"`, "e3bb1892-f89e-4258-9495-427ed5ace8c7")

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
					tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "cel", `claims.repository == "example/repo"`),
					tfresource.TestCheckResourceAttr(oidcTrustConfigResourceAddress, "workload_federation_oidc_config_id", "e3bb1892-f89e-4258-9495-427ed5ace8c7"),
				),
			},
		},
	})
}
