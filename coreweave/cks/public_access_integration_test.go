package cks_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"buf.build/gen/go/coreweave/cks/connectrpc/go/coreweave/cks/v1beta1/cksv1beta1connect"
	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const publicClusterAddress = "coreweave_cks_cluster.test"

type publicAccessServer struct {
	cksv1beta1connect.UnimplementedClusterServiceHandler
	mu          sync.Mutex
	cluster     *cksv1beta1.Cluster
	creates     []*cksv1beta1.CreateClusterRequest
	updates     []*cksv1beta1.UpdateClusterRequest
	updateCalls int
}

func startPublicAccessServer(t *testing.T) *publicAccessServer {
	t.Helper()
	service := &publicAccessServer{}
	path, handler := cksv1beta1connect.NewClusterServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	// Environment wins over provider HCL, so isolate all requests from real credentials and services.
	// Do not parallelize these tests: the in-process provider reads the process environment.
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	return service
}

func (s *publicAccessServer) CreateCluster(_ context.Context, req *connect.Request[cksv1beta1.CreateClusterRequest]) (*connect.Response[cksv1beta1.CreateClusterResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creates = append(s.creates, proto.Clone(req.Msg).(*cksv1beta1.CreateClusterRequest))
	s.cluster = &cksv1beta1.Cluster{
		Id: "00000000-0000-4000-8000-000000000001", Name: req.Msg.Name, Zone: req.Msg.Zone,
		VpcId: req.Msg.VpcId, Version: req.Msg.Version, Public: req.Msg.Public,
		Network: proto.Clone(req.Msg.Network).(*cksv1beta1.ClusterNetworkConfig),
		Status:  cksv1beta1.Cluster_STATUS_RUNNING, ApiServerEndpoint: "https://legacy.example.test",
		AuditPolicy: req.Msg.AuditPolicy, PublicAccess: clonePublicAccess(req.Msg.PublicAccess),
	}
	return connect.NewResponse(&cksv1beta1.CreateClusterResponse{Cluster: proto.Clone(s.cluster).(*cksv1beta1.Cluster)}), nil
}

func (s *publicAccessServer) GetCluster(_ context.Context, _ *connect.Request[cksv1beta1.GetClusterRequest]) (*connect.Response[cksv1beta1.GetClusterResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cluster == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("cluster not found"))
	}
	cluster := proto.Clone(s.cluster).(*cksv1beta1.Cluster)
	if config := cluster.PublicAccess; config != nil {
		slices.Reverse(config.AllowCidrs)
	}
	return connect.NewResponse(&cksv1beta1.GetClusterResponse{Cluster: cluster}), nil
}

func (s *publicAccessServer) UpdateCluster(_ context.Context, req *connect.Request[cksv1beta1.UpdateClusterRequest]) (*connect.Response[cksv1beta1.UpdateClusterResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalls++
	if s.cluster == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("cluster not found"))
	}
	paths := req.Msg.GetUpdateMask().GetPaths()
	if len(paths) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("update mask is required"))
	}
	if req.Msg.PublicAccess != nil && !slices.Contains(paths, "public_access") {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unmasked public access body"))
	}
	if slices.Contains(paths, "version") && slices.Contains(paths, "public_access") {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("cannot change public access during an upgrade"))
	}
	s.updates = append(s.updates, proto.Clone(req.Msg).(*cksv1beta1.UpdateClusterRequest))
	for _, path := range paths {
		switch path {
		case "public_access":
			s.cluster.PublicAccess = clonePublicAccess(req.Msg.PublicAccess)
		case "version":
			s.cluster.Version = req.Msg.Version
		case "public":
			s.cluster.Public = req.Msg.Public
		case "audit_policy":
			s.cluster.AuditPolicy = req.Msg.AuditPolicy
		default:
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unexpected update path %q", path))
		}
	}
	return connect.NewResponse(&cksv1beta1.UpdateClusterResponse{Cluster: proto.Clone(s.cluster).(*cksv1beta1.Cluster)}), nil
}

func (s *publicAccessServer) DeleteCluster(_ context.Context, _ *connect.Request[cksv1beta1.DeleteClusterRequest]) (*connect.Response[cksv1beta1.DeleteClusterResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cluster == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("cluster not found"))
	}
	cluster := proto.Clone(s.cluster).(*cksv1beta1.Cluster)
	s.cluster = nil
	return connect.NewResponse(&cksv1beta1.DeleteClusterResponse{Cluster: cluster}), nil
}

func (s *publicAccessServer) checkDestroyed(_ *terraform.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cluster != nil {
		return fmt.Errorf("cluster was not deleted")
	}
	return nil
}

func (s *publicAccessServer) setPublicAccess(config *cksv1beta1.PublicAccessConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cluster.PublicAccess = clonePublicAccess(config)
}

func clonePublicAccess(config *cksv1beta1.PublicAccessConfig) *cksv1beta1.PublicAccessConfig {
	if config == nil {
		return nil
	}
	return proto.Clone(config).(*cksv1beta1.PublicAccessConfig)
}

func requirePublicAccess(t *testing.T, want, got *cksv1beta1.PublicAccessConfig) {
	t.Helper()
	if want == nil {
		require.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	require.Equal(t, want.Mode, got.Mode)
	require.ElementsMatch(t, want.AllowCidrs, got.AllowCidrs)
}

func publicClusterConfig(publicAccess, version, extra string) string {
	attribute := ""
	if publicAccess != "" {
		attribute = "public_access = " + publicAccess
	}
	return fmt.Sprintf(`
provider "coreweave" {}
resource "coreweave_cks_cluster" "test" {
  name = "public-access-test"
  zone = "US-LAB-01A"
  vpc_id = "00000000-0000-4000-8000-000000000002"
  version = %q
  public = false
  pod_cidr_name = "pods"
  service_cidr_name = "services"
  internal_lb_cidr_names = ["lb"]
  %s
  %s
}
data "coreweave_cks_cluster" "test" {
  id = coreweave_cks_cluster.test.id
  depends_on = [coreweave_cks_cluster.test]
}
`, version, attribute, extra)
}

func publicAccessStateChecks(config *cksv1beta1.PublicAccessConfig) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(publicClusterAddress, tfjsonpath.New("public_access"), publicAccessKnownValue(config)),
		statecheck.ExpectKnownValue("data.coreweave_cks_cluster.test", tfjsonpath.New("public_access"), publicAccessKnownValue(config)),
	}
}

func publicAccessKnownValue(config *cksv1beta1.PublicAccessConfig) knownvalue.Check {
	if config == nil {
		return knownvalue.Null()
	}
	var modeCheck knownvalue.Check = knownvalue.Null()
	if config.Mode != nil {
		modeCheck = knownvalue.StringExact(map[cksv1beta1.PublicAccessConfig_Mode]string{
			cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED: "MODE_UNSPECIFIED",
			cksv1beta1.PublicAccessConfig_DISABLED:         "DISABLED",
			cksv1beta1.PublicAccessConfig_TLS:              "TLS",
		}[*config.Mode])
	}
	cidrs := make([]knownvalue.Check, len(config.AllowCidrs))
	for i, cidr := range config.AllowCidrs {
		cidrs[i] = knownvalue.StringExact(cidr)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"mode": modeCheck, "allow_cidrs": knownvalue.SetExact(cidrs)})
}

func directPublicAccess(mode *cksv1beta1.PublicAccessConfig_Mode, cidrs ...string) *cksv1beta1.PublicAccessConfig {
	return &cksv1beta1.PublicAccessConfig{Mode: mode, AllowCidrs: cidrs}
}

func publicAccessCIDRList(count int) ([]string, string) {
	cidrs := make([]string, count)
	quoted := make([]string, count)
	for i := range cidrs {
		cidrs[i] = fmt.Sprintf("10.0.%d.0/24", i)
		quoted[i] = fmt.Sprintf("%q", cidrs[i])
	}
	return cidrs, "[" + strings.Join(quoted, ",") + "]"
}

func TestPublicAccessTerraformLifecycle(t *testing.T) {
	service := startPublicAccessServer(t)
	stages := []struct {
		hcl  string
		want *cksv1beta1.PublicAccessConfig
	}{
		{hcl: ""},
		{hcl: "{}", want: &cksv1beta1.PublicAccessConfig{}},
		{hcl: `{ mode = "MODE_UNSPECIFIED" }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum())},
		{hcl: `{ mode = "DISABLED" }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_DISABLED.Enum())},
		{hcl: `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24")},
		{hcl: `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24", "2001:db8::/32"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24", "2001:db8::/32")},
		{hcl: `{ mode = "TLS", allow_cidrs = ["0.0.0.0/0", "::/0"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "0.0.0.0/0", "::/0")},
		{hcl: `{ mode = "DISABLED", allow_cidrs = ["0.0.0.0/0", "::/0"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_DISABLED.Enum(), "0.0.0.0/0", "::/0")},
		{hcl: `{ mode = "TLS", allow_cidrs = ["0.0.0.0/0", "::/0"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "0.0.0.0/0", "::/0")},
		{hcl: `{ mode = "MODE_UNSPECIFIED", allow_cidrs = ["0.0.0.0/0", "::/0"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum(), "0.0.0.0/0", "::/0")},
		{hcl: `{ mode = null, allow_cidrs = ["0.0.0.0/0", "::/0"] }`, want: directPublicAccess(nil, "0.0.0.0/0", "::/0")},
		{hcl: `{ mode = "DISABLED" }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_DISABLED.Enum())},
		{hcl: `{ allow_cidrs = ["203.0.113.0/24"] }`, want: directPublicAccess(nil, "203.0.113.0/24")},
		{hcl: `{ mode = "TLS", allow_cidrs = ["203.0.113.7/24", "2001:0DB8:0000::/32"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.7/24", "2001:0DB8:0000::/32")},
		{hcl: ""},
	}
	var steps []tfresource.TestStep
	for _, stage := range stages {
		config := publicClusterConfig(stage.hcl, "v1.33", "")
		steps = append(steps,
			tfresource.TestStep{Config: config, ConfigStateChecks: publicAccessStateChecks(stage.want)},
			tfresource.TestStep{ResourceName: publicClusterAddress, ImportState: true, ImportStateVerify: true},
			tfresource.TestStep{Config: config, PlanOnly: true},
		)
	}
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed, Steps: steps,
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.creates, 1)
	requirePublicAccess(t, nil, service.creates[0].PublicAccess)
	require.Len(t, service.updates, len(stages)-1)
	for i, update := range service.updates {
		require.Equal(t, []string{"public_access"}, update.UpdateMask.Paths)
		require.Empty(t, update.Version)
		require.Nil(t, update.Network)
		requirePublicAccess(t, stages[i+1].want, update.PublicAccess)
	}
}

func TestPublicAccessTerraformEmptyCIDRs(t *testing.T) {
	for _, tc := range []struct {
		hcl  string
		mode *cksv1beta1.PublicAccessConfig_Mode
	}{
		{hcl: "{}"},
		{hcl: "{ mode = null }"},
		{hcl: "{ allow_cidrs = null }"},
		{hcl: "{ allow_cidrs = [] }"},
		{hcl: `{ mode = "MODE_UNSPECIFIED" }`, mode: cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum()},
		{hcl: `{ mode = "MODE_UNSPECIFIED", allow_cidrs = null }`, mode: cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum()},
		{hcl: `{ mode = "MODE_UNSPECIFIED", allow_cidrs = [] }`, mode: cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum()},
		{hcl: `{ mode = "DISABLED" }`, mode: cksv1beta1.PublicAccessConfig_DISABLED.Enum()},
		{hcl: `{ mode = "DISABLED", allow_cidrs = null }`, mode: cksv1beta1.PublicAccessConfig_DISABLED.Enum()},
		{hcl: `{ mode = "DISABLED", allow_cidrs = [] }`, mode: cksv1beta1.PublicAccessConfig_DISABLED.Enum()},
	} {
		t.Run(tc.hcl, func(t *testing.T) {
			service := startPublicAccessServer(t)
			config := publicClusterConfig(tc.hcl, "v1.33", "")
			want := directPublicAccess(tc.mode)
			steps := []tfresource.TestStep{{Config: config, ConfigStateChecks: publicAccessStateChecks(want)}}
			// Omitted, null, and empty lists all produce the same disabled state.
			for _, list := range []string{"", "allow_cidrs = null", "allow_cidrs = []"} {
				mode := ""
				if tc.mode != nil {
					mode = fmt.Sprintf("mode = %q\n", tc.mode.String())
				}
				equivalent := publicClusterConfig("{\n"+mode+list+"\n}", "v1.33", "")
				steps = append(steps, tfresource.TestStep{Config: equivalent, PlanOnly: true})
			}
			steps = append(steps,
				tfresource.TestStep{Config: publicClusterConfig(`{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`, "v1.33", "")},
				tfresource.TestStep{Config: config, ConfigStateChecks: publicAccessStateChecks(want)},
				tfresource.TestStep{ResourceName: publicClusterAddress, ImportState: true, ImportStateVerify: true},
				tfresource.TestStep{Config: config, PlanOnly: true},
			)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: service.checkDestroyed, Steps: steps,
			})
			service.mu.Lock()
			defer service.mu.Unlock()
			require.Len(t, service.creates, 1)
			requirePublicAccess(t, want, service.creates[0].PublicAccess)
			require.Len(t, service.updates, 2)
			require.Equal(t, []string{"public_access"}, service.updates[1].UpdateMask.Paths)
			requirePublicAccess(t, want, service.updates[1].PublicAccess)
		})
	}
}

func TestPublicAccessTerraformDriftAndUnrelatedUpdate(t *testing.T) {
	service := startPublicAccessServer(t)
	policy := `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`
	config := publicClusterConfig(policy, "v1.33", "")
	want := directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24")
	steps := []tfresource.TestStep{{Config: config}}
	for _, drift := range []*cksv1beta1.PublicAccessConfig{directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "192.0.2.0/24"), nil} {
		steps = append(steps,
			tfresource.TestStep{
				Config: config, PreConfig: func() { service.setPublicAccess(drift) },
				PlanOnly: true, ExpectNonEmptyPlan: true,
			},
			tfresource.TestStep{
				Config: config, ConfigStateChecks: publicAccessStateChecks(want),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(publicClusterAddress, plancheck.ResourceActionUpdate)}},
			},
		)
	}
	absent := publicClusterConfig("", "v1.33", "")
	steps = append(steps,
		tfresource.TestStep{Config: absent, ConfigStateChecks: publicAccessStateChecks(nil)},
		tfresource.TestStep{
			Config: absent, PreConfig: func() { service.setPublicAccess(want) },
			PlanOnly: true, ExpectNonEmptyPlan: true,
		},
		tfresource.TestStep{
			Config: absent, ConfigStateChecks: publicAccessStateChecks(nil),
			ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(publicClusterAddress, plancheck.ResourceActionUpdate)}},
		},
		tfresource.TestStep{Config: config, ConfigStateChecks: publicAccessStateChecks(want)},
		tfresource.TestStep{Config: publicClusterConfig(policy, "v1.34", ""), ConfigStateChecks: publicAccessStateChecks(want)},
		tfresource.TestStep{Config: publicClusterConfig(policy, "v1.34", `audit_policy = "dGVzdA=="`), ConfigStateChecks: publicAccessStateChecks(want)},
	)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: service.checkDestroyed, Steps: steps,
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.updates, 7)
	for i := range 5 {
		require.Equal(t, []string{"public_access"}, service.updates[i].UpdateMask.Paths)
		if i == 2 || i == 3 {
			require.Nil(t, service.updates[i].PublicAccess)
		} else {
			requirePublicAccess(t, want, service.updates[i].PublicAccess)
		}
	}
	require.Equal(t, []string{"version"}, service.updates[5].UpdateMask.Paths)
	require.Equal(t, "v1.34", service.updates[5].Version)
	require.Nil(t, service.updates[5].PublicAccess)
	require.Equal(t, []string{"audit_policy"}, service.updates[6].UpdateMask.Paths)
	require.Equal(t, "dGVzdA==", service.updates[6].AuditPolicy)
	require.Nil(t, service.updates[6].PublicAccess)
}

func TestPublicAccessTerraformRejectsUpgradeAndPolicyChange(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{name: "TLS", before: "", after: `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`},
		{name: "disable", before: `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`, after: `{ mode = "DISABLED" }`},
		{name: "unspecified", before: `{ allow_cidrs = ["203.0.113.0/24"] }`, after: `{ mode = "MODE_UNSPECIFIED", allow_cidrs = ["203.0.113.0/24"] }`},
		{name: "CIDRs", before: `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`, after: `{ mode = "TLS", allow_cidrs = ["2001:db8::/32"] }`},
		{name: "clear", before: `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`, after: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := startPublicAccessServer(t)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: service.checkDestroyed,
				Steps: []tfresource.TestStep{
					{Config: publicClusterConfig(tc.before, "v1.33", "")},
					{
						Config:      publicClusterConfig(tc.after, "v1.34", ""),
						ExpectError: regexp.MustCompile(`(?is)(public.access.*(version|upgrade)|(version|upgrade).*public.access)`),
					},
				},
			})
			service.mu.Lock()
			defer service.mu.Unlock()
			require.Empty(t, service.updates)
			require.Zero(t, service.updateCalls)
			require.Len(t, service.creates, 1)
		})
	}
}

func TestPublicAccessTerraformReplacementAllowsVersionAndPolicyChange(t *testing.T) {
	service := startPublicAccessServer(t)
	initial := publicClusterConfig(`{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`, "v1.33", "")
	replacement := strings.Replace(
		publicClusterConfig(`{ mode = "DISABLED" }`, "v1.34", ""),
		`name = "public-access-test"`, `name = "replacement-cluster"`, 1,
	)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: service.checkDestroyed,
		Steps: []tfresource.TestStep{
			{Config: initial},
			{
				Config: replacement,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(publicClusterAddress, plancheck.ResourceActionDestroyBeforeCreate),
				}},
				ConfigStateChecks: publicAccessStateChecks(directPublicAccess(cksv1beta1.PublicAccessConfig_DISABLED.Enum())),
			},
			{Config: replacement, PlanOnly: true},
		},
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.creates, 2)
	require.Zero(t, service.updateCalls)
	require.Equal(t, "v1.34", service.creates[1].Version)
	requirePublicAccess(t, directPublicAccess(cksv1beta1.PublicAccessConfig_DISABLED.Enum()), service.creates[1].PublicAccess)
}

func TestPublicAccessTerraformAccepts100CIDRs(t *testing.T) {
	service := startPublicAccessServer(t)
	cidrs, list := publicAccessCIDRList(100)
	var steps []tfresource.TestStep
	for _, mode := range []cksv1beta1.PublicAccessConfig_Mode{cksv1beta1.PublicAccessConfig_TLS, cksv1beta1.PublicAccessConfig_DISABLED} {
		config := publicClusterConfig(fmt.Sprintf("{ mode = %q, allow_cidrs = %s }", mode.String(), list), "v1.33", "")
		steps = append(steps,
			tfresource.TestStep{Config: config, ConfigStateChecks: publicAccessStateChecks(directPublicAccess(mode.Enum(), cidrs...))},
			tfresource.TestStep{ResourceName: publicClusterAddress, ImportState: true, ImportStateVerify: true},
			tfresource.TestStep{Config: config, PlanOnly: true},
		)
	}
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: service.checkDestroyed, Steps: steps,
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	requirePublicAccess(t, directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), cidrs...), service.creates[0].PublicAccess)
	require.Len(t, service.updates, 1)
	requirePublicAccess(t, directPublicAccess(cksv1beta1.PublicAccessConfig_DISABLED.Enum(), cidrs...), service.updates[0].PublicAccess)
}

func TestPublicAccessTerraformUnknownConfiguration(t *testing.T) {
	cidrs, list := publicAccessCIDRList(100)
	for _, tc := range []struct {
		name, expression, input string
		want                    *cksv1beta1.PublicAccessConfig
	}{
		{
			name: "whole object", expression: "terraform_data.value.output",
			input: `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24"),
		},
		{
			name: "mode", expression: `{ mode = terraform_data.value.output, allow_cidrs = ["203.0.113.0/24"] }`,
			input: `"TLS"`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24"),
		},
		{
			name: "CIDR set", expression: `{ mode = "TLS", allow_cidrs = terraform_data.value.output }`,
			input: `["203.0.113.0/24"]`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24"),
		},
		{
			name: "CIDR element", expression: `{ mode = "TLS", allow_cidrs = [terraform_data.value.output] }`,
			input: `"203.0.113.0/24"`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24"),
		},
		{
			name: "disabled empty", expression: `{ mode = terraform_data.value.output, allow_cidrs = [] }`,
			input: `"DISABLED"`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_DISABLED.Enum()),
		},
		{
			name: "unspecified empty", expression: `{ mode = terraform_data.value.output, allow_cidrs = [] }`,
			input: `"MODE_UNSPECIFIED"`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_MODE_UNSPECIFIED.Enum()),
		},
		{
			name: "null mode", expression: `{ mode = terraform_data.value.output == "null" ? null : "TLS", allow_cidrs = [] }`,
			input: `"null"`, want: directPublicAccess(nil),
		},
		{
			name: "unknown duplicate at limit", expression: `{ mode = "TLS", allow_cidrs = concat(` + list + ", [terraform_data.value.output]) }",
			input: `"10.0.0.0/24"`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), cidrs...),
		},
	} {
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/update=%t", tc.name, update), func(t *testing.T) {
				service := startPublicAccessServer(t)
				config := publicClusterConfig(tc.expression, "v1.33", "") + fmt.Sprintf("\nresource \"terraform_data\" \"value\" { input = %s }\n", tc.input)
				var steps []tfresource.TestStep
				if update {
					steps = append(steps, tfresource.TestStep{Config: publicClusterConfig(`{ mode = "TLS", allow_cidrs = ["192.0.2.0/24"] }`, "v1.33", "")})
				}
				steps = append(steps,
					tfresource.TestStep{Config: config, ConfigStateChecks: publicAccessStateChecks(tc.want)},
					tfresource.TestStep{Config: config, PlanOnly: true},
				)
				tfresource.UnitTest(t, tfresource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: service.checkDestroyed, Steps: steps,
				})
				service.mu.Lock()
				defer service.mu.Unlock()
				require.Len(t, service.creates, 1)
				if update {
					require.Equal(t, 1, service.updateCalls)
					require.Len(t, service.updates, 1)
					require.Equal(t, []string{"public_access"}, service.updates[0].UpdateMask.Paths)
					requirePublicAccess(t, tc.want, service.updates[0].PublicAccess)
				} else {
					require.Zero(t, service.updateCalls)
					requirePublicAccess(t, tc.want, service.creates[0].PublicAccess)
				}
			})
		}
	}
}

func TestPublicAccessTerraformRejectsResolvedInvalidConfiguration(t *testing.T) {
	_, tooMany := publicAccessCIDRList(101)
	for _, tc := range []struct{ name, expression, input, expectedError string }{
		{name: "whole object", expression: "terraform_data.value.output", input: `{ mode = "TLS", allow_cidrs = [] }`, expectedError: `(?i)CIDR`},
		{name: "mode requires CIDRs", expression: "{ mode = terraform_data.value.output, allow_cidrs = [] }", input: `"TLS"`, expectedError: `(?i)CIDR`},
		{name: "invalid mode", expression: `{ mode = terraform_data.value.output, allow_cidrs = ["203.0.113.0/24"] }`, input: `"MTLS"`, expectedError: `(?i)mode`},
		{name: "lowercase mode", expression: `{ mode = terraform_data.value.output, allow_cidrs = ["203.0.113.0/24"] }`, input: `"tls"`, expectedError: `(?i)mode`},
		{name: "numeric mode", expression: `{ mode = terraform_data.value.output, allow_cidrs = ["203.0.113.0/24"] }`, input: `"2"`, expectedError: `(?i)mode`},
		{name: "empty mode", expression: `{ mode = terraform_data.value.output, allow_cidrs = ["203.0.113.0/24"] }`, input: `""`, expectedError: `(?i)mode`},
		{name: "CIDR set", expression: `{ mode = "TLS", allow_cidrs = terraform_data.value.output }`, input: "[]", expectedError: `(?i)CIDR`},
		// A literal null input is already known at plan; the condition must resolve at apply.
		{name: "null CIDR set", expression: `{ mode = "TLS", allow_cidrs = terraform_data.value.output == "null" ? null : ["203.0.113.0/24"] }`, input: `"null"`, expectedError: `(?i)CIDR`},
		{name: "invalid disabled element", expression: `{ mode = "DISABLED", allow_cidrs = [terraform_data.value.output] }`, input: `"not-a-cidr"`, expectedError: `(?i)CIDR`},
		{name: "null disabled element", expression: `{ mode = "DISABLED", allow_cidrs = [terraform_data.value.output == "null" ? null : "203.0.113.0/24"] }`, input: `"null"`, expectedError: `(?i)CIDR`},
		{name: "101 disabled", expression: `{ mode = "DISABLED", allow_cidrs = terraform_data.value.output }`, input: tooMany, expectedError: "100"},
	} {
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/update=%t", tc.name, update), func(t *testing.T) {
				service := startPublicAccessServer(t)
				config := publicClusterConfig(tc.expression, "v1.33", "") + fmt.Sprintf("\nresource \"terraform_data\" \"value\" { input = %s }\n", tc.input)
				var steps []tfresource.TestStep
				if update {
					steps = append(steps, tfresource.TestStep{Config: publicClusterConfig(`{ mode = "TLS", allow_cidrs = ["192.0.2.0/24"] }`, "v1.33", "")})
				}
				// A successful plan proves deferral; only the resolved apply may reject the value.
				steps = append(steps,
					tfresource.TestStep{Config: config, PlanOnly: true, ExpectNonEmptyPlan: true},
					tfresource.TestStep{Config: config, ExpectError: regexp.MustCompile(tc.expectedError)},
				)
				tfresource.UnitTest(t, tfresource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: service.checkDestroyed, Steps: steps,
				})
				service.mu.Lock()
				defer service.mu.Unlock()
				if update {
					require.Len(t, service.creates, 1)
				} else {
					require.Empty(t, service.creates)
				}
				require.Zero(t, service.updateCalls)
			})
		}
	}
}

func TestPublicAccessTerraformLegacyPublicIndependence(t *testing.T) {
	service := startPublicAccessServer(t)
	var steps []tfresource.TestStep
	for _, tc := range []struct {
		legacy     bool
		expression string
		want       *cksv1beta1.PublicAccessConfig
	}{
		{legacy: true},
		{legacy: true, expression: `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24")},
		{legacy: false, expression: `{ mode = "TLS", allow_cidrs = ["203.0.113.0/24"] }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_TLS.Enum(), "203.0.113.0/24")},
		{legacy: false, expression: `{ mode = "DISABLED" }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_DISABLED.Enum())},
		{legacy: true, expression: `{ mode = "DISABLED" }`, want: directPublicAccess(cksv1beta1.PublicAccessConfig_DISABLED.Enum())},
	} {
		config := strings.Replace(publicClusterConfig(tc.expression, "v1.33", ""), "public = false", fmt.Sprintf("public = %t", tc.legacy), 1)
		checks := publicAccessStateChecks(tc.want)
		checks = append(checks,
			statecheck.ExpectKnownValue(publicClusterAddress, tfjsonpath.New("public"), knownvalue.Bool(tc.legacy)),
			statecheck.ExpectKnownValue("data.coreweave_cks_cluster.test", tfjsonpath.New("public"), knownvalue.Bool(tc.legacy)),
			statecheck.ExpectKnownValue(publicClusterAddress, tfjsonpath.New("api_server_endpoint"), knownvalue.StringExact("https://legacy.example.test")),
			statecheck.ExpectKnownValue("data.coreweave_cks_cluster.test", tfjsonpath.New("api_server_endpoint"), knownvalue.StringExact("https://legacy.example.test")),
		)
		steps = append(steps, tfresource.TestStep{Config: config, ConfigStateChecks: checks})
	}
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: service.checkDestroyed, Steps: steps,
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.updates, 4)
	for _, i := range []int{0, 2} {
		require.Equal(t, []string{"public_access"}, service.updates[i].UpdateMask.Paths)
	}
	for _, i := range []int{1, 3} {
		require.Equal(t, []string{"public"}, service.updates[i].UpdateMask.Paths)
		require.Nil(t, service.updates[i].PublicAccess)
	}
	require.False(t, service.updates[1].Public)
	require.True(t, service.updates[3].Public)
}

func TestPublicAccessTerraformCIDRValidation(t *testing.T) {
	_, tooMany := publicAccessCIDRList(101)
	for _, tc := range []struct {
		name, expression, expectedError string
	}{
		{name: "malformed", expression: `{ mode = "TLS", allow_cidrs = ["not-a-cidr"] }`, expectedError: `(?i)CIDR`},
		{name: "malformed while disabled", expression: `{ mode = "DISABLED", allow_cidrs = ["not-a-cidr"] }`, expectedError: `(?i)CIDR`},
		{name: "malformed while unspecified", expression: `{ mode = "MODE_UNSPECIFIED", allow_cidrs = ["not-a-cidr"] }`, expectedError: `(?i)CIDR`},
		{name: "bare IP without mode", expression: `{ allow_cidrs = ["203.0.113.1"] }`, expectedError: `(?i)CIDR`},
		{name: "bare IP while disabled", expression: `{ mode = "DISABLED", allow_cidrs = ["203.0.113.1"] }`, expectedError: `(?i)CIDR`},
		{name: "invalid IPv6 prefix", expression: `{ mode = "TLS", allow_cidrs = ["2001:db8::/129"] }`, expectedError: `(?i)CIDR`},
		{name: "too many", expression: `{ mode = "TLS", allow_cidrs = ` + tooMany + " }", expectedError: "100"},
		{name: "too many while disabled", expression: `{ mode = "DISABLED", allow_cidrs = ` + tooMany + " }", expectedError: "100"},
		{name: "null element", expression: `{ mode = "TLS", allow_cidrs = [null] }`, expectedError: `(?i)CIDR`},
		{name: "null element while disabled", expression: `{ mode = "DISABLED", allow_cidrs = [null] }`, expectedError: `(?i)CIDR`},
		{name: "TLS missing CIDRs", expression: `{ mode = "TLS" }`, expectedError: `(?i)CIDR`},
		{name: "TLS null CIDRs", expression: `{ mode = "TLS", allow_cidrs = null }`, expectedError: `(?i)CIDR`},
		{name: "TLS empty CIDRs", expression: `{ mode = "TLS", allow_cidrs = [] }`, expectedError: `(?i)CIDR`},
		{name: "invalid mode", expression: `{ mode = "MTLS", allow_cidrs = ["203.0.113.0/24"] }`, expectedError: `(?i)mode`},
		{name: "lowercase mode", expression: `{ mode = "tls", allow_cidrs = ["203.0.113.0/24"] }`, expectedError: `(?i)mode`},
		{name: "numeric mode", expression: `{ mode = "2", allow_cidrs = ["203.0.113.0/24"] }`, expectedError: `(?i)mode`},
		{name: "empty mode", expression: `{ mode = "", allow_cidrs = ["203.0.113.0/24"] }`, expectedError: `(?i)mode`},
	} {
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/update=%t", tc.name, update), func(t *testing.T) {
				service := startPublicAccessServer(t)
				var steps []tfresource.TestStep
				if update {
					steps = append(steps, tfresource.TestStep{Config: publicClusterConfig(`{ mode = "TLS", allow_cidrs = ["192.0.2.0/24"] }`, "v1.33", "")})
				}
				steps = append(steps, tfresource.TestStep{
					Config: publicClusterConfig(tc.expression, "v1.33", ""), PlanOnly: true, ExpectError: regexp.MustCompile(tc.expectedError),
				})
				if update {
					// Restore valid configuration so destroy is not blocked by config validation.
					steps = append(steps, tfresource.TestStep{Config: publicClusterConfig(`{ mode = "TLS", allow_cidrs = ["192.0.2.0/24"] }`, "v1.33", "")})
				}
				tfresource.UnitTest(t, tfresource.TestCase{
					ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: service.checkDestroyed, Steps: steps,
				})
				service.mu.Lock()
				defer service.mu.Unlock()
				if update {
					require.Len(t, service.creates, 1)
				} else {
					require.Empty(t, service.creates)
				}
				require.Zero(t, service.updateCalls)
			})
		}
	}
}
