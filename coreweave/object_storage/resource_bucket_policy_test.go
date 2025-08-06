package objectstorage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
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

type bucketPolicyTestStep struct {
	name             string
	resourceName     string
	bucket           objectstorage.BucketResourceModel
	rawPolicy        *string
	policyDoc        *objectstorage.BucketPolicyDocumentModel
	configPlanChecks resource.ConfigPlanChecks
}

func createBucketPolicyTestStep(ctx context.Context, t *testing.T, opts bucketPolicyTestStep) resource.TestStep {
	t.Helper()
	bucketConfig := objectstorage.MustRenderBucketResource(ctx, "test_bucket", &opts.bucket)
	policyConfig := ""
	dataSourceConfig := ""
	var policyCheck knownvalue.Check

	// prioritize the raw policy first if specified
	if opts.rawPolicy != nil {
		policyConfig = objectstorage.MustRenderBucketPolicyResource(ctx, opts.resourceName, &objectstorage.BucketPolicyResourceModel{
			Bucket: types.StringValue(
				fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket"),
			),
			Policy: types.StringPointerValue(opts.rawPolicy),
		})
		policyCheck = knownvalue.StringExact(*opts.rawPolicy)
	}

	// if still empty, use the data source
	if policyConfig == "" && opts.policyDoc != nil {
		policyConfig = objectstorage.MustRenderBucketPolicyResourceWithDataSource(ctx, opts.resourceName, &objectstorage.BucketPolicyResourceModel{
			Bucket: types.StringValue(
				fmt.Sprintf("coreweave_object_storage_bucket.%s.name", "test_bucket"),
			),
			Policy: types.StringValue(
				fmt.Sprintf("data.coreweave_object_storage_bucket_policy_document.%s.json", opts.resourceName),
			),
		})
		dataSourceConfig = objectstorage.MustRenderBucketPolicyDocument(ctx, opts.resourceName, opts.policyDoc)

		// generate the JSON that is expected to be stored in state
		pd := objectstorage.BuildPolicyDocument(ctx, *opts.policyDoc)
		rawJSON, err := json.Marshal(pd)
		if err != nil {
			panic(fmt.Sprintf("failed to marshal policy document json: %v", err))
		}
		policyCheck = knownvalue.StringExact(string(rawJSON))
	}

	if policyConfig == "" {
		panic("could not create policy config, please check the function parameters")
	}

	fullConfig := bucketConfig + policyConfig + dataSourceConfig
	rs := fmt.Sprintf("coreweave_object_storage_bucket_policy.%s", opts.resourceName)
	checks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("bucket"), knownvalue.StringExact(opts.bucket.Name.ValueString())),
		statecheck.ExpectKnownValue(rs, tfjsonpath.New("policy"), policyCheck),
	}

	return resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning coreweave_object_storage_bucket_policy %s test", opts.name)
		},
		Config:            fullConfig,
		ConfigStateChecks: checks,
		ConfigPlanChecks:  opts.configPlanChecks,
	}
}

func TestBucketPolicyResourceRaw(t *testing.T) {
	ctx := context.Background()

	randomInt := rand.IntN(100)
	bucket := objectstorage.BucketResourceModel{
		Name: types.StringValue(fmt.Sprintf("tf-acc-bucket-policy-%d", randomInt)),
		Zone: types.StringValue("US-EAST-04A"),
	}
	resourceName := "test_policy"

	steps := []resource.TestStep{
		createBucketPolicyTestStep(ctx, t, bucketPolicyTestStep{
			name:         "initial raw policy",
			resourceName: resourceName,
			bucket:       bucket,
			rawPolicy:    aws.String(`{"Statement":[{"Action":["s3:*"],"Effect":"Allow","Principal":{"CW":"*"},"Resource":["arn:aws:s3:::*"],"Sid":"allow-all"}],"Version":"2012-10-17"}`),
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_policy.%s", resourceName), plancheck.ResourceActionCreate),
				},
			},
		}),
		createBucketPolicyTestStep(ctx, t, bucketPolicyTestStep{
			name:         "limit-conditions",
			resourceName: resourceName,
			bucket:       bucket,
			rawPolicy:    aws.String(`{"Version": "2012-10-17", "Statement": [{"Action":["s3:*"],"Effect":"Allow","Principal":{"CW":"*"},"Resource":["arn:aws:s3:::*"],"Sid":"allow-all"}, {"Sid": "AllowIfPrefixEquals", "Effect": "Allow", "Action": "s3:ListBucket", "Resource": "arn:aws:s3:::*", "Condition": {"StringEquals": {"s3:prefix": "projects"}}}, {"Sid": "DenyIfPrefixNotEquals", "Effect": "Deny", "Action": "s3:ListBucket", "Resource": "arn:aws:s3:::*", "Condition": {"StringNotEquals": {"s3:prefix": "projects"}}}]}`),
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_policy.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		{
			PreConfig: func() {
				t.Log("Beginning coreweave_object_storage_bucket_policy import test")
			},
			ResourceName:                         fmt.Sprintf("coreweave_object_storage_bucket_policy.%s", resourceName),
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

func TestBucketPolicyResourceDocument(t *testing.T) {
	ctx := context.Background()

	randomInt := rand.IntN(100)
	bucket := objectstorage.BucketResourceModel{
		Name: types.StringValue(fmt.Sprintf("tf-acc-bucket-policy-doc-%d", randomInt)),
		Zone: types.StringValue("US-EAST-04A"),
	}
	resourceName := "test_policy"

	initial := &objectstorage.BucketPolicyDocumentModel{
		Version: types.StringValue("2012-10-17"),
		Statement: []objectstorage.StatementModel{
			{
				Sid:      types.StringValue("allow-all"),
				Effect:   types.StringValue("Allow"),
				Action:   types.ListValueMust(types.StringType, []attr.Value{types.StringValue("s3:*")}),
				Resource: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("arn:aws:s3:::*")}),
				Principal: types.MapValueMust(types.ListType{ElemType: types.StringType}, map[string]attr.Value{
					"CW": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("*")}),
				}),
			},
		},
	}

	update := &objectstorage.BucketPolicyDocumentModel{
		Version: types.StringValue("2012-10-17"),
		Statement: []objectstorage.StatementModel{
			{
				Sid:      types.StringValue("allow-all"),
				Effect:   types.StringValue("Allow"),
				Action:   types.ListValueMust(types.StringType, []attr.Value{types.StringValue("s3:*")}),
				Resource: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("arn:aws:s3:::*")}),
				Principal: types.MapValueMust(types.ListType{ElemType: types.StringType}, map[string]attr.Value{
					"CW": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("*")}),
				}),
			},
			///{'Sid': 'AllowIfPrefixEquals', 'Effect': 'Allow', 'Action': 's3:ListBucket', 'Resource': 'arn:aws:s3:::my-bucket', 'Condition': {'StringEquals': {'s3:prefix': 'projects'}}}
			{
				Sid:      types.StringValue("AllowIfPrefixEquals"),
				Effect:   types.StringValue("Allow"),
				Action:   types.ListValueMust(types.StringType, []attr.Value{types.StringValue("s3:ListBucket")}),
				Resource: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("arn:aws:s3:::*")}),
				Principal: types.MapValueMust(types.ListType{ElemType: types.StringType}, map[string]attr.Value{
					"CW": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("*")}),
				}),
				Condition: types.MapValueMust(types.MapType{ElemType: types.StringType}, map[string]attr.Value{
					"StringEquals": types.MapValueMust(types.StringType, map[string]attr.Value{
						"s3:prefix": types.StringValue("projects"),
					}),
				}),
			},
			{
				Sid:      types.StringValue("DenyIfPrefixNotEquals"),
				Effect:   types.StringValue("Deny"),
				Action:   types.ListValueMust(types.StringType, []attr.Value{types.StringValue("s3:ListBucket")}),
				Resource: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("arn:aws:s3:::*")}),
				Principal: types.MapValueMust(types.ListType{ElemType: types.StringType}, map[string]attr.Value{
					"CW": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("*")}),
				}),
				Condition: types.MapValueMust(types.MapType{ElemType: types.StringType}, map[string]attr.Value{
					"StringNotEquals": types.MapValueMust(types.StringType, map[string]attr.Value{
						"s3:prefix": types.StringValue("projects"),
					}),
				}),
			},
		},
	}

	steps := []resource.TestStep{
		createBucketPolicyTestStep(ctx, t, bucketPolicyTestStep{
			name:         "initial document policy",
			resourceName: resourceName,
			bucket:       bucket,
			policyDoc:    initial,
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_policy.%s", resourceName), plancheck.ResourceActionCreate),
				},
			},
		}),
		createBucketPolicyTestStep(ctx, t, bucketPolicyTestStep{
			name:         "limit-conditions",
			resourceName: resourceName,
			bucket:       bucket,
			policyDoc:    update,
			configPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_bucket_policy.%s", resourceName), plancheck.ResourceActionUpdate),
				},
			},
		}),
		{
			PreConfig: func() {
				t.Log("Beginning coreweave_object_storage_bucket_policy import test")
			},
			ResourceName:                         fmt.Sprintf("coreweave_object_storage_bucket_policy.%s", resourceName),
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
