package objectstorage_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

type lifecycleTestConfig struct {
	name             string
	resourceName     string
	bucket           objectstorage.BucketResourceModel
	bucketVersioning *objectstorage.VersioningConfigurationModel
	rules            []objectstorage.LifecycleRuleModel
	configPlanChecks resource.ConfigPlanChecks
}

func createLifecycleTestStep(
	ctx context.Context,
	t *testing.T,
	opts lifecycleTestConfig,
) resource.TestStep {
	t.Helper()

	// Render bucket + lifecycle HCL
	bucketHCL := objectstorage.MustRenderBucketResource(ctx, "test_bucket", &opts.bucket)
	lcModel := &objectstorage.BucketLifecycleResourceModel{
		Bucket: types.StringValue(
			fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket"),
		),
		Rule: opts.rules,
	}
	lcHCL := objectstorage.MustRenderBucketLifecycleConfigurationResource(ctx, opts.resourceName, lcModel)
	config := bucketHCL + lcHCL

	if opts.bucketVersioning != nil {
		config += objectstorage.MustRenderBucketVersioningResource(ctx, opts.resourceName, &objectstorage.BucketVersioningResourceModel{
			Bucket: types.StringValue(
				fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket"),
			),
			VersioningConfiguration: *opts.bucketVersioning,
		})
	}

	rs := fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", opts.resourceName)
	checks := []statecheck.StateCheck{}

	// for each rule, generate one ExpectKnownValue per possible attribute
	for i, r := range opts.rules {
		base := tfjsonpath.New("rule")
		ruleBase := base.AtSliceIndex(i)

		// id (optional)
		idCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("id"), knownvalue.Null())
		if !r.ID.IsNull() {
			idCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("id"), knownvalue.StringExact(r.ID.ValueString()))
		}
		checks = append(checks, idCheck)

		// prefix (optional)
		prefixCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("prefix"), knownvalue.Null())
		if !r.Prefix.IsNull() {
			prefixCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("prefix"), knownvalue.StringExact(r.Prefix.ValueString()))
		}
		checks = append(checks, prefixCheck)

		// status (required)
		checks = append(checks, statecheck.ExpectKnownValue(
			rs,
			ruleBase.AtMapKey("status"),
			knownvalue.StringExact(r.Status.ValueString()),
		))

		expirationCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("expiration"), knownvalue.Null())
		if r.Expiration != nil {
			expirationCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("expiration"), knownvalue.NotNull())

			// expiration.date
			dateCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("expiration").AtMapKey("date"), knownvalue.Null())
			if !r.Expiration.Date.IsNull() {
				dateCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("expiration").AtMapKey("date"), knownvalue.StringExact(r.Expiration.Date.ValueString()))
			}
			checks = append(checks, dateCheck)

			// expiration.days
			daysCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("expiration").AtMapKey("days"), knownvalue.Null())
			if !r.Expiration.Days.IsNull() {
				daysCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("expiration").AtMapKey("days"), knownvalue.Int32Exact(r.Expiration.Days.ValueInt32()))
			}
			checks = append(checks, daysCheck)

			// expiration.expired_object_delete_marker
			expiredObjectcheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("expiration").AtMapKey("expired_object_delete_marker"), knownvalue.Null())
			if !r.Expiration.ExpiredObjectDeleteMarker.IsNull() {
				expiredObjectcheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("expiration").AtMapKey("expired_object_delete_marker"), knownvalue.Bool(r.Expiration.ExpiredObjectDeleteMarker.ValueBool()))
			}
			checks = append(checks, expiredObjectcheck)
		}
		checks = append(checks, expirationCheck)

		nonCurrentVersionExpirationCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("noncurrent_version_expiration"), knownvalue.Null())
		if r.NoncurrentVersionExpiration != nil {
			nonCurrentVersionExpirationCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("noncurrent_version_expiration"), knownvalue.NotNull())
			daysCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("noncurrent_version_expiration").AtMapKey("noncurrent_days"), knownvalue.Null())
			if !r.NoncurrentVersionExpiration.NoncurrentDays.IsNull() {
				daysCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("noncurrent_version_expiration").AtMapKey("noncurrent_days"), knownvalue.Int32Exact(r.NoncurrentVersionExpiration.NoncurrentDays.ValueInt32()))
			}
			checks = append(checks, daysCheck)

			newerCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("noncurrent_version_expiration").AtMapKey("newer_noncurrent_versions"), knownvalue.Null())
			if !r.NoncurrentVersionExpiration.NewerNoncurrentVersions.IsNull() {
				newerCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("noncurrent_version_expiration").AtMapKey("newer_noncurrent_versions"), knownvalue.Int32Exact(r.NoncurrentVersionExpiration.NewerNoncurrentVersions.ValueInt32()))
			}
			checks = append(checks, newerCheck)
		}
		checks = append(checks, nonCurrentVersionExpirationCheck)

		abortIncompleteMultipartCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("abort_incomplete_multipart_upload"), knownvalue.Null())
		if r.AbortIncompleteMultipart != nil {
			abortIncompleteMultipartCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("abort_incomplete_multipart_upload"), knownvalue.NotNull())
			checks = append(checks, statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("abort_incomplete_multipart_upload").AtMapKey("days_after_initiation"), knownvalue.Int32Exact(r.AbortIncompleteMultipart.DaysAfterInitiation.ValueInt32())))
		}
		checks = append(checks, abortIncompleteMultipartCheck)

		filterCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter"), knownvalue.Null())
		if r.Filter != nil {
			filterCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter"), knownvalue.NotNull())
			filterPrefixCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("prefix"), knownvalue.StringExact(r.Filter.Prefix.ValueString()))
			if r.Filter.Prefix.IsNull() {
				filterPrefixCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("prefix"), knownvalue.Null())
			}
			checks = append(checks, filterPrefixCheck)

			filterObjLessCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("object_size_less_than"), knownvalue.Int64Exact(r.Filter.ObjectSizeLessThan.ValueInt64()))
			if r.Filter.ObjectSizeLessThan.IsNull() {
				filterObjLessCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("object_size_less_than"), knownvalue.Null())
			}
			checks = append(checks, filterObjLessCheck)

			filterObjGreaterCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("object_size_greater_than"), knownvalue.Int64Exact(r.Filter.ObjectSizeGreaterThan.ValueInt64()))
			if r.Filter.ObjectSizeGreaterThan.IsNull() {
				filterObjGreaterCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("object_size_greater_than"), knownvalue.Null())
			}
			checks = append(checks, filterObjGreaterCheck)

			filterAndCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("and"), knownvalue.Null())
			if r.Filter.And != nil {
				filterAndCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("and"), knownvalue.NotNull())
				checks = append(checks, statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("and").AtMapKey("prefix"), knownvalue.StringExact(r.Filter.And.Prefix.ValueString())))
				checks = append(checks, statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("and").AtMapKey("object_size_less_than"), knownvalue.Int64Exact(r.Filter.And.ObjectSizeLessThan.ValueInt64())))
				checks = append(checks, statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("and").AtMapKey("object_size_greater_than"), knownvalue.Int64Exact(r.Filter.And.ObjectSizeGreaterThan.ValueInt64())))
				tagMap := map[string]string{}
				tagCheck := map[string]knownvalue.Check{}
				r.Filter.And.Tags.ElementsAs(ctx, &tagMap, false)
				for key, value := range tagMap {
					tagCheck[key] = knownvalue.StringExact(value)
				}

				if len(tagCheck) > 0 {
					checks = append(checks, statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("and").AtMapKey("tags"), knownvalue.MapExact(tagCheck)))
				} else {
					checks = append(checks, statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("and").AtMapKey("tags"), knownvalue.MapSizeExact(0)))
				}
			}
			checks = append(checks, filterAndCheck)

			filterTagCheck := statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("tag"), knownvalue.Null())
			if r.Filter.Tag != nil {
				filterTagCheck = statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("tag"), knownvalue.NotNull())
				checks = append(checks, statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("tag").AtMapKey("key"), knownvalue.StringExact(r.Filter.Tag.Key.ValueString())))
				checks = append(checks, statecheck.ExpectKnownValue(rs, ruleBase.AtMapKey("filter").AtMapKey("tag").AtMapKey("value"), knownvalue.StringExact(r.Filter.Tag.Value.ValueString())))
			}
			checks = append(checks, filterTagCheck)
		}
		checks = append(checks, filterCheck)
	}

	return resource.TestStep{
		PreConfig: func() {
			t.Logf("beginning coreweave_object_storage_bucket_lifecycle_configuration %s test", opts.name)
		},
		Config:            config,
		ConfigPlanChecks:  opts.configPlanChecks,
		ConfigStateChecks: checks,
	}
}

func TestBucketLifecycleConfiguration(t *testing.T) {
	ctx := context.Background()

	randomInt := rand.IntN(100)
	bucket := objectstorage.BucketResourceModel{
		Name: types.StringValue(fmt.Sprintf("%slc-bucket-%d", AcceptanceTestPrefix, randomInt)),
		Zone: types.StringValue("US-EAST-04A"),
	}
	versioning := &objectstorage.VersioningConfigurationModel{
		Status: types.StringValue("Enabled"),
	}

	resourceName := "test_lc"

	// Full rule with every field
	fullRule := objectstorage.LifecycleRuleModel{
		ID:     types.StringValue("full"),
		Status: types.StringValue("Enabled"),
		Expiration: &objectstorage.ExpirationModel{
			Days: types.Int32Value(90),
		},
		Filter: &objectstorage.FilterModel{
			And: &objectstorage.AndFilterModel{
				Prefix: types.StringValue("metrics/2025/"),
				Tags: types.MapValueMust(types.StringType, map[string]attr.Value{
					"foo": types.StringValue("bar"),
				}),
				ObjectSizeGreaterThan: types.Int64Value(2048),
				ObjectSizeLessThan:    types.Int64Value(8192),
			},
		},
	}
	// variants with only one sub-block
	expOnly := objectstorage.LifecycleRuleModel{
		ID:     types.StringValue("expiration-only"),
		Status: types.StringValue("Enabled"),
		Expiration: &objectstorage.ExpirationModel{
			Days: types.Int32Value(15),
		},
	}
	noncurrOnly := objectstorage.LifecycleRuleModel{
		ID: types.StringValue("noncurrent-only"),
		Filter: &objectstorage.FilterModel{
			Prefix: types.StringValue("metrics/"),
		},
		Status: types.StringValue("Enabled"),
		NoncurrentVersionExpiration: &objectstorage.NoncurrentVersionExpirationModel{
			NoncurrentDays:          types.Int32Value(5),
			NewerNoncurrentVersions: types.Int32Value(1),
		},
	}
	noncurrDaysOnly := objectstorage.LifecycleRuleModel{
		ID:     types.StringValue("noncurrent-days-only"),
		Status: types.StringValue("Enabled"),
		NoncurrentVersionExpiration: &objectstorage.NoncurrentVersionExpirationModel{
			NoncurrentDays: types.Int32Value(7),
		},
	}
	noncurrNewerOnly := objectstorage.LifecycleRuleModel{
		ID:     types.StringValue("noncurrent-newer-only"),
		Status: types.StringValue("Enabled"),
		Filter: &objectstorage.FilterModel{
			Prefix: types.StringValue("metrics/"),
		},
		NoncurrentVersionExpiration: &objectstorage.NoncurrentVersionExpirationModel{
			NewerNoncurrentVersions: types.Int32Value(2),
		},
	}
	abortOnly := objectstorage.LifecycleRuleModel{
		ID:     types.StringValue("abort-only"),
		Status: types.StringValue("Enabled"),
		AbortIncompleteMultipart: &objectstorage.AbortIncompleteMultipartModel{
			DaysAfterInitiation: types.Int32Value(3),
		},
	}
	filterOnly := objectstorage.LifecycleRuleModel{
		ID:     types.StringValue("filter only"),
		Status: types.StringValue("Enabled"),
		Filter: &objectstorage.FilterModel{
			Prefix: types.StringValue("tmp/"),
		},
	}

	steps := []resource.TestStep{
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:             "full ruleset",
			resourceName:     resourceName,
			bucket:           bucket,
			bucketVersioning: versioning,
			rules:            []objectstorage.LifecycleRuleModel{fullRule},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionCreate),
				},
			},
		}),
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:             "expiration only",
			resourceName:     resourceName,
			bucket:           bucket,
			bucketVersioning: versioning,
			rules:            []objectstorage.LifecycleRuleModel{expOnly},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:             "noncurrent only",
			resourceName:     resourceName,
			bucket:           bucket,
			bucketVersioning: versioning,
			rules:            []objectstorage.LifecycleRuleModel{noncurrOnly},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:             "noncurrent only, days only",
			resourceName:     resourceName,
			bucket:           bucket,
			bucketVersioning: versioning,
			rules:            []objectstorage.LifecycleRuleModel{noncurrDaysOnly},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:             "noncurrent only, newer only",
			resourceName:     resourceName,
			bucket:           bucket,
			bucketVersioning: versioning,
			rules:            []objectstorage.LifecycleRuleModel{noncurrNewerOnly},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:             "abort only",
			resourceName:     resourceName,
			bucket:           bucket,
			bucketVersioning: versioning,
			rules:            []objectstorage.LifecycleRuleModel{abortOnly},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:             "filter only",
			resourceName:     resourceName,
			bucket:           bucket,
			bucketVersioning: versioning,
			rules:            []objectstorage.LifecycleRuleModel{filterOnly},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		{
			PreConfig: func() {
				t.Log("Beginning coreweave_object_storage_bucket_lifecycle_configuration import test")
			},
			ResourceName:                         fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName),
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

func TestBucketLifecycleConfiguration_MultiRule(t *testing.T) {
	ctx := context.Background()

	randInt := rand.IntN(100)
	bucket := objectstorage.BucketResourceModel{
		Name: types.StringValue(fmt.Sprintf("%slc-bucket-multi%d", AcceptanceTestPrefix, randInt)),
		Zone: types.StringValue("US-EAST-04A"),
	}

	// two simple rules
	r1 := objectstorage.LifecycleRuleModel{
		ID:     types.StringValue("r1"),
		Prefix: types.StringValue("metrics/"),
		Status: types.StringValue("Enabled"),
		Expiration: &objectstorage.ExpirationModel{
			Days: types.Int32Value(10),
		},
	}
	r2 := objectstorage.LifecycleRuleModel{
		ID:     types.StringValue("r2"),
		Prefix: types.StringValue("logs/"),
		Status: types.StringValue("Enabled"),
		AbortIncompleteMultipart: &objectstorage.AbortIncompleteMultipartModel{
			DaysAfterInitiation: types.Int32Value(5),
		},
	}

	r3 := objectstorage.LifecycleRuleModel{
		ID:     types.StringValue("expiration-only"),
		Status: types.StringValue("Enabled"),
		Expiration: &objectstorage.ExpirationModel{
			Days: types.Int32Value(15),
		},
	}

	resourceName := "multi_rule"

	steps := []resource.TestStep{
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:         "multi rule",
			resourceName: resourceName,
			bucket:       bucket,
			rules:        []objectstorage.LifecycleRuleModel{r1, r2},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionCreate),
				},
			},
		}),
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:         "add third rule",
			resourceName: resourceName,
			bucket:       bucket,
			rules:        []objectstorage.LifecycleRuleModel{r1, r2, r3},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		createLifecycleTestStep(ctx, t, lifecycleTestConfig{
			name:         "remove rules",
			resourceName: resourceName,
			bucket:       bucket,
			rules:        []objectstorage.LifecycleRuleModel{r1},
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_lifecycle_configuration.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps:                    steps,
	})
}
