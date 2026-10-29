package networking_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"buf.build/gen/go/coreweave/networking/connectrpc/go/v2/coreweave/networking/v1beta1/networkingv1beta1connect"
	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const (
	prefixTypeAttribute  = "type"
	prefixValueAttribute = "value"
)

// prefixTypeServer models omitted types and API-controlled mutation policy.
type prefixTypeServer struct {
	networkingv1beta1connect.UnimplementedVPCServiceHandler
	mu                        sync.Mutex
	vpc                       *networkingv1beta1.VPC
	allowTypeChanges          bool
	creates, deletes, updates int
}

// CreateVPC returns an immediately ready VPC with the requested prefixes.
func (s *prefixTypeServer) CreateVPC(_ context.Context, req *networkingv1beta1.CreateVPCRequest) (*networkingv1beta1.CreateVPCResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creates++
	s.vpc = &networkingv1beta1.VPC{
		Id: "00000000-0000-4000-8000-000000000001", Name: req.Name, Zone: req.Zone,
		HostPrefix: "10.128.0.0/16", HostPrefixes: req.HostPrefixes, VpcPrefixes: req.VpcPrefixes,
		Ingress: req.Ingress, Egress: req.Egress, Status: networkingv1beta1.VPC_STATUS_READY,
	}
	return &networkingv1beta1.CreateVPCResponse{Vpc: proto.Clone(s.vpc).(*networkingv1beta1.VPC)}, nil
}

// GetVPC returns current state or NotFound after deletion.
func (s *prefixTypeServer) GetVPC(context.Context, *networkingv1beta1.GetVPCRequest) (*networkingv1beta1.GetVPCResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vpc == nil {
		return nil, connect.NewError(connect.CodeNotFound, "VPC not found")
	}
	return &networkingv1beta1.GetVPCResponse{Vpc: proto.Clone(s.vpc).(*networkingv1beta1.VPC)}, nil
}

// UpdateVPC preserves omitted types and optionally rejects type changes.
func (s *prefixTypeServer) UpdateVPC(_ context.Context, req *networkingv1beta1.UpdateVPCRequest) (*networkingv1beta1.UpdateVPCResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates++
	for _, prefix := range req.VpcPrefixes {
		for _, old := range s.vpc.VpcPrefixes {
			if prefix.Name != old.Name {
				continue
			}
			if prefix.Type == nil {
				prefix.Type = old.Type
			} else if prefix.GetType() != old.GetType() && !s.allowTypeChanges {
				return nil, connect.NewError(connect.CodeInvalidArgument, "prefix type is immutable")
			}
		}
	}
	s.vpc.VpcPrefixes = req.VpcPrefixes
	s.vpc.Ingress, s.vpc.Egress = req.Ingress, req.Egress
	return &networkingv1beta1.UpdateVPCResponse{Vpc: proto.Clone(s.vpc).(*networkingv1beta1.VPC)}, nil
}

// DeleteVPC removes the VPC so the provider's deletion poll completes.
func (s *prefixTypeServer) DeleteVPC(context.Context, *networkingv1beta1.DeleteVPCRequest) (*networkingv1beta1.DeleteVPCResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	old := s.vpc
	s.vpc = nil
	return &networkingv1beta1.DeleteVPCResponse{Vpc: old}, nil
}

// prefixTypeConfig creates configurations with typed and untyped prefixes together.
func prefixTypeConfig(prefixType, extra string) string {
	return fmt.Sprintf(`
provider "coreweave" {}
resource "coreweave_networking_vpc" "test" {
 name = "prefix-types"
 zone = "US-LAB-01A"
 vpc_prefixes = [
 { name = "legacy", value = "10.0.0.0/24" },
 { name = "private", value = "10.1.0.0/24", type = %q },
 %s
 ]
}
data "coreweave_networking_vpc" "test" {
 id = coreweave_networking_vpc.test.id
}
`, prefixType, extra)
}

func TestVpcPrefixTypeTerraformLifecycle(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("allow_changes_%t", allowed), func(t *testing.T) {
			for _, deferred := range []bool{false, true} {
				t.Run(fmt.Sprintf("deferred_%t", deferred), func(t *testing.T) { runPrefixTypeLifecycle(t, allowed, deferred) })
			}
		})
	}
}

func runPrefixTypeLifecycle(t *testing.T, allowed, deferred bool) {
	service := &prefixTypeServer{allowTypeChanges: allowed}
	mux := http.NewServeMux()
	server := connect.NewServer()
	networkingv1beta1connect.RegisterVPCServiceHandler(server, service)
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, httpServer.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "test-token")
	const address = "coreweave_networking_vpc.test"
	initial := prefixTypeConfig("PRIVATE_ACCESS", "")
	added := prefixTypeConfig("PRIVATE_ACCESS", `{ name = "pods", value = "10.2.0.0/24", type = "POD" },`)
	changed := prefixTypeConfig("SERVICE", `{ name = "pods", value = "10.2.0.0/24", type = "POD" },`)
	if deferred {
		changed = strings.Replace(changed, `type = "SERVICE"`, "type = terraform_data.prefix_type.output", 1) + "\nresource \"terraform_data\" \"prefix_type\" { input = \"SERVICE\" }\n"
	}
	changeStep := tfresource.TestStep{Config: changed, ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}}
	if allowed {
		changeStep.ConfigStateChecks = []statecheck.StateCheck{statecheck.ExpectKnownValue(address, tfjsonpath.New("vpc_prefixes"), knownvalue.SetPartial([]knownvalue.Check{
			knownvalue.ObjectPartial(map[string]knownvalue.Check{prefixValueAttribute: knownvalue.StringExact("10.1.0.0/24"), prefixTypeAttribute: knownvalue.StringExact("SERVICE")}),
		}))}
	}
	if !allowed {
		changeStep.ExpectError = regexp.MustCompile("prefix type is immutable")
	}
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{Config: initial, ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(address, tfjsonpath.New("vpc_prefixes"), knownvalue.SetPartial([]knownvalue.Check{
					knownvalue.ObjectPartial(map[string]knownvalue.Check{prefixValueAttribute: knownvalue.StringExact("10.1.0.0/24"), prefixTypeAttribute: knownvalue.StringExact("PRIVATE_ACCESS")}),
					knownvalue.ObjectPartial(map[string]knownvalue.Check{prefixValueAttribute: knownvalue.StringExact("10.0.0.0/24"), prefixTypeAttribute: knownvalue.Null()}),
				})),
				statecheck.ExpectKnownValue("data."+address, tfjsonpath.New("vpc_prefixes"), knownvalue.ListSizeExact(2)),
			}},
			{Config: initial, PlanOnly: true},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{Config: strings.Replace(added, `, type = "PRIVATE_ACCESS"`, "", 1), ConfigPlanChecks: tfresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}},
			{Config: added, PlanOnly: true},
			// Removing the type from configuration preserves the observed type.
			{Config: strings.Replace(added, `, type = "PRIVATE_ACCESS"`, "", 1), PlanOnly: true},
			changeStep,
		},
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	require.Nil(t, service.vpc)
	require.Equal(t, 1, service.creates)
	require.Equal(t, 1, service.deletes)
	require.GreaterOrEqual(t, service.updates, 2)
}
