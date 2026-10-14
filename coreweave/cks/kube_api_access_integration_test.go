package cks_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"buf.build/gen/go/coreweave/cks/connectrpc/go/v2/coreweave/cks/v1beta1/cksv1beta1connect"
	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/cks"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
)

const (
	kubeAPIClusterAddress    = "coreweave_cks_cluster.test"
	kubeAPIDataSourceAddress = "data.coreweave_cks_cluster.test"
	kubeAPILegacyEndpoint    = "https://legacy.example.test"
	kubeAPIOtherEndpoint     = "https://kube-api-2.example.test:443"
	kubeAPIGateMessage       = "kube API access configuration is not enabled for this organization"
	kubeAPIPolicy            = `{ public = { allow_cidrs = ["203.0.113.0/24"] } }`
	kubeAPIOtherPolicy       = `{ public = { allow_cidrs = ["192.0.2.0/24"] } }`
)

var (
	errKubeAPIInvalidCIDR = regexp.MustCompile(`Invalid CIDR prefix`)
	errKubeAPIUpgrade     = regexp.MustCompile(`(?is)cannot\s+change\s+during\s+a\s+Kubernetes\s+upgrade`)
	errKubeAPIGate        = regexp.MustCompile(`(?s)Unauthorized.*permission_denied`)
	testCAPEMA            = []byte(cks.KubeAPIAccessCACertPEM)
	testCAPEMB            = []byte("-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ0FfUk9UQVRFRA==\n-----END CERTIFICATE-----\n" +
		"-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ0FfQlVORExF\n-----END CERTIFICATE-----\n")
)

// kubeAPIAccessServer is a fake CKS API implementing the kube_api_access update contract:
// only the parent mask is accepted, a masked nil body clears, explicit writes are gated,
// and observations are controlled by the test.
type kubeAPIAccessServer struct {
	cksv1beta1connect.UnimplementedClusterServiceHandler
	mu          sync.Mutex
	cluster     *cksv1beta1.Cluster
	createCalls int
	creates     []*cksv1beta1.CreateClusterRequest
	updateCalls int
	updates     []*cksv1beta1.UpdateClusterRequest
	// gateDisabled rejects explicit access writes with PermissionDenied, as upstream does when the flag is off.
	gateDisabled bool
	// accessError, when set, rejects explicit access writes with this error.
	accessError error
	// status is the observation returned with every cluster response.
	status *cksv1beta1.KubeAPIAccessStatus
	// statusOnWrite, when set, replaces status when an access write is accepted.
	statusOnWrite *cksv1beta1.KubeAPIAccessStatus
}

func startKubeAPIAccessServer(t *testing.T) *kubeAPIAccessServer {
	t.Helper()
	service := &kubeAPIAccessServer{}
	mux := http.NewServeMux()
	connectServer := connect.NewServer()
	cksv1beta1connect.RegisterClusterServiceHandler(connectServer, service)
	connecthttp.Mount(mux, connectServer, connecthttp.WithReadMaxBytes(0))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	// Environment wins over provider HCL, so isolate all requests from real credentials and services.
	// Do not parallelize these tests: the in-process provider reads the process environment.
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	return service
}

// fakeInvalidArgument returns a plain InvalidArgument error without details.
func fakeInvalidArgument(description string) error {
	return connect.NewError(connect.CodeInvalidArgument, description)
}

// fakeFieldViolation models a server-side field validation failure with BadRequest details.
func fakeFieldViolation(field, description string) error {
	err := connect.Errorf(connect.CodeInvalidArgument, "%s: %s", field, description)
	if detail, detailErr := connectproto.NewErrorDetail(&errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: field, Description: description}},
	}); detailErr == nil {
		err = err.WithDetail(detail)
	}
	return err
}

func (s *kubeAPIAccessServer) accessWriteError() error {
	if s.gateDisabled {
		return connect.NewError(connect.CodePermissionDenied, kubeAPIGateMessage)
	}
	return s.accessError
}

func (s *kubeAPIAccessServer) render(reverse bool) *cksv1beta1.Cluster {
	cluster := proto.Clone(s.cluster).(*cksv1beta1.Cluster)
	cluster.KubeApiAccessStatus = cloneAccessStatus(s.status)
	if public := cluster.GetKubeApiAccess().GetPublic(); reverse && public != nil {
		slices.Reverse(public.AllowCidrs)
		slices.Reverse(public.DenyCidrs)
	}
	return cluster
}

func (s *kubeAPIAccessServer) CreateCluster(_ context.Context, req *cksv1beta1.CreateClusterRequest) (*cksv1beta1.CreateClusterResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCalls++
	if req.KubeApiAccess != nil {
		if err := s.accessWriteError(); err != nil {
			return nil, err
		}
		if s.statusOnWrite != nil {
			s.status = cloneAccessStatus(s.statusOnWrite)
		}
	}
	s.creates = append(s.creates, proto.Clone(req).(*cksv1beta1.CreateClusterRequest))
	s.cluster = &cksv1beta1.Cluster{
		Id: "00000000-0000-4000-8000-000000000001", Name: req.Name, Zone: req.Zone,
		VpcId: req.VpcId, Version: req.Version, Public: req.Public,
		Network: proto.Clone(req.Network).(*cksv1beta1.ClusterNetworkConfig),
		Status:  cksv1beta1.Cluster_STATUS_RUNNING, ApiServerEndpoint: kubeAPILegacyEndpoint,
		AuditPolicy: req.AuditPolicy, KubeApiAccess: cloneAccess(req.KubeApiAccess),
	}
	return &cksv1beta1.CreateClusterResponse{Cluster: s.render(false)}, nil
}

func (s *kubeAPIAccessServer) GetCluster(_ context.Context, _ *cksv1beta1.GetClusterRequest) (*cksv1beta1.GetClusterResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cluster == nil {
		return nil, connect.NewError(connect.CodeNotFound, "cluster not found")
	}
	// The API does not promise CIDR order; reverse it so order-sensitive state would drift.
	return &cksv1beta1.GetClusterResponse{Cluster: s.render(true)}, nil
}

func (s *kubeAPIAccessServer) UpdateCluster(_ context.Context, req *cksv1beta1.UpdateClusterRequest) (*cksv1beta1.UpdateClusterResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalls++
	if s.cluster == nil {
		return nil, connect.NewError(connect.CodeNotFound, "cluster not found")
	}
	paths := req.GetUpdateMask().GetPaths()
	if len(paths) == 0 {
		return nil, fakeInvalidArgument("update mask is required")
	}
	accessWrite := slices.Contains(paths, "kube_api_access")
	if req.KubeApiAccess != nil && !accessWrite {
		return nil, fakeInvalidArgument("unmasked kube_api_access body")
	}
	if accessWrite && slices.Contains(paths, "version") {
		return nil, fakeInvalidArgument("cannot change kube_api_access during an upgrade")
	}
	if accessWrite {
		if err := s.accessWriteError(); err != nil {
			return nil, err
		}
	}
	for _, path := range paths {
		switch path {
		case "kube_api_access", "version", "public", "audit_policy":
		default:
			// Includes kube_api_access leaf paths: the provider owns and replaces the whole subtree.
			return nil, fakeInvalidArgument("unexpected update path: " + path)
		}
	}
	s.updates = append(s.updates, proto.Clone(req).(*cksv1beta1.UpdateClusterRequest))
	for _, path := range paths {
		switch path {
		case "kube_api_access":
			s.cluster.KubeApiAccess = cloneAccess(req.KubeApiAccess)
			if s.statusOnWrite != nil {
				s.status = cloneAccessStatus(s.statusOnWrite)
			}
		case "version":
			s.cluster.Version = req.Version
		case "public":
			s.cluster.Public = req.Public
		case "audit_policy":
			s.cluster.AuditPolicy = req.AuditPolicy
		}
	}
	return &cksv1beta1.UpdateClusterResponse{Cluster: s.render(false)}, nil
}

func (s *kubeAPIAccessServer) DeleteCluster(_ context.Context, _ *cksv1beta1.DeleteClusterRequest) (*cksv1beta1.DeleteClusterResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cluster == nil {
		return nil, connect.NewError(connect.CodeNotFound, "cluster not found")
	}
	cluster := s.render(false)
	s.cluster = nil
	return &cksv1beta1.DeleteClusterResponse{Cluster: cluster}, nil
}

func (s *kubeAPIAccessServer) checkDestroyed(_ *terraform.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cluster != nil {
		return errors.New("cluster was not deleted")
	}
	return nil
}

func (s *kubeAPIAccessServer) with(fn func(s *kubeAPIAccessServer)) func() {
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		fn(s)
	}
}

func (s *kubeAPIAccessServer) setAccess(config *cksv1beta1.KubeAPIAccess) func() {
	return s.with(func(s *kubeAPIAccessServer) { s.cluster.KubeApiAccess = cloneAccess(config) })
}

func (s *kubeAPIAccessServer) setStatus(status *cksv1beta1.KubeAPIAccessStatus) func() {
	return s.with(func(s *kubeAPIAccessServer) { s.status = cloneAccessStatus(status) })
}

func (s *kubeAPIAccessServer) setGateDisabled(disabled bool) func() {
	return s.with(func(s *kubeAPIAccessServer) { s.gateDisabled = disabled })
}

func cloneAccess(config *cksv1beta1.KubeAPIAccess) *cksv1beta1.KubeAPIAccess {
	if config == nil {
		return nil
	}
	return proto.Clone(config).(*cksv1beta1.KubeAPIAccess)
}

func cloneAccessStatus(status *cksv1beta1.KubeAPIAccessStatus) *cksv1beta1.KubeAPIAccessStatus {
	if status == nil {
		return nil
	}
	return proto.Clone(status).(*cksv1beta1.KubeAPIAccessStatus)
}

func accessDisabled() *cksv1beta1.KubeAPIAccess {
	return &cksv1beta1.KubeAPIAccess{}
}

func observed(endpoint *string, status cksv1beta1.PublicKubeAPIAccessStatus_Status, ca []byte) *cksv1beta1.KubeAPIAccessStatus {
	return &cksv1beta1.KubeAPIAccessStatus{Public: &cksv1beta1.PublicKubeAPIAccessStatus{Endpoint: endpoint, Status: status}, CaData: ca}
}

func accessEqual(a, b *cksv1beta1.KubeAPIAccess) bool {
	if (a == nil) != (b == nil) || (a.GetPublic() == nil) != (b.GetPublic() == nil) {
		return false
	}
	sorted := func(cidrs []string) []string {
		cidrs = slices.Clone(cidrs)
		slices.Sort(cidrs)
		return cidrs
	}
	return slices.Equal(sorted(a.GetPublic().GetAllowCidrs()), sorted(b.GetPublic().GetAllowCidrs())) &&
		slices.Equal(sorted(a.GetPublic().GetDenyCidrs()), sorted(b.GetPublic().GetDenyCidrs()))
}

// requireAccessWrite asserts a full-parent replacement: one parent mask and the desired body.
func requireAccessWrite(t *testing.T, update *cksv1beta1.UpdateClusterRequest, want *cksv1beta1.KubeAPIAccess) {
	t.Helper()
	require.Equal(t, []string{"kube_api_access"}, update.UpdateMask.Paths)
	require.Empty(t, update.Version)
	require.Nil(t, update.Network)
	cks.RequireKubeAPIAccessProto(t, want, update.KubeApiAccess)
}

func kubeAPIClusterConfig(access, version, extra string) string {
	attribute := ""
	if access != "" {
		attribute = "kube_api_access = " + access
	}
	return fmt.Sprintf(`
provider "coreweave" {}
resource "coreweave_cks_cluster" "test" {
  name = "kube-api-access-test"
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

func kubeAPIUnknownConfig(expression, input string) string {
	return kubeAPIClusterConfig(expression, "v1.33", "") + fmt.Sprintf("\nresource \"terraform_data\" \"value\" { input = %s }\n", input)
}

func cidrList(cidrs []string) string {
	quoted := make([]string, len(cidrs))
	for i, cidr := range cidrs {
		quoted[i] = fmt.Sprintf("%q", cidr)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func accessKnownValue(config *cksv1beta1.KubeAPIAccess) knownvalue.Check {
	if config == nil {
		return knownvalue.Null()
	}
	if config.Public == nil {
		return knownvalue.ObjectExact(map[string]knownvalue.Check{"public": knownvalue.Null()})
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"public": knownvalue.ObjectExact(map[string]knownvalue.Check{
		"allow_cidrs": cidrSetKnownValue(config.Public.AllowCidrs),
		"deny_cidrs":  cidrSetKnownValue(config.Public.DenyCidrs),
	})})
}

func cidrSetKnownValue(cidrs []string) knownvalue.Check {
	checks := make([]knownvalue.Check, len(cidrs))
	for i, cidr := range cidrs {
		checks[i] = knownvalue.StringExact(cidr)
	}
	return knownvalue.SetExact(checks)
}

func statusKnownValue(status *cksv1beta1.KubeAPIAccessStatus) knownvalue.Check {
	if status == nil {
		return knownvalue.Null()
	}
	var public knownvalue.Check = knownvalue.Null()
	if status.Public != nil {
		var endpoint knownvalue.Check = knownvalue.Null()
		if status.Public.Endpoint != nil {
			endpoint = knownvalue.StringExact(*status.Public.Endpoint)
		}
		public = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"endpoint": endpoint,
			"status":   knownvalue.StringExact(status.Public.Status.String()),
		})
	}
	var ca knownvalue.Check = knownvalue.Null()
	if len(status.CaData) > 0 {
		ca = knownvalue.StringExact(base64.StdEncoding.EncodeToString(status.CaData))
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"public": public, "ca_data": ca})
}

// accessStateChecks verifies configuration and observations on both the resource and the data source.
func accessStateChecks(config *cksv1beta1.KubeAPIAccess, status *cksv1beta1.KubeAPIAccessStatus) []statecheck.StateCheck {
	var checks []statecheck.StateCheck
	for _, address := range []string{kubeAPIClusterAddress, kubeAPIDataSourceAddress} {
		checks = append(checks,
			statecheck.ExpectKnownValue(address, tfjsonpath.New("kube_api_access"), accessKnownValue(config)),
			statecheck.ExpectKnownValue(address, tfjsonpath.New("kube_api_access_status"), statusKnownValue(status)),
		)
	}
	return checks
}

func expectUpdate() tfresource.ConfigPlanChecks {
	return tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(kubeAPIClusterAddress, plancheck.ResourceActionUpdate)}}
}

func runKubeAPIAccessTest(t *testing.T, service *kubeAPIAccessServer, steps ...tfresource.TestStep) {
	t.Helper()
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             service.checkDestroyed, Steps: steps,
	})
}

func TestKubeAPIAccessTerraformLifecycle(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	allow := []string{"203.0.113.0/24", "2001:db8::/32"}
	deny := []string{"203.0.113.10/32", "2001:db8::10/128"}
	stages := []struct {
		hcl  string
		want *cksv1beta1.KubeAPIAccess
	}{
		{hcl: ""},
		{hcl: "{}", want: accessDisabled()},
		{hcl: "{ public = null }", want: accessDisabled()},
		{hcl: "{ public = {} }", want: cks.KubeAPIAccessProto(nil, nil)},
		{hcl: fmt.Sprintf("{ public = { allow_cidrs = %s } }", cidrList(allow)), want: cks.KubeAPIAccessProto(allow, nil)},
		{hcl: fmt.Sprintf("{ public = { deny_cidrs = %s } }", cidrList(deny[:1])), want: cks.KubeAPIAccessProto(nil, deny[:1])},
		{hcl: fmt.Sprintf("{ public = { allow_cidrs = %s, deny_cidrs = %s } }", cidrList(allow), cidrList(deny)), want: cks.KubeAPIAccessProto(allow, deny)},
		{hcl: fmt.Sprintf("{ public = { allow_cidrs = [], deny_cidrs = %s } }", cidrList(deny)), want: cks.KubeAPIAccessProto(nil, deny)},
		{hcl: fmt.Sprintf("{ public = { allow_cidrs = %s, deny_cidrs = [] } }", cidrList(allow)), want: cks.KubeAPIAccessProto(allow, nil)},
		{hcl: "{ public = { allow_cidrs = [], deny_cidrs = [] } }", want: cks.KubeAPIAccessProto(nil, nil)},
		{hcl: "{}", want: accessDisabled()},
		{hcl: fmt.Sprintf("{ public = { allow_cidrs = %s, deny_cidrs = %s } }", cidrList(allow), cidrList(deny)), want: cks.KubeAPIAccessProto(allow, deny)},
		{hcl: ""},
	}
	var steps []tfresource.TestStep
	for _, stage := range stages {
		config := kubeAPIClusterConfig(stage.hcl, "v1.33", "")
		steps = append(steps,
			tfresource.TestStep{Config: config, ConfigStateChecks: accessStateChecks(stage.want, nil)},
			tfresource.TestStep{ResourceName: kubeAPIClusterAddress, ImportState: true, ImportStateVerify: true},
			tfresource.TestStep{Config: config, PlanOnly: true},
		)
	}
	runKubeAPIAccessTest(t, service, steps...)

	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.creates, 1)
	cks.RequireKubeAPIAccessProto(t, nil, service.creates[0].KubeApiAccess)
	var want []*cksv1beta1.KubeAPIAccess
	for i := 1; i < len(stages); i++ {
		if !accessEqual(stages[i-1].want, stages[i].want) {
			want = append(want, stages[i].want)
		}
	}
	require.Len(t, service.updates, len(want), "equivalent configurations must not write")
	for i, update := range service.updates {
		requireAccessWrite(t, update, want[i])
	}
}

func TestKubeAPIAccessTerraformCreatePresence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hcl         string
		equivalents []string
		want        *cksv1beta1.KubeAPIAccess
	}{
		{name: "empty outer", hcl: "{}", equivalents: []string{"{ public = null }"}, want: accessDisabled()},
		{
			name: "empty public", hcl: "{ public = {} }", want: cks.KubeAPIAccessProto(nil, nil),
			equivalents: []string{"{ public = { allow_cidrs = null } }", "{ public = { deny_cidrs = null } }", "{ public = { allow_cidrs = [], deny_cidrs = [] } }"},
		},
		{
			name: "allow only", hcl: `{ public = { allow_cidrs = ["203.0.113.0/24"] } }`, want: cks.KubeAPIAccessProto([]string{"203.0.113.0/24"}, nil),
			equivalents: []string{`{ public = { allow_cidrs = ["203.0.113.0/24"], deny_cidrs = [] } }`},
		},
		{
			name: "deny only", hcl: `{ public = { deny_cidrs = ["192.0.2.0/24"] } }`, want: cks.KubeAPIAccessProto(nil, []string{"192.0.2.0/24"}),
			equivalents: []string{`{ public = { allow_cidrs = null, deny_cidrs = ["192.0.2.0/24"] } }`},
		},
		{
			name: "combined", hcl: `{ public = { allow_cidrs = ["203.0.113.0/24", "2001:db8::/32"], deny_cidrs = ["203.0.113.10/32"] } }`,
			want: cks.KubeAPIAccessProto([]string{"203.0.113.0/24", "2001:db8::/32"}, []string{"203.0.113.10/32"}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := startKubeAPIAccessServer(t)
			steps := []tfresource.TestStep{{Config: kubeAPIClusterConfig(tc.hcl, "v1.33", ""), ConfigStateChecks: accessStateChecks(tc.want, nil)}}
			for _, equivalent := range tc.equivalents {
				steps = append(steps, tfresource.TestStep{Config: kubeAPIClusterConfig(equivalent, "v1.33", ""), PlanOnly: true})
			}
			steps = append(steps, tfresource.TestStep{ResourceName: kubeAPIClusterAddress, ImportState: true, ImportStateVerify: true})
			runKubeAPIAccessTest(t, service, steps...)
			service.mu.Lock()
			defer service.mu.Unlock()
			require.Len(t, service.creates, 1)
			cks.RequireKubeAPIAccessProto(t, tc.want, service.creates[0].KubeApiAccess)
			require.Zero(t, service.updateCalls)
		})
	}
}

func TestKubeAPIAccessTerraformDrift(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	config := kubeAPIClusterConfig(kubeAPIPolicy, "v1.33", "")
	want := cks.KubeAPIAccessProto([]string{"203.0.113.0/24"}, nil)
	var steps []tfresource.TestStep
	steps = append(steps, tfresource.TestStep{Config: config})
	for _, drift := range []*cksv1beta1.KubeAPIAccess{
		cks.KubeAPIAccessProto([]string{"192.0.2.0/24"}, nil),
		cks.KubeAPIAccessProto([]string{"203.0.113.0/24"}, []string{"203.0.113.10/32"}),
		accessDisabled(),
		nil,
	} {
		steps = append(steps,
			tfresource.TestStep{Config: config, PreConfig: service.setAccess(drift), PlanOnly: true, ExpectNonEmptyPlan: true},
			tfresource.TestStep{Config: config, ConfigPlanChecks: expectUpdate(), ConfigStateChecks: accessStateChecks(want, nil)},
		)
	}
	// Configuration appearing outside Terraform is cleared when the attribute is omitted.
	absent := kubeAPIClusterConfig("", "v1.33", "")
	steps = append(steps,
		tfresource.TestStep{Config: absent, ConfigStateChecks: accessStateChecks(nil, nil)},
		tfresource.TestStep{Config: absent, PreConfig: service.setAccess(want), PlanOnly: true, ExpectNonEmptyPlan: true},
		tfresource.TestStep{Config: absent, ConfigPlanChecks: expectUpdate(), ConfigStateChecks: accessStateChecks(nil, nil)},
	)
	runKubeAPIAccessTest(t, service, steps...)

	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.updates, 6)
	for i := range 4 {
		requireAccessWrite(t, service.updates[i], want)
	}
	requireAccessWrite(t, service.updates[4], nil)
	requireAccessWrite(t, service.updates[5], nil)
}

func TestKubeAPIAccessTerraformUnrelatedUpdatesOmitAccess(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	policy := `{ public = { allow_cidrs = ["203.0.113.0/24", "2001:db8::/32"], deny_cidrs = ["203.0.113.10/32", "2001:db8::10/128"] } }`
	want := cks.KubeAPIAccessProto([]string{"203.0.113.0/24", "2001:db8::/32"}, []string{"203.0.113.10/32", "2001:db8::10/128"})
	legacy := strings.Replace(kubeAPIClusterConfig(policy, "v1.34", `audit_policy = "dGVzdA=="`), "public = false", "public = true", 1)
	runKubeAPIAccessTest(t, service,
		tfresource.TestStep{Config: kubeAPIClusterConfig(policy, "v1.33", ""), ConfigStateChecks: accessStateChecks(want, nil)},
		// Reversed API order and an unchanged config are a no-op.
		tfresource.TestStep{Config: kubeAPIClusterConfig(policy, "v1.33", ""), ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		tfresource.TestStep{Config: kubeAPIClusterConfig(policy, "v1.34", ""), ConfigStateChecks: accessStateChecks(want, nil)},
		tfresource.TestStep{Config: kubeAPIClusterConfig(policy, "v1.34", `audit_policy = "dGVzdA=="`), ConfigStateChecks: accessStateChecks(want, nil)},
		tfresource.TestStep{Config: legacy, ConfigStateChecks: accessStateChecks(want, nil)},
	)
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.creates, 1)
	cks.RequireKubeAPIAccessProto(t, want, service.creates[0].KubeApiAccess)
	require.Equal(t, 3, service.updateCalls)
	for i, path := range []string{"version", "audit_policy", "public"} {
		require.Equal(t, []string{path}, service.updates[i].UpdateMask.Paths)
		require.Nil(t, service.updates[i].KubeApiAccess, "unrelated updates must omit the access body")
	}
}

func TestKubeAPIAccessTerraformRejectsUpgradeAndAccessChange(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{name: "set", before: "", after: kubeAPIPolicy},
		{name: "explicit empty outer", before: "", after: "{}"},
		{name: "change", before: kubeAPIPolicy, after: `{ public = { allow_cidrs = ["2001:db8::/32"] } }`},
		{name: "deny-only enable", before: "{}", after: `{ public = { deny_cidrs = ["192.0.2.0/24"] } }`},
		{name: "remove public", before: kubeAPIPolicy, after: "{}"},
		{name: "clear", before: kubeAPIPolicy, after: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := startKubeAPIAccessServer(t)
			runKubeAPIAccessTest(t, service,
				tfresource.TestStep{Config: kubeAPIClusterConfig(tc.before, "v1.33", "")},
				tfresource.TestStep{Config: kubeAPIClusterConfig(tc.after, "v1.34", ""), ExpectError: errKubeAPIUpgrade},
			)
			service.mu.Lock()
			defer service.mu.Unlock()
			require.Zero(t, service.updateCalls, "the combined change must be rejected before any RPC")
			require.Len(t, service.creates, 1)
		})
	}
}

func TestKubeAPIAccessTerraformReplacementAllowsVersionAndAccessChange(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	replacement := strings.Replace(
		kubeAPIClusterConfig(`{ public = { deny_cidrs = ["192.0.2.0/24"] } }`, "v1.34", ""),
		`name = "kube-api-access-test"`, `name = "replacement-cluster"`, 1,
	)
	want := cks.KubeAPIAccessProto(nil, []string{"192.0.2.0/24"})
	runKubeAPIAccessTest(t, service,
		tfresource.TestStep{Config: kubeAPIClusterConfig(kubeAPIPolicy, "v1.33", "")},
		tfresource.TestStep{
			Config: replacement,
			ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(kubeAPIClusterAddress, plancheck.ResourceActionDestroyBeforeCreate),
			}},
			ConfigStateChecks: accessStateChecks(want, nil),
		},
		tfresource.TestStep{Config: replacement, PlanOnly: true},
	)
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.creates, 2)
	require.Zero(t, service.updateCalls)
	require.Equal(t, "v1.34", service.creates[1].Version)
	cks.RequireKubeAPIAccessProto(t, want, service.creates[1].KubeApiAccess)
}

// TestKubeAPIAccessTerraformCIDRLimits proves maximum-size sets round-trip through Terraform and the request.
// Limit enforcement is covered by TestKubeAPIAccessValidation.
func TestKubeAPIAccessTerraformCIDRLimits(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	fixture := cks.KubeAPIAccessCIDRFixture(1025)
	allow, deny := fixture[:1024], fixture[1:]
	full := kubeAPIClusterConfig(fmt.Sprintf("{ public = { allow_cidrs = %s, deny_cidrs = %s } }", cidrList(allow), cidrList(deny)), "v1.33", "")
	// The framework's post-apply refresh plan proves reversed API order does not drift.
	runKubeAPIAccessTest(t, service, tfresource.TestStep{Config: full, ConfigStateChecks: accessStateChecks(cks.KubeAPIAccessProto(allow, deny), nil)})
	service.mu.Lock()
	defer service.mu.Unlock()
	cks.RequireKubeAPIAccessProto(t, cks.KubeAPIAccessProto(allow, deny), service.creates[0].KubeApiAccess)
	require.Zero(t, service.updateCalls)
}

func TestKubeAPIAccessTerraformAcceptsValidCIDRForms(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	stages := []struct {
		hcl  string
		want *cksv1beta1.KubeAPIAccess
	}{
		{
			hcl:  `{ public = { allow_cidrs = ["0.0.0.0/0", "::/0"], deny_cidrs = ["0.0.0.0/0", "::/0"] } }`,
			want: cks.KubeAPIAccessProto([]string{"0.0.0.0/0", "::/0"}, []string{"0.0.0.0/0", "::/0"}),
		},
		{
			hcl:  `{ public = { allow_cidrs = ["203.0.113.0/24", "203.0.113.0/24"], deny_cidrs = ["203.0.113.128/25"] } }`,
			want: cks.KubeAPIAccessProto([]string{"203.0.113.0/24"}, []string{"203.0.113.128/25"}),
		},
		{hcl: `{ public = { allow_cidrs = ["2001:0DB8::/32"] } }`, want: cks.KubeAPIAccessProto([]string{"2001:0DB8::/32"}, nil)},
		{
			hcl:  `{ public = { allow_cidrs = ["203.0.113.10/32", "2001:db8::10/128"], deny_cidrs = ["::ffff:203.0.113.0/120"] } }`,
			want: cks.KubeAPIAccessProto([]string{"203.0.113.10/32", "2001:db8::10/128"}, []string{"::ffff:203.0.113.0/120"}),
		},
	}
	var steps []tfresource.TestStep
	for _, stage := range stages {
		config := kubeAPIClusterConfig(stage.hcl, "v1.33", "")
		steps = append(steps,
			tfresource.TestStep{Config: config, ConfigStateChecks: accessStateChecks(stage.want, nil)},
			tfresource.TestStep{Config: config, PlanOnly: true},
		)
	}
	runKubeAPIAccessTest(t, service, steps...)
	service.mu.Lock()
	defer service.mu.Unlock()
	cks.RequireKubeAPIAccessProto(t, stages[0].want, service.creates[0].KubeApiAccess)
	require.Len(t, service.updates, len(stages)-1)
	for i, update := range service.updates {
		// Valid spellings are sent verbatim; the provider must not canonicalize or mask them.
		requireAccessWrite(t, update, stages[i+1].want)
	}
}

// TestKubeAPIAccessTerraformCIDRValidation proves the schema validator rejects invalid entries at config time.
// Every malformed input is covered by TestKubeAPIAccessValidation.
func TestKubeAPIAccessTerraformCIDRValidation(t *testing.T) {
	for _, tc := range []struct{ field, list string }{
		{field: "allow_cidrs", list: `["not-a-cidr"]`},
		{field: "deny_cidrs", list: `["203.0.113.7/24"]`},
	} {
		t.Run(tc.field, func(t *testing.T) {
			service := startKubeAPIAccessServer(t)
			runKubeAPIAccessTest(t, service, tfresource.TestStep{
				Config:   kubeAPIClusterConfig(fmt.Sprintf("{ public = { %s = %s } }", tc.field, tc.list), "v1.33", ""),
				PlanOnly: true, ExpectError: errKubeAPIInvalidCIDR,
			})
			service.mu.Lock()
			defer service.mu.Unlock()
			require.Zero(t, service.createCalls)
			require.Zero(t, service.updateCalls)
		})
	}
}

// TestKubeAPIAccessTerraformUnknownConfiguration proves values unknown at plan are validated and sent once resolved.
// Each unknown position, create and update, is covered by TestKubeAPIAccessUnknownsDeferUntilApply.
func TestKubeAPIAccessTerraformUnknownConfiguration(t *testing.T) {
	t.Run("resolves valid", func(t *testing.T) {
		service := startKubeAPIAccessServer(t)
		want := cks.KubeAPIAccessProto([]string{"203.0.113.0/24"}, []string{"203.0.113.10/32"})
		config := kubeAPIUnknownConfig("terraform_data.value.output", `{ public = { allow_cidrs = ["203.0.113.0/24"], deny_cidrs = ["203.0.113.10/32"] } }`)
		runKubeAPIAccessTest(t, service, tfresource.TestStep{Config: config, ConfigStateChecks: accessStateChecks(want, nil)})
		service.mu.Lock()
		defer service.mu.Unlock()
		require.Len(t, service.creates, 1)
		cks.RequireKubeAPIAccessProto(t, want, service.creates[0].KubeApiAccess)
		require.Zero(t, service.updateCalls)
	})

	t.Run("resolves invalid", func(t *testing.T) {
		service := startKubeAPIAccessServer(t)
		config := kubeAPIUnknownConfig("{ public = { allow_cidrs = terraform_data.value.output } }", `["203.0.113.7/24"]`)
		runKubeAPIAccessTest(t, service,
			// A successful plan proves deferral; only the resolved apply may reject the value.
			tfresource.TestStep{Config: config, PlanOnly: true, ExpectNonEmptyPlan: true},
			tfresource.TestStep{Config: config, ExpectError: errKubeAPIInvalidCIDR},
		)
		service.mu.Lock()
		defer service.mu.Unlock()
		require.Zero(t, service.createCalls, "invalid values must be rejected before any RPC")
		require.Zero(t, service.updateCalls)
	})
}

func TestKubeAPIAccessTerraformFeatureGate(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		for _, hcl := range []string{"{}", "{ public = null }", "{ public = {} }", `{ public = { deny_cidrs = ["192.0.2.0/24"] } }`} {
			t.Run(hcl, func(t *testing.T) {
				service := startKubeAPIAccessServer(t)
				service.setGateDisabled(true)()
				runKubeAPIAccessTest(t, service, tfresource.TestStep{Config: kubeAPIClusterConfig(hcl, "v1.33", ""), ExpectError: errKubeAPIGate})
				service.mu.Lock()
				defer service.mu.Unlock()
				require.Equal(t, 1, service.createCalls, "the provider must not retry after dropping the policy")
				require.Empty(t, service.creates)
			})
		}
	})

	t.Run("update", func(t *testing.T) {
		service := startKubeAPIAccessServer(t)
		policy := kubeAPIClusterConfig(kubeAPIPolicy, "v1.33", "")
		want := cks.KubeAPIAccessProto([]string{"203.0.113.0/24"}, nil)
		var steps []tfresource.TestStep
		steps = append(steps, tfresource.TestStep{Config: policy, ConfigStateChecks: accessStateChecks(want, nil)})
		for i, rejected := range []string{kubeAPIOtherPolicy, `{ public = { deny_cidrs = ["203.0.113.10/32"] } }`, "{}", ""} {
			step := tfresource.TestStep{Config: kubeAPIClusterConfig(rejected, "v1.33", ""), ExpectError: errKubeAPIGate}
			if i == 0 {
				step.PreConfig = service.setGateDisabled(true)
			}
			// Prior state is preserved: the original configuration still plans clean.
			steps = append(steps, step, tfresource.TestStep{Config: policy, PlanOnly: true})
		}
		steps = append(steps,
			// Reads and unrelated updates do not become access writes.
			tfresource.TestStep{Config: policy, ConfigStateChecks: accessStateChecks(want, nil)},
			tfresource.TestStep{Config: kubeAPIClusterConfig(kubeAPIPolicy, "v1.34", ""), ConfigStateChecks: accessStateChecks(want, nil)},
			tfresource.TestStep{Config: kubeAPIClusterConfig(kubeAPIPolicy, "v1.34", `audit_policy = "dGVzdA=="`), ConfigStateChecks: accessStateChecks(want, nil)},
			tfresource.TestStep{
				Config: kubeAPIClusterConfig(kubeAPIOtherPolicy, "v1.34", `audit_policy = "dGVzdA=="`), PreConfig: service.setGateDisabled(false),
				ConfigStateChecks: accessStateChecks(cks.KubeAPIAccessProto([]string{"192.0.2.0/24"}, nil), nil),
			},
		)
		runKubeAPIAccessTest(t, service, steps...)
		service.mu.Lock()
		defer service.mu.Unlock()
		require.Equal(t, 7, service.updateCalls, "each rejected write is attempted exactly once")
		require.Len(t, service.updates, 3)
		require.Equal(t, []string{"version"}, service.updates[0].UpdateMask.Paths)
		require.Nil(t, service.updates[0].KubeApiAccess)
		require.Equal(t, []string{"audit_policy"}, service.updates[1].UpdateMask.Paths)
		require.Nil(t, service.updates[1].KubeApiAccess)
		requireAccessWrite(t, service.updates[2], cks.KubeAPIAccessProto([]string{"192.0.2.0/24"}, nil))
	})

	t.Run("set from null", func(t *testing.T) {
		service := startKubeAPIAccessServer(t)
		service.setGateDisabled(true)()
		absent := kubeAPIClusterConfig("", "v1.33", "")
		runKubeAPIAccessTest(t, service,
			tfresource.TestStep{Config: absent, ConfigStateChecks: accessStateChecks(nil, nil)},
			tfresource.TestStep{Config: kubeAPIClusterConfig("{ public = {} }", "v1.33", ""), ExpectError: errKubeAPIGate},
			tfresource.TestStep{Config: absent, PlanOnly: true},
			tfresource.TestStep{Config: kubeAPIClusterConfig("{}", "v1.33", ""), ExpectError: errKubeAPIGate},
			tfresource.TestStep{Config: absent, PlanOnly: true},
		)
		service.mu.Lock()
		defer service.mu.Unlock()
		require.Len(t, service.creates, 1)
		require.Nil(t, service.creates[0].KubeApiAccess)
		require.Equal(t, 2, service.updateCalls)
		require.Empty(t, service.updates)
	})
}

func TestKubeAPIAccessTerraformServerValidationError(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	policy := kubeAPIClusterConfig(kubeAPIPolicy, "v1.33", "")
	var stored *cksv1beta1.KubeAPIAccess
	expected := regexp.MustCompile(`(?s)Bad Request.*kube_api_access\.public\.deny_cidrs\[0\]:\s+value\s+must\s+be\s+a\s+valid\s+IP\s+prefix`)
	runKubeAPIAccessTest(t, service,
		tfresource.TestStep{Config: policy},
		tfresource.TestStep{
			Config: kubeAPIClusterConfig(`{ public = { allow_cidrs = ["203.0.113.0/24"], deny_cidrs = ["203.0.113.10/32"] } }`, "v1.33", ""),
			PreConfig: service.with(func(s *kubeAPIAccessServer) {
				s.accessError = fakeFieldViolation("kube_api_access.public.deny_cidrs[0]", "value must be a valid IP prefix")
			}),
			ExpectError: expected,
		},
		tfresource.TestStep{Config: policy, PlanOnly: true},
		tfresource.TestStep{Config: policy, PreConfig: service.with(func(s *kubeAPIAccessServer) {
			stored = cloneAccess(s.cluster.KubeApiAccess)
			s.accessError = nil
		})},
	)
	require.True(t, accessEqual(cks.KubeAPIAccessProto([]string{"203.0.113.0/24"}, nil), stored), "rejected writes must not change the API: %v", stored)
}

func TestKubeAPIAccessTerraformObservations(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	enabled := cks.KubeAPIAccessProto(nil, nil)
	config := kubeAPIClusterConfig("{ public = {} }", "v1.33", "")
	disabled := kubeAPIClusterConfig("{}", "v1.33", "")
	pending := observed(nil, cksv1beta1.PublicKubeAPIAccessStatus_STATUS_PENDING, nil)
	steps := []tfresource.TestStep{
		// The cluster is RUNNING before any access observation exists.
		{Config: config, ConfigStateChecks: accessStateChecks(enabled, nil)},
		{Config: config, PreConfig: service.setStatus(&cksv1beta1.KubeAPIAccessStatus{}), ConfigStateChecks: accessStateChecks(enabled, &cksv1beta1.KubeAPIAccessStatus{})},
		{Config: config, PreConfig: service.setStatus(pending), ConfigStateChecks: accessStateChecks(enabled, pending)},
	}
	sequence := []*cksv1beta1.KubeAPIAccessStatus{
		{CaData: testCAPEMA},
		observed(nil, cksv1beta1.PublicKubeAPIAccessStatus_STATUS_PENDING, testCAPEMA),
		observed(nil, cksv1beta1.PublicKubeAPIAccessStatus_STATUS_UNSPECIFIED, testCAPEMA),
		observed(proto.String(cks.KubeAPIAccessEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_PENDING, testCAPEMA),
		observed(proto.String(cks.KubeAPIAccessEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_ACCEPTED, testCAPEMA),
		observed(proto.String(cks.KubeAPIAccessEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_REJECTED, testCAPEMA),
		observed(proto.String(cks.KubeAPIAccessEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_ERROR, testCAPEMA),
		observed(proto.String(cks.KubeAPIAccessEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_UNKNOWN, testCAPEMA),
		observed(proto.String(cks.KubeAPIAccessEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_UNSPECIFIED, testCAPEMA),
		observed(proto.String(kubeAPIOtherEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_ACCEPTED, testCAPEMA),
		// CA rotation replaces the bundle.
		observed(proto.String(kubeAPIOtherEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_ACCEPTED, testCAPEMB),
		// Endpoint removal keeps the API-reported status and CA.
		observed(nil, cksv1beta1.PublicKubeAPIAccessStatus_STATUS_PENDING, testCAPEMB),
	}
	for _, status := range sequence {
		steps = append(steps,
			tfresource.TestStep{Config: config, PreConfig: service.setStatus(status), ConfigStateChecks: accessStateChecks(enabled, status)},
			tfresource.TestStep{Config: config, PlanOnly: true},
		)
	}
	caOnly := &cksv1beta1.KubeAPIAccessStatus{CaData: testCAPEMB}
	steps = append(steps,
		// Disabling access removes public observations while the API retains the CA.
		tfresource.TestStep{Config: disabled, PreConfig: service.with(func(s *kubeAPIAccessServer) { s.statusOnWrite = caOnly }), ConfigStateChecks: accessStateChecks(accessDisabled(), caOnly)},
		tfresource.TestStep{ResourceName: kubeAPIClusterAddress, ImportState: true, ImportStateVerify: true},
		tfresource.TestStep{Config: disabled, PlanOnly: true},
		// The provider does not cache observations the API stopped reporting.
		tfresource.TestStep{Config: disabled, PreConfig: service.setStatus(nil), ConfigStateChecks: accessStateChecks(accessDisabled(), nil)},
	)
	runKubeAPIAccessTest(t, service, steps...)
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Len(t, service.updates, 1, "observation changes must not cause writes")
	requireAccessWrite(t, service.updates[0], accessDisabled())
}

func TestKubeAPIAccessTerraformObservationsChangeDuringUpdate(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	before := observed(proto.String(cks.KubeAPIAccessEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_ACCEPTED, testCAPEMA)
	after := observed(proto.String(kubeAPIOtherEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_PENDING, testCAPEMB)
	want := cks.KubeAPIAccessProto([]string{"192.0.2.0/24"}, nil)
	runKubeAPIAccessTest(t, service,
		tfresource.TestStep{
			Config: kubeAPIClusterConfig(kubeAPIPolicy, "v1.33", ""), PreConfig: service.with(func(s *kubeAPIAccessServer) { s.statusOnWrite = before }),
			ConfigStateChecks: accessStateChecks(cks.KubeAPIAccessProto([]string{"203.0.113.0/24"}, nil), before),
		},
		tfresource.TestStep{
			Config:    kubeAPIClusterConfig(kubeAPIOtherPolicy, "v1.33", ""),
			PreConfig: service.with(func(s *kubeAPIAccessServer) { s.statusOnWrite = after }),
			// Mutable observations must not be copied from prior state into the plan.
			ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction(kubeAPIClusterAddress, plancheck.ResourceActionUpdate),
				plancheck.ExpectUnknownValue(kubeAPIClusterAddress, tfjsonpath.New("kube_api_access_status")),
			}},
			ConfigStateChecks: accessStateChecks(want, after),
		},
		tfresource.TestStep{
			Config: kubeAPIClusterConfig(kubeAPIOtherPolicy, "v1.33", ""),
			ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectEmptyPlan(),
				plancheck.ExpectKnownValue(kubeAPIClusterAddress, tfjsonpath.New("kube_api_access_status"), statusKnownValue(after)),
			}},
		},
	)
}

func TestKubeAPIAccessTerraformLegacyPublicIndependence(t *testing.T) {
	service := startKubeAPIAccessServer(t)
	policy := cks.KubeAPIAccessProto([]string{"203.0.113.0/24"}, nil)
	status := observed(proto.String(cks.KubeAPIAccessEndpoint), cksv1beta1.PublicKubeAPIAccessStatus_STATUS_ACCEPTED, testCAPEMA)
	service.setStatus(status)()
	var steps []tfresource.TestStep
	for _, tc := range []struct {
		legacy     bool
		expression string
		want       *cksv1beta1.KubeAPIAccess
	}{
		{legacy: true},
		{legacy: true, expression: kubeAPIPolicy, want: policy},
		{legacy: false, expression: kubeAPIPolicy, want: policy},
		{legacy: false, expression: "{}", want: accessDisabled()},
		{legacy: true, expression: "{}", want: accessDisabled()},
	} {
		config := strings.Replace(kubeAPIClusterConfig(tc.expression, "v1.33", ""), "public = false", fmt.Sprintf("public = %t", tc.legacy), 1)
		checks := accessStateChecks(tc.want, status)
		for _, address := range []string{kubeAPIClusterAddress, kubeAPIDataSourceAddress} {
			checks = append(checks,
				statecheck.ExpectKnownValue(address, tfjsonpath.New("public"), knownvalue.Bool(tc.legacy)),
				statecheck.ExpectKnownValue(address, tfjsonpath.New("api_server_endpoint"), knownvalue.StringExact(kubeAPILegacyEndpoint)),
			)
		}
		steps = append(steps, tfresource.TestStep{Config: config, ConfigStateChecks: checks})
	}
	runKubeAPIAccessTest(t, service, steps...)
	service.mu.Lock()
	defer service.mu.Unlock()
	require.True(t, service.creates[0].Public)
	require.Nil(t, service.creates[0].KubeApiAccess)
	require.Len(t, service.updates, 4)
	requireAccessWrite(t, service.updates[0], policy)
	requireAccessWrite(t, service.updates[2], accessDisabled())
	for _, i := range []int{1, 3} {
		require.Equal(t, []string{"public"}, service.updates[i].UpdateMask.Paths)
		require.Nil(t, service.updates[i].KubeApiAccess)
	}
	require.False(t, service.updates[1].Public)
	require.True(t, service.updates[3].Public)
}
