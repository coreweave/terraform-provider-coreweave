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

func TestOrganizationAccessPolicySchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaRequest := fwresource.SchemaRequest{}
	schemaResponse := &fwresource.SchemaResponse{}

	objectstorage.NewOrganizationAccessPolicyResource().Schema(ctx, schemaRequest, schemaResponse)

	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("Schema method diagnostics: %+v", schemaResponse.Diagnostics)
	}

	// Validate the schema
	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)

	if diagnostics.HasError() {
		t.Fatalf("Schema validation diagnostics: %+v", diagnostics)
	}
}

type orgAccessPolicyTestStep struct {
	TestName         string
	ResourceName     string
	Policy           objectstorage.OrganizationAccessPolicyResourceModel
	ConfigPlanChecks resource.ConfigPlanChecks
}

func createOrgAccessPolicyTestStep(ctx context.Context, t *testing.T, opts orgAccessPolicyTestStep) resource.TestStep {
	t.Helper()

	fullResourceName := fmt.Sprintf("coreweave_object_storage_organization_access_policy.%s", opts.ResourceName)
	statechecks := make([]statecheck.StateCheck, 0, 2)
	statechecks = append(statechecks,
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New(schemaKeyName), knownvalue.StringExact(opts.Policy.Name.ValueString())),
	)

	statements := make([]knownvalue.Check, len(opts.Policy.Statements))
	for i, s := range opts.Policy.Statements {
		actions := []string{}
		s.Actions.ElementsAs(ctx, &actions, false)
		actionValues := make([]knownvalue.Check, len(actions))
		for i, a := range actions {
			actionValues[i] = knownvalue.StringExact(a)
		}

		principals := []string{}
		s.Principals.ElementsAs(ctx, &principals, false)
		principalValues := make([]knownvalue.Check, len(principals))
		for i, p := range principals {
			principalValues[i] = knownvalue.StringExact(p)
		}

		resources := []string{}
		s.Resources.ElementsAs(ctx, &resources, false)
		resourceValues := make([]knownvalue.Check, len(resources))
		for i, r := range resources {
			resourceValues[i] = knownvalue.StringExact(r)
		}

		statements[i] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			schemaKeyName:   knownvalue.StringExact(s.Name.ValueString()),
			schemaKeyEffect: knownvalue.StringExact(s.Effect.ValueString()),
			"actions":       knownvalue.SetExact(actionValues),
			"principals":    knownvalue.SetExact(principalValues),
			"resources":     knownvalue.SetExact(resourceValues),
		})
	}

	statechecks = append(statechecks, statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("statements"), knownvalue.SetExact(statements)))

	return resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning coreweave_object_storage_organization_access_policy_test test: %s", opts.TestName)
		},
		Config:            objectstorage.MustRenderOrganizationAccessPolicy(ctx, opts.ResourceName, &opts.Policy),
		ConfigPlanChecks:  opts.ConfigPlanChecks,
		ConfigStateChecks: statechecks,
	}
}

func TestOrganizationAccessPolicyResource(t *testing.T) {
	randomInt := rand.IntN(100)
	policyName := fmt.Sprintf("%stest-org-access-policy-%d", AcceptanceTestPrefix, randomInt)

	ctx := context.Background()
	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			createOrgAccessPolicyTestStep(ctx, t, orgAccessPolicyTestStep{
				TestName:     "create initial policy",
				ResourceName: testPolicyResourceName,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_organization_access_policy.%s", testPolicyResourceName), plancheck.ResourceActionCreate),
					},
				},
				Policy: objectstorage.OrganizationAccessPolicyResourceModel{
					Name: types.StringValue(policyName),
					Statements: []objectstorage.PolicyStatementResourceModel{
						{
							Name:   types.StringValue(policyName),
							Effect: types.StringValue("Allow"),
							Actions: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("s3:CreateBucket"),
							}),
							Resources: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("my-specific-bucket/*"),
							}),
							Principals: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
						},
					},
				},
			}),
			createOrgAccessPolicyTestStep(ctx, t, orgAccessPolicyTestStep{
				TestName:     "add statement",
				ResourceName: testPolicyResourceName,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_organization_access_policy.%s", testPolicyResourceName), plancheck.ResourceActionUpdate),
					},
				},
				Policy: objectstorage.OrganizationAccessPolicyResourceModel{
					Name: types.StringValue(policyName),
					Statements: []objectstorage.PolicyStatementResourceModel{
						{
							Name:   types.StringValue(policyName),
							Effect: types.StringValue("Allow"),
							Actions: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("s3:CreateBucket"),
							}),
							Resources: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
							Principals: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
						},
						{
							Name:   types.StringValue("another statement"),
							Effect: types.StringValue("Deny"),
							Actions: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("s3:DeleteBucket"),
							}),
							Resources: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
							Principals: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("role/Viewer"),
							}),
						},
					},
				},
			}),
			createOrgAccessPolicyTestStep(ctx, t, orgAccessPolicyTestStep{
				TestName:     "remove statement",
				ResourceName: testPolicyResourceName,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_organization_access_policy.%s", testPolicyResourceName), plancheck.ResourceActionUpdate),
					},
				},
				Policy: objectstorage.OrganizationAccessPolicyResourceModel{
					Name: types.StringValue(policyName),
					Statements: []objectstorage.PolicyStatementResourceModel{
						{
							Name:   types.StringValue(policyName),
							Effect: types.StringValue("Allow"),
							Actions: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("s3:CreateBucket"),
							}),
							Resources: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
							Principals: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
						},
					},
				},
			}),
			createOrgAccessPolicyTestStep(ctx, t, orgAccessPolicyTestStep{
				TestName:     "change statement",
				ResourceName: testPolicyResourceName,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_organization_access_policy.%s", testPolicyResourceName), plancheck.ResourceActionUpdate),
					},
				},
				Policy: objectstorage.OrganizationAccessPolicyResourceModel{
					Name: types.StringValue(policyName),
					Statements: []objectstorage.PolicyStatementResourceModel{
						{
							Name:   types.StringValue(policyName),
							Effect: types.StringValue("Deny"),
							Actions: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("s3:CreateBucket"),
							}),
							Resources: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
							Principals: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
						},
					},
				},
			}),
			createOrgAccessPolicyTestStep(ctx, t, orgAccessPolicyTestStep{
				TestName:     "require replace",
				ResourceName: testPolicyResourceName,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fmt.Sprintf("coreweave_object_storage_organization_access_policy.%s", testPolicyResourceName), plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Policy: objectstorage.OrganizationAccessPolicyResourceModel{
					Name: types.StringValue(fmt.Sprintf("%s-require-replace", policyName)),
					Statements: []objectstorage.PolicyStatementResourceModel{
						{
							Name:   types.StringValue(policyName),
							Effect: types.StringValue("Deny"),
							Actions: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("s3:CreateBucket"),
							}),
							Resources: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
							Principals: types.SetValueMust(types.StringType, []attr.Value{
								types.StringValue("*"),
							}),
						},
					},
				},
			}),
			{
				PreConfig: func() {
					t.Log("Beginning coreweave_object_storage_organization_access_policy import test")
				},
				ResourceName:                         fmt.Sprintf("coreweave_object_storage_organization_access_policy.%s", testPolicyResourceName),
				ImportState:                          true,
				ImportStateVerifyIdentifierAttribute: schemaKeyName,
				ImportStateId:                        fmt.Sprintf("%s-require-replace", policyName),
			},
		},
	})
}
