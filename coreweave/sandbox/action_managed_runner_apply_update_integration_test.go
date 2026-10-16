package sandbox_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"buf.build/gen/go/coreweave/sandbox/connectrpc/go/v2/coreweave/sandbox/v1/sandboxv1connect"
	sandboxv1 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const actionExistingRunnerID = "existing-runner"

type runnerUpdateServer struct {
	sandboxv1connect.UnimplementedRunnerManagementServiceHandler
	mu      sync.Mutex
	runner  *sandboxv1.ManagedRunner
	applies int
	reads   int
}

func (s *runnerUpdateServer) ApplyAvailableUpdate(_ context.Context, req *sandboxv1.ApplyAvailableUpdateRequest) (*sandboxv1.ManagedRunner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.RunnerId != actionExistingRunnerID {
		return nil, connect.Errorf(connect.CodeNotFound, "unexpected runner %s", req.RunnerId)
	}
	s.applies++
	return proto.Clone(s.runner).(*sandboxv1.ManagedRunner), nil
}
func (s *runnerUpdateServer) GetManagedRunner(_ context.Context, req *sandboxv1.GetManagedRunnerRequest) (*sandboxv1.ManagedRunner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.RunnerId != actionExistingRunnerID {
		return nil, connect.Errorf(connect.CodeNotFound, "unexpected runner %s", req.RunnerId)
	}
	s.reads++
	s.runner.ActiveRevision = s.runner.TargetRevision
	s.runner.InstallStatus = sandboxv1.RunnerInstallStatus_RUNNER_INSTALL_STATUS_READY
	s.runner.ConnectionStatus = sandboxv1.RunnerConnectionStatus_RUNNER_CONNECTION_STATUS_CONNECTED
	return proto.Clone(s.runner).(*sandboxv1.ManagedRunner), nil
}
func startRunnerUpdateServer(t *testing.T) *runnerUpdateServer {
	t.Helper()
	s := &runnerUpdateServer{runner: &sandboxv1.ManagedRunner{
		Identity: &sandboxv1.RunnerIdentity{RunnerId: actionExistingRunnerID}, ActiveRevision: 1, TargetRevision: 2,
		InstallStatus:    sandboxv1.RunnerInstallStatus_RUNNER_INSTALL_STATUS_PROVISIONING,
		ConnectionStatus: sandboxv1.RunnerConnectionStatus_RUNNER_CONNECTION_STATUS_DISCONNECTED,
	}}
	rpc := connect.NewServer()
	sandboxv1connect.RegisterRunnerManagementServiceHandler(rpc, s)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer authentication")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	return s
}
func updateActionConfig(timeout, trigger string, input int) string {
	return fmt.Sprintf(`
provider "coreweave" {}
action "coreweave_sandbox_managed_runner_apply_update" "test" {
  config {
    runner_id = "existing-runner"
    %s
  }
}
resource "terraform_data" "test" {
  input = %d
  %s
}
`, timeout, input, trigger)
}

const updateActionTrigger = `lifecycle {
  action_trigger {
    events = [after_create, after_update]
    actions = [action.coreweave_sandbox_managed_runner_apply_update.test]
  }
}`

func TestManagedRunnerUpdateActionTerraform(t *testing.T) {
	s := startRunnerUpdateServer(t)
	check := func(applies, reads int) func() {
		return func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			require.Equal(t, applies, s.applies)
			require.Equal(t, reads, s.reads)
		}
	}
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_14_0)},
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Merely declaring an action or planning a trigger never upgrades a runner.
			{Config: updateActionConfig("", "", 1), PostApplyFunc: check(0, 0)},
			{Config: updateActionConfig("", updateActionTrigger, 2), PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: updateActionConfig("", updateActionTrigger, 2), PreConfig: check(0, 0), PostApplyFunc: check(1, 1)},
			// An unchanged apply does not invoke it again.
			{Config: updateActionConfig("", updateActionTrigger, 2), PostApplyFunc: check(1, 1)},
			// An explicit second invocation is a no-op when already current.
			{Config: updateActionConfig(`timeout = "1m"`, updateActionTrigger, 3), PostApplyFunc: check(2, 1)},
		},
	})
}
func TestManagedRunnerUpdateActionInvalidTimeout(t *testing.T) {
	s := startRunnerUpdateServer(t)
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_14_0)},
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps:                    []resource.TestStep{{Config: updateActionConfig(`timeout = "0s"`, updateActionTrigger, 1), ExpectError: regexp.MustCompile("timeout must be a positive duration")}},
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Zero(t, s.applies)
}
