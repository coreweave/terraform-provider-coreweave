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

	document := objectstorage.BuildPolicyDocument(t.Context(), model)
	conditionJSON, err := json.Marshal(document.Statement[0].Condition)
	require.NoError(t, err)
	require.JSONEq(t, `{"StringEquals":{"cw:PrincipalOrgID":["test-org-id"]}}`, string(conditionJSON))
}
