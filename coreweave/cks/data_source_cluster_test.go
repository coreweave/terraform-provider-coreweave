package cks_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/coreweave/cks"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestClusterDataSource(t *testing.T) {
	t.Parallel()

	t.Run("schema", func(t *testing.T) {
		ctx := context.Background()
		schemaRequest := fwdatasource.SchemaRequest{}
		schemaResponse := &fwdatasource.SchemaResponse{}

		cks.NewClusterDataSource().Schema(ctx, schemaRequest, schemaResponse)

		if schemaResponse.Diagnostics.HasError() {
			t.Fatalf("Schema method diagnostics: %+v", schemaResponse.Diagnostics)
		}

		diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)

		if diagnostics.HasError() {
			t.Fatalf("Schema validation diagnostics: %+v", diagnostics)
		}
	})

	t.Run("not found", func(t *testing.T) {
		config := generateResourceNames("ds-not-found")

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
						fmt.Sprintf(`data "%s" "%s" { id = "%s" }`, "coreweave_cks_cluster", config.ResourceName, "1b5274f2-8012-4b68-9010-cc4c51613302"),
					}, "\n"),
					ExpectError: regexp.MustCompile(`(?i)cluster .*not found`),
				},
			},
		})
	})

	t.Run("basic", func(t *testing.T) {
		config := generateResourceNames("ds-cluster")
		zone := testutil.AcceptanceTestZone
		kubeVersion := testutil.AcceptanceTestKubeVersion

		vpc := defaultVpc(config.ClusterName, zone)
		npTypes := map[string]attr.Type{
			"start": types.Int32Type,
			"end":   types.Int32Type,
		}
		npVals := map[string]attr.Value{
			"start": types.Int32Value(30000),
			"end":   types.Int32Value(50000),
		}
		np, _ := types.ObjectValue(npTypes, npVals)

		cluster := &cks.ClusterResourceModel{
			VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
			Name:                types.StringValue(config.ClusterName),
			Zone:                types.StringValue(zone),
			Version:             types.StringValue(kubeVersion),
			Public:              types.BoolValue(false),
			PodCidrName:         types.StringValue("pod-cidr"),
			ServiceCidrName:     types.StringValue("service-cidr"),
			InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
			NodePortRange:       np,
		}

		dataSource := &cks.ClusterDataSourceModel{
			Id: types.StringValue(fmt.Sprintf("coreweave_cks_cluster.%s.id", config.ResourceName)),
		}

		ctx := context.Background()

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck: func() {
				testutil.SetEnvDefaults()
			},
			Steps: []resource.TestStep{
				// Create the cluster resource first
				{
					PreConfig: func() {
						t.Log("Creating cluster for data source test")
					},
					Config: networking.MustRenderVpcResource(ctx, config.ResourceName, vpc) + "\n" + cks.MustRenderClusterResource(ctx, config.ResourceName, cluster),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionCreate),
						},
					},
				},
				// Test the data source
				{
					PreConfig: func() {
						t.Log("Testing coreweave_cks_cluster data source")
					},
					Config: strings.Join([]string{
						networking.MustRenderVpcResource(ctx, config.ResourceName, vpc),
						cks.MustRenderClusterResource(ctx, config.ResourceName, cluster),
						cks.MustRenderClusterDataSource(ctx, config.ResourceName, dataSource),
					}, "\n"),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionNoop),
						},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttrPair(config.FullDataSourceName, "id", config.FullResourceName, "id"),
					),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("name"), knownvalue.StringExact(config.ClusterName)),
						statecheck.CompareValuePairs(config.FullDataSourceName, tfjsonpath.New("id"), config.FullResourceName, tfjsonpath.New("id"), compare.ValuesSame()),
						statecheck.CompareValuePairs(config.FullDataSourceName, tfjsonpath.New("vpc_id"), config.FullResourceName, tfjsonpath.New("vpc_id"), compare.ValuesSame()),
						statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("zone"), knownvalue.StringExact(cluster.Zone.ValueString())),
						statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("version"), knownvalue.StringExact(cluster.Version.ValueString())),
						statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("public"), knownvalue.Bool(cluster.Public.ValueBool())),
						statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("pod_cidr_name"), knownvalue.StringExact(cluster.PodCidrName.ValueString())),
						statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("service_cidr_name"), knownvalue.StringExact(cluster.ServiceCidrName.ValueString())),
						statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("node_port_range").AtMapKey("start"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("node_port_range").AtMapKey("end"), knownvalue.NotNull()),
					},
				},
			},
		})
	})
}
