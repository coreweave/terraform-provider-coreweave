package cks_test

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/cks"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/go-uuid"
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

func deleteCluster(ctx context.Context, client *coreweave.Client, cluster *cksv1beta1.Cluster) error {
	retryDelay := 30 * time.Second
	for {
		if cluster.Status == cksv1beta1.Cluster_STATUS_CREATING || cluster.Status == cksv1beta1.Cluster_STATUS_UPDATING || cluster.Status == cksv1beta1.Cluster_STATUS_DELETING {
			log.Printf("cluster %s is in creating or updating state, waiting before deletion", cluster.Name)
			select {
			case <-ctx.Done():
				return fmt.Errorf("timed out waiting for cluster %s to reach stable state: %w", cluster.Name, ctx.Err())
			case <-time.After(retryDelay):
			}
			clusterResp, err := client.GetCluster(ctx, connect.NewRequest(&cksv1beta1.GetClusterRequest{
				Id: cluster.Id,
			}))
			if connect.CodeOf(err) == connect.CodeNotFound {
				log.Printf("cluster %s has already been deleted", cluster.Name)
				return nil
			} else if err != nil {
				return fmt.Errorf("failed to get cluster %s: %w", cluster.Name, err)
			}

			cluster = clusterResp.Msg.Cluster
		} else {
			_, err := client.DeleteCluster(ctx, connect.NewRequest(&cksv1beta1.DeleteClusterRequest{
				Id: cluster.Id,
			}))
			if err == nil {
				return nil
			} else if connect.CodeOf(err) == connect.CodeNotFound {
				log.Printf("cluster %s has already been deleted", cluster.Name)
				return nil
			} else {
				return fmt.Errorf("failed to delete cluster %s: %w", cluster.Name, err)
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting to delete cluster %s: %w", cluster.Name, ctx.Err())
		case <-time.After(retryDelay):
			continue
		}
	}
}

func init() {
	resource.AddTestSweepers("coreweave_cks_cluster", &resource.Sweeper{
		Name:         "coreweave_cks_cluster",
		Dependencies: []string{}, // left as a placeholder; more types are likely to be added that would need to be torn down first.
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
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
					log.Printf("skipping Cluster %s because of dry-run mode", cluster.Name)
					continue
				}

				deleteCtx, deleteCancel := context.WithTimeout(ctx, 30*time.Minute)
				defer deleteCancel()

				if err := deleteCluster(deleteCtx, client, cluster); err != nil {
					return fmt.Errorf("failed to delete cluster %s: %w", cluster.Name, err)
				}

				waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Minute)
				defer waitCancel()
				if err := testutil.WaitForDelete(waitCtx, 5*time.Minute, 15*time.Second, client.GetCluster, &cksv1beta1.GetClusterRequest{
					Id: cluster.Id,
				}); err != nil {
					return fmt.Errorf("failed to wait for cluster %s to be deleted: %w", cluster.Name, err)
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

func defaultVpc(name, zone string) *networking.VpcResourceModel { //nolint:unparam
	if len(name) > 30 {
		// Bail on the tests as early as possible; this is a test definition failure.
		panic("Vpc name must be 30 characters or less")
	}

	return &networking.VpcResourceModel{
		Name:       types.StringValue(name),
		Zone:       types.StringValue(zone),
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
}

type resourceNames struct {
	ClusterName        string
	ResourceName       string
	FullResourceName   string
	FullDataSourceName string
}

func generateResourceNames(clusterNamePrefix string) resourceNames {
	randomInt := rand.IntN(100)

	clusterName := fmt.Sprintf("%s%s-%x", AcceptanceTestPrefix, clusterNamePrefix, randomInt)
	resourceName := fmt.Sprintf("test_acc_cks_cluster_%s_%x", clusterNamePrefix, randomInt)
	fullResourceName := fmt.Sprintf("coreweave_cks_cluster.%s", resourceName)
	fullDataSourceName := fmt.Sprintf("data.coreweave_cks_cluster.%s", resourceName)

	return resourceNames{
		ClusterName:        clusterName,
		ResourceName:       resourceName,
		FullResourceName:   fullResourceName,
		FullDataSourceName: fullDataSourceName,
	}
}

type testStepConfig struct {
	TestName                string
	Resources               resourceNames
	ConfigPlanChecks        resource.ConfigPlanChecks
	vpc                     networking.VpcResourceModel
	cluster                 cks.ClusterResourceModel
	PlanOnly                bool
	ExpectNonEmptyPlan      bool
	ImportStateVerifyIgnore []string
}

func stringOrNull(s types.String) knownvalue.Check {
	if s.IsNull() || s.IsUnknown() {
		return knownvalue.Null()
	}

	return knownvalue.StringExact(s.ValueString())
}

type saOIDCIssuerURLComparer struct{}

var saOIDCIssuerURLRegex = regexp.MustCompile(`^https?://(?P<Host>[\w\d\.\-]+)(:(?P<Port>[0-9]+))?/id/(?P<ID>[0-9a-f\-]+)/?$`)

var _ compare.ValueComparer = saOIDCIssuerURLComparer{}

// CompareValues implements compare.ValueComparer.
func (c saOIDCIssuerURLComparer) CompareValues(values ...any) error {
	comparableValues := make([]string, len(values))
	for i, v := range values {
		vs, ok := v.(string)
		if !ok {
			return fmt.Errorf("value %d is not a string, got type %T: %v", i, v, v)
		}

		if _, err := uuid.ParseUUID(vs); err == nil {
			comparableValues[i] = vs
			continue
		}

		matches := saOIDCIssuerURLRegex.FindStringSubmatch(vs)
		if matches != nil {
			idGroup := matches[saOIDCIssuerURLRegex.SubexpIndex("ID")]
			if _, err := uuid.ParseUUID(idGroup); err == nil {
				comparableValues[i] = idGroup
				continue
			}
		}

		return fmt.Errorf("value %d is not a valid UUID or OIDC issuer URL: %q", i, vs)
	}

	for _, comparableValue := range comparableValues[1:] {
		if comparableValue != comparableValues[0] {
			return fmt.Errorf("values are not equal: %v != %v", comparableValues[0], comparableValue)
		}
	}
	return nil
}

func createClusterTestStep(ctx context.Context, t *testing.T, config testStepConfig) resource.TestStep {
	t.Helper()

	var statechecks []statecheck.StateCheck
	if !config.PlanOnly {
		// immutable fields
		statechecks = []statecheck.StateCheck{
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("name"), knownvalue.StringExact(config.cluster.Name.ValueString())),
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("zone"), knownvalue.StringExact(config.cluster.Zone.ValueString())),
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("api_server_endpoint"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("status"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("vpc_id"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("version"), knownvalue.StringExact(config.cluster.Version.ValueString())),
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("public"), knownvalue.Bool(config.cluster.Public.ValueBool())),
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("pod_cidr_name"), knownvalue.StringExact(config.cluster.PodCidrName.ValueString())),
			statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("service_cidr_name"), knownvalue.StringExact(config.cluster.ServiceCidrName.ValueString())),
			statecheck.CompareValuePairs(config.Resources.FullResourceName, tfjsonpath.New("service_account_oidc_issuer_url"), config.Resources.FullResourceName, tfjsonpath.New("id"), saOIDCIssuerURLComparer{}),
		}

		// internal lb cidrs
		internalLbCidrs := []knownvalue.Check{}
		for _, c := range config.cluster.InternalLbCidrNames(ctx) {
			internalLbCidrs = append(internalLbCidrs, knownvalue.StringExact(c))
		}
		statechecks = append(statechecks, statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("internal_lb_cidr_names"), knownvalue.SetExact(internalLbCidrs)))

		// oidc
		if config.cluster.Oidc != nil {
			oidc := map[string]knownvalue.Check{
				"issuer_url":          stringOrNull(config.cluster.Oidc.IssuerURL),
				"client_id":           stringOrNull(config.cluster.Oidc.ClientID),
				"username_claim":      stringOrNull(config.cluster.Oidc.UsernameClaim),
				"username_prefix":     stringOrNull(config.cluster.Oidc.UsernamePrefix),
				"groups_claim":        stringOrNull(config.cluster.Oidc.GroupsClaim),
				"groups_prefix":       stringOrNull(config.cluster.Oidc.GroupsPrefix),
				"ca":                  stringOrNull(config.cluster.Oidc.CA),
				"required_claim":      stringOrNull(config.cluster.Oidc.RequiredClaim),
				"admin_group_binding": stringOrNull(config.cluster.Oidc.AdminGroupBinding),
			}
			if len(config.cluster.Oidc.SigningAlgs.Elements()) == 0 {
				oidc["signing_algs"] = knownvalue.SetSizeExact(0)
			} else {
				algs := []types.String{}
				if diag := config.cluster.Oidc.SigningAlgs.ElementsAs(ctx, &algs, false); diag.HasError() {
					t.Logf("failed to cast oidc signing algs to string slice: %v", diag.Errors())
					t.FailNow()
				}

				checks := []knownvalue.Check{}
				for _, a := range algs {
					checks = append(checks, knownvalue.StringExact(a.ValueString()))
				}
				oidc["signing_algs"] = knownvalue.SetExact(checks)
			}
			statechecks = append(statechecks, statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("oidc"), knownvalue.ObjectExact(oidc)))
		}

		// webhooks
		if config.cluster.AuthNWebhook != nil {
			authn := map[string]knownvalue.Check{
				"server": knownvalue.StringExact(config.cluster.AuthNWebhook.Server.ValueString()),
				"ca":     stringOrNull(config.cluster.AuthNWebhook.CA),
			}
			statechecks = append(statechecks, statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("authn_webhook"), knownvalue.ObjectExact(authn)))
		}

		if config.cluster.AuthZWebhook != nil {
			authz := map[string]knownvalue.Check{
				"server": knownvalue.StringExact(config.cluster.AuthZWebhook.Server.ValueString()),
				"ca":     stringOrNull(config.cluster.AuthZWebhook.CA),
			}
			statechecks = append(statechecks, statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("authz_webhook"), knownvalue.ObjectExact(authz)))
		}

		// node_port_range
		if !config.cluster.NodePortRange.IsNull() && !config.cluster.NodePortRange.IsUnknown() {
			attrs := config.cluster.NodePortRange.Attributes()
			if s, ok := attrs["start"].(types.Int32); ok && !s.IsNull() && !s.IsUnknown() {
				statechecks = append(statechecks, statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("node_port_range").AtMapKey("start"), knownvalue.Int64Exact(int64(s.ValueInt32()))))
			}
			if e, ok := attrs["end"].(types.Int32); ok && !e.IsNull() && !e.IsUnknown() {
				statechecks = append(statechecks, statecheck.ExpectKnownValue(config.Resources.FullResourceName, tfjsonpath.New("node_port_range").AtMapKey("end"), knownvalue.Int64Exact(int64(e.ValueInt32()))))
			}
		}
	}

	step := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning coreweave_cks_cluster %s test", config.TestName)
		},
		Config:                  networking.MustRenderVpcResource(ctx, config.Resources.ResourceName, &config.vpc) + "\n" + cks.MustRenderClusterResource(ctx, config.Resources.ResourceName, &config.cluster),
		ConfigPlanChecks:        config.ConfigPlanChecks,
		PlanOnly:                config.PlanOnly,
		ExpectNonEmptyPlan:      config.ExpectNonEmptyPlan,
		ImportStateVerifyIgnore: config.ImportStateVerifyIgnore,
	}

	if !config.PlanOnly {
		step.Check = resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr(config.Resources.FullResourceName, "name", config.cluster.Name.ValueString()),
			resource.TestCheckResourceAttr(config.Resources.FullResourceName, "zone", config.cluster.Zone.ValueString()),
		)
		step.ConfigStateChecks = statechecks
	}

	return step
}

func TestClusterResource(t *testing.T) {
	config := generateResourceNames("cks-cluster")
	zone := testutil.AcceptanceTestZone
	kubeVersion := testutil.AcceptanceTestKubeVersion

	vpc := defaultVpc(config.ClusterName, zone)
	npTypes := map[string]attr.Type{
		"start": types.Int32Type,
		"end":   types.Int32Type,
	}
	npVals := map[string]attr.Value{
		"start": types.Int32Value(30000),
		"end":   types.Int32Value(39534),
	}
	np, _ := types.ObjectValue(npTypes, npVals)
	npLargeVals := map[string]attr.Value{
		"start": types.Int32Value(30000),
		"end":   types.Int32Value(60000),
	}
	npLarge, _ := types.ObjectValue(npTypes, npLargeVals)
	npSmallVals := map[string]attr.Value{
		"start": types.Int32Value(30000),
		"end":   types.Int32Value(34534),
	}
	npSmall, _ := types.ObjectValue(npTypes, npSmallVals)
	initial := &cks.ClusterResourceModel{
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

	update := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(true),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr"), types.StringValue("internal-lb-cidr-2")}),
		AuditPolicy:         types.StringValue(AuditPolicyB64),
		NodePortRange:       npLarge,
		Oidc: &cks.OidcResourceModel{
			IssuerURL:         types.StringValue("https://samples.auth0.com/"),
			ClientID:          types.StringValue("kbyuFDidLLm280LIwVFiazOqjO3ty8KH"),
			UsernameClaim:     types.StringValue("user_id"),
			UsernamePrefix:    types.StringValue("cw"),
			GroupsClaim:       types.StringValue("read-only"),
			GroupsPrefix:      types.StringValue("cw"),
			CA:                types.StringValue(ExampleCAB64),
			SigningAlgs:       types.SetValueMust(types.StringType, []attr.Value{types.StringValue("SIGNING_ALGORITHM_RS256")}),
			RequiredClaim:     types.StringValue("group=admin"),
			AdminGroupBinding: types.StringValue("cw-admins"),
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
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(true),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
		NodePortRange:       np,
	}

	requiresReplaceNodePortShrink := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(true),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr"), types.StringValue("internal-lb-cidr-2")}),
		AuditPolicy:         types.StringValue(AuditPolicyB64),
		NodePortRange:       npSmall,
	}

	ctx := context.Background()

	steps := []resource.TestStep{
		{
			PreConfig: func() {
				t.Log("Beginning data source not found test")
			},
			Config: strings.Join([]string{
				fmt.Sprintf(`data "%s" "%s" { id = "%s" }`, "coreweave_cks_cluster", config.ResourceName, "1b5274f2-8012-4b68-9010-cc4c51613302"),
			}, "\n"),
			ExpectError: regexp.MustCompile(`(?i)cluster .*not found`),
		},
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "create",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionCreate),
				},
			},
			Resources: config,
			cluster:   *initial,
			vpc:       *vpc,
		}),
		{
			PreConfig: func() {
				t.Log("Beginning coreweave_cks_cluster data source test")
			},
			Config: strings.Join([]string{
				networking.MustRenderVpcResource(ctx, config.ResourceName, vpc),
				cks.MustRenderClusterResource(ctx, config.ResourceName, initial),
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
				// Note: for values which are not known at plan time, we need to compare the value pairs instead of expecting a known value.
				statecheck.CompareValuePairs(config.FullDataSourceName, tfjsonpath.New("id"), config.FullResourceName, tfjsonpath.New("id"), compare.ValuesSame()),
				statecheck.CompareValuePairs(config.FullDataSourceName, tfjsonpath.New("vpc_id"), config.FullResourceName, tfjsonpath.New("vpc_id"), compare.ValuesSame()),
				statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("zone"), knownvalue.StringExact(initial.Zone.ValueString())),
				statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("version"), knownvalue.StringExact(initial.Version.ValueString())),
				statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("public"), knownvalue.Bool(initial.Public.ValueBool())),
				statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("pod_cidr_name"), knownvalue.StringExact(initial.PodCidrName.ValueString())),
				statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("service_cidr_name"), knownvalue.StringExact(initial.ServiceCidrName.ValueString())),
				statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("node_port_range").AtMapKey("start"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue(config.FullDataSourceName, tfjsonpath.New("node_port_range").AtMapKey("end"), knownvalue.NotNull()),
			},
		},
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "update",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionUpdate),
				},
			},
			Resources: config,
			vpc:       *vpc,
			cluster:   *update,
		}),
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "requires replace on internal_lb_cidr_names removal and audit policy removal",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionDestroyBeforeCreate),
				},
			},
			Resources: config,
			vpc:       *vpc,
			cluster:   *requiresReplace,
		}),
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "requires replace on node_port_range shrink",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPreRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionDestroyBeforeCreate),
				},
			},
			Resources:          config,
			vpc:                *vpc,
			cluster:            *requiresReplaceNodePortShrink,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		}),
		{
			PreConfig: func() {
				t.Log("Beginning coreweave_cks_cluster import test")
			},
			ResourceName:      config.FullResourceName,
			ImportState:       true,
			ImportStateVerify: true,
		},
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: steps,
	})
}

func TestPartialOidcConfig(t *testing.T) {
	config := generateResourceNames("partial-oidc")
	zone := testutil.AcceptanceTestZone
	kubeVersion := testutil.AcceptanceTestKubeVersion

	vpc := defaultVpc(config.ClusterName, zone)

	initial := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(false),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
		Oidc: &cks.OidcResourceModel{
			CA:          types.StringNull(),
			IssuerURL:   types.StringValue("https://samples.auth0.com/"),
			ClientID:    types.StringValue("kbyuFDidLLm280LIwVFiazOqjO3ty8KH"),
			SigningAlgs: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("SIGNING_ALGORITHM_RS256")}),
		},
	}

	updateToFull := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
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
	}

	updateToEmpty := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(true),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr"), types.StringValue("internal-lb-cidr-2")}),
		AuditPolicy:         types.StringValue(AuditPolicyB64),
	}

	updateToPartial := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
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
		},
	}

	ctx := context.Background()

	steps := []resource.TestStep{
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "partial oidc initial",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionCreate),
				},
			},
			Resources: config,
			vpc:       *vpc,
			cluster:   *initial,
		}),
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "partial oidc update full",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionUpdate),
				},
			},
			Resources: config,
			vpc:       *vpc,
			cluster:   *updateToFull,
		}),
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "partial oidc update to empty",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionUpdate),
				},
			},
			Resources: config,
			vpc:       *vpc,
			cluster:   *updateToEmpty,
		}),
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "partial oidc update to partial",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionUpdate),
				},
			},
			Resources: config,
			vpc:       *vpc,
			cluster:   *updateToPartial,
		}),
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: steps,
	})
}

func TestPartialWebhookConfig(t *testing.T) {
	config := generateResourceNames("partial-webhook")
	zone := testutil.AcceptanceTestZone
	kubeVersion := testutil.AcceptanceTestKubeVersion

	vpc := defaultVpc(config.ClusterName, zone)

	initial := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(false),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
		AuthNWebhook: &cks.AuthWebhookResourceModel{
			Server: types.StringValue("https://samples.auth0.com/"),
			CA:     types.StringValue(ExampleCAB64),
		},
	}

	updateToFull := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(true),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr"), types.StringValue("internal-lb-cidr-2")}),
		AuditPolicy:         types.StringValue(AuditPolicyB64),
		AuthNWebhook: &cks.AuthWebhookResourceModel{
			Server: types.StringValue("https://samples.auth0.com/"),
			CA:     types.StringValue(ExampleCAB64),
		},
		AuthZWebhook: &cks.AuthWebhookResourceModel{
			Server: types.StringValue("https://samples.auth0.com/"),
			CA:     types.StringValue(ExampleCAB64),
		},
	}

	updateToEmpty := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(true),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr"), types.StringValue("internal-lb-cidr-2")}),
		AuditPolicy:         types.StringValue(AuditPolicyB64),
	}

	ctx := context.Background()

	steps := []resource.TestStep{
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "partial webook initial",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionCreate),
				},
			},
			Resources: config,
			vpc:       *vpc,
			cluster:   *initial,
		}),
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "partial webhook update full",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionUpdate),
				},
			},
			Resources: config,
			vpc:       *vpc,
			cluster:   *updateToFull,
		}),
		createClusterTestStep(ctx, t, testStepConfig{
			TestName: "partial webook update to empty",
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(config.FullResourceName, plancheck.ResourceActionUpdate),
				},
			},
			Resources: config,
			vpc:       *vpc,
			cluster:   *updateToEmpty,
		}),
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: steps,
	})
}

func TestEmptyAuditPolicy(t *testing.T) {
	config := generateResourceNames("audit-policy")
	zone := testutil.AcceptanceTestZone
	kubeVersion := testutil.AcceptanceTestKubeVersion

	vpc := defaultVpc(config.ClusterName, zone)
	cluster := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", config.ResourceName)),
		Name:                types.StringValue(config.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(false),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
		AuditPolicy:         types.StringValue(""),
	}

	ctx := context.Background()
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			createClusterTestStep(ctx, t, testStepConfig{
				TestName:  "empty audit policy",
				Resources: config,
				vpc:       *vpc,
				cluster:   *cluster,
			}),
		},
	})
}

func TestSharedStorage(t *testing.T) {
	zone := testutil.AcceptanceTestZone
	kubeVersion := testutil.AcceptanceTestKubeVersion
	ctx := t.Context()
	// Create base (original/source) cluster that is sharing it's storage
	baseConfig1 := generateResourceNames("shared")
	baseVpc1 := defaultVpc(baseConfig1.ClusterName, zone)
	baseCluster1 := &cks.ClusterResourceModel{
		VpcId:               types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", baseConfig1.ResourceName)),
		Name:                types.StringValue(baseConfig1.ClusterName),
		Zone:                types.StringValue(zone),
		Version:             types.StringValue(kubeVersion),
		Public:              types.BoolValue(false),
		PodCidrName:         types.StringValue("pod-cidr"),
		ServiceCidrName:     types.StringValue("service-cidr"),
		InternalLBCidrNames: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
	}

	// Create migrated cluster which is taking over it's storage
	dependentConfig := generateResourceNames("migrated")
	dependentVpc := defaultVpc(dependentConfig.ClusterName, zone)

	dependentClusterInitial := &cks.ClusterResourceModel{
		VpcId:                  types.StringValue(fmt.Sprintf("coreweave_networking_vpc.%s.id", dependentConfig.ResourceName)),
		Name:                   types.StringValue(dependentConfig.ClusterName),
		Zone:                   types.StringValue(zone),
		Version:                types.StringValue(kubeVersion),
		Public:                 types.BoolValue(false),
		PodCidrName:            types.StringValue("pod-cidr"),
		ServiceCidrName:        types.StringValue("service-cidr"),
		InternalLBCidrNames:    types.SetValueMust(types.StringType, []attr.Value{types.StringValue("internal-lb-cidr")}),
		SharedStorageClusterId: types.StringValue(fmt.Sprintf("coreweave_cks_cluster.%s.id", baseConfig1.ResourceName)),
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck: func() {
			testutil.SetEnvDefaults()
		},
		Steps: []resource.TestStep{
			// Create base clusters
			{
				PreConfig: func() {
					t.Log("Creating base clusters for shared storage test")
				},
				Config: strings.Join([]string{
					networking.MustRenderVpcResource(ctx, baseConfig1.ResourceName, baseVpc1),
					cks.MustRenderClusterResource(ctx, baseConfig1.ResourceName, baseCluster1),
				}, "\n"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(baseConfig1.FullResourceName, plancheck.ResourceActionCreate),
					},
				},
			},
			// Create dependent cluster with shared_storage_cluster_id pointing to base1
			{
				PreConfig: func() {
					t.Log("Creating dependent cluster with shared_storage_cluster_id pointing to base1")
				},
				Config: strings.Join([]string{
					networking.MustRenderVpcResource(ctx, baseConfig1.ResourceName, baseVpc1),
					cks.MustRenderClusterResource(ctx, baseConfig1.ResourceName, baseCluster1),
					networking.MustRenderVpcResource(ctx, dependentConfig.ResourceName, dependentVpc),
					cks.MustRenderClusterResource(ctx, dependentConfig.ResourceName, dependentClusterInitial),
				}, "\n"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(dependentConfig.FullResourceName, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair(dependentConfig.FullResourceName, "shared_storage_cluster_id", baseConfig1.FullResourceName, "id"),
				),
			},
		},
	})
}
