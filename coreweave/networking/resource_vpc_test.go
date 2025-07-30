package networking_test

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
)

const (
	AcceptanceTestPrefix = "test-acc-"
)

func init() {
	resource.AddTestSweepers("coreweave_vpc", &resource.Sweeper{
		Name:         "coreweave_networking_vpc",
		Dependencies: []string{},
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{})
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
				if err != nil {
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

	ctx := context.Background()
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

func TestVpcResource(t *testing.T) {
	randomInt := rand.IntN(100)
	vpcName := fmt.Sprintf("test-acc-vpc-%x", randomInt)
	resourceName := fmt.Sprintf("test_vpc_%x", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_networking_vpc.%s", resourceName)
	fullDataSourceName := fmt.Sprintf("data.coreweave_networking_vpc.%s", resourceName)
	zone := testutil.AcceptanceTestZone

	initial := &networking.VpcResourceModel{
		Name:       types.StringValue(vpcName),
		Zone:       types.StringValue(zone),
		HostPrefix: types.StringValue("10.16.192.0/18"),
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
		HostPrefix: types.StringValue("10.16.192.0/18"),
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

	ctx := context.Background()

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
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("ingress"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"disable_public_services": knownvalue.Bool(false),
						},
					)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("egress"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"disable_public_access": knownvalue.Bool(false),
						},
					)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefix"), knownvalue.StringExact("10.16.192.0/18")),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("vpc_prefixes"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("pod cidr"),
							"value": knownvalue.StringExact("10.0.0.0/13"),
						}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("service cidr"),
							"value": knownvalue.StringExact("10.16.0.0/22"),
						}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("internal lb cidr"),
							"value": knownvalue.StringExact("10.32.4.0/22"),
						}),
					})),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("dhcp"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"dns": knownvalue.ObjectExact(
								map[string]knownvalue.Check{
									"servers": knownvalue.SetExact([]knownvalue.Check{
										knownvalue.StringExact("1.1.1.1"),
									}),
								},
							),
						},
					)),
				},
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
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("name"), knownvalue.StringExact(vpcName)),
					statecheck.CompareValuePairs(fullDataSourceName, tfjsonpath.New("id"), fullResourceName, tfjsonpath.New("id"), compare.ValuesSame()),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("zone"), knownvalue.StringExact(zone)),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("ingress"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"disable_public_services": knownvalue.Bool(false),
						},
					)),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("egress"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"disable_public_access": knownvalue.Bool(false),
						},
					)),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("host_prefix"), knownvalue.StringExact("10.16.192.0/18")),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("vpc_prefixes"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("pod cidr"),
							"value": knownvalue.StringExact("10.0.0.0/13"),
						}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("service cidr"),
							"value": knownvalue.StringExact("10.16.0.0/22"),
						}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("internal lb cidr"),
							"value": knownvalue.StringExact("10.32.4.0/22"),
						}),
					})),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("dhcp"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"dns": knownvalue.ObjectExact(
								map[string]knownvalue.Check{
									"servers": knownvalue.SetExact([]knownvalue.Check{
										knownvalue.StringExact("1.1.1.1"),
									}),
								},
							),
						},
					)),
				},
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
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("ingress"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"disable_public_services": knownvalue.Bool(true),
						},
					)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("egress"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"disable_public_access": knownvalue.Bool(true),
						},
					)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefix"), knownvalue.StringExact("10.16.192.0/18")),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("vpc_prefixes"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("pod cidr"),
							"value": knownvalue.StringExact("10.0.0.0/13"),
						}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("service cidr"),
							"value": knownvalue.StringExact("10.16.0.0/22"),
						}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("internal lb cidr"),
							"value": knownvalue.StringExact("10.32.4.0/22"),
						}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"name":  knownvalue.StringExact("internal lb cidr 2"),
							"value": knownvalue.StringExact("10.45.4.0/22"),
						}),
					})),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("dhcp"), knownvalue.Null()),
				},
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
}

func TestHostPrefixReplace(t *testing.T) {
	randomInt := rand.IntN(99)
	vpcName := fmt.Sprintf("test-acc-hostprefix-replace-%x", randomInt)
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

	ctx := context.Background()

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
	vpcName := fmt.Sprintf("test-acc-hostprefix-default-%x", randomInt)
	resourceName := fmt.Sprintf("test_hostprefix_default_%x", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_networking_vpc.%s", resourceName)
	zone := testutil.AcceptanceTestZone

	vpc := &networking.VpcResourceModel{
		Name:       types.StringValue(vpcName),
		Zone:       types.StringValue(zone),
		HostPrefix: types.StringNull(),
	}

	ctx := context.Background()

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
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefix"), knownvalue.NotNull()),
				},
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
