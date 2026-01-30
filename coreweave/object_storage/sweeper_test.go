package objectstorage_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
)

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

			s3Client, err := client.S3Client(ctx, "")
			if err != nil {
				return fmt.Errorf("failed to create S3 client: %w", err)
			}

			return testutil.SweepSequential(ctx, testutil.SweeperConfig[s3types.Bucket]{
				Lister: func(ctx context.Context) ([]s3types.Bucket, error) {
					resp, err := s3Client.ListBuckets(ctx, &s3.ListBucketsInput{})
					if err != nil {
						return nil, err
					}
					return resp.Buckets, nil
				},
				NameGetter: func(b s3types.Bucket) string {
					return aws.ToString(b.Name)
				},
				ZoneGetter: func(b s3types.Bucket) string {
					return ""
				},
				Deleter: func(ctx context.Context, bucket s3types.Bucket) error {
					return deleteBucket(ctx, s3Client, *bucket.Name)
				},
				Prefix:         AcceptanceTestPrefix,
				Zone:           zone, // Ignored for global resources
				GlobalResource: true, // S3 buckets are global
				Timeout:        30 * time.Minute,
			})
		},
	})

	resource.AddTestSweepers("coreweave_object_storage_organization_access_policy", &resource.Sweeper{
		Name:         "coreweave_object_storage_organization_access_policy",
		Dependencies: []string{},
		F: func(zone string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			return testutil.SweepSequential(ctx, testutil.SweeperConfig[*cwobjectv1.CWObjectPolicy]{
				Lister: func(ctx context.Context) ([]*cwobjectv1.CWObjectPolicy, error) {
					resp, err := client.ListAccessPolicies(ctx, connect.NewRequest(&cwobjectv1.ListAccessPoliciesRequest{}))
					if err != nil {
						return nil, err
					}
					return resp.Msg.Policies, nil
				},
				NameGetter: func(p *cwobjectv1.CWObjectPolicy) string {
					return p.Name
				},
				Deleter: func(ctx context.Context, policy *cwobjectv1.CWObjectPolicy) error {
					_, err := client.DeleteAccessPolicy(ctx, connect.NewRequest(&cwobjectv1.DeleteAccessPolicyRequest{
						Name: policy.Name,
					}))
					if connect.CodeOf(err) == connect.CodeNotFound {
						return nil // Already deleted - idempotent
					}
					return err
				},
				Prefix:         AcceptanceTestPrefix,
				Zone:           zone,
				GlobalResource: true,
				Timeout:        10 * time.Minute,
			})
		},
	})
}

func deleteBucket(ctx context.Context, s3Client *s3.Client, bucketName string) error {
	slog.InfoContext(ctx, "deleting bucket", "bucket", bucketName)

	_, err := s3Client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})

	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchBucket" {
			slog.InfoContext(ctx, "bucket already deleted", "bucket", bucketName)
			return nil
		}
		return fmt.Errorf("failed to delete bucket %q: %w", bucketName, err)
	}

	slog.InfoContext(ctx, "successfully deleted bucket", "bucket", bucketName)
	return nil
}

func TestMain(m *testing.M) {
	resource.TestMain(m)
}
