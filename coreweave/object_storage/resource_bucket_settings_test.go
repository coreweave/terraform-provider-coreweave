package objectstorage_test

import (
	"context"
	"math/rand/v2"
	"testing"

	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestBucketSettingsSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaRequest := fwresource.SchemaRequest{}
	schemaResponse := &fwresource.SchemaResponse{}

	objectstorage.NewBucketSettingsResource().Schema(ctx, schemaRequest, schemaResponse)

	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema method diagnostics: %+v", schemaResponse.Diagnostics)
	}

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)

	if diagnostics.HasError() {
		t.Fatalf("Schema validation diagnostics: %+v", diagnostics)
	}
}

func TestBucketSettingsResource(t *testing.T) {
	t.Parallel()
	randomInt := rand.IntN(100)
	bucketName := fmt.Sprintf("%sbucketsettings-%d", AcceptanceTestPrefix, randomInt)
	ctx := t.Context()

	zone := "US-EAST-04A"

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			createBucketTestStep(ctx, t, bucketTestStep{
				TestName: "bucket settings initial create",
				ResourceName: "test_acc_bucketsettings",
				Bucket: objectstorage.BucketResourceModel{
					Name: types.StringValue(bucketName),
					Zone: types.StringValue(zone),
				},
				ConfigPlanChecks: ,
			})
		},
	})

}
