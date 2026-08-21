package objectstorage_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	bucketSweeperName          = "coreweave_object_storage_bucket"
	orgAccessPolicySweeperName = "coreweave_object_storage_organization_access_policy"
	nonAcceptanceTestName      = "production"
)

func normalizeObjectStorageSweepRegion(region string) (string, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return "", errors.New("object storage sweep region must not be empty")
	}
	return region, nil
}

func newBucketSweepConfig(client *coreweave.Client, zone string) (testutil.SweepConfig[*cwobjectv1.BucketInfo], error) {
	zone, err := normalizeObjectStorageSweepRegion(zone)
	if err != nil {
		return testutil.SweepConfig[*cwobjectv1.BucketInfo]{}, err
	}

	return testutil.SweepConfig[*cwobjectv1.BucketInfo]{
		ResourceType: bucketSweeperName,
		List: func(ctx context.Context) ([]*cwobjectv1.BucketInfo, error) {
			response, err := client.ListBucketInfo(ctx, connect.NewRequest(&cwobjectv1.ListBucketInfoRequest{}))
			if err != nil {
				return nil, fmt.Errorf("failed to list buckets: %w", err)
			}
			return response.Msg.GetInfo(), nil
		},
		Name: func(info *cwobjectv1.BucketInfo) string { return info.GetName() },
		Match: func(info *cwobjectv1.BucketInfo) bool {
			return strings.HasPrefix(info.GetName(), AcceptanceTestPrefix) && info.GetLocation() == zone
		},
		Delete: func(ctx context.Context, info *cwobjectv1.BucketInfo) error {
			if err := deleteBucket(ctx, client, info.GetName(), info.GetLocation()); err != nil {
				return fmt.Errorf("failed to delete bucket %s: %w", info.GetName(), err)
			}
			return nil
		},
	}, nil
}

func newOrgAccessPolicySweepConfig(client *coreweave.Client) testutil.SweepConfig[*cwobjectv1.CWObjectPolicy] {
	return testutil.SweepConfig[*cwobjectv1.CWObjectPolicy]{
		ResourceType: orgAccessPolicySweeperName,
		List: func(ctx context.Context) ([]*cwobjectv1.CWObjectPolicy, error) {
			response, err := client.ListAccessPolicies(ctx, connect.NewRequest(&cwobjectv1.ListAccessPoliciesRequest{}))
			if err != nil {
				return nil, fmt.Errorf("failed to list org access policies: %w", err)
			}
			return response.Msg.GetPolicies(), nil
		},
		Name: func(policy *cwobjectv1.CWObjectPolicy) string { return policy.GetName() },
		Match: func(policy *cwobjectv1.CWObjectPolicy) bool {
			return strings.HasPrefix(policy.GetName(), AcceptanceTestPrefix)
		},
		Delete: func(ctx context.Context, policy *cwobjectv1.CWObjectPolicy) error {
			_, err := client.DeleteAccessPolicy(ctx, connect.NewRequest(&cwobjectv1.DeleteAccessPolicyRequest{Name: policy.GetName()}))
			if coreweave.IsNotFoundError(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed to delete org access policy %s: %w", policy.GetName(), err)
			}
			return nil
		},
	}
}

func newBucketSweeper() *resource.Sweeper {
	return &resource.Sweeper{
		Name:         bucketSweeperName,
		Dependencies: []string{},
		F: func(zone string) error {
			zone, err := normalizeObjectStorageSweepRegion(zone)
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
				return fmt.Errorf("failed to build client: %w", err)
			}
			config, err := newBucketSweepConfig(client, zone)
			if err != nil {
				return err
			}
			return testutil.Sweep(ctx, runtime, config)
		},
	}
}

func newOrgAccessPolicySweeper() *resource.Sweeper {
	return &resource.Sweeper{
		Name:         orgAccessPolicySweeperName,
		Dependencies: []string{},
		F: func(region string) error {
			if _, err := normalizeObjectStorageSweepRegion(region); err != nil {
				return err
			}
			runtime, err := testutil.SweepRuntimeFromEnv()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}
			return testutil.Sweep(ctx, runtime, newOrgAccessPolicySweepConfig(client))
		},
	}
}

func TestMain(m *testing.M) {
	resource.TestMain(m)
}

func init() {
	resource.AddTestSweepers(bucketSweeperName, newBucketSweeper())

	// Organization access policies are org-scoped (not zone-scoped). Sweep them
	// unconditionally on any zone sweep — the prefix filter keeps the blast
	// radius limited to acceptance-test artifacts.
	resource.AddTestSweepers(orgAccessPolicySweeperName, newOrgAccessPolicySweeper())
}

func TestObjectStorageSweeperRegistrations(t *testing.T) {
	tests := []struct {
		name    string
		sweeper *resource.Sweeper
	}{
		{name: bucketSweeperName, sweeper: newBucketSweeper()},
		{name: orgAccessPolicySweeperName, sweeper: newOrgAccessPolicySweeper()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.name, tt.sweeper.Name)
			assert.Empty(t, tt.sweeper.Dependencies)
			assert.NotNil(t, tt.sweeper.F)
		})
	}
}

func TestObjectStorageSweeperValidationPrecedesSetup(t *testing.T) {
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "")
	t.Setenv(provider.CoreweaveApiEndpointEnvVar, "restored by testing")
	require.NoError(t, os.Unsetenv(provider.CoreweaveApiEndpointEnvVar))
	t.Setenv("TEST_ACC_SWEEP_PARALLEL", "invalid")

	tests := []struct {
		name      string
		callback  func(string) error
		selector  string
		wantError string
	}{
		{name: "bucket/blank selector", callback: newBucketSweeper().F, selector: " \t\n", wantError: "object storage sweep region must not be empty"},
		{name: "bucket/invalid runtime", callback: newBucketSweeper().F, selector: "zone-a", wantError: "parse TEST_ACC_SWEEP_PARALLEL as integer"},
		{name: "org policy/blank selector", callback: newOrgAccessPolicySweeper().F, selector: " \t\n", wantError: "object storage sweep region must not be empty"},
		{name: "org policy/invalid runtime", callback: newOrgAccessPolicySweeper().F, selector: "zone-a", wantError: "parse TEST_ACC_SWEEP_PARALLEL as integer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorContains(t, tt.callback(tt.selector), tt.wantError)
		})
	}

	_, found := os.LookupEnv(provider.CoreweaveApiEndpointEnvVar)
	require.False(t, found, "client setup must not run before region and runtime validation")
}

func TestBucketSweepConfigMatch(t *testing.T) {
	const zone = "zone-a"
	config, err := newBucketSweepConfig(nil, " \t"+zone+"\n")
	require.NoError(t, err)

	tests := []struct {
		name string
		info *cwobjectv1.BucketInfo
		want bool
	}{
		{name: "acceptance prefix and zone", info: &cwobjectv1.BucketInfo{Name: AcceptanceTestPrefix + "selected", Location: zone}, want: true},
		{name: "non-acceptance prefix", info: &cwobjectv1.BucketInfo{Name: nonAcceptanceTestName, Location: zone}},
		{name: "different zone", info: &cwobjectv1.BucketInfo{Name: AcceptanceTestPrefix + "other-zone", Location: "zone-b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, config.Match(tt.info))
		})
	}
}

func TestOrgAccessPolicySweepConfigMatch(t *testing.T) {
	config := newOrgAccessPolicySweepConfig(nil)
	assert.True(t, config.Match(&cwobjectv1.CWObjectPolicy{Name: AcceptanceTestPrefix + "policy"}))
	assert.False(t, config.Match(&cwobjectv1.CWObjectPolicy{Name: nonAcceptanceTestName}))
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
