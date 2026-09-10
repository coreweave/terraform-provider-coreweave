package sandbox_test

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/gen/go/coreweave/sandbox/connectrpc/go/coreweave/sandbox/v1/sandboxv1connect"
	sandboxv1 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const runnerAddress = "coreweave_sandbox_managed_runner.test"

//go:embed testdata/managed_runner.tf
var fullRunnerConfig string

type runnerServer struct {
	sandboxv1connect.UnimplementedRunnerManagementServiceHandler
	mu              sync.Mutex
	runner          *sandboxv1.ManagedRunner
	updates         [][]string
	creates         int
	deletes         int
	deleting        bool
	deletePolls     int
	staleOnUpdate   bool
	missingOnUpdate bool
}

func (s *runnerServer) CreateManagedRunner(_ context.Context, req *connect.Request[sandboxv1.CreateManagedRunnerRequest]) (*connect.Response[sandboxv1.ManagedRunner], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runner != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("runner already exists"))
	}
	if req.Msg.GetRequestId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("missing idempotency token"))
	}
	if req.Msg.ManagedRunner.Policy == nil || len(req.Msg.ManagedRunner.ProfileBindings) != 0 || req.Msg.ManagedRunner.GetSpec().GetAllowPrivilegedProfileAnnotations() {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("expected a policy and no legacy configuration"))
	}
	s.runner = proto.Clone(req.Msg.ManagedRunner).(*sandboxv1.ManagedRunner)
	s.runner.Identity.Zone = strings.ToLower(s.runner.Identity.Zone)
	if s.runner.Identity.RunnerGroupId == "" {
		s.runner.Identity.RunnerGroupId = "default"
	}
	if s.runner.Spec == nil {
		s.runner.Spec = &sandboxv1.ManagedRunnerSpec{}
	}
	if s.runner.Spec.ReleaseChannel == sandboxv1.ReleaseChannel_RELEASE_CHANNEL_UNSPECIFIED {
		s.runner.Spec.ReleaseChannel = sandboxv1.ReleaseChannel_RELEASE_CHANNEL_STABLE
	}
	s.runner.Identity.ClusterName = "test-cluster"
	s.runner.Etag = "etag-1"
	s.runner.InstallStatus = sandboxv1.RunnerInstallStatus_RUNNER_INSTALL_STATUS_READY
	s.runner.ConnectionStatus = sandboxv1.RunnerConnectionStatus_RUNNER_CONNECTION_STATUS_CONNECTED
	s.runner.CreateTime = timestamppb.New(time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC))
	s.runner.UpdateTime = s.runner.CreateTime
	s.runner.ActiveRevision = 1
	s.runner.TargetRevision = 1
	s.creates++
	return connect.NewResponse(proto.Clone(s.runner).(*sandboxv1.ManagedRunner)), nil
}

func (s *runnerServer) GetManagedRunner(_ context.Context, req *connect.Request[sandboxv1.GetManagedRunnerRequest]) (*connect.Response[sandboxv1.ManagedRunner], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleting {
		s.deletePolls++
		if s.deletePolls%2 == 0 {
			s.runner = nil
			s.deleting = false
		}
	}
	if s.runner == nil || req.Msg.RunnerId != s.runner.Identity.RunnerId {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("runner not found"))
	}
	return connect.NewResponse(proto.Clone(s.runner).(*sandboxv1.ManagedRunner)), nil
}

func (s *runnerServer) UpdateManagedRunner(_ context.Context, req *connect.Request[sandboxv1.UpdateManagedRunnerRequest]) (*connect.Response[sandboxv1.ManagedRunner], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.missingOnUpdate {
		s.runner = nil
		s.missingOnUpdate = false
	}
	if s.runner == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("runner not found during update"))
	}
	paths := req.Msg.GetUpdateMask().GetPaths()
	if len(paths) == 0 || slices.Contains(paths, "*") {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("an explicit update mask is required"))
	}
	if slices.Contains(paths, "policy") {
		if s.staleOnUpdate {
			s.runner.Etag = "concurrent-etag"
			s.staleOnUpdate = false
		}
		if req.Msg.ManagedRunner.Etag != s.runner.Etag {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("managed_runner.etag is stale; reload the runner and retry"))
		}
	}
	for _, mask := range paths {
		if err := applyRunnerMask(s.runner.ProtoReflect(), req.Msg.ManagedRunner.ProtoReflect(), strings.Split(mask, ".")); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	if s.runner.Identity.RunnerGroupId == "" {
		s.runner.Identity.RunnerGroupId = "default"
	}
	s.updates = append(s.updates, slices.Clone(paths))
	if slices.Contains(paths, "policy") {
		s.runner.Etag = fmt.Sprintf("etag-%d", len(s.updates)+1)
	}
	return connect.NewResponse(proto.Clone(s.runner).(*sandboxv1.ManagedRunner)), nil
}

func applyRunnerMask(target, source protoreflect.Message, parts []string) error {
	field := target.Descriptor().Fields().ByName(protoreflect.Name(parts[0]))
	if field == nil {
		return fmt.Errorf("unknown path %s", parts[0])
	}
	if len(parts) > 1 {
		return applyRunnerMask(target.Mutable(field).Message(), source.Get(field).Message(), parts[1:])
	}
	if !source.Has(field) {
		target.Clear(field)
		return nil
	}
	target.Set(field, source.Get(field))
	return nil
}

func (s *runnerServer) DeleteManagedRunner(_ context.Context, req *connect.Request[sandboxv1.DeleteManagedRunnerRequest]) (*connect.Response[emptypb.Empty], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !req.Msg.AllowMissing {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("allow_missing must be true"))
	}
	s.deletes++
	if s.runner != nil {
		s.deleting = true
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func startRunnerServer(t *testing.T) *runnerServer {
	t.Helper()
	service := &runnerServer{}
	route, handler := sandboxv1connect.NewRunnerManagementServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(route, handler)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing bearer authentication")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/coreweave.sandbox.v1.RunnerManagementService/") {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	// Environment takes precedence over HCL. Always isolate these tests from
	// developer credentials and endpoints, including acceptance-test runs.
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	return service
}

func (s *runnerServer) checkDestroyed(*terraform.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runner != nil {
		return fmt.Errorf("runner still exists after delete")
	}
	return nil
}

func minimalConfig(policy, extra string) string {
	return fmt.Sprintf(`
provider "coreweave" {}
resource "coreweave_sandbox_managed_runner" "test" {
  runner_id = "test-runner"
  cluster_id = "00000000-0000-4000-8000-000000000001"
  zone = "us-east-04a"
  policy = %s
  %s
}
`, policy, extra)
}

func TestManagedRunnerLifecycle(t *testing.T) {
	service := startRunnerServer(t)
	cleared := minimalConfig(`{ control_plane_access = { source_ip_allowlist = {} } }`, `spec = { data_plane = { disabled = true } }`)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed,
		Steps: []tfresource.TestStep{
			{Config: fullRunnerConfig, Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr(runnerAddress, "id", "test-runner"),
				tfresource.TestCheckResourceAttr(runnerAddress, "zone", "us-east-04a"),
				tfresource.TestCheckResourceAttr(runnerAddress, "policy.constraints.resources.max_gpu_count", "0"),
				tfresource.TestCheckResourceAttr(runnerAddress, "spec.volumes.enabled", "true"),
				tfresource.TestCheckResourceAttr(runnerAddress, "spec.data_plane.load_balancer.hostname", "runner.example.com"),
			)},
			{Config: fullRunnerConfig, PlanOnly: true},
			{Config: cleared, Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr(runnerAddress, "spec.data_plane.disabled", "true"),
				tfresource.TestCheckNoResourceAttr(runnerAddress, "spec.volumes"),
				tfresource.TestCheckNoResourceAttr(runnerAddress, "policy.constraints"),
			)},
			{Config: minimalConfig(`{}`, `spec = {}`)},
			{Config: minimalConfig(`{}`, `spec = { data_plane = { cluster_ip = {} } }`)},
			{Config: minimalConfig(`{}`, `spec = { data_plane = { custom = { advertised_uri = "https://runner.example.com", service_annotations = { "example.com/discovery" = "enabled" } } } }`)},
		},
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	assert.Equal(t, 1, service.creates)
	assert.Equal(t, 1, service.deletes)
	assert.Equal(t, 2, service.deletePolls)
	require.Len(t, service.updates, 4)
	assert.Contains(t, service.updates[0], "spec.volumes")
	assert.Contains(t, service.updates[0], "policy")
	assert.NotContains(t, service.updates[0], "spec")
	assert.NotContains(t, service.updates[0], "identity.cluster_id")
}

func TestManagedRunnerImportAndReplacement(t *testing.T) {
	service := startRunnerServer(t)
	config := minimalConfig(`{}`, "")
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed,
		Steps: []tfresource.TestStep{
			{Config: config},
			{ResourceName: runnerAddress, ImportState: true, ImportStateVerify: true},
			{Config: strings.ReplaceAll(config, "us-east-04a", "us-west-04a")},
		},
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	assert.Equal(t, 2, service.creates)
	assert.Equal(t, 2, service.deletes)
	assert.Equal(t, 4, service.deletePolls)
}

func TestManagedRunnerDefaultGroup(t *testing.T) {
	service := startRunnerServer(t)
	config := minimalConfig(`{}`, "")
	checkGroup := func(group string) tfresource.TestCheckFunc {
		return tfresource.TestCheckResourceAttr(runnerAddress, "runner_group_id", group)
	}
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed,
		Steps: []tfresource.TestStep{
			{Config: config, Check: checkGroup("default")},
			{Config: config, PlanOnly: true},
			{ResourceName: runnerAddress, ImportState: true, ImportStateVerify: true},
			{Config: minimalConfig(`{}`, `runner_group_id = "custom"`), Check: checkGroup("custom")},
			{Config: config, Check: checkGroup("default")},
			{Config: minimalConfig(`{}`, `runner_group_id = null`), PlanOnly: true},
		},
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	assert.Equal(t, 1, service.creates)
	assert.Equal(t, [][]string{{"identity.runner_group_id"}, {"identity.runner_group_id"}}, service.updates)
}

func TestManagedRunnerEmptyGroupRejected(t *testing.T) {
	service := startRunnerServer(t)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed,
		Steps: []tfresource.TestStep{{
			Config:      minimalConfig(`{}`, `runner_group_id = ""`),
			ExpectError: regexp.MustCompile("string length must be at least 1"),
		}},
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	assert.Zero(t, service.creates)
}

func TestManagedRunnerNoncanonicalZoneRejected(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expression string
		extra      string
	}{
		{name: "uppercase", expression: `"US-EAST-04A"`},
		{name: "mixed case", expression: `"Us-East-04a"`},
		{
			name:       "unknown at plan",
			expression: `terraform_data.cluster_zone.output`,
			extra:      `resource "terraform_data" "cluster_zone" { input = "US-EAST-04A" }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := startRunnerServer(t)
			config := strings.Replace(minimalConfig(`{}`, ""), `"us-east-04a"`, tc.expression, 1) + tc.extra
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				CheckDestroy:             service.checkDestroyed,
				Steps: []tfresource.TestStep{{
					Config:      config,
					ExpectError: regexp.MustCompile("zone must be lowercase"),
				}},
			})
			service.mu.Lock()
			defer service.mu.Unlock()
			assert.Zero(t, service.creates, "invalid zones must be rejected before creating a runner")
			assert.Nil(t, service.runner)
		})
	}
}

func TestManagedRunnerLowercaseComputedZone(t *testing.T) {
	service := startRunnerServer(t)
	config := strings.Replace(minimalConfig(`{}`, ""), `"us-east-04a"`, `lower(terraform_data.cluster_zone.output)`, 1) + `
resource "terraform_data" "cluster_zone" { input = "US-EAST-04A" }
`
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed,
		Steps: []tfresource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectUnknownValue(runnerAddress, tfjsonpath.New("zone"))},
				},
				Check: tfresource.TestCheckResourceAttr(runnerAddress, "zone", "us-east-04a"),
			},
			{Config: config, PlanOnly: true},
			{ResourceName: runnerAddress, ImportState: true, ImportStateVerify: true},
			{Config: config, PlanOnly: true},
		},
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	assert.Equal(t, 1, service.creates)
	assert.Empty(t, service.updates)
}

func TestManagedRunnerDriftAndRemoteDeletion(t *testing.T) {
	service := startRunnerServer(t)
	config := minimalConfig(`{ constraints = { resources = { max_gpu_count = 0 } } }`, "")
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed,
		Steps: []tfresource.TestStep{
			{Config: config},
			{PreConfig: func() {
				service.mu.Lock()
				defer service.mu.Unlock()
				service.runner.Policy.Constraints.Resources.MaxGpuCount = nil
				service.runner.Etag = "drift-etag"
			}, Config: config},
			{PreConfig: func() { service.mu.Lock(); defer service.mu.Unlock(); service.runner = nil }, Config: config},
		},
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	assert.Equal(t, 2, service.creates)
	require.Len(t, service.updates, 1)
	assert.Equal(t, []string{"policy"}, service.updates[0])
}

func TestManagedRunnerStaleEtag(t *testing.T) {
	service := startRunnerServer(t)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed,
		Steps: []tfresource.TestStep{
			{Config: minimalConfig(`{}`, "")},
			{PreConfig: func() { service.mu.Lock(); defer service.mu.Unlock(); service.staleOnUpdate = true }, Config: minimalConfig(`{ display_name = "updated" }`, ""), ExpectError: regexp.MustCompile("etag is stale")},
		},
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	assert.Empty(t, service.updates, "stale etags must never be retried with a fresh token")
}

func TestManagedRunnerMissingDuringUpdate(t *testing.T) {
	service := startRunnerServer(t)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed,
		Steps: []tfresource.TestStep{
			{Config: minimalConfig(`{}`, "")},
			{PreConfig: func() { service.mu.Lock(); defer service.mu.Unlock(); service.missingOnUpdate = true }, Config: minimalConfig(`{}`, `display_name = "updated"`), ExpectError: regexp.MustCompile("runner not found during update")},
		},
	})
}

func TestManagedRunnerUnknownPolicyResolvesBeforeApply(t *testing.T) {
	for _, tc := range []struct {
		name    string
		policy  string
		input   string
		unknown tfjsonpath.Path
	}{
		{
			name:    "whole policy",
			policy:  `terraform_data.policy.output`,
			input:   `{ constraints = { resources = { max_cpu = %q } } }`,
			unknown: tfjsonpath.New("policy"),
		},
		{
			name:    "nested constraint",
			policy:  `{ constraints = { resources = { max_cpu = terraform_data.policy.output } } }`,
			input:   `%q`,
			unknown: tfjsonpath.New("policy").AtMapKey("constraints").AtMapKey("resources").AtMapKey("max_cpu"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := startRunnerServer(t)
			config := func(cpu, replacement string) string {
				return minimalConfig(tc.policy, "") + fmt.Sprintf(`
resource "terraform_data" "policy" {
  input = %s
  triggers_replace = %q
}
`, fmt.Sprintf(tc.input, cpu), replacement)
			}
			unknownPlan := tfresource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectUnknownValue(runnerAddress, tc.unknown)},
			}
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				CheckDestroy:             service.checkDestroyed,
				Steps: []tfresource.TestStep{
					{
						Config: config("1", "create"), ConfigPlanChecks: unknownPlan,
						Check: tfresource.TestCheckResourceAttr(runnerAddress, "policy.constraints.resources.max_cpu", "1"),
					},
					{
						Config: config("1", "unchanged"), ConfigPlanChecks: unknownPlan,
						Check: tfresource.ComposeAggregateTestCheckFunc(
							tfresource.TestCheckResourceAttr(runnerAddress, "policy.constraints.resources.max_cpu", "1"),
							func(*terraform.State) error {
								service.mu.Lock()
								defer service.mu.Unlock()
								if len(service.updates) != 0 {
									return fmt.Errorf("unchanged policy caused %d updates", len(service.updates))
								}
								return nil
							},
						),
					},
					{
						Config: config("2", "changed"), ConfigPlanChecks: unknownPlan,
						Check: tfresource.TestCheckResourceAttr(runnerAddress, "policy.constraints.resources.max_cpu", "2"),
					},
					{Config: config("2", "changed"), PlanOnly: true},
				},
			})
			service.mu.Lock()
			defer service.mu.Unlock()
			assert.Equal(t, [][]string{{"policy"}}, service.updates)
		})
	}
}
