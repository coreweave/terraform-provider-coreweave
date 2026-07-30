package objectstorage_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestBucketSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaRequest := fwresource.SchemaRequest{}
	schemaResponse := &fwresource.SchemaResponse{}

	objectstorage.NewBucketResource().Schema(ctx, schemaRequest, schemaResponse)

	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema method diagnostics: %+v", schemaResponse.Diagnostics)
	}

	// Validate the schema
	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)

	if diagnostics.HasError() {
		t.Fatalf("Schema validation diagnostics: %+v", diagnostics)
	}
}

type bucketTestStep struct {
	TestName         string
	ResourceName     string
	Bucket           objectstorage.BucketResourceModel
	ConfigPlanChecks resource.ConfigPlanChecks
}

func createBucketTestStep(ctx context.Context, t *testing.T, opts bucketTestStep) resource.TestStep {
	t.Helper()

	fullResourceName := fmt.Sprintf("coreweave_object_storage_bucket.%s", opts.ResourceName)

	statechecks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("name"), knownvalue.StringExact(opts.Bucket.Name.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("zone"), knownvalue.StringExact(opts.Bucket.Zone.ValueString())),
	}

	tagCheck := statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tags"), knownvalue.Null())

	if !opts.Bucket.Tags.IsNull() {
		tagMap := map[string]string{}
		opts.Bucket.Tags.ElementsAs(ctx, &tagMap, false)
		tagCheckMap := map[string]knownvalue.Check{}

		for key, value := range tagMap {
			tagCheckMap[key] = knownvalue.StringExact(value)
		}
		tagCheck = statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tags"), knownvalue.MapExact(tagCheckMap))
	}

	statechecks = append(statechecks, tagCheck)

	return resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning coreweave_object_storage_bucket test: %s", opts.TestName)
		},
		Config:            objectstorage.MustRenderBucketResource(ctx, opts.ResourceName, &opts.Bucket),
		ConfigPlanChecks:  opts.ConfigPlanChecks,
		ConfigStateChecks: statechecks,
	}
}

func TestBucketResource(t *testing.T) {
	randomInt := rand.IntN(100)
	bucketName := fmt.Sprintf("%stest-bucket-%d", AcceptanceTestPrefix, randomInt)
	zone := "US-EAST-04A"

	ctx := context.Background()
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			createBucketTestStep(ctx, t, bucketTestStep{
				TestName:     "initial bucket no tags",
				ResourceName: "test_acc_bucket",
				Bucket: objectstorage.BucketResourceModel{
					Name: types.StringValue(bucketName),
					Zone: types.StringValue(zone),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket.%s", "test_acc_bucket"), plancheck.ResourceActionCreate),
					},
				},
			}),
			createBucketTestStep(ctx, t, bucketTestStep{
				TestName:     "update with tags",
				ResourceName: "test_acc_bucket",
				Bucket: objectstorage.BucketResourceModel{
					Name: types.StringValue(bucketName),
					Zone: types.StringValue(zone),
					Tags: types.MapValueMust(types.StringType, map[string]attr.Value{
						"test-tag":         types.StringValue("foo"),
						"another-test-tag": types.StringValue("bar"),
					}),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket.%s", "test_acc_bucket"), plancheck.ResourceActionUpdate),
					},
				},
			}),
			createBucketTestStep(ctx, t, bucketTestStep{
				TestName:     "remove tags",
				ResourceName: "test_acc_bucket",
				Bucket: objectstorage.BucketResourceModel{
					Name: types.StringValue(bucketName),
					Zone: types.StringValue(zone),
					Tags: types.MapNull(types.StringType),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket.%s", "test_acc_bucket"), plancheck.ResourceActionUpdate),
					},
				},
			}),
			createBucketTestStep(ctx, t, bucketTestStep{
				TestName:     "requires replace zone",
				ResourceName: "test_acc_bucket",
				Bucket: objectstorage.BucketResourceModel{
					Name: types.StringValue(bucketName),
					Zone: types.StringValue("US-EAST-02A"),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket.%s", "test_acc_bucket"), plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
			}),
			createBucketTestStep(ctx, t, bucketTestStep{
				TestName:     "requires replace name",
				ResourceName: "test_acc_bucket",
				Bucket: objectstorage.BucketResourceModel{
					Name: types.StringValue(fmt.Sprintf("%srequires-replace-%d", AcceptanceTestPrefix, randomInt)),
					Zone: types.StringValue("US-EAST-02A"),
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket.%s", "test_acc_bucket"), plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
			}),
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_object_storage_bucket import test")
				},
				ResourceName:                         fmt.Sprintf("coreweave_object_storage_bucket.%s", "test_acc_bucket"),
				ImportState:                          true,
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateId:                        fmt.Sprintf("%srequires-replace-%d", AcceptanceTestPrefix, randomInt),
			},
		},
	})
}
