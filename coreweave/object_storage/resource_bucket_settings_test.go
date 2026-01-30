package objectstorage_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestBucketSettingsSchema(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
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

type bucketSettingsTestConfig struct {
	name             string
	resourceName     string
	bucket           objectstorage.BucketResourceModel
	settings         objectstorage.BucketSettingsResourceModel
	configPlanChecks resource.ConfigPlanChecks
}

func createBucketSettingsTestStep(ctx context.Context, t *testing.T, opts bucketSettingsTestConfig) resource.TestStep {
	t.Helper()

	bucketHCL := objectstorage.MustRenderBucketResource(ctx, "test_bucket", &opts.bucket)
	settingsHCL := objectstorage.MustRenderBucketSettingsResource(ctx, opts.resourceName, &objectstorage.BucketSettingsResourceModel{
		Bucket: types.StringValue(
			fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket"),
		),
		AuditLoggingEnabled: opts.settings.AuditLoggingEnabled,
	})
	config := bucketHCL + settingsHCL

	rs := fmt.Sprintf("coreweave_object_storage_bucket_settings.%s", opts.resourceName)
	checks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("bucket"), knownvalue.StringExact(opts.bucket.Name.ValueString())),
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("audit_logging_enabled"), knownvalue.Bool(opts.settings.AuditLoggingEnabled.ValueBool())),
	}

	return resource.TestStep{
		PreConfig: func() {
			t.Logf("beginning coreweave_object_storage_bucket_settings %s test", opts.name)
		},
		Config:            config,
		ConfigStateChecks: checks,
		ConfigPlanChecks:  opts.configPlanChecks,
	}
}

func TestBucketSettingsResource(t *testing.T) {
	ctx := t.Context()

	randomInt := rand.IntN(100)
	bucket := objectstorage.BucketResourceModel{
		Name: types.StringValue(fmt.Sprintf("%sbucket-settings-%d", AcceptanceTestPrefix, randomInt)),
		Zone: types.StringValue("US-EAST-04A"),
	}
	resourceName := "test_settings"

	steps := []resource.TestStep{
		createBucketSettingsTestStep(ctx, t, bucketSettingsTestConfig{
			name:         "audit logging disabled",
			resourceName: resourceName,
			bucket:       bucket,
			settings: objectstorage.BucketSettingsResourceModel{
				AuditLoggingEnabled: types.BoolValue(false),
			},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_settings.%s", resourceName), plancheck.ResourceActionCreate),
				},
			},
		}),
		createBucketSettingsTestStep(ctx, t, bucketSettingsTestConfig{
			name:         "audit logging enabled",
			resourceName: resourceName,
			bucket:       bucket,
			settings: objectstorage.BucketSettingsResourceModel{
				AuditLoggingEnabled: types.BoolValue(true),
			},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_settings.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		{
			PreConfig: func() {
				t.Log("Beginning coreweave_object_storage_bucket_settings import test")
			},
			ResourceName:                         fmt.Sprintf("coreweave_object_storage_bucket_settings.%s", resourceName),
			ImportState:                          true,
			ImportStateId:                        bucket.Name.ValueString(),
			ImportStateVerifyIdentifierAttribute: "bucket",
			ImportStateVerify:                    true,
		},
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps:                    steps,
	})
}
