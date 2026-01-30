package cks_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
)

func init() {
	resource.AddTestSweepers("coreweave_cks_cluster", &resource.Sweeper{
		Name:         "coreweave_cks_cluster",
		Dependencies: []string{},
		F: func(zone string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			return testutil.SweepSequential(ctx, testutil.SweeperConfig[*cksv1beta1.Cluster]{
				Lister: func(ctx context.Context) ([]*cksv1beta1.Cluster, error) {
					listResp, err := client.ListClusters(ctx, &connect.Request[cksv1beta1.ListClustersRequest]{})
					if err != nil {
						return nil, err
					}
					return listResp.Msg.Items, nil
				},
				NameGetter: func(cluster *cksv1beta1.Cluster) string {
					return cluster.Name
				},
				ZoneGetter: func(cluster *cksv1beta1.Cluster) string {
					return cluster.Zone
				},
				Deleter: func(ctx context.Context, cluster *cksv1beta1.Cluster) error {
					return deleteCluster(ctx, client, cluster)
				},
				Prefix:         AcceptanceTestPrefix,
				Zone:           zone,
				Timeout:        30 * time.Minute,
			})
		},
	})
}

// deleteCluster handles the deletion logic for CKS clusters.
// Clusters must be in a stable state (not CREATING/UPDATING/DELETING) before they can be deleted.
func deleteCluster(ctx context.Context, client *coreweave.Client, cluster *cksv1beta1.Cluster) error {
	stableRetry := retry.StateChangeConf{
		Pending: []string{
			cksv1beta1.Cluster_STATUS_CREATING.String(),
			cksv1beta1.Cluster_STATUS_UPDATING.String(),
			cksv1beta1.Cluster_STATUS_DELETING.String(),
		},
		Target: []string{
			cksv1beta1.Cluster_STATUS_RUNNING.String(),
			"",
		},
		Refresh: func() (result interface{}, state string, err error) {
			resp, err := client.GetCluster(ctx, connect.NewRequest(&cksv1beta1.GetClusterRequest{
				Id: cluster.Id,
			}))
			if err != nil {
				return nil, "", err
			}
			return resp.Msg.Cluster, resp.Msg.Cluster.Status.String(), nil
		},
		Timeout: 30 * time.Minute,
	}

	if _, err := stableRetry.WaitForStateContext(ctx); err != nil {
		return fmt.Errorf("failed to wait for cluster %s to reach stable state: %w", cluster.Name, err)
	}

	_, err := client.DeleteCluster(ctx, connect.NewRequest(&cksv1beta1.DeleteClusterRequest{
		Id: cluster.Id,
	}))
	if coreweave.IsNotFoundError(err) {
		return nil // Already deleted
	} else if err != nil {
		return fmt.Errorf("failed to delete cluster %s: %w", cluster.Name, err)
	}

	return nil
}

func TestMain(m *testing.M) {
	resource.TestMain(m)
}
