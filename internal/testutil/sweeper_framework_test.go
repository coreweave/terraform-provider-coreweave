package testutil_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
)

// mockResource is a simple test resource
type mockResource struct {
	Name string
	Zone string
}

// TestSweepSequential uses table-driven tests to exercise all success and failure cases
func TestSweepSequential(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		resources      []mockResource
		prefix         string
		zone           string
		globalResource bool
		listerError    error
		deleterError   map[string]error // Map resource name to error
		expectError    *regexp.Regexp   // If set, expect error matching regex
		expectDeleted  []string         // Expected deleted resource names
	}{
		{
			name: "basic_prefix_filtering",
			resources: []mockResource{
				{Name: "test-acc-resource-1", Zone: "US-LAB-01A"},
				{Name: "test-acc-resource-2", Zone: "US-LAB-01A"},
				{Name: "prod-resource-1", Zone: "US-LAB-01A"}, // Wrong prefix
			},
			prefix:         "test-acc-",
			zone:           "US-LAB-01A",
			globalResource: false,
			expectDeleted:  []string{"test-acc-resource-1", "test-acc-resource-2"},
		},
		{
			name: "zone_filtering_matches",
			resources: []mockResource{
				{Name: "test-acc-resource-1", Zone: "US-EAST-01A"},
				{Name: "test-acc-resource-2", Zone: "US-WEST-02A"},
				{Name: "test-acc-resource-3", Zone: "US-EAST-01A"},
			},
			prefix:         "test-acc-",
			zone:           "US-EAST-01A",
			globalResource: false,
			expectDeleted:  []string{"test-acc-resource-1", "test-acc-resource-3"},
		},
		{
			name: "zone_filtering_no_matches",
			resources: []mockResource{
				{Name: "test-acc-resource-1", Zone: "US-EAST-01A"},
				{Name: "test-acc-resource-2", Zone: "US-EAST-01A"},
			},
			prefix:         "test-acc-",
			zone:           "EU-WEST-01A",
			globalResource: false,
			expectDeleted:  []string{}, // No matches in EU-WEST-01A
		},
		{
			name: "global_resource_ignores_zone",
			resources: []mockResource{
				{Name: "test-acc-bucket-1", Zone: ""},
				{Name: "test-acc-bucket-2", Zone: ""},
				{Name: "prod-bucket-1", Zone: ""},
			},
			prefix:         "test-acc-",
			zone:           "US-EAST-01A", // Should be ignored
			globalResource: true,
			expectDeleted:  []string{"test-acc-bucket-1", "test-acc-bucket-2"},
		},
		{
			name:          "no_resources",
			resources:     []mockResource{},
			prefix:        "test-acc-",
			zone:          "US-LAB-01A",
			expectDeleted: []string{},
		},
		{
			name: "no_matching_prefix",
			resources: []mockResource{
				{Name: "prod-resource-1", Zone: "US-LAB-01A"},
				{Name: "dev-resource-1", Zone: "US-LAB-01A"},
			},
			prefix:        "test-acc-",
			zone:          "US-LAB-01A",
			expectDeleted: []string{},
		},
		{
			name: "lister_error",
			resources: []mockResource{
				{Name: "test-acc-resource-1", Zone: "US-LAB-01A"},
			},
			prefix:      "test-acc-",
			zone:        "US-LAB-01A",
			listerError: fmt.Errorf("API connection failed"),
			expectError: regexp.MustCompile(`failed to list resources: API connection failed`),
		},
		{
			name: "deleter_error_first_resource",
			resources: []mockResource{
				{Name: "test-acc-resource-1", Zone: "US-LAB-01A"},
				{Name: "test-acc-resource-2", Zone: "US-LAB-01A"},
			},
			prefix: "test-acc-",
			zone:   "US-LAB-01A",
			deleterError: map[string]error{
				"test-acc-resource-1": fmt.Errorf("permission denied"),
			},
			expectError: regexp.MustCompile(`failed to delete resource "test-acc-resource-1": permission denied`),
		},
		{
			name: "deleter_error_second_resource",
			resources: []mockResource{
				{Name: "test-acc-resource-1", Zone: "US-LAB-01A"},
				{Name: "test-acc-resource-2", Zone: "US-LAB-01A"},
			},
			prefix: "test-acc-",
			zone:   "US-LAB-01A",
			deleterError: map[string]error{
				"test-acc-resource-2": fmt.Errorf("resource locked"),
			},
			expectDeleted: []string{"test-acc-resource-1"}, // First one succeeds
			expectError:   regexp.MustCompile(`failed to delete resource "test-acc-resource-2": resource locked`),
		},
		{
			name: "mixed_zones_global_resource",
			resources: []mockResource{
				{Name: "test-acc-policy-1", Zone: ""},
				{Name: "test-acc-policy-2", Zone: ""},
			},
			prefix:         "test-acc-",
			globalResource: true,
			expectDeleted:  []string{"test-acc-policy-1", "test-acc-policy-2"},
		},
		{
			name: "zone_required_for_zone_specific_resources",
			resources: []mockResource{
				{Name: "test-acc-resource-1", Zone: "US-EAST-01A"},
			},
			prefix:         "test-acc-",
			zone:           "", // Empty zone with globalResource: false should error
			globalResource: false,
			expectError:    regexp.MustCompile(`zone parameter is required for zone-specific resources`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			deleted := make([]string, 0)

			config := testutil.SweeperConfig[mockResource]{
				Lister: func(ctx context.Context) ([]mockResource, error) {
					if tt.listerError != nil {
						return nil, tt.listerError
					}
					return tt.resources, nil
				},
				NameGetter: func(r mockResource) string {
					return r.Name
				},
				ZoneGetter: func(r mockResource) string {
					return r.Zone
				},
				Deleter: func(ctx context.Context, resource mockResource) error {
					if tt.deleterError != nil {
						if err, ok := tt.deleterError[resource.Name]; ok {
							return err
						}
					}
					deleted = append(deleted, resource.Name)
					return nil
				},
				Prefix:         tt.prefix,
				Zone:           tt.zone,
				GlobalResource: tt.globalResource,
				Timeout:        10 * time.Second,
			}

			err := testutil.SweepSequential(t.Context(), config)

			if tt.expectError != nil {
				assert.Error(t, err, "Expected an error but got nil")
				assert.Regexp(t, tt.expectError, err.Error(), "Error message should match expected pattern")
				return
			}

			assert.NoError(t, err, "Expected no error but got: %v", err)
			assert.ElementsMatch(t, deleted, tt.expectDeleted, "Expected resources %v to be deleted, but got %v", tt.expectDeleted, deleted)
		})
	}
}

// TestSweepSequential_DryRun tests dry-run mode separately since it modifies global env var
func TestSweepSequential_DryRun(t *testing.T) {
	// NOTE: Cannot use t.Parallel() because we're modifying global env var
	os.Setenv("SWEEP_DRY_RUN", "true")
	defer os.Unsetenv("SWEEP_DRY_RUN")

	resources := []mockResource{
		{Name: "test-acc-resource-1", Zone: "US-EAST-01A"},
		{Name: "test-acc-resource-2", Zone: "US-WEST-02A"},
	}

	deleted := make([]string, 0)

	config := testutil.SweeperConfig[mockResource]{
		Lister: func(ctx context.Context) ([]mockResource, error) {
			return resources, nil
		},
		NameGetter: func(r mockResource) string {
			return r.Name
		},
		ZoneGetter: func(r mockResource) string {
			return r.Zone
		},
		Deleter: func(ctx context.Context, resource mockResource) error {
			deleted = append(deleted, resource.Name)
			return nil
		},
		Prefix:         "test-acc-",
		Zone:           "",
		GlobalResource: true, // Test global resource handling
		Timeout:        10 * time.Second,
	}

	err := testutil.SweepSequential(t.Context(), config)
	assert.NoError(t, err, "SweepSequential should succeed in dry-run mode")
	assert.Empty(t, deleted, "No resources should be deleted in dry-run mode")
}
