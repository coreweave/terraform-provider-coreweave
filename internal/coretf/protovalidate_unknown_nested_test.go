package coretf

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// NestedModel demonstrates the issue with pointer fields that can't handle unknowns
type NestedModel struct {
	Value types.String `tfsdk:"value"`
}

// ParentModel with pointer to nested struct - CANNOT handle unknown nested values
type ParentModelWithPointer struct {
	Name   types.String  `tfsdk:"name"`
	Nested *NestedModel  `tfsdk:"nested"` // Pointer - cannot handle <unknown>
}

// ToProto implements ToProto for testing (returns a simple wrapper)
func (m *ParentModelWithPointer) ToProto() *wrapperspb.StringValue {
	if m.Name.IsNull() || m.Name.IsUnknown() {
		return nil
	}
	return wrapperspb.String(m.Name.ValueString())
}

// ParentModel with types.Object - CAN handle unknown nested values
type ParentModelWithObject struct {
	Name   types.String `tfsdk:"name"`
	Nested types.Object `tfsdk:"nested"` // Object - can handle <unknown>
}

// TestSetValidator_UnknownNestedPointer demonstrates the issue
// This test shows that when a set element has a nested unknown value,
// ElementsAs fails if the model uses a pointer for the nested field.
func TestSetValidator_UnknownNestedPointer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create object type for nested field
	nestedObjType := types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"value": types.StringType,
		},
	}

	// Create set element with UNKNOWN nested object
	// This is what Terraform creates when you do: nested = var.unknown_var
	elem := types.ObjectValueMust(
		map[string]attr.Type{
			"name":   types.StringType,
			"nested": nestedObjType,
		},
		map[string]attr.Value{
			"name":   types.StringValue("test"),
			"nested": types.ObjectUnknown(nestedObjType.AttrTypes), // <-- UNKNOWN nested object
		},
	)

	// Create a set containing this element
	setObjType := types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"name":   types.StringType,
			"nested": nestedObjType,
		},
	}
	set := types.SetValueMust(setObjType, []attr.Value{elem})

	// Try to extract elements into []ParentModelWithPointer
	// This WILL FAIL because *NestedModel cannot handle <unknown>
	var models []ParentModelWithPointer
	diags := set.ElementsAs(ctx, &models, false)

	// This is the error we're seeing
	assert.True(t, diags.HasError(), "ElementsAs should fail when nested field is unknown and uses pointer")

	// The error message will say something like:
	// "Received unknown value, however the target type cannot handle unknown values"
	// "Target Type: *NestedModel"
	// "Suggested Type: basetypes.ObjectValue"
	if diags.HasError() {
		t.Logf("Expected error: %v", diags.Errors()[0].Detail())
	}
}

// TestSetValidator_UnknownNestedObject shows the solution
// Using types.Object instead of pointer allows handling unknown values
func TestSetValidator_UnknownNestedObject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create object type for nested field
	nestedObjType := types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"value": types.StringType,
		},
	}

	// Create set element with UNKNOWN nested object
	elem := types.ObjectValueMust(
		map[string]attr.Type{
			"name":   types.StringType,
			"nested": nestedObjType,
		},
		map[string]attr.Value{
			"name":   types.StringValue("test"),
			"nested": types.ObjectUnknown(nestedObjType.AttrTypes), // <-- UNKNOWN nested object
		},
	)

	// Create a set containing this element
	setObjType := types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"name":   types.StringType,
			"nested": nestedObjType,
		},
	}
	set := types.SetValueMust(setObjType, []attr.Value{elem})

	// Try to extract elements into []ParentModelWithObject
	// This WILL SUCCEED because types.Object CAN handle <unknown>
	var models []ParentModelWithObject
	diags := set.ElementsAs(ctx, &models, false)

	assert.False(t, diags.HasError(), "ElementsAs should succeed when nested field uses types.Object")
	assert.Len(t, models, 1, "Should extract one element")
	assert.Equal(t, "test", models[0].Name.ValueString(), "Name should be extracted")
	assert.True(t, models[0].Nested.IsUnknown(), "Nested should be unknown")
}

// TestProtoSetValidator_HandlesUnknownNestedPointer tests if validator handles unknown nested pointer fields
// This test verifies that the validator properly skips validation when elements have unknown nested values,
// even when the model uses pointer fields that can't represent unknown state.
func TestProtoSetValidator_HandlesUnknownNestedPointer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create a validator for ParentModelWithPointer
	// This model has a pointer field (*NestedModel) that CANNOT handle unknown values
	v := ProtoSetValidator[ParentModelWithPointer]()

	// Create object type for nested field
	nestedObjType := types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"value": types.StringType,
		},
	}

	// Create set element with UNKNOWN nested object
	// This is what Terraform creates when you do: nested = var.unknown_var
	elem := types.ObjectValueMust(
		map[string]attr.Type{
			"name":   types.StringType,
			"nested": nestedObjType,
		},
		map[string]attr.Value{
			"name":   types.StringValue("test"),
			"nested": types.ObjectUnknown(nestedObjType.AttrTypes), // <-- UNKNOWN nested object
		},
	)

	setObjType := types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"name":   types.StringType,
			"nested": nestedObjType,
		},
	}
	set := types.SetValueMust(setObjType, []attr.Value{elem})

	req := validator.SetRequest{
		Path:        path.Root("test"),
		ConfigValue: set,
	}
	resp := &validator.SetResponse{}

	// Run the validator
	// With our fix, this should:
	// 1. Detect that the element has an unknown nested value
	// 2. Skip validation (return without error)
	// 3. NOT call ElementsAs (which would fail with "target type cannot handle unknown values")
	v.ValidateSet(ctx, req, resp)

	// Should have no errors - validation was properly skipped
	assert.False(t, resp.Diagnostics.HasError(),
		"Should not error when element has unknown nested pointer field")
}
