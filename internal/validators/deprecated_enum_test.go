package validators_test

import (
	"testing"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/internal/validators"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
)

const (
	capacityTypeServerless = "CAPACITY_TYPE_SERVERLESS"
	capacityTypeManaged    = "CAPACITY_TYPE_MANAGED"
	capacityTypeCustomer   = "CAPACITY_TYPE_CUSTOMER"
	deprecationGuidance    = "Use CAPACITY_TYPE_MANAGED instead."
)

func TestCapacityTypeServerlessIsNotGenerated(t *testing.T) {
	t.Parallel()

	_, ok := inferencev1.CapacityType_value[capacityTypeServerless]
	assert.False(t, ok, "%s should not be present in the current generated CapacityType enum", capacityTypeServerless)
}

func TestDeprecatedEnumValue_CurrentCapacityTypeDescriptor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		value       types.String
		wantWarning bool
	}{
		{"removed serverless value does not warn", types.StringValue(capacityTypeServerless), false},
		{"replacement value does not warn", types.StringValue(capacityTypeManaged), false},
		{"other valid value does not warn", types.StringValue(capacityTypeCustomer), false},
		{"unrecognized value does not warn", types.StringValue("NOT_A_REAL_VALUE"), false},
		{"null does not warn", types.StringNull(), false},
		{"unknown does not warn", types.StringUnknown(), false},
	}

	v := validators.DeprecatedEnumValue(inferencev1.CapacityType_CAPACITY_TYPE_MANAGED.Descriptor())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := validator.StringRequest{Path: path.Root("capacity_type"), ConfigValue: tt.value}
			resp := &validator.StringResponse{}
			v.ValidateString(t.Context(), req, resp)

			assert.False(t, resp.Diagnostics.HasError(), "warn validator must never error")
			assert.Equal(t, tt.wantWarning, resp.Diagnostics.WarningsCount() > 0)
		})
	}
}

func TestRejectDeprecatedEnumValue_CurrentCapacityTypeDescriptor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     types.String
		wantError bool
	}{
		{"removed serverless value does not error", types.StringValue(capacityTypeServerless), false},
		{"replacement value does not error", types.StringValue(capacityTypeManaged), false},
		{"other valid value does not error", types.StringValue(capacityTypeCustomer), false},
		{"unrecognized value does not error", types.StringValue("NOT_A_REAL_VALUE"), false},
		{"null does not error", types.StringNull(), false},
		{"unknown does not error", types.StringUnknown(), false},
	}

	v := validators.RejectDeprecatedEnumValue(inferencev1.CapacityType_CAPACITY_TYPE_MANAGED.Descriptor(), deprecationGuidance)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := validator.StringRequest{Path: path.Root("capacity_type"), ConfigValue: tt.value}
			resp := &validator.StringResponse{}
			v.ValidateString(t.Context(), req, resp)

			assert.Equal(t, tt.wantError, resp.Diagnostics.HasError())
			assert.Zero(t, resp.Diagnostics.WarningsCount(), "reject validator emits errors, not warnings")
		})
	}
}

func TestDeprecatedEnumValue_NilDescriptor(t *testing.T) {
	t.Parallel()

	req := validator.StringRequest{Path: path.Root("capacity_type"), ConfigValue: types.StringValue(capacityTypeServerless)}

	for _, v := range []validator.String{
		validators.DeprecatedEnumValue(nil),
		validators.RejectDeprecatedEnumValue(nil, deprecationGuidance),
	} {
		resp := &validator.StringResponse{}
		// Must not panic and must add no diagnostics.
		v.ValidateString(t.Context(), req, resp)
		assert.False(t, resp.Diagnostics.HasError())
		assert.Zero(t, resp.Diagnostics.WarningsCount())
	}
}
