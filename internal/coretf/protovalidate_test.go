package coretf

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// TestModel is a simple model for testing
type TestModel struct {
	Value types.String `tfsdk:"value"`
}

// ToProto implements the simple ToProto signature
func (m *TestModel) ToProto() *wrapperspb.StringValue {
	if m.Value.IsNull() || m.Value.IsUnknown() {
		return nil
	}
	return wrapperspb.String(m.Value.ValueString())
}

// TestProtoValidator_SkipsUnknown verifies that validators skip unknown values
func TestProtoValidator_SkipsUnknown(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	v := ProtoValidator[TestModel]()

	// Create an object with an unknown field
	obj := types.ObjectValueMust(
		map[string]attr.Type{
			"value": types.StringType,
		},
		map[string]attr.Value{
			"value": types.StringUnknown(),
		},
	)

	req := validator.ObjectRequest{
		Path:        path.Root("test"),
		ConfigValue: obj,
	}
	resp := &validator.ObjectResponse{}

	v.ValidateObject(ctx, req, resp)

	// Should have no diagnostics (validation was skipped)
	assert.False(t, resp.Diagnostics.HasError(), "Should not have errors for unknown values")
}

// TestProtoValidator_ValidatesKnown verifies that validators validate known values
func TestProtoValidator_ValidatesKnown(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	v := ProtoValidator[TestModel]()

	// Create an object with a known field
	obj := types.ObjectValueMust(
		map[string]attr.Type{
			"value": types.StringType,
		},
		map[string]attr.Value{
			"value": types.StringValue("test"),
		},
	)

	req := validator.ObjectRequest{
		Path:        path.Root("test"),
		ConfigValue: obj,
	}
	resp := &validator.ObjectResponse{}

	v.ValidateObject(ctx, req, resp)

	// Should have no diagnostics (valid proto)
	assert.False(t, resp.Diagnostics.HasError(), "Should not have errors for valid values")
}

// TestProtoValidator_SkipsNull verifies that validators skip null values
func TestProtoValidator_SkipsNull(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	v := ProtoValidator[TestModel]()

	req := validator.ObjectRequest{
		Path:        path.Root("test"),
		ConfigValue: types.ObjectNull(map[string]attr.Type{"value": types.StringType}),
	}
	resp := &validator.ObjectResponse{}

	v.ValidateObject(ctx, req, resp)

	// Should have no diagnostics (validation was skipped)
	assert.False(t, resp.Diagnostics.HasError(), "Should not have errors for null values")
}

// TestSetModel is a simple model for testing set validators
type TestSetModel struct {
	Name types.String `tfsdk:"name"`
}

// ToProto implements ToProto with diagnostics
func (m *TestSetModel) ToProto() (*wrapperspb.StringValue, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	if m.Name.IsNull() || m.Name.IsUnknown() {
		return nil, diagnostics
	}

	if m.Name.ValueString() == "" {
		diagnostics.AddError("Invalid name", "Name cannot be empty")
		return nil, diagnostics
	}

	return wrapperspb.String(m.Name.ValueString()), diagnostics
}

// TestProtoSetValidator_SkipsUnknownElements verifies that set validators skip unknown elements
func TestProtoSetValidator_SkipsUnknownElements(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	v := ProtoSetValidator[TestSetModel]()

	// Create a set with mixed known and unknown elements
	objType := types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"name": types.StringType,
		},
	}

	knownElem := types.ObjectValueMust(
		objType.AttrTypes,
		map[string]attr.Value{
			"name": types.StringValue("known"),
		},
	)

	unknownElem := types.ObjectValueMust(
		objType.AttrTypes,
		map[string]attr.Value{
			"name": types.StringUnknown(),
		},
	)

	set := types.SetValueMust(objType, []attr.Value{knownElem, unknownElem})

	req := validator.SetRequest{
		Path:        path.Root("test"),
		ConfigValue: set,
	}
	resp := &validator.SetResponse{}

	v.ValidateSet(ctx, req, resp)

	// Should have no diagnostics (unknown element skipped, known element valid)
	assert.False(t, resp.Diagnostics.HasError(), "Should not have errors when unknown elements are skipped")
}

// TestCallToProto_SimpleSignature tests calling ToProto with no args, no diagnostics
func TestCallToProto_SimpleSignature(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	model := &TestModel{Value: types.StringValue("test")}

	protoMsg, diags := callToProto(ctx, model)

	assert.False(t, diags.HasError(), "Should not have errors")
	assert.NotNil(t, protoMsg, "Should return proto message")

	stringVal, ok := protoMsg.(*wrapperspb.StringValue)
	assert.True(t, ok, "Should be StringValue")
	assert.Equal(t, "test", stringVal.Value, "Should have correct value")
}

// TestCallToProto_WithDiagnostics tests calling ToProto that returns diagnostics
func TestCallToProto_WithDiagnostics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	model := &TestSetModel{Name: types.StringValue("test")}

	protoMsg, diags := callToProto(ctx, model)

	assert.False(t, diags.HasError(), "Should not have errors for valid value")
	assert.NotNil(t, protoMsg, "Should return proto message")
}

// TestCallToProto_InvalidValue tests calling ToProto with invalid value
func TestCallToProto_InvalidValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	model := &TestSetModel{Name: types.StringValue("")}

	protoMsg, diags := callToProto(ctx, model)

	assert.True(t, diags.HasError(), "Should have error for empty name")
	assert.Nil(t, protoMsg, "Should return nil proto message on error")
}

// TestHasUnknownInObject tests the unknown field detection using Framework types
func TestHasUnknownInObject(t *testing.T) {
	t.Parallel()

	t.Run("known fields", func(t *testing.T) {
		obj := types.ObjectValueMust(
			map[string]attr.Type{"value": types.StringType},
			map[string]attr.Value{"value": types.StringValue("test")},
		)
		assert.False(t, hasUnknownInObject(obj), "Should return false for known fields")
	})

	t.Run("unknown fields", func(t *testing.T) {
		obj := types.ObjectValueMust(
			map[string]attr.Type{"value": types.StringType},
			map[string]attr.Value{"value": types.StringUnknown()},
		)
		assert.True(t, hasUnknownInObject(obj), "Should return true for unknown fields")
	})

	t.Run("null fields", func(t *testing.T) {
		obj := types.ObjectValueMust(
			map[string]attr.Type{"value": types.StringType},
			map[string]attr.Value{"value": types.StringNull()},
		)
		assert.False(t, hasUnknownInObject(obj), "Should return false for null fields")
	})

	t.Run("nested unknown fields", func(t *testing.T) {
		nestedType := types.ObjectType{
			AttrTypes: map[string]attr.Type{"inner": types.StringType},
		}
		obj := types.ObjectValueMust(
			map[string]attr.Type{
				"name":   types.StringType,
				"nested": nestedType,
			},
			map[string]attr.Value{
				"name":   types.StringValue("test"),
				"nested": types.ObjectUnknown(nestedType.AttrTypes),
			},
		)
		assert.True(t, hasUnknownInObject(obj), "Should return true for nested unknown object")
	})
}
