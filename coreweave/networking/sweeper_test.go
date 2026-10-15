package networking_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil/vpcsweeper"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const networkingVPCSweeperName = "coreweave_networking_vpc"

func normalizeNetworkingSweepZone(zone string) (string, error) {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return "", errors.New("networking VPC sweep zone must not be empty")
	}
	return zone, nil
}

func newNetworkingVPCSweeper() *resource.Sweeper {
	return &resource.Sweeper{
		Name:         networkingVPCSweeperName,
		Dependencies: []string{},
		F: func(zone string) error {
			zone, err := normalizeNetworkingSweepZone(zone)
			if err != nil {
				return err
			}
			runtime, err := testutil.SweepRuntimeFromEnv()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("build client: %w", err)
			}
			config, err := vpcsweeper.New(client, vpcsweeper.Config{Prefix: AcceptanceTestPrefix, Zone: zone})
			if err != nil {
				return err
			}
			return testutil.Sweep(ctx, runtime, config)
		},
	}
}

func init() {
	resource.AddTestSweepers(networkingVPCSweeperName, newNetworkingVPCSweeper())
}

func TestNetworkingVPCSweeperRegistration(t *testing.T) {
	sweeper := newNetworkingVPCSweeper()
	assert.Equal(t, networkingVPCSweeperName, sweeper.Name)
	assert.Empty(t, sweeper.Dependencies)
}

func TestNetworkingVPCSweeperRejectsBlankZoneBeforeSetup(t *testing.T) {
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "")
	unsetEnv(t, provider.CoreweaveApiEndpointEnvVar)
	t.Setenv("TEST_ACC_SWEEP_PARALLEL", "invalid")

	for _, tt := range []struct {
		name string
		zone string
	}{
		{name: "empty"},
		{name: "whitespace", zone: " \t\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualError(t, newNetworkingVPCSweeper().F(tt.zone), "networking VPC sweep zone must not be empty")
			_, endpointWasDefaulted := os.LookupEnv(provider.CoreweaveApiEndpointEnvVar)
			assert.False(t, endpointWasDefaulted)
		})
	}
}

func TestNetworkingVPCSweeperRejectsMalformedRuntimeBeforeSetup(t *testing.T) {
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "")
	unsetEnv(t, provider.CoreweaveApiEndpointEnvVar)
	t.Setenv("TEST_ACC_SWEEP_PARALLEL", "invalid")

	require.ErrorContains(t, newNetworkingVPCSweeper().F("US-LAB-01A"), "parse TEST_ACC_SWEEP_PARALLEL as integer")
	_, endpointWasDefaulted := os.LookupEnv(provider.CoreweaveApiEndpointEnvVar)
	assert.False(t, endpointWasDefaulted)
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "restored by testing")
	require.NoError(t, os.Unsetenv(name))
}

func TestMain(m *testing.M) {
	resource.TestMain(m)
}
