package objectstorage

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
)

func TestFlattenLifecycleRulesPreservesDeprecatedPrefixForMissingIDs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		apiID      *string
		priorID    types.String
		wantPrefix string
	}{
		"nil ID": {
			apiID:      nil,
			priorID:    types.StringNull(),
			wantPrefix: "logs/",
		},
		"empty ID": {
			apiID:      aws.String(""),
			priorID:    types.StringValue(""),
			wantPrefix: "metrics/",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := flattenLifecycleRules(
				[]s3types.LifecycleRule{
					{
						ID:     tc.apiID,
						Status: s3types.ExpirationStatusEnabled,
					},
				},
				[]LifecycleRuleModel{
					{
						ID:     tc.priorID,
						Prefix: types.StringValue(tc.wantPrefix),
					},
				},
			)

			require.Len(t, got, 1)
			require.Equal(t, tc.wantPrefix, got[0].Prefix.ValueString())
		})
	}
}
