package containerregistry_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	containerregistry "github.com/coreweave/terraform-provider-coreweave/coreweave/container_registry"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testSweepNamespaceName = "namespaces/q-tfacc-cr-0123abcd"
	namespaceResourceType  = "coreweave_container_registry_namespace"
)

// registryTestPrefix requires a recognizable, explicitly selected test namespace prefix.
func registryTestPrefix() (string, error) {
	prefix := os.Getenv("COREWEAVE_CONTAINER_REGISTRY_TEST_PREFIX")
	if !regexp.MustCompile(`^([a-z0-9]+-)*tfacc-cr-$`).MatchString(prefix) || len(prefix) > 45 {
		return "", fmt.Errorf("COREWEAVE_CONTAINER_REGISTRY_TEST_PREFIX must be a namespace prefix ending in tfacc-cr- (maximum 45 characters)")
	}
	return prefix, nil
}

// sweepTarget scopes destructive cleanup to an exact zone and generated test name.
func sweepTarget(n *api.RegistryNamespace, prefix, zone string) bool {
	id := strings.TrimPrefix(n.Name, "namespaces/")
	return n.Zone == zone && regexp.MustCompile(`^`+regexp.QuoteMeta(prefix)+`[0-9a-f]{8}$`).MatchString(id)
}

// sweepRegistryNamespaces deletes only explicitly designated acceptance namespaces.
func sweepRegistryNamespaces(zone string) error {
	if os.Getenv("COREWEAVE_CONTAINER_REGISTRY_TEST_ZONE") == "" {
		log.Print("skipping registry sweep: no designated Container Registry test zone")
		return nil
	}
	if zone == "" || zone != os.Getenv("COREWEAVE_CONTAINER_REGISTRY_TEST_ZONE") {
		return fmt.Errorf("registry sweep zone must match COREWEAVE_CONTAINER_REGISTRY_TEST_ZONE")
	}
	prefix, err := registryTestPrefix()
	if err != nil {
		return err
	}
	if os.Getenv("COREWEAVE_API_ENDPOINT") == "" || os.Getenv("COREWEAVE_API_TOKEN") == "" {
		return fmt.Errorf("registry sweep requires an explicit endpoint and token")
	}
	runtime, err := testutil.SweepRuntimeFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	c, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
	if err != nil {
		return err
	}
	registry := c.ContainerRegistry
	return testutil.Sweep(ctx, runtime, testutil.SweepConfig[*api.RegistryNamespace]{
		ResourceType: namespaceResourceType,
		CreatedAt:    (*api.RegistryNamespace).GetCreateTime,
		// Collect every page before deleting so offset-based pagination cannot skip shifted rows.
		List: func(ctx context.Context) ([]*api.RegistryNamespace, error) {
			return containerregistry.ListNamespaces(ctx, registry)
		},
		Name:  func(n *api.RegistryNamespace) string { return n.Name },
		Match: func(n *api.RegistryNamespace) bool { return sweepTarget(n, prefix, zone) },
		Delete: func(ctx context.Context, n *api.RegistryNamespace) error {
			key, err := containerregistry.NewIdempotencyKey()
			if err != nil {
				return err
			}
			response, err := registry.DeleteRegistryNamespace(ctx, &api.DeleteRegistryNamespaceRequest{Name: n.Name, Force: true, IdempotencyKey: key})
			if coreweave.IsNotFoundError(err) {
				return nil
			}
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
			defer cancel()
			return containerregistry.WaitOperation(ctx, registry, response, &emptypb.Empty{})
		},
	})
}

// init registers actual namespace cleanup with the acceptance-test runner.
func init() {
	resource.AddTestSweepers(namespaceResourceType, &resource.Sweeper{Name: namespaceResourceType, F: sweepRegistryNamespaces})
}

// TestMain enables the testing helper's sweeper command-line flags.
func TestMain(m *testing.M) { resource.TestMain(m) }

// TestSweepScope rejects production names, different zones and partial-prefix matches.
func TestSweepScope(t *testing.T) {
	for _, tc := range []struct {
		name, zone string
		want       bool
	}{
		{testSweepNamespaceName, testutil.AcceptanceTestZone, true},
		{"namespaces/cw-bitnami", testutil.AcceptanceTestZone, false},
		{testSweepNamespaceName, "US-EAST-14A", false},
		{"namespaces/q-tfacc-cr-0123abcd-extra", testutil.AcceptanceTestZone, false},
	} {
		require.Equal(t, tc.want, sweepTarget(&api.RegistryNamespace{Name: tc.name, Zone: tc.zone}, "q-tfacc-cr-", testutil.AcceptanceTestZone))
	}
	t.Setenv("COREWEAVE_CONTAINER_REGISTRY_TEST_PREFIX", "cw-")
	_, err := registryTestPrefix()
	require.Error(t, err)
}

// TestSweeperDeletesOnlySelectedNamespace exercises registered cleanup against the Connect harness.
func TestSweeperDeletesOnlySelectedNamespace(t *testing.T) {
	h := newHarness(t, "namespace")
	t.Setenv("COREWEAVE_CONTAINER_REGISTRY_TEST_ZONE", testutil.AcceptanceTestZone)
	t.Setenv("COREWEAVE_CONTAINER_REGISTRY_TEST_PREFIX", "q-tfacc-cr-")
	t.Setenv("TEST_ACC_SWEEP_MIN_AGE", "2h")
	t.Setenv("SWEEP_DRY_RUN", "true")
	h.fake.namespace.Name = testSweepNamespaceName
	h.fake.namespace.CreateTime = timestamppb.New(time.Now().Add(-24 * time.Hour))
	require.NoError(t, sweepRegistryNamespaces(testutil.AcceptanceTestZone))
	require.Empty(t, h.fake.deletes)
	t.Setenv("SWEEP_DRY_RUN", "false")
	h.fake.namespace.CreateTime = timestamppb.Now()
	require.NoError(t, sweepRegistryNamespaces(testutil.AcceptanceTestZone))
	require.Empty(t, h.fake.deletes)
	h.fake.namespace.CreateTime = nil
	require.NoError(t, sweepRegistryNamespaces(testutil.AcceptanceTestZone))
	require.Empty(t, h.fake.deletes)
	h.fake.namespace.CreateTime = timestamppb.New(time.Now().Add(-24 * time.Hour))
	require.NoError(t, sweepRegistryNamespaces(testutil.AcceptanceTestZone))
	require.Len(t, h.fake.deletes, 1)
	require.True(t, h.fake.deletes[0].Force)
	require.Nil(t, h.fake.namespace)
}
