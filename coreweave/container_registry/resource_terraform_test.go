package containerregistry_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/v2/coreweave/registry/v1alpha1/registryv1alpha1connect"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// registryTestProvider injects the local API client independently of live acceptance credentials.
type registryTestProvider struct {
	frameworkprovider.Provider
	client *coreweave.Client
}

// Configure isolates parallel fake-server tests from provider environment overrides.
func (p *registryTestProvider) Configure(_ context.Context, _ frameworkprovider.ConfigureRequest, resp *frameworkprovider.ConfigureResponse) {
	resp.ResourceData = p.client
	resp.DataSourceData = p.client
}

// privateStateObserver records private state returned by Terraform after an interrupted create.
type privateStateObserver struct {
	mu       sync.Mutex
	observed bool
}

// fakeTerraformProviderFactories supplies the real schemas and resources through the standard test harness.
func fakeTerraformProviderFactories(t *testing.T, fake *fakeRegistry, observe *privateStateObserver) map[string]func() (tfprotov6.ProviderServer, error) {
	t.Helper()
	mux := http.NewServeMux()
	srv := connect.NewServer()
	client.RegisterRegistryServiceHandler(srv, fake)
	connecthttp.Mount(mux, srv, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	configured := &coreweave.Client{ContainerRegistry: client.NewRegistryServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))}
	factory := providerserver.NewProtocol6WithError(&registryTestProvider{Provider: provider.New("test")(), client: configured})
	return map[string]func() (tfprotov6.ProviderServer, error){"coreweave": func() (tfprotov6.ProviderServer, error) {
		protocol, err := factory()
		if err != nil || observe == nil {
			return protocol, err
		}
		// Each Terraform command gets a new protocol server; the observer spans commands.
		return &observedRegistryServer{ProviderServer: protocol, observer: observe}, nil
	}}
}

// observedRegistryServer delegates requests to the command's server while sharing recovery observations.
type observedRegistryServer struct {
	tfprotov6.ProviderServer
	observer *privateStateObserver
}

// ReadResource records Terraform's persisted private data across provider restarts.
func (s *observedRegistryServer) ReadResource(ctx context.Context, req *tfprotov6.ReadResourceRequest) (*tfprotov6.ReadResourceResponse, error) {
	if len(req.Private) > 0 {
		s.observer.mu.Lock()
		s.observer.observed = true
		s.observer.mu.Unlock()
	}
	return s.ProviderServer.ReadResource(ctx, req)
}

// TestNamespaceTerraformRecovery verifies taint, persisted private recovery and explicit import without replaying create.
func TestNamespaceTerraformRecovery(t *testing.T) {
	fake := newFakeRegistry()
	fake.pending = true
	observer := &privateStateObserver{}
	factories := fakeTerraformProviderFactories(t, fake, observer)
	address := "coreweave_container_registry_namespace.test"
	config := `resource "coreweave_container_registry_namespace" "test" {
 name = "example-images"
 zone = "US-LAB-01A"
}`
	resource.ParallelTest(t, resource.TestCase{
		IsUnitTest:               true,
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_7_0)},
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: config[:len(config)-1] + ` timeouts = { create = "100ms" }
}`, ExpectError: regexp.MustCompile("server work continues")},
			{
				PreConfig:          func() { fake.mu.Lock(); fake.pending = false; fake.mu.Unlock() },
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
				Check: func(state *terraform.State) error {
					instance := state.RootModule().Resources[address]
					if instance == nil || instance.Primary == nil || !instance.Primary.Tainted || instance.Primary.ID != "namespaces/example-images" {
						return fmt.Errorf("failed create did not retain a tainted namespace: %+v", instance)
					}
					observer.mu.Lock()
					persisted := observer.observed
					observer.mu.Unlock()
					if !persisted {
						return fmt.Errorf("Terraform did not return persisted private recovery state")
					}
					fake.mu.Lock()
					defer fake.mu.Unlock()
					if len(fake.creates) != 1 || len(fake.deletes) != 0 {
						return fmt.Errorf("refresh replayed a write: creates=%d deletes=%d", len(fake.creates), len(fake.deletes))
					}
					return nil
				},
				RefreshPlanChecks: resource.RefreshPlanChecks{PostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionReplace)}},
			},
			{
				// Explicitly relinquish the tainted state before importing the verified namespace.
				Config: `removed {
 from = coreweave_container_registry_namespace.test
 lifecycle { destroy = false }
}`,
				Check: func(state *terraform.State) error {
					if len(state.RootModule().Resources) != 0 {
						return fmt.Errorf("removed block did not relinquish state")
					}
					fake.mu.Lock()
					defer fake.mu.Unlock()
					if fake.namespace == nil || len(fake.deletes) != 0 {
						return fmt.Errorf("relinquishing state deleted the namespace")
					}
					return nil
				},
			},
			{Config: config, ResourceName: address, ImportState: true, ImportStateId: "example-images", ImportStatePersist: true},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionNoop)}},
				Check: func(state *terraform.State) error {
					if state.RootModule().Resources[address].Primary.Tainted {
						return fmt.Errorf("explicit import did not clear taint")
					}
					fake.mu.Lock()
					defer fake.mu.Unlock()
					if len(fake.creates) != 1 || len(fake.deletes) != 0 {
						return fmt.Errorf("recovery replaced the namespace")
					}
					return nil
				},
			},
		},
	})
}

// TestNamespaceTerraformChildIdentityPlanning verifies dependency planning and apply across quota and force_destroy changes.
func TestNamespaceTerraformChildIdentityPlanning(t *testing.T) {
	fake := newFakeRegistry()
	factories := fakeTerraformProviderFactories(t, fake, nil)
	config := func(quota int64, force bool) string {
		return fmt.Sprintf(`resource "coreweave_container_registry_namespace" "test" {
 name = "example-images"
 zone = "US-LAB-01A"
 storage_quota_bytes = %d
 force_destroy = %t
}
resource "coreweave_container_registry_access_configuration" "test" {
 namespace = coreweave_container_registry_namespace.test.name
}
resource "coreweave_container_registry_lifecycle_policy" "test" {
 namespace = coreweave_container_registry_namespace.test.name
 enabled = false
}`, quota, force)
	}
	childChecks := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
		plancheck.ExpectResourceAction("coreweave_container_registry_namespace.test", plancheck.ResourceActionUpdate),
		plancheck.ExpectResourceAction("coreweave_container_registry_access_configuration.test", plancheck.ResourceActionNoop),
		plancheck.ExpectResourceAction("coreweave_container_registry_lifecycle_policy.test", plancheck.ResourceActionNoop),
	}}
	noPolicyWrites := func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.accessUpdates) != 0 || len(fake.lifecycleUpdates) != 0 || len(fake.creates) != 1 || len(fake.deletes) != 0 {
			return fmt.Errorf("namespace update replaced resources or wrote policies: creates=%d deletes=%d access=%d lifecycle=%d", len(fake.creates), len(fake.deletes), len(fake.accessUpdates), len(fake.lifecycleUpdates))
		}
		return nil
	}
	resource.ParallelTest(t, resource.TestCase{IsUnitTest: true, ProtoV6ProviderFactories: factories, Steps: []resource.TestStep{
		{Config: config(1048576, false), Check: noPolicyWrites},
		{Config: config(2097152, false), ConfigPlanChecks: childChecks, Check: noPolicyWrites},
		{Config: config(2097152, true), ConfigPlanChecks: childChecks, Check: noPolicyWrites},
	}})
}
