package objectstorage_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

type bucketVersioningTestConfig struct {
	name             string
	resourceName     string
	bucket           objectstorage.BucketResourceModel
	bucketVersioning objectstorage.VersioningConfigurationModel
	configPlanChecks resource.ConfigPlanChecks
}

func createBucketVersioningTestStep(ctx context.Context, t *testing.T, opts bucketVersioningTestConfig) resource.TestStep {
	t.Helper()

	// Render bucket + versioning HCL
	bucketHCL := objectstorage.MustRenderBucketResource(ctx, "test_bucket", &opts.bucket)
	versioningHCL := objectstorage.MustRenderBucketVersioningResource(ctx, opts.resourceName, &objectstorage.BucketVersioningResourceModel{
		Bucket: types.StringValue(
			fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket"),
		),
		VersioningConfiguration: opts.bucketVersioning,
	})
	config := bucketHCL + versioningHCL

	rs := fmt.Sprintf("coreweave_object_storage_bucket_versioning.%s", opts.resourceName)
	checks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("bucket"), knownvalue.StringExact(opts.bucket.Name.ValueString())),
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("versioning_configuration").AtMapKey("status"), knownvalue.StringExact(opts.bucketVersioning.Status.ValueString())),
	}

	return resource.TestStep{
		PreConfig: func() {
			t.Logf("beginning coreweave_object_storage_bucket_versioning %s test", opts.name)
		},
		Config:            config,
		ConfigStateChecks: checks,
		ConfigPlanChecks:  opts.configPlanChecks,
	}
}

func TestBucketVersioningResource(t *testing.T) {
	ctx := context.Background()

	randomInt := rand.IntN(100)
	bucket := objectstorage.BucketResourceModel{
		Name: types.StringValue(fmt.Sprintf("tf-acc-bucket-versioning-%d", randomInt)),
		Zone: types.StringValue("US-EAST-04A"),
	}
	resourceName := "test_versioning"
	steps := []resource.TestStep{
		createBucketVersioningTestStep(ctx, t, bucketVersioningTestConfig{
			name:         "enabled",
			resourceName: resourceName,
			bucket:       bucket,
			bucketVersioning: objectstorage.VersioningConfigurationModel{
				Status: types.StringValue("Enabled"),
			},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_versioning.%s", resourceName), plancheck.ResourceActionCreate),
				},
			},
		}),
		createBucketVersioningTestStep(ctx, t, bucketVersioningTestConfig{
			name:         "suspended",
			resourceName: resourceName,
			bucket:       bucket,
			bucketVersioning: objectstorage.VersioningConfigurationModel{
				Status: types.StringValue("Suspended"),
			},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_versioning.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		{
			PreConfig: func() {
				t.Log("Beginning coreweave_object_storage_bucket_versioning import test")
			},
			ResourceName:                         fmt.Sprintf("coreweave_object_storage_bucket_versioning.%s", resourceName),
			ImportState:                          true,
			ImportStateVerifyIdentifierAttribute: "name",
			ImportStateId:                        bucket.Name.ValueString(),
		},
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps:                    steps,
	})
}
