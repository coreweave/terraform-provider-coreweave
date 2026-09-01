package cks_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil/vpcsweeper"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cksClusterSweeperName       = "coreweave_cks_cluster"
	cksVPCSweeperName           = "coreweave_cks_vpc"
	cksClusterSweeperTimeout    = 30 * time.Minute
	cksVPCSweeperTimeout        = 10 * time.Minute
	testAccSweepTimeout         = 45 * time.Minute
	sweepTimeoutHeadroom        = 5 * time.Minute
	clusterDeleteTimeout        = 30 * time.Minute
	clusterTransitionRetryDelay = 30 * time.Second
	clusterDeleteWaitTimeout    = 10 * time.Minute
	clusterDeletePollTimeout    = 5 * time.Minute
	clusterDeletePollInterval   = 15 * time.Second
	testSweepZone               = "zone-a"
	testSweepClusterName        = "test-acc-cluster-12345"
)

type clusterSweepClient interface {
	ListClusters(context.Context, *connect.Request[cksv1beta1.ListClustersRequest]) (*connect.Response[cksv1beta1.ListClustersResponse], error)
	GetCluster(context.Context, *connect.Request[cksv1beta1.GetClusterRequest]) (*connect.Response[cksv1beta1.GetClusterResponse], error)
	DeleteCluster(context.Context, *connect.Request[cksv1beta1.DeleteClusterRequest]) (*connect.Response[cksv1beta1.DeleteClusterResponse], error)
}

type clusterSweepOptions struct {
	transitionRetryDelay time.Duration
	deleteTimeout        time.Duration
	waitTimeout          time.Duration
	waitForDelete        func(context.Context, clusterSweepClient, string) error
}

func defaultClusterSweepOptions() clusterSweepOptions {
	return clusterSweepOptions{
		transitionRetryDelay: clusterTransitionRetryDelay,
		deleteTimeout:        clusterDeleteTimeout,
		waitTimeout:          clusterDeleteWaitTimeout,
		waitForDelete: func(ctx context.Context, client clusterSweepClient, id string) error {
			return testutil.WaitForDelete(ctx, clusterDeletePollTimeout, clusterDeletePollInterval, client.GetCluster, &cksv1beta1.GetClusterRequest{Id: id})
		},
	}
}

func normalizeCKSSweepZone(zone string) (string, error) {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return "", fmt.Errorf("CKS sweep zone must not be empty")
	}
	return zone, nil
}

func newClusterSweepConfig(client clusterSweepClient, zone string, options clusterSweepOptions) (testutil.SweepConfig[*cksv1beta1.Cluster], error) {
	zone, err := normalizeCKSSweepZone(zone)
	if err != nil {
		return testutil.SweepConfig[*cksv1beta1.Cluster]{}, err
	}

	return testutil.SweepConfig[*cksv1beta1.Cluster]{
		ResourceType: cksClusterSweeperName,
		List: func(ctx context.Context) ([]*cksv1beta1.Cluster, error) {
			response, err := client.ListClusters(ctx, connect.NewRequest(&cksv1beta1.ListClustersRequest{}))
			if err != nil {
				return nil, fmt.Errorf("list clusters: %w", err)
			}
			return response.Msg.Items, nil
		},
		Name: func(cluster *cksv1beta1.Cluster) string {
			return cluster.GetName()
		},
		Match: func(cluster *cksv1beta1.Cluster) bool {
			return strings.HasPrefix(cluster.GetName(), AcceptanceTestPrefix) && cluster.GetZone() == zone
		},
		Delete: func(ctx context.Context, cluster *cksv1beta1.Cluster) error {
			deleteCtx, deleteCancel := context.WithTimeout(ctx, options.deleteTimeout)
			err := deleteCluster(deleteCtx, client, cluster, options.transitionRetryDelay)
			deleteCancel()
			if err != nil {
				return err
			}

			waitCtx, waitCancel := context.WithTimeout(ctx, options.waitTimeout)
			defer waitCancel()
			if err := options.waitForDelete(waitCtx, client, cluster.GetId()); err != nil {
				return fmt.Errorf("wait for cluster deletion: %w", err)
			}
			return nil
		},
	}, nil
}

func deleteCluster(ctx context.Context, client clusterSweepClient, listedCluster *cksv1beta1.Cluster, retryDelay time.Duration) error {
	cluster := listedCluster
	for isTransitionalClusterStatus(cluster.GetStatus()) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for cluster %s to reach stable state: %w", listedCluster.GetName(), ctx.Err())
		case <-time.After(retryDelay):
		}

		response, err := client.GetCluster(ctx, connect.NewRequest(&cksv1beta1.GetClusterRequest{Id: listedCluster.GetId()}))
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get cluster %s: %w", listedCluster.GetName(), err)
		}
		cluster = response.Msg.Cluster
	}

	_, err := client.DeleteCluster(ctx, connect.NewRequest(&cksv1beta1.DeleteClusterRequest{Id: listedCluster.GetId()}))
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete cluster %s: %w", listedCluster.GetName(), err)
	}
	return nil
}

func isTransitionalClusterStatus(status cksv1beta1.Cluster_Status) bool {
	return status == cksv1beta1.Cluster_STATUS_CREATING ||
		status == cksv1beta1.Cluster_STATUS_UPDATING ||
		status == cksv1beta1.Cluster_STATUS_UPGRADING ||
		status == cksv1beta1.Cluster_STATUS_DELETING
}

func newCKSClusterSweeper() *resource.Sweeper {
	return &resource.Sweeper{
		Name:         cksClusterSweeperName,
		Dependencies: []string{},
		F: func(zone string) error {
			zone, err := normalizeCKSSweepZone(zone)
			if err != nil {
				return err
			}
			runtime, err := testutil.SweepRuntimeFromEnv()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), cksClusterSweeperTimeout)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("build client: %w", err)
			}
			config, err := newClusterSweepConfig(client, zone, defaultClusterSweepOptions())
			if err != nil {
				return err
			}
			return testutil.Sweep(ctx, runtime, config)
		},
	}
}

func newCKSVPCSweeper() *resource.Sweeper {
	return &resource.Sweeper{
		Name:         cksVPCSweeperName,
		Dependencies: []string{cksClusterSweeperName},
		F: func(zone string) error {
			zone, err := normalizeCKSSweepZone(zone)
			if err != nil {
				return err
			}
			runtime, err := testutil.SweepRuntimeFromEnv()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), cksVPCSweeperTimeout)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("build client: %w", err)
			}
			config, err := newCKSVPCSweepConfig(client, zone)
			if err != nil {
				return err
			}
			return testutil.Sweep(ctx, runtime, config)
		},
	}
}

func newCKSVPCSweepConfig(client vpcsweeper.Client, zone string) (testutil.SweepConfig[*networkingv1beta1.VPC], error) {
	return vpcsweeper.New(client, vpcsweeper.Config{Prefix: cksVPCNamePrefix, Zone: zone})
}

func init() {
	resource.AddTestSweepers(cksClusterSweeperName, newCKSClusterSweeper())
	resource.AddTestSweepers(cksVPCSweeperName, newCKSVPCSweeper())
}

type fakeClusterSweepClient struct {
	getResponses   []*cksv1beta1.Cluster
	getError       error
	deleteError    error
	getIDs         []string
	deletedIDs     []string
	getResponseIdx int
}

func (*fakeClusterSweepClient) ListClusters(context.Context, *connect.Request[cksv1beta1.ListClustersRequest]) (*connect.Response[cksv1beta1.ListClustersResponse], error) {
	return connect.NewResponse(&cksv1beta1.ListClustersResponse{}), nil
}

func (client *fakeClusterSweepClient) GetCluster(_ context.Context, request *connect.Request[cksv1beta1.GetClusterRequest]) (*connect.Response[cksv1beta1.GetClusterResponse], error) {
	client.getIDs = append(client.getIDs, request.Msg.GetId())
	if client.getError != nil {
		return nil, client.getError
	}
	response := client.getResponses[client.getResponseIdx]
	client.getResponseIdx++
	return connect.NewResponse(&cksv1beta1.GetClusterResponse{Cluster: response}), nil
}

func (client *fakeClusterSweepClient) DeleteCluster(_ context.Context, request *connect.Request[cksv1beta1.DeleteClusterRequest]) (*connect.Response[cksv1beta1.DeleteClusterResponse], error) {
	client.deletedIDs = append(client.deletedIDs, request.Msg.GetId())
	if client.deleteError != nil {
		return nil, client.deleteError
	}
	return connect.NewResponse(&cksv1beta1.DeleteClusterResponse{}), nil
}

func TestCKSSweeperRegistrations(t *testing.T) {
	clusterSweeper := newCKSClusterSweeper()
	assert.Equal(t, cksClusterSweeperName, clusterSweeper.Name)
	assert.Empty(t, clusterSweeper.Dependencies)
	assert.NotNil(t, clusterSweeper.F)

	vpcSweeper := newCKSVPCSweeper()
	assert.Equal(t, cksVPCSweeperName, vpcSweeper.Name)
	assert.Equal(t, []string{cksClusterSweeperName}, vpcSweeper.Dependencies)
	assert.NotNil(t, vpcSweeper.F)
}

func TestCKSSweepersValidateZoneBeforeSetup(t *testing.T) {
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "")
	unsetCKSEnv(t, provider.CoreweaveApiEndpointEnvVar)
	t.Setenv("TEST_ACC_SWEEP_PARALLEL", "invalid")

	tests := []struct {
		name string
		zone string
		fn   func(string) error
	}{
		{name: "cluster empty", fn: newCKSClusterSweeper().F},
		{name: "cluster whitespace", zone: " \t\n", fn: newCKSClusterSweeper().F},
		{name: "VPC empty", fn: newCKSVPCSweeper().F},
		{name: "VPC whitespace", zone: " \t\n", fn: newCKSVPCSweeper().F},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualError(t, tt.fn(tt.zone), "CKS sweep zone must not be empty")
			_, found := os.LookupEnv(provider.CoreweaveApiEndpointEnvVar)
			assert.False(t, found)
		})
	}
}

func TestCKSSweepersValidateRuntimeBeforeSetup(t *testing.T) {
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "")
	unsetCKSEnv(t, provider.CoreweaveApiEndpointEnvVar)
	t.Setenv("TEST_ACC_SWEEP_PARALLEL", "invalid")

	for _, tt := range []struct {
		name string
		fn   func(string) error
	}{
		{name: "cluster", fn: newCKSClusterSweeper().F},
		{name: "VPC", fn: newCKSVPCSweeper().F},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, tt.fn(testSweepZone), "parse TEST_ACC_SWEEP_PARALLEL as integer")
			_, endpointWasDefaulted := os.LookupEnv(provider.CoreweaveApiEndpointEnvVar)
			assert.False(t, endpointWasDefaulted)
		})
	}
}

func unsetCKSEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "restored by testing")
	require.NoError(t, os.Unsetenv(name))
}

func TestCKSSweeperTimeoutBudget(t *testing.T) {
	chainTimeout := cksClusterSweeperTimeout + cksVPCSweeperTimeout
	require.LessOrEqual(t, chainTimeout, testAccSweepTimeout-sweepTimeoutHeadroom)
}

func TestIsTransitionalClusterStatus(t *testing.T) {
	for _, status := range []cksv1beta1.Cluster_Status{
		cksv1beta1.Cluster_STATUS_CREATING,
		cksv1beta1.Cluster_STATUS_UPDATING,
		cksv1beta1.Cluster_STATUS_UPGRADING,
		cksv1beta1.Cluster_STATUS_DELETING,
	} {
		assert.True(t, isTransitionalClusterStatus(status), status.String())
	}
	assert.False(t, isTransitionalClusterStatus(cksv1beta1.Cluster_STATUS_RUNNING))
}

func TestCKSVPCSweepConfigMatchesOnlyNewPrefixAndZone(t *testing.T) {
	config, err := newCKSVPCSweepConfig(nil, testSweepZone)
	require.NoError(t, err)
	tests := []struct {
		name string
		vpc  *networkingv1beta1.VPC
		want bool
	}{
		{name: "new CKS VPC", vpc: &networkingv1beta1.VPC{Name: "test-acc-cks-vpc-12345", Zone: testSweepZone}, want: true},
		{name: "legacy acceptance VPC", vpc: &networkingv1beta1.VPC{Name: "test-acc-cks-cluster-12", Zone: testSweepZone}},
		{name: "wrong zone", vpc: &networkingv1beta1.VPC{Name: "test-acc-cks-vpc-12345", Zone: "zone-b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, config.Match(tt.vpc))
		})
	}
}

func TestClusterSweepConfigMatchesPrefixAndZone(t *testing.T) {
	config, err := newClusterSweepConfig(&fakeClusterSweepClient{}, "  "+testSweepZone+"\n", testClusterSweepOptions(nil))
	require.NoError(t, err)
	tests := []struct {
		name    string
		cluster *cksv1beta1.Cluster
		want    bool
	}{
		{name: "both match", cluster: &cksv1beta1.Cluster{Name: testSweepClusterName, Zone: testSweepZone}, want: true},
		{name: "prefix mismatch", cluster: &cksv1beta1.Cluster{Name: "production", Zone: testSweepZone}},
		{name: "zone mismatch", cluster: &cksv1beta1.Cluster{Name: testSweepClusterName, Zone: "zone-b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, config.Match(tt.cluster))
		})
	}
}

func TestClusterSweepDeleteBehavior(t *testing.T) {
	tests := []struct {
		name            string
		status          cksv1beta1.Cluster_Status
		getResponses    []*cksv1beta1.Cluster
		getError        error
		deleteError     error
		waitError       error
		wantError       string
		wantGetCalls    int
		wantDeleteCalls int
		wantWaitCalls   int
	}{
		{name: "stable delete and post-delete wait", status: cksv1beta1.Cluster_STATUS_RUNNING, wantDeleteCalls: 1, wantWaitCalls: 1},
		{name: "transitional states retry until stable", status: cksv1beta1.Cluster_STATUS_CREATING, getResponses: []*cksv1beta1.Cluster{{Status: cksv1beta1.Cluster_STATUS_UPGRADING}, {Status: cksv1beta1.Cluster_STATUS_RUNNING}}, wantGetCalls: 2, wantDeleteCalls: 1, wantWaitCalls: 1},
		{name: "deleting retries until not found", status: cksv1beta1.Cluster_STATUS_DELETING, getError: connect.NewError(connect.CodeNotFound, assert.AnError), wantGetCalls: 1, wantWaitCalls: 1},
		{name: "refresh API failure", status: cksv1beta1.Cluster_STATUS_CREATING, getError: connect.NewError(connect.CodeUnavailable, assert.AnError), wantError: "get cluster", wantGetCalls: 1},
		{name: "delete not found succeeds", status: cksv1beta1.Cluster_STATUS_RUNNING, deleteError: connect.NewError(connect.CodeNotFound, assert.AnError), wantDeleteCalls: 1, wantWaitCalls: 1},
		{name: "delete API failure", status: cksv1beta1.Cluster_STATUS_RUNNING, deleteError: connect.NewError(connect.CodeUnavailable, assert.AnError), wantError: "delete cluster", wantDeleteCalls: 1},
		{name: "post-delete polling failure", status: cksv1beta1.Cluster_STATUS_RUNNING, waitError: assert.AnError, wantError: "wait for cluster deletion", wantDeleteCalls: 1, wantWaitCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeClusterSweepClient{getResponses: tt.getResponses, getError: tt.getError, deleteError: tt.deleteError}
			waitCalls := 0
			options := testClusterSweepOptions(func(_ context.Context, _ clusterSweepClient, id string) error {
				waitCalls++
				assert.Equal(t, "listed-id", id)
				return tt.waitError
			})
			config, err := newClusterSweepConfig(client, testSweepZone, options)
			require.NoError(t, err)
			err = config.Delete(t.Context(), &cksv1beta1.Cluster{Id: "listed-id", Name: testSweepClusterName, Zone: testSweepZone, Status: tt.status})
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
			assert.Len(t, client.getIDs, tt.wantGetCalls)
			assert.Len(t, client.deletedIDs, tt.wantDeleteCalls)
			assert.Equal(t, tt.wantWaitCalls, waitCalls)
			for _, id := range append(client.getIDs, client.deletedIDs...) {
				assert.Equal(t, "listed-id", id)
			}
		})
	}
}

func testClusterSweepOptions(waitForDelete func(context.Context, clusterSweepClient, string) error) clusterSweepOptions {
	if waitForDelete == nil {
		waitForDelete = func(context.Context, clusterSweepClient, string) error { return nil }
	}
	return clusterSweepOptions{
		transitionRetryDelay: 0,
		deleteTimeout:        time.Second,
		waitTimeout:          time.Second,
		waitForDelete:        waitForDelete,
	}
}

var _ clusterSweepClient = (*coreweave.Client)(nil)

func TestMain(m *testing.M) {
	resource.TestMain(m)
}
