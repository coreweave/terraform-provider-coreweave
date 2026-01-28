package networking_test

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework-nettypes/cidrtypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
)

const (
	AcceptanceTestPrefix = "test-acc-vpc-"

	defaultPrimaryHostPrefixName = "host primary"
)

func init() {
	resource.AddTestSweepers("coreweave_vpc", &resource.Sweeper{
		Name:         "coreweave_networking_vpc",
		Dependencies: []string{},
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.ListVPCs(ctx, &connect.Request[networkingv1beta1.ListVPCsRequest]{})
			if err != nil {
				return fmt.Errorf("failed to list VPCs: %w", err)
			}
			for _, vpc := range listResp.Msg.Items {
				if !strings.HasPrefix(vpc.Name, AcceptanceTestPrefix) {
					log.Printf("skipping VPC %s because it does not have prefix %s", vpc.Name, AcceptanceTestPrefix)
					continue
				}

				if vpc.GetZone() != r {
					log.Printf("skipping VPC %s in zone %s because it does not match sweep zone %s", vpc.Name, vpc.Zone, r)
					continue
				}

				log.Printf("sweeping VPC %s", vpc.Name)
				if testutil.SweepDryRun() {
					log.Printf("skipping VPC %s because of dry-run mode", vpc.Name)
					continue
				}

				deleteReq := connect.NewRequest(&networkingv1beta1.DeleteVPCRequest{
					Id: vpc.Id,
				})

				deleteResp, err := client.DeleteVPC(ctx, deleteReq)
				if connect.CodeOf(err) == connect.CodeNotFound {
					log.Printf("VPC %s already deleted", vpc.Name)
					continue
				} else if err != nil {
					return fmt.Errorf("failed to delete VPC %s: %w", vpc.Name, err)
				}
				deletedVpc := deleteResp.Msg.Vpc

				if err := testutil.WaitForDelete(ctx, 5*time.Minute, 15*time.Second, client.GetVPC, &networkingv1beta1.GetVPCRequest{
					Id: deletedVpc.Id,
				}); err != nil {
					return fmt.Errorf("failed to wait for VPC %s to be deleted: %w", deletedVpc.Name, err)
				}
			}

			return nil
		},
	})
}

func TestVpcSchema(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	schemaRequest := fwresource.SchemaRequest{}
	schemaResponse := &fwresource.SchemaResponse{}

	networking.NewVpcResource().Schema(ctx, schemaRequest, schemaResponse)

	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema method diagnostics: %+v", schemaResponse.Diagnostics)
	}

	// Validate the schema
	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)

	if diagnostics.HasError() {
		t.Fatalf("Schema validation diagnostics: %+v", diagnostics)
	}
}

func hostPrefixesToSet(t *testing.T, hp []networking.HostPrefixResourceModel) types.Set {
	t.Helper()
	setVal, diags := types.SetValueFrom(t.Context(), networking.HostPrefixObjectType, hp)
	if diags.HasError() {
		t.Fatalf("failed to create host prefix set: %+v", diags)
	}
	return setVal
}

func hostPrefixObjectExact(t *testing.T, hp networking.HostPrefixResourceModel) knownvalue.Check {
	t.Helper()

	prefixes := make([]knownvalue.Check, len(hp.Prefixes))
	for i, prefix := range hp.Prefixes {
		prefixes[i] = knownvalue.StringExact(prefix.ValueString())
	}

	obj := map[string]knownvalue.Check{
		"name":     knownvalue.StringExact(hp.Name.ValueString()),
		"type":     knownvalue.StringExact(hp.Type.ValueString()),
		"prefixes": knownvalue.SetExact(prefixes),
		"ipam":     knownvalue.Null(),
	}
	if hp.IPAM != nil {
		policy := networkingv1beta1.IPAddressManagementPolicy_UNSPECIFIED.String()
		if !hp.IPAM.GatewayAddressPolicy.IsNull() && !hp.IPAM.GatewayAddressPolicy.IsUnknown() {
			policy = hp.IPAM.GatewayAddressPolicy.ValueString()
		}
		obj["ipam"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"prefix_length":          knownvalue.Int32Exact(hp.IPAM.PrefixLength.ValueInt32()),
			"gateway_address_policy": knownvalue.StringExact(policy),
		})
	}
	return knownvalue.ObjectExact(obj)
}

// defaultExpectedValues constructs the expected values for the VPC resource based on the given model. This validates behavior of the given properties, where possible.
// It should behave as an equivalent of "make sure the resource matches this object for all configured fields".
// Derived fields are calculated where feasible.
func defaultExpectedValues(t *testing.T, resourceAddress string, m *networking.VpcResourceModel) []statecheck.StateCheck {
	t.Helper()

	stateChecks := make([]statecheck.StateCheck, 0)

	// These attributes should always be present..
	stateChecks = append(stateChecks,
		statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("id"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("zone"), knownvalue.StringExact(m.Zone.ValueString())),
		statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("name"), knownvalue.StringExact(m.Name.ValueString())),
	)

	// NOTE: this uses a common pattern that validates attributes based on the definition of the correct behavior.
	// Code here should be written very carefully, as it will exercise all of the tests. This should be a STRICT definition of the behavior.
	// Not all checks need to be here, but checks where we can make an authoritative determination about the end state may be present here.
	// When values are optional, we often want to validate behavior: either default behavior, or else derived behavior.
	// When values are supplied, they should generally be validated against the supplied value.

	// If you do not specify any host prefix(es), something will be assigned for you in the output.
	if m.HostPrefix.IsNull() || m.HostPrefix.IsUnknown() {
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("host_prefix"), knownvalue.NotNull()))
	} else {
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("host_prefix"), knownvalue.StringExact(m.HostPrefix.ValueString())))

		// Validate that the host_prefixes attribute contains an element equivalent to the host prefix.
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("host_prefixes"), knownvalue.SetPartial([]knownvalue.Check{
			hostPrefixObjectExact(t, networking.HostPrefixResourceModel{
				Name: types.StringValue(defaultPrimaryHostPrefixName),
				Type: types.StringValue(networkingv1beta1.HostPrefix_PRIMARY.String()),
				Prefixes: []cidrtypes.IPPrefix{cidrtypes.NewIPPrefixValue(m.HostPrefix.ValueString())},
			}),
		})))
	}

	if m.HostPrefixes.IsNull() || m.HostPrefixes.IsUnknown() {
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("host_prefixes"), knownvalue.NotNull()))
	} else {
		hpModels := make([]networking.HostPrefixResourceModel, 0)
		if diags := m.HostPrefixes.ElementsAs(t.Context(), &hpModels, false); diags.HasError() {
			t.Fatalf("failed to get host prefixes: %+v", diags)
		}

		setChecks := make([]knownvalue.Check, len(hpModels))
		for i, hpModel := range hpModels {
			setChecks[i] = hostPrefixObjectExact(t, hpModel)
		}
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("host_prefixes"), knownvalue.SetExact(setChecks)))
	}

	// We can assert strict identity against the vpc_prefixes attribute.
	if len(m.VpcPrefixes) == 0 {
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("vpc_prefixes"), knownvalue.Null()))
	} else {
		setChecks := make([]knownvalue.Check, len(m.VpcPrefixes))
		for i, vp := range m.VpcPrefixes {
			setChecks[i] = knownvalue.ObjectExact(map[string]knownvalue.Check{
				"name":  knownvalue.StringExact(vp.Name.ValueString()),
				"value": knownvalue.StringExact(vp.Value.ValueString()),
			})
		}
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("vpc_prefixes"), knownvalue.SetExact(setChecks)))
	}

	// Full ingress objects are validated for backwards compatibility.
	expectedIngressObj := make(map[string]knownvalue.Check)
	if m.Ingress == nil {
		expectedIngressObj["disable_public_services"] = knownvalue.Bool(false)
	} else {
		expectedIngressObj["disable_public_services"] = knownvalue.Bool(m.Ingress.DisablePublicServices.ValueBool())
	}
	stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("ingress"), knownvalue.ObjectExact(expectedIngressObj)))

	// Full egress objects are validated for backwards compatibility.
	expectedEgressObj := make(map[string]knownvalue.Check)
	if m.Egress == nil {
		expectedEgressObj["disable_public_access"] = knownvalue.Bool(false)
	} else {
		expectedEgressObj["disable_public_access"] = knownvalue.Bool(m.Egress.DisablePublicAccess.ValueBool())
	}
	stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("egress"), knownvalue.ObjectExact(expectedEgressObj)))

	// Full DHCP objects are validated for backwards compatibility, but we may want to change this later if the complexity of the object increases.
	if m.Dhcp == nil || m.Dhcp.IsEmpty() {
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("dhcp"), knownvalue.Null()))
	} else {
		dhcpObj := make(map[string]knownvalue.Check)
		if m.Dhcp.Dns == nil {
			dhcpObj["dns"] = knownvalue.Null()
		} else {
			servers := make([]string, 0)
			if diags := m.Dhcp.Dns.Servers.ElementsAs(t.Context(), &servers, false); diags.HasError() {
				t.Fatalf("failed to get DHCP servers from model: %+v", diags)
			}
			serverChecks := make([]knownvalue.Check, len(servers))
			for i, server := range servers {
				serverChecks[i] = knownvalue.StringExact(server)
			}
			dhcpObj["dns"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
				"servers": knownvalue.SetExact(serverChecks),
			})
		}
		stateChecks = append(stateChecks, statecheck.ExpectKnownValue(resourceAddress, tfjsonpath.New("dhcp"), knownvalue.ObjectExact(dhcpObj)))
	}

	return stateChecks
}

func TestVpcResource(t *testing.T) {
	t.Parallel()

	t.Run("lifecycle", func(t *testing.T) {
		randomInt := rand.IntN(100)
		vpcName := fmt.Sprintf("%s%x", AcceptanceTestPrefix, randomInt)
		resourceName := fmt.Sprintf("test_vpc_%x", randomInt)
		fullResourceName := fmt.Sprintf("coreweave_networking_vpc.%s", resourceName)
		fullDataSourceName := fmt.Sprintf("data.coreweave_networking_vpc.%s", resourceName)
		zone := testutil.AcceptanceTestZone

		initial := &networking.VpcResourceModel{
			Name: types.StringValue(vpcName),
			Zone: types.StringValue(zone),
			HostPrefixes: hostPrefixesToSet(t, []networking.HostPrefixResourceModel{

				{
					Name: types.StringValue("primary-prefix"),
					Type: types.StringValue(networkingv1beta1.HostPrefix_PRIMARY.String()),
					Prefixes: []cidrtypes.IPPrefix{
						cidrtypes.NewIPPrefixValue("172.16.0.0/12"),
						cidrtypes.NewIPPrefixValue("2601:db8:aaaa::/48"),
					},
				},
				{
					Name: types.StringValue("container-network"),
					Type: types.StringValue(networkingv1beta1.HostPrefix_ROUTED.String()),
					Prefixes: []cidrtypes.IPPrefix{
						cidrtypes.NewIPPrefixValue("2601:db8:bbbb::/48"),
					},
					IPAM: &networking.IPAMPolicyResourceModel{
						PrefixLength:         types.Int32Value(80),
					},
				},
				{
					Name: types.StringValue("container-network-attached"),
					Type: types.StringValue(networkingv1beta1.HostPrefix_ATTACHED.String()),
					Prefixes: []cidrtypes.IPPrefix{
						cidrtypes.NewIPPrefixValue("2601:db8:cccc::/48"),
					},
					IPAM: &networking.IPAMPolicyResourceModel{
						PrefixLength:         types.Int32Value(64),
						GatewayAddressPolicy: types.StringNull(), // Explicitly null to trigger default
					},
				},
			}),
			VpcPrefixes: []networking.VpcPrefixResourceModel{
				{
					Name:  types.StringValue("pod cidr"),
					Value: types.StringValue("10.0.0.0/13"),
				},
				{
					Name:  types.StringValue("service cidr"),
					Value: types.StringValue("10.16.0.0/22"),
				},
				{
					Name:  types.StringValue("internal lb cidr"),
					Value: types.StringValue("10.32.4.0/22"),
				},
			},
			Dhcp: &networking.VpcDhcpResourceModel{
				Dns: &networking.VpcDhcpDnsResourceModel{
					Servers: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("1.1.1.1")}),
				},
			},
		}

		dataSource := &networking.VpcDataSourceModel{
			Id: types.StringValue(fmt.Sprintf("%s.id", fullResourceName)),
		}

		update := &networking.VpcResourceModel{
			Name: types.StringValue(vpcName),
			Zone: types.StringValue(zone),
			Ingress: &networking.VpcIngressResourceModel{
				DisablePublicServices: types.BoolValue(true),
			},
			Egress: &networking.VpcEgressResourceModel{
				DisablePublicAccess: types.BoolValue(true),
			},
			// Migrate from the legacy host_prefix in the update.
			HostPrefixes: initial.HostPrefixes,
			VpcPrefixes: []networking.VpcPrefixResourceModel{
				{
					Name:  types.StringValue("pod cidr"),
					Value: types.StringValue("10.0.0.0/13"),
				},
				{
					Name:  types.StringValue("service cidr"),
					Value: types.StringValue("10.16.0.0/22"),
				},
				{
					Name:  types.StringValue("internal lb cidr"),
					Value: types.StringValue("10.32.4.0/22"),
				},
				{
					Name:  types.StringValue("internal lb cidr 2"),
					Value: types.StringValue("10.45.4.0/22"),
				},
			},
		}

		ctx := t.Context()

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
			Steps: []resource.TestStep{
				{
					PreConfig: func() {
						t.Log("Beginning data source not found test")
					},
					Config: strings.Join([]string{
						fmt.Sprintf(`data "%s" "%s" { id = "%s" }`, "coreweave_networking_vpc", resourceName, "1b5274f2-8012-4b68-9010-cc4c51613302"),
					}, "\n"),
					ExpectError: regexp.MustCompile(`(?i)VPC .*not found`),
				},
				{
					PreConfig: func() {
						t.Log("Beginning coreweave_networking_vpc create test")
					},
					Config: networking.MustRenderVpcResource(ctx, resourceName, initial),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(fullResourceName, "name", initial.Name.ValueString()),
						resource.TestCheckResourceAttr(fullResourceName, "zone", initial.Zone.ValueString()),
					),
					ConfigStateChecks: defaultExpectedValues(t, fullResourceName, initial),
				},
				{
					PreConfig: func() {
						t.Log("Beginning coreweave_networking_vpc data source test")
					},
					Config: strings.Join([]string{
						networking.MustRenderVpcResource(ctx, resourceName, initial),
						networking.MustRenderVpcDataSource(ctx, resourceName, dataSource),
					}, "\n"),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttrPair(fullDataSourceName, "id", fullResourceName, "id"),
					),
					ConfigStateChecks: defaultExpectedValues(t, fullDataSourceName, initial),
				},
				{
					PreConfig: func() {
						t.Log("Beginning coreweave_networking_vpc update test")
					},
					Config: networking.MustRenderVpcResource(ctx, resourceName, update),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(fullResourceName, "name", update.Name.ValueString()),
						resource.TestCheckResourceAttr(fullResourceName, "zone", update.Zone.ValueString()),
					),
					ConfigStateChecks: defaultExpectedValues(t, fullResourceName, update),
				},
				{
					PreConfig: func() {
						t.Log("Beginning coreweave_networking_vpc import test")
					},
					ResourceName:      fullResourceName,
					ImportState:       true,
					ImportStateVerify: true,
				},
			},
		})
	})

	t.Run("host prefix migration", func(t *testing.T) {
		randomInt := rand.IntN(100)
		vpcName := fmt.Sprintf("%shprefix-mig-%x", AcceptanceTestPrefix, randomInt)
		resourceName := fmt.Sprintf("test_hostprefix_migrate_%x", randomInt)
		fullResourceName := fmt.Sprintf("coreweave_networking_vpc.%s", resourceName)
		zone := testutil.AcceptanceTestZone
		ctx := t.Context()

		cidrInitial := "10.16.192.0/18"
		cidrUpdate := "10.16.64.0/18"

		initial := &networking.VpcResourceModel{
			Name:       types.StringValue(vpcName),
			Zone:       types.StringValue(zone),
			HostPrefix: types.StringValue(cidrInitial),
		}

		equivalentToInitial := &networking.VpcResourceModel{
			Name:       types.StringValue(vpcName),
			Zone:       types.StringValue(zone),
			HostPrefix: types.StringNull(),
			HostPrefixes: hostPrefixesToSet(t, []networking.HostPrefixResourceModel{
				{
					Name:     types.StringValue(defaultPrimaryHostPrefixName),
					Type:     types.StringValue(networkingv1beta1.HostPrefix_PRIMARY.String()),
					Prefixes: []cidrtypes.IPPrefix{cidrtypes.NewIPPrefixValue(cidrInitial)},
				},
			}),
		}

		update := &networking.VpcResourceModel{
			Name: types.StringValue(vpcName),
			Zone: types.StringValue(zone),
			HostPrefixes: hostPrefixesToSet(t, []networking.HostPrefixResourceModel{
				{
					Name:     types.StringValue(defaultPrimaryHostPrefixName),
					Type:     types.StringValue(networkingv1beta1.HostPrefix_PRIMARY.String()),
					Prefixes: []cidrtypes.IPPrefix{cidrtypes.NewIPPrefixValue(cidrUpdate)},
				},
			}),
		}

		updateReversion := &networking.VpcResourceModel{
			Name:       types.StringValue(vpcName),
			Zone:       types.StringValue(zone),
			HostPrefix: types.StringValue(cidrUpdate),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
			Steps: []resource.TestStep{
				{
					PreConfig: func() {
						t.Log("Beginning coreweave_networking_vpc create test for host prefix migration")
					},
					Config: networking.MustRenderVpcResource(ctx, resourceName, initial),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(fullResourceName, "host_prefix", initial.HostPrefix.ValueString()),
					),
					ConfigStateChecks: slices.Concat(defaultExpectedValues(t, fullResourceName, initial), []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefix"), knownvalue.StringExact("10.16.192.0/18")),
						// Make sure that the host prefix is copied to the host_prefixes attribute using expected values.
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefixes"), knownvalue.SetPartial([]knownvalue.Check{
							knownvalue.ObjectExact(map[string]knownvalue.Check{
								"ipam": knownvalue.Null(),
								"name": knownvalue.NotNull(),
								"type": knownvalue.StringExact(networkingv1beta1.HostPrefix_PRIMARY.String()),
								"prefixes": knownvalue.SetExact([]knownvalue.Check{
									knownvalue.StringExact(initial.HostPrefix.ValueString()),
								}),
							}),
						})),
					}),
				},
				{
					PreConfig: func() {
						t.Log("Beginning coreweave_networking_vpc equivalent host_prefixes test, should be a noop")
					},
					Config: networking.MustRenderVpcResource(ctx, resourceName, equivalentToInitial),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
					ConfigStateChecks: defaultExpectedValues(t, fullResourceName, equivalentToInitial),
				},
				{
					PreConfig: func() {
						t.Log("Beginning coreweave_networking_vpc host_prefixes update requires replace test")
					},
					Config: networking.MustRenderVpcResource(ctx, resourceName, update),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PostApplyPreRefresh: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionDestroyBeforeCreate),
						},
					},
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
				},
				{
					PreConfig: func() {
						t.Log("Beginning coreweave_networking_vpc host_prefix reversion requires replace test")
					},
					Config: networking.MustRenderVpcResource(ctx, resourceName, updateReversion),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PostApplyPreRefresh: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionDestroyBeforeCreate),
						},
					},
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
				},
			},
		})
	})
}

func TestHostPrefixReplace(t *testing.T) {
	randomInt := rand.IntN(100)
	vpcName := fmt.Sprintf("%shprefix-repl-%x", AcceptanceTestPrefix, randomInt)
	resourceName := fmt.Sprintf("test_hostprefix_replace_%x", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_networking_vpc.%s", resourceName)
	zone := testutil.AcceptanceTestZone

	initial := &networking.VpcResourceModel{
		Name:       types.StringValue(vpcName),
		Zone:       types.StringValue(zone),
		HostPrefix: types.StringNull(),
	}

	replace := &networking.VpcResourceModel{
		Name:       types.StringValue(vpcName),
		Zone:       types.StringValue(zone),
		HostPrefix: types.StringValue("172.0.0.0/18"),
	}

	ctx := t.Context()

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					t.Log("Setting up coreweave_networking_vpc test for host prefix requires replace")
				},
				Config: networking.MustRenderVpcResource(ctx, resourceName, initial),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fullResourceName, "name", initial.Name.ValueString()),
					resource.TestCheckResourceAttr(fullResourceName, "zone", initial.Zone.ValueString()),
				),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefix"), knownvalue.NotNull()),
				},
			},
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_networking_vpc test for host prefix requires replace")
				},
				Config: networking.MustRenderVpcResource(ctx, resourceName, replace),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fullResourceName, "name", replace.Name.ValueString()),
					resource.TestCheckResourceAttr(fullResourceName, "zone", replace.Zone.ValueString()),
				),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefix"), knownvalue.StringExact(replace.HostPrefix.ValueString())),
				},
			},
		},
	})
}

func TestHostPrefixDefault(t *testing.T) {
	randomInt := rand.IntN(99)
	vpcName := fmt.Sprintf("%shprefix-%x", AcceptanceTestPrefix, randomInt)
	resourceName := fmt.Sprintf("test_hostprefix_default_%x", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_networking_vpc.%s", resourceName)
	zone := testutil.AcceptanceTestZone

	vpc := &networking.VpcResourceModel{
		Name: types.StringValue(vpcName),
		Zone: types.StringValue(zone),
	}

	ctx := t.Context()

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_networking_vpc create test for default host prefix")
				},
				Config: networking.MustRenderVpcResource(ctx, resourceName, vpc),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fullResourceName, "name", vpc.Name.ValueString()),
					resource.TestCheckResourceAttr(fullResourceName, "zone", vpc.Zone.ValueString()),
				),
				ConfigStateChecks: slices.Concat(defaultExpectedValues(t, fullResourceName, vpc), []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefix"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefixes"), knownvalue.SetPartial([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name": knownvalue.StringExact(defaultPrimaryHostPrefixName),
							"type": knownvalue.StringExact(networkingv1beta1.HostPrefix_PRIMARY.String()),
							"prefixes": knownvalue.SetPartial([]knownvalue.Check{
								knownvalue.NotNull(),
							}),
							"ipam": knownvalue.Null(),
						}),
					})),
				}),
			},
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_networking_vpc import test for default host prefix")
				},
				ResourceName:      fullResourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestMustRenderVpcResource(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	resourceName := "test_vpc"

	t.Run("legacy host prefix", func(t *testing.T) {
		t.Parallel()
		m := &networking.VpcResourceModel{
			Name:       types.StringValue("my-vpc"),
			Zone:       types.StringValue("US-WEST-04A"),
			HostPrefix: types.StringValue("10.16.192.0/18"),
			VpcPrefixes: []networking.VpcPrefixResourceModel{
				{
					Name:  types.StringValue("pod-cidr"),
					Value: types.StringValue("10.0.0.0/13"),
				},
				{
					Name:  types.StringValue("service-cidr"),
					Value: types.StringValue("10.16.0.0/22"),
				},
				{
					Name:  types.StringValue("internal-lb-cidr"),
					Value: types.StringValue("10.16.4.0/22"),
				},
			},
		}

		expected := `
resource "coreweave_networking_vpc" "test_vpc" {
  name        = "my-vpc"
  zone        = "US-WEST-04A"
  host_prefix = "10.16.192.0/18"
  vpc_prefixes = [{
    name  = "pod-cidr"
    value = "10.0.0.0/13"
    }, {
    name  = "service-cidr"
    value = "10.16.0.0/22"
    }, {
    name  = "internal-lb-cidr"
    value = "10.16.4.0/22"
  }]
}
`
		expected = strings.TrimLeft(expected, "\n")
		assert.Equal(t, expected, networking.MustRenderVpcResource(ctx, resourceName, m))
	})

	t.Run("host prefixes", func(t *testing.T) {
		t.Parallel()
		hp, diags := types.SetValueFrom(ctx, networking.HostPrefixObjectType, []networking.HostPrefixResourceModel{
			{
				Name:     types.StringValue("primary-prefix"),
				Type:     types.StringValue(networkingv1beta1.HostPrefix_PRIMARY.String()),
				Prefixes: []cidrtypes.IPPrefix{cidrtypes.NewIPPrefixValue("10.0.0.0/13")},
			},
			{
				Name:     types.StringValue("attached-prefix"),
				Type:     types.StringValue(networkingv1beta1.HostPrefix_ATTACHED.String()),
				Prefixes: []cidrtypes.IPPrefix{cidrtypes.NewIPPrefixValue("2001:db8::/48")},
				IPAM: &networking.IPAMPolicyResourceModel{
					PrefixLength:         types.Int32Value(64),
					GatewayAddressPolicy: types.StringValue(networkingv1beta1.IPAddressManagementPolicy_FIRST_IP.String()),
				},
			},
			{
				Name:     types.StringValue("routed-prefix"),
				Type:     types.StringValue(networkingv1beta1.HostPrefix_ROUTED.String()),
				Prefixes: []cidrtypes.IPPrefix{cidrtypes.NewIPPrefixValue("2000::/48")},
				IPAM: &networking.IPAMPolicyResourceModel{
					PrefixLength:         types.Int32Value(64),
				},
			},
		})
		if diags.HasError() {
			t.Fatalf("failed to create host prefix type: %+v", diags)
		}

		m := &networking.VpcResourceModel{
			Name:         types.StringValue("my-vpc"),
			Zone:         types.StringValue("US-WEST-04A"),
			HostPrefixes: hp,
		}

		// note: order of hosts is deterministic, but may change when the values change due to being a set.
		expected := `
resource "coreweave_networking_vpc" "test_vpc" {
  name = "my-vpc"
  zone = "US-WEST-04A"
  host_prefixes = [{
    name     = "primary-prefix"
    prefixes = ["10.0.0.0/13"]
    type     = "PRIMARY"
    }, {
    ipam = {
      gateway_address_policy = "FIRST_IP"
      prefix_length          = 64
    }
    name     = "attached-prefix"
    prefixes = ["2001:db8::/48"]
    type     = "ATTACHED"
    }, {
    ipam = {
      prefix_length = 64
    }
    name     = "routed-prefix"
    prefixes = ["2000::/48"]
    type     = "ROUTED"
  }]
}
`
		expected = strings.TrimLeft(expected, "\n")
		assert.Equal(t, expected, networking.MustRenderVpcResource(ctx, resourceName, m))
	})

	t.Run("dhcp", func(t *testing.T) {
		t.Parallel()
		m := &networking.VpcResourceModel{
			Name: types.StringValue("my-vpc"),
			Zone: types.StringValue("US-WEST-04A"),
			Dhcp: &networking.VpcDhcpResourceModel{
				Dns: &networking.VpcDhcpDnsResourceModel{
					Servers: types.SetValueMust(types.StringType, []attr.Value{
						types.StringValue("1.1.1.1"),
						types.StringValue("8.8.8.8"),
					}),
				},
			},
		}

		expected := `
resource "coreweave_networking_vpc" "test_vpc" {
  name = "my-vpc"
  zone = "US-WEST-04A"
  dhcp = {
    dns = {
      servers = ["1.1.1.1", "8.8.8.8"]
    }
  }
}
`
		expected = strings.TrimLeft(expected, "\n")
		assert.Equal(t, expected, networking.MustRenderVpcResource(ctx, resourceName, m))
	})
}
