package coreweave_test

import (
	"context"
	"testing"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
)

func TestDeprecatedEnumValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		value       types.String
		wantWarning bool
	}{
		{
			name:        "deprecated value warns",
			value:       types.StringValue("CAPACITY_TYPE_SERVERLESS"),
			wantWarning: true,
		},
		{
			name:        "replacement value does not warn",
			value:       types.StringValue("CAPACITY_TYPE_MANAGED"),
			wantWarning: false,
		},
		{
			name:        "other valid value does not warn",
			value:       types.StringValue("CAPACITY_TYPE_CUSTOMER"),
			wantWarning: false,
		},
		{
			name:        "unrecognized value does not warn",
			value:       types.StringValue("NOT_A_REAL_VALUE"),
			wantWarning: false,
		},
		{
			name:        "null does not warn",
			value:       types.StringNull(),
			wantWarning: false,
		},
		{
			name:        "unknown does not warn",
			value:       types.StringUnknown(),
			wantWarning: false,
		},
	}

	v := coreweave.DeprecatedEnumValue(inferencev1.CapacityType(0).Descriptor())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := validator.StringRequest{
				Path:        path.Root("capacity_type"),
				ConfigValue: tt.value,
			}
			resp := &validator.StringResponse{}
			v.ValidateString(context.Background(), req, resp)

			// The validator must never produce errors — rejection is the API's job.
			assert.False(t, resp.Diagnostics.HasError(), "expected no error diagnostics")
			assert.Equal(t, tt.wantWarning, resp.Diagnostics.WarningsCount() > 0)
		})
	}
}
