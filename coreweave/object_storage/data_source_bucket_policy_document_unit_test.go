package objectstorage_test

import (
	"encoding/json"
	"testing"

	objectstorage "github.com/coreweave/terraform-provider-coreweave/coreweave/object_storage"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
)

func TestBuildPolicyDocumentPreservesCondition(t *testing.T) {
	t.Parallel()

	model := objectstorage.BucketPolicyDocumentModel{
		Statement: []objectstorage.StatementModel{
			{
				Condition: types.MapValueMust(types.MapType{ElemType: types.StringType}, map[string]attr.Value{
					"StringEquals": types.MapValueMust(types.StringType, map[string]attr.Value{
						"cw:PrincipalOrgID": types.StringValue("test-org-id"),
					}),
				}),
			},
		},
	}

	document, diagnostics := objectstorage.BuildPolicyDocument(t.Context(), model)
	require.False(t, diagnostics.HasError(), diagnostics.Errors())
	conditionJSON, err := json.Marshal(document.Statement[0].Condition)
	require.NoError(t, err)
	require.JSONEq(t, `{"StringEquals":{"cw:PrincipalOrgID":["test-org-id"]}}`, string(conditionJSON))
}

func TestBuildPolicyDocumentReturnsConditionDiagnostics(t *testing.T) {
	t.Parallel()

	model := objectstorage.BucketPolicyDocumentModel{
		Version: types.StringValue("2012-10-17"),
		Statement: []objectstorage.StatementModel{
			{
				Effect: types.StringValue("Allow"),
			},
			{
				Condition: types.MapValueMust(types.MapType{ElemType: types.StringType}, map[string]attr.Value{
					"StringEquals": types.MapValueMust(types.StringType, map[string]attr.Value{
						"cw:PrincipalOrgID": types.StringNull(),
					}),
				}),
			},
		},
	}

	document, diagnostics := objectstorage.BuildPolicyDocument(t.Context(), model)
	require.True(t, diagnostics.HasError())
	require.Contains(t, diagnostics.Errors()[0].Detail(), "cw:PrincipalOrgID")
	require.Equal(t, objectstorage.PolicyDocument{}, document)
}
