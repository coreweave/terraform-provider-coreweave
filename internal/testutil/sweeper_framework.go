package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// SweeperConfig defines how to sweep a particular resource type T.
// It provides a generic, reusable interface for cleaning up test resources.
//
// Example usage:
//
//	err := SweepSequential(ctx, SweeperConfig[s3types.Bucket]{
//	    Lister: func(ctx context.Context) ([]s3types.Bucket, error) {
//	        resp, err := s3Client.ListBuckets(ctx, &s3.ListBucketsInput{})
//	        if err != nil {
//	            return nil, err
//	        }
//	        return resp.Buckets, nil
//	    },
//	    NameGetter: func(b s3types.Bucket) string { return aws.ToString(b.Name) },
//	    ZoneGetter: func(b s3types.Bucket) string { return "" },
//	    Deleter: func(ctx context.Context, bucket s3types.Bucket) error {
//	        return deleteBucket(ctx, s3Client, bucket)
//	    },
//	    Prefix:         "tf-acc-objs-",
//	    Zone:           zone,
//	    GlobalResource: true,
//	    Timeout:        30 * time.Minute,
//	})
type SweeperConfig[T any] struct {
	// Lister fetches all resources of type T from the API.
	// Should return an empty slice if no resources exist.
	Lister func(ctx context.Context) ([]T, error)

	// NameGetter extracts the resource name for filtering and logging.
	// This name is used for prefix matching and display in logs.
	NameGetter func(T) string

	// ZoneGetter extracts the zone/region for filtering and logging.
	// Only used if GlobalResource is false.
	// Return the zone/region string for the resource.
	ZoneGetter func(T) string

	// Deleter deletes a single resource.
	// Should be idempotent - returning nil if the resource is already deleted.
	// Should return error for actual failures.
	Deleter func(ctx context.Context, resource T) error

	// Prefix to filter resources by name (e.g., "test-acc-", "tf-acc-objs-").
	// Only resources whose names start with this prefix will be deleted.
	Prefix string

	// Zone to filter resources by (from sweeper parameter).
	// Only used if GlobalResource is false.
	// Empty string means "all zones".
	Zone string

	// GlobalResource indicates whether this resource type is global (not zone-specific).
	// Set to true for resources like S3 buckets or organization policies.
	// Set to false for zone-specific resources like VPCs or clusters.
	// When true, zone filtering is skipped entirely.
	GlobalResource bool

	// Timeout for the entire sweep operation.
	// Should account for listing + deleting all resources.
	Timeout time.Duration
}

// SweepSequential performs sequential deletion of resources matching the configuration.
// It lists all resources, filters by prefix and zone, and deletes them one by one.
//
// This implementation is simple, maintainable, and adequate for typical test workloads
// (10-20 resources taking 2-3 minutes). For high-performance scenarios with 100+ resources,
// use SweepConcurrent instead.
//
// The sweeper respects SweepDryRun() - when enabled, it logs what would be deleted
// without actually deleting resources.
//
// Error Handling:
//   - Fails fast on listing errors (can't proceed without resource list)
//   - Fails fast on deletion errors (indicates real problems that will break tests)
//   - Treats "already deleted" as success (idempotent operations)
//
// Returns nil on success, error on failure.
func SweepSequential[T any](ctx context.Context, config SweeperConfig[T]) error {
	// Validate configuration
	if !config.GlobalResource && config.Zone == "" {
		return fmt.Errorf("zone parameter is required for zone-specific resources (set GlobalResource: true for global resources)")
	}

	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	slog.InfoContext(ctx, "listing resources for sweep")
	resources, err := config.Lister(ctx)
	if err != nil {
		return fmt.Errorf("failed to list resources: %w", err)
	}

	slog.InfoContext(ctx, "found resources before filtering", "count", len(resources))

	filtered := make([]T, 0, len(resources))
	for _, resource := range resources {
		name := config.NameGetter(resource)

		if !strings.HasPrefix(name, config.Prefix) {
			continue
		}

		if !config.GlobalResource {
			zone := config.ZoneGetter(resource)
			if zone != config.Zone {
				slog.DebugContext(ctx, "skipping resource - zone mismatch", "name", name, "resource_zone", zone, "target_zone", config.Zone)
				continue
			}
		}

		filtered = append(filtered, resource)
	}

	slog.InfoContext(ctx, "filtered resources for deletion", "total", len(resources), "matching_prefix", len(filtered), "prefix", config.Prefix)

	if SweepDryRun() {
		for _, resource := range filtered {
			name := config.NameGetter(resource)
			zone := config.ZoneGetter(resource)
			if config.GlobalResource {
				zone = "global"
			}
			slog.InfoContext(ctx, "dry-run: would sweep resource", "name", name, "zone", zone, "global", config.GlobalResource)
		}
		slog.InfoContext(ctx, "dry-run complete - no resources deleted", "count", len(filtered))
		return nil
	}

	for i, resource := range filtered {
		name := config.NameGetter(resource)
		zone := config.ZoneGetter(resource)
		if config.GlobalResource {
			zone = "global"
		}

		slog.InfoContext(ctx, "deleting resource", "name", name, "zone", zone, "progress", fmt.Sprintf("%d/%d", i+1, len(filtered)))

		if err := config.Deleter(ctx, resource); err != nil {
			return fmt.Errorf("failed to delete resource %q: %w", name, err)
		}

		slog.InfoContext(ctx, "successfully deleted resource", "name", name)
	}

	slog.InfoContext(ctx, "sweep complete", "deleted", len(filtered))
	return nil
}
