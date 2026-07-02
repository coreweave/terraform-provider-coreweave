package objectstorage_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// inventoryDestinationPolicy grants the S3 inventory service write access to
// the destination bucket so PutBucketInventoryConfiguration is accepted. It
// mirrors the allow-all policy used by the bucket policy acceptance tests.
const inventoryDestinationPolicy = `{"Statement":[{"Action":["s3:*"],"Effect":"Allow","Principal":{"CW":"*"},"Resource":["arn:aws:s3:::*"],"Sid":"allow-inventory"}],"Version":"2012-10-17"}`

type inventoryTestConfig struct {
	name             string
	resourceName     string
	bucket           objectstorage.BucketResourceModel
	inventory        objectstorage.BucketInventoryResourceModel
	configPlanChecks resource.ConfigPlanChecks
}

// createInventoryTestStep renders a bucket + inventory config and builds a test
// step that asserts the resulting state matches the requested inventory model.
func createInventoryTestStep(ctx context.Context, t *testing.T, opts inventoryTestConfig) resource.TestStep {
	t.Helper()

	bucketHCL := objectstorage.MustRenderBucketResource(ctx, "test_bucket", &opts.bucket)

	// The S3 inventory service must be able to PutObject to the destination
	// bucket, or PutBucketInventoryConfiguration is rejected with
	// IllegalInventoryConfigurationException. Grant that access via a bucket
	// policy on the destination (here the same bucket as the source).
	policyName := opts.resourceName + "_dest"
	policyHCL := objectstorage.MustRenderBucketPolicyResource(ctx, policyName, &objectstorage.BucketPolicyResourceModel{
		Bucket: types.StringValue(fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket")),
		Policy: types.StringValue(inventoryDestinationPolicy),
	})

	inv := opts.inventory
	inv.Bucket = types.StringValue(fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket"))
	// depends_on the destination policy so it is in place before the inventory
	// service validates it can write reports to the destination.
	invHCL := objectstorage.MustRenderBucketInventoryResource(ctx, opts.resourceName, &inv,
		fmt.Sprintf("coreweave_object_storage_bucket_policy.%s", policyName))

	config := bucketHCL + policyHCL + invHCL
	rs := fmt.Sprintf("coreweave_object_storage_bucket_inventory.%s", opts.resourceName)

	checks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("name"), knownvalue.StringExact(inv.Name.ValueString())),
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("included_object_versions"), knownvalue.StringExact(inv.IncludedObjectVersions.ValueString())),
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("enabled"), knownvalue.Bool(inv.Enabled.ValueBool())),
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("schedule").AtMapKey("frequency"), knownvalue.StringExact(inv.Schedule.Frequency.ValueString())),
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("destination").AtMapKey("bucket").AtMapKey("format"), knownvalue.StringExact(inv.Destination.Bucket.Format.ValueString())),
	}

	// optional_fields (set) — assert exact membership, including LastAccessedDate.
	if !inv.OptionalFields.IsNull() {
		var fields []string
		inv.OptionalFields.ElementsAs(ctx, &fields, false)
		elems := make([]knownvalue.Check, 0, len(fields))
		for _, f := range fields {
			elems = append(elems, knownvalue.StringExact(f))
		}
		checks = append(checks, statecheck.ExpectKnownValue(rs, tfjsonpath.New("optional_fields"), knownvalue.SetExact(elems)))
	}

	return resource.TestStep{
		PreConfig: func() {
			t.Logf("beginning coreweave_object_storage_bucket_inventory %s step", opts.name)
		},
		Config:            config,
		ConfigPlanChecks:  opts.configPlanChecks,
		ConfigStateChecks: checks,
		Check: func(*terraform.State) error {
			t.Logf("completed coreweave_object_storage_bucket_inventory %s step", opts.name)
			return nil
		},
	}
}

// TestBucketInventory exercises the full CRUD lifecycle:
//   - create
//   - re-apply the same config and assert an EMPTY plan (no spurious updates)
//   - change optional_fields + schedule and assert an UPDATE is planned & applied
//   - re-apply the changed config and assert an EMPTY plan again
//   - import round-trip
func TestBucketInventoryBasic(t *testing.T) {
	ctx := context.Background()

	randomInt := rand.IntN(100000)
	bucket := objectstorage.BucketResourceModel{
		Name: types.StringValue(fmt.Sprintf("%sinv-bucket-%x", AcceptanceTestPrefix, randomInt)),
		Zone: types.StringValue("US-LAB-01A"),
	}

	resourceName := "test_inv"
	rs := fmt.Sprintf("coreweave_object_storage_bucket_inventory.%s", resourceName)

	// destination is the same bucket as the source; the S3 inventory API expects
	// the destination bucket as an ARN.
	destArn := fmt.Sprintf("arn:aws:s3:::%s", bucket.Name.ValueString())

	base := objectstorage.BucketInventoryResourceModel{
		Name:                   types.StringValue("daily-report"),
		Enabled:                types.BoolValue(true),
		IncludedObjectVersions: types.StringValue("All"),
		OptionalFields: types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("Size"),
			types.StringValue("LastAccessedDate"),
		}),
		Schedule: &objectstorage.ScheduleModel{Frequency: types.StringValue("Daily")},
		Destination: &objectstorage.DestinationModel{
			Bucket: &objectstorage.BucketModel{
				BucketArn: types.StringValue(destArn),
				Format:    types.StringValue("CSV"),
			},
		},
	}

	// updated changes both a scalar-ish field (schedule frequency) and the set
	// (adds ETag) to prove a real diff is detected and applied.
	updated := base
	updated.OptionalFields = types.SetValueMust(types.StringType, []attr.Value{
		types.StringValue("Size"),
		types.StringValue("LastAccessedDate"),
		types.StringValue("ETag"),
	})
	updated.Schedule = &objectstorage.ScheduleModel{Frequency: types.StringValue("Weekly")}

	steps := []resource.TestStep{
		// 1. create
		createInventoryTestStep(ctx, t, inventoryTestConfig{
			name:         "create",
			resourceName: resourceName,
			bucket:       bucket,
			inventory:    base,
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(rs, plancheck.ResourceActionCreate),
				},
			},
		}),
		// 2. no-op: identical config must produce an EMPTY plan (no spurious update)
		createInventoryTestStep(ctx, t, inventoryTestConfig{
			name:         "no-op after create",
			resourceName: resourceName,
			bucket:       bucket,
			inventory:    base,
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				},
			},
		}),
		// 3. update: changed config must plan and apply an in-place update
		createInventoryTestStep(ctx, t, inventoryTestConfig{
			name:         "update",
			resourceName: resourceName,
			bucket:       bucket,
			inventory:    updated,
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(rs, plancheck.ResourceActionUpdate),
				},
			},
		}),
		// 4. no-op after update: still stable, no drift
		createInventoryTestStep(ctx, t, inventoryTestConfig{
			name:         "no-op after update",
			resourceName: resourceName,
			bucket:       bucket,
			inventory:    updated,
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				},
			},
		}),
		// 5. import round-trip using the "<bucket>:<name>" composite ID
		{
			PreConfig: func() {
				t.Logf("beginning coreweave_object_storage_bucket_inventory import step")
			},
			ResourceName:                         rs,
			ImportState:                          true,
			ImportStateVerify:                    true,
			ImportStateVerifyIdentifierAttribute: "name",
			ImportStateId:                        fmt.Sprintf("%s:%s", bucket.Name.ValueString(), updated.Name.ValueString()),
			ImportStateCheck: func([]*terraform.InstanceState) error {
				t.Logf("completed coreweave_object_storage_bucket_inventory import step")
				return nil
			},
		},
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckInventoryDestroy(ctx),
		Steps:                    steps,
	})
}

// TestBucketInventoryDisappears verifies the Read path's drift detection: after
// the config is deleted out-of-band, the next refresh must notice it is gone
// (Read -> RemoveResource) and plan to recreate it, producing a non-empty plan.
func TestBucketInventoryDisappears(t *testing.T) {
	ctx := context.Background()

	randomInt := rand.IntN(100000)
	bucket := objectstorage.BucketResourceModel{
		Name: types.StringValue(fmt.Sprintf("%sinv-disp-%x", AcceptanceTestPrefix, randomInt)),
		Zone: types.StringValue("US-LAB-01A"),
	}

	resourceName := "test_inv_disappear"
	inventoryName := "daily-report"
	destArn := fmt.Sprintf("arn:aws:s3:::%s", bucket.Name.ValueString())

	inv := objectstorage.BucketInventoryResourceModel{
		Bucket:                 types.StringValue(fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket")),
		Name:                   types.StringValue(inventoryName),
		Enabled:                types.BoolValue(true),
		IncludedObjectVersions: types.StringValue("All"),
		OptionalFields: types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("LastAccessedDate"),
		}),
		Schedule: &objectstorage.ScheduleModel{Frequency: types.StringValue("Daily")},
		Destination: &objectstorage.DestinationModel{
			Bucket: &objectstorage.BucketModel{
				BucketArn: types.StringValue(destArn),
				Format:    types.StringValue("CSV"),
			},
		},
	}

	// Grant the inventory service write access to the destination bucket, and
	// order the inventory after it, so PutBucketInventoryConfiguration is accepted.
	policyName := resourceName + "_dest"
	policyHCL := objectstorage.MustRenderBucketPolicyResource(ctx, policyName, &objectstorage.BucketPolicyResourceModel{
		Bucket: types.StringValue(fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket")),
		Policy: types.StringValue(inventoryDestinationPolicy),
	})

	config := objectstorage.MustRenderBucketResource(ctx, "test_bucket", &bucket) +
		policyHCL +
		objectstorage.MustRenderBucketInventoryResource(ctx, resourceName, &inv,
			fmt.Sprintf("coreweave_object_storage_bucket_policy.%s", policyName))

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckInventoryDestroy(ctx),
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					t.Logf("beginning coreweave_object_storage_bucket_inventory disappears step")
				},
				Config: config,
				// Delete the inventory configuration behind Terraform's back, then
				// let the built-in post-apply refresh+plan run. Read should detect
				// the disappearance and plan a recreate (hence a non-empty plan).
				Check: resource.ComposeAggregateTestCheckFunc(
					func(*terraform.State) error {
						t.Logf("completed apply; deleting inventory %q out of band", inventoryName)
						return nil
					},
					deleteInventoryOutOfBand(ctx, t, bucket.Name.ValueString(), inventoryName),
					func(*terraform.State) error {
						t.Logf("completed out-of-band delete; expecting non-empty plan on refresh")
						return nil
					},
				),
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// testAccCheckInventoryDestroy confirms every inventory configuration in state
// is gone from the API after destroy — the assertion that Delete actually
// removed the remote resource.
func testAccCheckInventoryDestroy(ctx context.Context) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		testutil.SetEnvDefaults()
		client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
		if err != nil {
			return fmt.Errorf("failed to build client: %w", err)
		}
		s3c, err := client.S3Client(ctx, "")
		if err != nil {
			return fmt.Errorf("failed to create S3 client: %w", err)
		}
		fmt.Printf("[testAccCheckInventoryDestroy] checking for remaining inventory configurations in state after destroy")
		for _, rs := range s.RootModule().Resources {
			if rs.Type != "coreweave_object_storage_bucket_inventory" {
				continue
			}
			bucket := rs.Primary.Attributes["bucket"]
			id := rs.Primary.Attributes["name"]

			_, err := s3c.GetBucketInventoryConfiguration(ctx, &s3.GetBucketInventoryConfigurationInput{
				Bucket: aws.String(bucket),
				Id:     aws.String(id),
			})
			if err == nil {
				return fmt.Errorf("inventory configuration %q on bucket %q still exists after destroy", id, bucket)
			}
			// The config (or the whole bucket) being gone is the expected outcome.
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) &&
				(apiErr.ErrorCode() == objectstorage.ErrNoSuchInventoryConfiguration || apiErr.ErrorCode() == objectstorage.ErrNoSuchBucket) {
				continue
			}
			return fmt.Errorf("unexpected error checking inventory %q on bucket %q: %w", id, bucket, err)
		}
		return nil
	}
}

// deleteInventoryOutOfBand deletes an inventory configuration directly via the
// API, simulating drift caused by an external actor.
func deleteInventoryOutOfBand(ctx context.Context, t *testing.T, bucket, id string) resource.TestCheckFunc {
	t.Helper()
	return func(*terraform.State) error {
		testutil.SetEnvDefaults()
		client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
		if err != nil {
			return fmt.Errorf("failed to build client: %w", err)
		}
		fmt.Printf("[deleteInventoryOutOfBand] Deleting inventory configuration %q on bucket %q out of band", id, bucket)
		s3c, err := client.S3Client(ctx, "")
		if err != nil {
			return fmt.Errorf("failed to create S3 client: %w", err)
		}
		if _, err := s3c.DeleteBucketInventoryConfiguration(ctx, &s3.DeleteBucketInventoryConfigurationInput{
			Bucket: aws.String(bucket),
			Id:     aws.String(id),
		}); err != nil {
			return fmt.Errorf("failed to delete inventory %q on bucket %q out of band: %w", id, bucket, err)
		}
		fmt.Printf("[deleteInventoryOutOfBand] Deleted")
		return nil
	}
}
