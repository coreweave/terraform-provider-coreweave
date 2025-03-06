package cks_test

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/cks"
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
)

const (
	AuditPolicyB64       = "ewogICJhcGlWZXJzaW9uIjogImF1ZGl0Lms4cy5pby92MSIsCiAgImtpbmQiOiAiUG9saWN5IiwKICAib21pdFN0YWdlcyI6IFsKICAgICJSZXF1ZXN0UmVjZWl2ZWQiCiAgXSwKICAicnVsZXMiOiBbCiAgICB7CiAgICAgICJsZXZlbCI6ICJSZXF1ZXN0UmVzcG9uc2UiLAogICAgICAicmVzb3VyY2VzIjogWwogICAgICAgIHsKICAgICAgICAgICJncm91cCI6ICIiLAogICAgICAgICAgInJlc291cmNlcyI6IFsKICAgICAgICAgICAgInBvZHMiCiAgICAgICAgICBdCiAgICAgICAgfQogICAgICBdCiAgICB9LAogICAgewogICAgICAibGV2ZWwiOiAiTWV0YWRhdGEiLAogICAgICAicmVzb3VyY2VzIjogWwogICAgICAgIHsKICAgICAgICAgICJncm91cCI6ICIiLAogICAgICAgICAgInJlc291cmNlcyI6IFsKICAgICAgICAgICAgInBvZHMvbG9nIiwKICAgICAgICAgICAgInBvZHMvc3RhdHVzIgogICAgICAgICAgXQogICAgICAgIH0KICAgICAgXQogICAgfSwKICAgIHsKICAgICAgImxldmVsIjogIk5vbmUiLAogICAgICAicmVzb3VyY2VzIjogWwogICAgICAgIHsKICAgICAgICAgICJncm91cCI6ICIiLAogICAgICAgICAgInJlc291cmNlcyI6IFsKICAgICAgICAgICAgImNvbmZpZ21hcHMiCiAgICAgICAgICBdLAogICAgICAgICAgInJlc291cmNlTmFtZXMiOiBbCiAgICAgICAgICAgICJjb250cm9sbGVyLWxlYWRlciIKICAgICAgICAgIF0KICAgICAgICB9CiAgICAgIF0KICAgIH0sCiAgICB7CiAgICAgICJsZXZlbCI6ICJOb25lIiwKICAgICAgInVzZXJzIjogWwogICAgICAgICJzeXN0ZW06a3ViZS1wcm94eSIKICAgICAgXSwKICAgICAgInZlcmJzIjogWwogICAgICAgICJ3YXRjaCIKICAgICAgXSwKICAgICAgInJlc291cmNlcyI6IFsKICAgICAgICB7CiAgICAgICAgICAiZ3JvdXAiOiAiIiwKICAgICAgICAgICJyZXNvdXJjZXMiOiBbCiAgICAgICAgICAgICJlbmRwb2ludHMiLAogICAgICAgICAgICAic2VydmljZXMiCiAgICAgICAgICBdCiAgICAgICAgfQogICAgICBdCiAgICB9LAogICAgewogICAgICAibGV2ZWwiOiAiTm9uZSIsCiAgICAgICJ1c2VyR3JvdXBzIjogWwogICAgICAgICJzeXN0ZW06YXV0aGVudGljYXRlZCIKICAgICAgXSwKICAgICAgIm5vblJlc291cmNlVVJMcyI6IFsKICAgICAgICAiL2FwaSoiLAogICAgICAgICIvdmVyc2lvbiIKICAgICAgXQogICAgfSwKICAgIHsKICAgICAgImxldmVsIjogIlJlcXVlc3QiLAogICAgICAicmVzb3VyY2VzIjogWwogICAgICAgIHsKICAgICAgICAgICJncm91cCI6ICIiLAogICAgICAgICAgInJlc291cmNlcyI6IFsKICAgICAgICAgICAgImNvbmZpZ21hcHMiCiAgICAgICAgICBdCiAgICAgICAgfQogICAgICBdLAogICAgICAibmFtZXNwYWNlcyI6IFsKICAgICAgICAia3ViZS1zeXN0ZW0iCiAgICAgIF0KICAgIH0sCiAgICB7CiAgICAgICJsZXZlbCI6ICJNZXRhZGF0YSIsCiAgICAgICJyZXNvdXJjZXMiOiBbCiAgICAgICAgewogICAgICAgICAgImdyb3VwIjogIiIsCiAgICAgICAgICAicmVzb3VyY2VzIjogWwogICAgICAgICAgICAic2VjcmV0cyIsCiAgICAgICAgICAgICJjb25maWdtYXBzIgogICAgICAgICAgXQogICAgICAgIH0KICAgICAgXQogICAgfSwKICAgIHsKICAgICAgImxldmVsIjogIlJlcXVlc3QiLAogICAgICAicmVzb3VyY2VzIjogWwogICAgICAgIHsKICAgICAgICAgICJncm91cCI6ICIiCiAgICAgICAgfSwKICAgICAgICB7CiAgICAgICAgICAiZ3JvdXAiOiAiZXh0ZW5zaW9ucyIKICAgICAgICB9CiAgICAgIF0KICAgIH0sCiAgICB7CiAgICAgICJsZXZlbCI6ICJNZXRhZGF0YSIsCiAgICAgICJvbWl0U3RhZ2VzIjogWwogICAgICAgICJSZXF1ZXN0UmVjZWl2ZWQiCiAgICAgIF0KICAgIH0KICBdCn0K"
	ExampleCAB64         = "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURnekNDQW11Z0F3SUJBZ0lRVGhhQitUNmdtdVVYN3dXZi9XUitmekFOQmdrcWhraUc5dzBCQVFzRkFEQUEKTUI0WERUSTBNRFl5TlRJeE1UY3pNVm9YRFRJME1Ea3lNekl4TVRjek1Wb3dBRENDQVNJd0RRWUpLb1pJaHZjTgpBUUVCQlFBRGdnRVBBRENDQVFvQ2dnRUJBT240VkVpRWJoL29GRkoxcG1QZXZxb1pBbWtVUjRqeWd5Y0MvRFhCCmVEWjYxd1NzV1FPU21peFg1bDZDd1FXNzdkV3NRVGhsU0RqN003RytxYjZCWHBSUWcrMndJOFVsVHp6Y0NpM0UKN1pib2M2LzI1YXd3NVpLOW1GVWVGWlBWemI4ZHNuVUFkbmFNa2V2ckFGQXNoL0NmSEh0cThzSUZnOVF2SWJnUApNRFJJcnZnSmlGY1NLS1E5clgxOWkzcFY3ZE9UaGxaYW11UWRGUjhGSVgyQ3BVQithajdSWkdMTFFra3AzMzhUCjFTRk5hK3V1THk3Mlh6MldIdEdqOTE5OFVENFFTRzByd2JUYXEvQVdxNjcvblhRS2FOQ2xHYzlGajNRSjU2NEUKK3cvWXBvK1krc053OXY0M1NVSVdyQXRMNGRicHNadlBEK0FKS1RDRXArUExZWlVDQXdFQUFhT0IrRENCOVRBTwpCZ05WSFE4QkFmOEVCQU1DQmFBd0RBWURWUjBUQVFIL0JBSXdBRENCMUFZRFZSMFJBUUgvQklISk1JSEdnaVJsCmVHVmpkWFJ2Y2kxcllYUmhiRzluTFdWNFpXTjFkRzl5TFhKbFkyOXVZMmxzWlhLQ0xHVjRaV04xZEc5eUxXdGgKZEdGc2IyY3RaWGhsWTNWMGIzSXRjbVZqYjI1amFXeGxjaTVyWVhSaGJHOW5nakJsZUdWamRYUnZjaTFyWVhSaApiRzluTFdWNFpXTjFkRzl5TFhKbFkyOXVZMmxzWlhJdWEyRjBZV3h2Wnk1emRtT0NQbVY0WldOMWRHOXlMV3RoCmRHRnNiMmN0WlhobFkzVjBiM0l0Y21WamIyNWphV3hsY2k1cllYUmhiRzluTG5OMll5NWpiSFZ6ZEdWeUxteHYKWTJGc01BMEdDU3FHU0liM0RRRUJDd1VBQTRJQkFRQlEvQ2JBdEFCQkZORUE5d1hYaE9vYUNrRFY1dTc3VFlzMQpFV2FJcFJFNjV5QmVtTDc2eXpYeEtoc2RmR3RJSmJ0THBWS1lUYlpBVTQrem9IS1NVTWs4REY4bXN0dGhOMWQ5CnR6a1d4ZXZ3UGViL2NtMVZVWlBzWkxvNnFRblJRUFJCUXc0dFpWdkhTWmtsSjBVb2lvVk5zOWJJY3ZQZ2Z4UW0KNkhDU3NEWU9sWnlPRHlrY045U21nbFZtVWFNeVkxMGcrL3BWRzg4WkRyLy9zdUI1ZERPaktUcDNGbjRPSGR0VwpnRmpuY3RVOEV4Zk5YNTR1Yndja2ZTMGdiOXRtejcyaHN3OU5KaTV2QXlMS2ZIcmxNNTJTeWhwUVZKbkpPYzF6ClhqQVlLTHE1M1E1TGt3RXBZMXpkL21XdVhkRWswWldZcHlXemk3WWN4UXQreUJkWVNJQzEKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo="
	AcceptanceTestPrefix = "test-acc-"
)

func init() {
	resource.AddTestSweepers("coreweave_cks_cluster", &resource.Sweeper{
		Name:         "coreweave_cks_cluster",
		Dependencies: []string{}, // left as a placeholder; more types are likely to be added that would need to be torn down first.
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{})
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.ListClusters(ctx, &connect.Request[cksv1beta1.ListClustersRequest]{})
			if err != nil {
				return fmt.Errorf("failed to list clusters: %w", err)
			}
			for _, cluster := range listResp.Msg.Items {
				if !strings.HasPrefix(cluster.Name, AcceptanceTestPrefix) {
					log.Printf("skipping cluster %s because it does not have prefix %s", cluster.Name, AcceptanceTestPrefix)
					continue
				}

				if cluster.GetZone() != r {
					log.Printf("skipping cluster %s in zone %s because it does not match sweep zone %s", cluster.Name, cluster.Zone, r)
					continue
				}

				log.Printf("sweeping cluster %s", cluster.Name)
				if testutil.SweepDryRun() {
					continue
				}
				deleteResp, err := client.DeleteCluster(ctx, connect.NewRequest(&cksv1beta1.DeleteClusterRequest{
					Id: cluster.Id,
				}))
				if err != nil {
					return fmt.Errorf("failed to delete cluster %s: %w", cluster.Name, err)
				}
				deletedCluster := deleteResp.Msg.Cluster

				waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Minute)
				defer waitCancel()
				if err := testutil.WaitForDelete(waitCtx, 5*time.Minute, 15*time.Second, client.GetCluster, &cksv1beta1.GetClusterRequest{
					Id: deletedCluster.Id,
				}); err != nil {
					return fmt.Errorf("failed to wait for cluster %s to be deleted: %w", deletedCluster.Name, err)
				}
			}

			return nil
		},
	})
}

func TestClusterSchema(t *testing.T) {
	ctx := context.Background()
	schemaRequest := fwresource.SchemaRequest{}
	schemaResponse := &fwresource.SchemaResponse{}

	cks.NewClusterResource().Schema(ctx, schemaRequest, schemaResponse)

	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema method diagnostics: %+v", schemaResponse.Diagnostics)
	}

	// Validate the schema
	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)

	if diagnostics.HasError() {
		t.Fatalf("Schema validation diagnostics: %+v", diagnostics)
	}
}

func TestClusterResource(t *testing.T) {
	t.Parallel()
	randomInt := rand.IntN(100)
	clusterName := fmt.Sprintf("%scks-cluster-%x", AcceptanceTestPrefix, randomInt)
	resourceName := fmt.Sprintf("test_acc_cks_cluster_%x", randomInt)
	fullResourceName := fmt.Sprintf("coreweave_cks_cluster.%s", resourceName)
	fullDataSourceName := fmt.Sprintf("data.coreweave_cks_cluster.%s", resourceName)
	vpc := &networking.VpcResourceModel{
		Name:       types.StringValue(clusterName),
		Zone:       types.StringValue("US-EAST-04A"),
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
				Value: types.StringValue("10.32.4.0/22"),
			},
			{
				Name:  types.StringValue("internal-lb-cidr-2"),
				Value: types.StringValue("10.45.4.0/22"),
			},
		},
	}

	initial := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", resourceName)),
		Name:                types.StringValue(clusterName),
		Zone:                types.StringValue("US-EAST-04A"),
		Version:             types.StringValue("v1.30"),
		Public:              types.BoolValue(false),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
	}

	dataSource := &cks.ClusterDataSourceModel{
		Id: types.StringValue(fmt.Sprintf("coreweave_cks_cluster.%s.id", resourceName)),
	}

	update := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", resourceName)),
		Name:                types.StringValue(clusterName),
		Zone:                types.StringValue("US-EAST-04A"),
		Version:             types.StringValue("v1.30"),
		Public:              types.BoolValue(true),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr"), types.StringValue("internal-lb-cidr-2")}),
		AuditPolicy:         types.StringValue(AuditPolicyB64),
		Oidc: &cks.OidcResourceModel{
			IssuerURL:      types.StringValue("https://samples.auth0.com/"),
			ClientID:       types.StringValue("kbyuFDidLLm280LIwVFiazOqjO3ty8KH"),
			UsernameClaim:  types.StringValue("user_id"),
			UsernamePrefix: types.StringValue("cw"),
			GroupsClaim:    types.StringValue("read-only"),
			GroupsPrefix:   types.StringValue("cw"),
			CA:             types.StringValue(ExampleCAB64),
			SigningAlgs:    types.SetValueMust(types.StringType, []attr.Value{types.StringValue("SIGNING_ALGORITHM_RS256")}),
			RequiredClaim:  types.StringValue("group=admin"),
		},
		AuthNWebhook: &cks.AuthWebhookResourceModel{
			Server: types.StringValue("https://samples.auth0.com/"),
			CA:     types.StringValue(ExampleCAB64),
		},
		AuthZWebhook: &cks.AuthWebhookResourceModel{
			Server: types.StringValue("https://samples.auth0.com/"),
			CA:     types.StringValue(ExampleCAB64),
		},
	}

	requiresReplace := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", resourceName)),
		Name:                types.StringValue(clusterName),
		Zone:                types.StringValue("US-EAST-04A"),
		Version:             types.StringValue("v1.30"),
		Public:              types.BoolValue(true),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
	}

	ctx := context.Background()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_cks_cluster create test")
				},
				// create both the VPC and the cluster, since a cluster must have a VPC
				Config: networking.MustRenderVpcResource(ctx, resourceName, vpc) + "\n" + cks.MustRenderClusterResource(ctx, resourceName, initial),
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
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("api_server_endpoint"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("vpc_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("version"), knownvalue.StringExact(initial.Version.ValueString())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("public"), knownvalue.Bool(initial.Public.ValueBool())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("pod_cidr_name"), knownvalue.StringExact(initial.PodCidrName.ValueString())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("service_cidr_name"), knownvalue.StringExact(initial.ServiceCidrName.ValueString())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("internal_lb_cidr_names"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("internal-lb-cidr"),
					})),
				},
			},
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_cks_cluster data source test")
				},
				Config: strings.Join([]string{
					networking.MustRenderVpcResource(ctx, resourceName, vpc),
					cks.MustRenderClusterResource(ctx, resourceName, initial),
					cks.MustRenderClusterDataSource(ctx, resourceName, dataSource),
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
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("name"), knownvalue.StringExact(clusterName)),
					// Note: for values which are not known at plan time, we need to compare the value pairs instead of expecting a known value.
					statecheck.CompareValuePairs(fullDataSourceName, tfjsonpath.New("id"), fullResourceName, tfjsonpath.New("id"), compare.ValuesSame()),
					statecheck.CompareValuePairs(fullDataSourceName, tfjsonpath.New("vpc_id"), fullResourceName, tfjsonpath.New("vpc_id"), compare.ValuesSame()),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("zone"), knownvalue.StringExact(initial.Zone.ValueString())),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("version"), knownvalue.StringExact(initial.Version.ValueString())),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("public"), knownvalue.Bool(initial.Public.ValueBool())),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("pod_cidr_name"), knownvalue.StringExact(initial.PodCidrName.ValueString())),
					statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("service_cidr_name"), knownvalue.StringExact(initial.ServiceCidrName.ValueString())),
				},
			},
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_cks_cluster update test")
				},
				// create both the VPC and the cluster, since a cluster must have a VPC
				Config: networking.MustRenderVpcResource(ctx, resourceName, vpc) + "\n" + cks.MustRenderClusterResource(ctx, resourceName, update),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fullResourceName, "name", initial.Name.ValueString()),
					resource.TestCheckResourceAttr(fullResourceName, "zone", initial.Zone.ValueString()),
				),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("api_server_endpoint"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("vpc_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("version"), knownvalue.StringExact(update.Version.ValueString())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("public"), knownvalue.Bool(update.Public.ValueBool())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("pod_cidr_name"), knownvalue.StringExact(update.PodCidrName.ValueString())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("service_cidr_name"), knownvalue.StringExact(update.ServiceCidrName.ValueString())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("internal_lb_cidr_names"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("internal-lb-cidr"),
						knownvalue.StringExact("internal-lb-cidr-2"),
					})),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("oidc"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"issuer_url":      knownvalue.StringExact(update.Oidc.IssuerURL.ValueString()),
							"client_id":       knownvalue.StringExact(update.Oidc.ClientID.ValueString()),
							"username_claim":  knownvalue.StringExact(update.Oidc.UsernameClaim.ValueString()),
							"username_prefix": knownvalue.StringExact(update.Oidc.UsernamePrefix.ValueString()),
							"groups_claim":    knownvalue.StringExact(update.Oidc.GroupsClaim.ValueString()),
							"groups_prefix":   knownvalue.StringExact(update.Oidc.GroupsPrefix.ValueString()),
							"ca":              knownvalue.StringExact(update.Oidc.CA.ValueString()),
							"signing_algs":    knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("SIGNING_ALGORITHM_RS256")}),
							"required_claim":  knownvalue.StringExact(update.Oidc.RequiredClaim.ValueString()),
						},
					)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("authn_webhook"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"server": knownvalue.StringExact(update.AuthNWebhook.Server.ValueString()),
							"ca":     knownvalue.StringExact(update.AuthNWebhook.CA.ValueString()),
						},
					)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("authz_webhook"), knownvalue.ObjectExact(
						map[string]knownvalue.Check{
							"server": knownvalue.StringExact(update.AuthZWebhook.Server.ValueString()),
							"ca":     knownvalue.StringExact(update.AuthZWebhook.CA.ValueString()),
						},
					)),
				},
			},
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_cks_cluster requires replace test")
				},
				// create both the VPC and the cluster, since a cluster must have a VPC
				Config: networking.MustRenderVpcResource(ctx, resourceName, vpc) + "\n" + cks.MustRenderClusterResource(ctx, resourceName, requiresReplace),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("api_server_endpoint"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("vpc_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("version"), knownvalue.StringExact(requiresReplace.Version.ValueString())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("public"), knownvalue.Bool(requiresReplace.Public.ValueBool())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("pod_cidr_name"), knownvalue.StringExact(requiresReplace.PodCidrName.ValueString())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("service_cidr_name"), knownvalue.StringExact(requiresReplace.ServiceCidrName.ValueString())),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("internal_lb_cidr_names"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("internal-lb-cidr"),
					})),
				},
			},
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_cks_cluster import test")
				},
				ResourceName:      fullResourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
