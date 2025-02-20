package networking_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

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
	t.Parallel()
	randomInt := rand.IntN(100)
	vpcName := fmt.Sprintf("test-acc-vpc-%x", randomInt)
	resourceName := fmt.Sprintf("test_vpc_%x", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_networking_vpc.%s", resourceName)
	initial := &networking.VpcResourceModel{
		Name:       types.StringValue(vpcName),
		Zone:       types.StringValue("US-EAST-04A"),
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

	update := &networking.VpcResourceModel{
		Name: types.StringValue(vpcName),
		Zone: types.StringValue("US-EAST-04A"),
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

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			os.Setenv("COREWEAVE_API_TOKEN", "test")
		},
		Steps: []resource.TestStep{
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
	t.Parallel()
	randomInt := rand.IntN(99)
	vpcName := fmt.Sprintf("test-acc-hostprefix-replace-%x", randomInt)
	resourceName := fmt.Sprintf("test_hostprefix_replace_%x", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_networking_vpc.%s", resourceName)

	initial := &networking.VpcResourceModel{
		Name:       types.StringValue(vpcName),
		Zone:       types.StringValue("US-EAST-04A"),
		HostPrefix: types.StringNull(),
	}

	replace := &networking.VpcResourceModel{
		Name:       types.StringValue(vpcName),
		Zone:       types.StringValue("US-EAST-04A"),
		HostPrefix: types.StringValue("172.0.0.0/18"),
	}

	ctx := context.Background()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			os.Setenv("COREWEAVE_API_TOKEN", "test")
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
					// 10.176.192.0/18 is the default host prefix for US-EAST-04A
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefix"), knownvalue.StringExact("10.176.192.0/18")),
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
	t.Parallel()
	randomInt := rand.IntN(99)
	vpcName := fmt.Sprintf("test-acc-hostprefix-default-%x", randomInt)
	resourceName := fmt.Sprintf("test_hostprefix_default_%x", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_networking_vpc.%s", resourceName)

	vpc := &networking.VpcResourceModel{
		Name:       types.StringValue(vpcName),
		Zone:       types.StringValue("US-EAST-04A"),
		HostPrefix: types.StringNull(),
	}

	ctx := context.Background()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			os.Setenv("COREWEAVE_API_TOKEN", "test")
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
					// 10.176.192.0/18 is the default host prefix for US-EAST-04A
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("host_prefix"), knownvalue.StringExact("10.176.192.0/18")),
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
