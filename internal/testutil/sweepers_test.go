package testutil_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/coreweave/cks"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/networking"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
)

func TestGetTerraformResourceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		testfunc func() string
		expected string
	}{
		{
			name:     "CKS Cluster resource",
			testfunc: testutil.MustGetTerraformResourceName[*cks.ClusterResource],
			expected: "coreweave_cks_cluster",
		},
		{
			name:     "Networking VPC Resource",
			testfunc: testutil.MustGetTerraformResourceName[*networking.VpcResource],
			expected: "coreweave_networking_vpc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.testfunc()
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}
