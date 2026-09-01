package cks_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateResourceNames(t *testing.T) {
	scenarios := []string{
		"cks-cluster",
		"cks-cluster-v6",
		"cks-tailscale",
		"cks-kubelet",
		"partial-oidc",
		"partial-webhook",
		"audit-policy",
		"shared",
		"migrated",
		"cks-sans",
	}
	fiveLowercaseHexDigits := regexp.MustCompile(`^[0-9a-f]{5}$`)

	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			names := generateResourceNames(t, scenario)
			clusterPrefix := AcceptanceTestPrefix + scenario + "-"

			require.True(t, strings.HasPrefix(names.ClusterName, clusterPrefix))
			suffix := strings.TrimPrefix(names.ClusterName, clusterPrefix)
			assert.Regexp(t, fiveLowercaseHexDigits, suffix)
			assert.Equal(t, cksVPCNamePrefix+suffix, names.VPCName)
			assert.True(t, strings.HasSuffix(names.ResourceName, "_"+suffix))
			assert.LessOrEqual(t, len(names.ClusterName), maxAcceptanceResourceNameLength)
			assert.LessOrEqual(t, len(names.VPCName), maxAcceptanceResourceNameLength)
		})
	}
}

func TestGenerateResourceNamesRejectsLongClusterName(t *testing.T) {
	assert.Panics(t, func() {
		generateResourceNames(t, "scenario-name-that-is-too-long")
	})
}
