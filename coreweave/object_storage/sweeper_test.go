package objectstorage_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestMain(m *testing.M) {
	resource.TestMain(m)
}

func init() {
	resource.AddTestSweepers("coreweave_object_storage_bucket", &resource.Sweeper{
		Name:         "coreweave_object_storage_bucket",
		Dependencies: []string{},
		F: func(zone string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.ListBucketInfo(ctx, connect.NewRequest(&cwobjectv1.ListBucketInfoRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list buckets: %w", err)
			}

			for _, info := range listResp.Msg.GetInfo() {
				name := info.GetName()
				location := info.GetLocation()

				if !strings.HasPrefix(name, AcceptanceTestPrefix) {
					log.Printf("skipping bucket %s because it does not have prefix %s", name, AcceptanceTestPrefix)
					continue
				}
				if location != zone {
					log.Printf("skipping bucket %s in zone %s because it does not match sweep zone %s", name, location, zone)
					continue
				}

				log.Printf("sweeping bucket %s (zone %s)", name, location)
				if testutil.SweepDryRun() {
					log.Printf("skipping bucket %s because of dry-run mode", name)
					continue
				}

				if err := deleteBucket(ctx, client, name, location); err != nil {
					return fmt.Errorf("failed to delete bucket %s: %w", name, err)
				}
			}

			return nil
		},
	})

	// Organization access policies are org-scoped (not zone-scoped). Sweep them
	// unconditionally on any zone sweep — the prefix filter keeps the blast
	// radius limited to acceptance-test artifacts.
	resource.AddTestSweepers("coreweave_object_storage_organization_access_policy", &resource.Sweeper{
		Name:         "coreweave_object_storage_organization_access_policy",
		Dependencies: []string{},
		F: func(_ string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.ListAccessPolicies(ctx, connect.NewRequest(&cwobjectv1.ListAccessPoliciesRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list org access policies: %w", err)
			}

			for _, policy := range listResp.Msg.GetPolicies() {
				name := policy.GetName()
				if !strings.HasPrefix(name, AcceptanceTestPrefix) {
					log.Printf("skipping org access policy %s because it does not have prefix %s", name, AcceptanceTestPrefix)
					continue
				}

				log.Printf("sweeping org access policy %s", name)
				if testutil.SweepDryRun() {
					log.Printf("skipping org access policy %s because of dry-run mode", name)
					continue
				}

				_, err := client.DeleteAccessPolicy(ctx, connect.NewRequest(&cwobjectv1.DeleteAccessPolicyRequest{Name: name}))
				if err != nil {
					if coreweave.IsNotFoundError(err) {
						log.Printf("org access policy %s already deleted", name)
						continue
					}
					return fmt.Errorf("failed to delete org access policy %s: %w", name, err)
				}
			}

			return nil
		},
	})
}

// deleteBucket empties a bucket (all object versions, delete markers, and
// in-flight multipart uploads) and then deletes the bucket itself. Test buckets
// generally don't contain objects, but versioning can leave delete markers
// behind, and a botched test can leave the bucket in a state where DeleteBucket
// returns BucketNotEmpty.
func deleteBucket(ctx context.Context, client *coreweave.Client, name, zone string) error {
	s3c, err := client.S3Client(ctx, zone)
	if err != nil {
		return fmt.Errorf("failed to create S3 client for zone %s: %w", zone, err)
	}

	if err := emptyBucket(ctx, s3c, name); err != nil {
		return fmt.Errorf("failed to empty bucket %s: %w", name, err)
	}

	if _, err := s3c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(name)}); err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == objectstorage.ErrNoSuchBucket {
			log.Printf("bucket %s already deleted", name)
			return nil
		}
		return fmt.Errorf("DeleteBucket %s: %w", name, err)
	}
	return nil
}

// emptyBucket removes all object versions, delete markers, and in-flight
// multipart uploads from a bucket. Buckets with versioning enabled need this
// before DeleteBucket will succeed.
func emptyBucket(ctx context.Context, s3c *s3.Client, name string) error {
	// Object versions and delete markers
	versionsPaginator := s3.NewListObjectVersionsPaginator(s3c, &s3.ListObjectVersionsInput{Bucket: aws.String(name)})
	for versionsPaginator.HasMorePages() {
		page, err := versionsPaginator.NextPage(ctx)
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == objectstorage.ErrNoSuchBucket {
				return nil
			}
			return fmt.Errorf("ListObjectVersions: %w", err)
		}

		ids := make([]s3types.ObjectIdentifier, 0, len(page.Versions)+len(page.DeleteMarkers))
		for _, v := range page.Versions {
			ids = append(ids, s3types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, m := range page.DeleteMarkers {
			ids = append(ids, s3types.ObjectIdentifier{Key: m.Key, VersionId: m.VersionId})
		}
		if len(ids) == 0 {
			continue
		}

		if _, err := s3c.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(name),
			Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		}); err != nil {
			return fmt.Errorf("DeleteObjects: %w", err)
		}
	}

	// In-flight multipart uploads
	uploadsPaginator := s3.NewListMultipartUploadsPaginator(s3c, &s3.ListMultipartUploadsInput{Bucket: aws.String(name)})
	for uploadsPaginator.HasMorePages() {
		page, err := uploadsPaginator.NextPage(ctx)
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == objectstorage.ErrNoSuchBucket {
				return nil
			}
			return fmt.Errorf("ListMultipartUploads: %w", err)
		}
		for _, u := range page.Uploads {
			if _, err := s3c.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(name),
				Key:      u.Key,
				UploadId: u.UploadId,
			}); err != nil {
				return fmt.Errorf("AbortMultipartUpload %s: %w", aws.ToString(u.Key), err)
			}
		}
	}

	return nil
}
