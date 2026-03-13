package coretf

import (
	"context"
	"fmt"
	"reflect"

	"buf.build/go/protovalidate"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"google.golang.org/protobuf/proto"
)

// Protoable is an interface for types that can be converted to proto messages.
// Models can implement any of these signatures:
//   - ToProto() *proto.Message
//   - ToProto() (*proto.Message, diag.Diagnostics)
//   - ToProto(context.Context) (*proto.Message, diag.Diagnostics)
type Protoable interface {
	any
}

// objectValidator validates single nested attributes by converting them to proto messages.
type objectValidator[T Protoable] struct {
	description string
}

// ProtoValidator creates a validator for single nested attributes.
// The model type T must have a ToProto method with one of these signatures:
//   - ToProto() *proto.Message
//   - ToProto() (*proto.Message, diag.Diagnostics)
//   - ToProto(context.Context) (*proto.Message, diag.Diagnostics)
//
// Example usage:
//
//	coretf.ProtoValidator[VpcIngressResourceModel]()
func ProtoValidator[T Protoable]() validator.Object {
	return &objectValidator[T]{
		description: fmt.Sprintf("validates %T using protovalidate", *new(T)),
	}
}

func (v *objectValidator[T]) Description(ctx context.Context) string {
	return v.description
}

func (v *objectValidator[T]) MarkdownDescription(ctx context.Context) string {
	return v.description
}

func (v *objectValidator[T]) ValidateObject(ctx context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	// Skip validation for unknown or null values
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}

	// Check if object has any unknown nested values before trying to extract
	// This prevents "target type cannot handle unknown values" errors when
	// the model has pointer fields that can't represent unknown state
	objValue, diags := req.ConfigValue.ToObjectValue(ctx)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}

	if hasUnknownInObject(objValue) {
		// Skip validation for objects with unknown nested values
		return
	}

	// Object is fully known - safe to extract to struct
	var model T
	diags = objValue.As(ctx, &model, basetypes.ObjectAsOptions{})
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}

	// Convert to proto message using reflection to find and call ToProto method
	protoMsg, diags := callToProto(ctx, &model)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}

	// Skip validation if conversion returned nil (model couldn't be fully converted)
	if protoMsg == nil {
		return
	}

	// Validate the proto message
	validator, err := protovalidate.New()
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Protovalidate initialization failed",
			fmt.Sprintf("Failed to create protovalidate validator: %s", err.Error()),
		)
		return
	}

	if err := validator.Validate(protoMsg); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Validation failed",
			formatProtoValidateError(err),
		)
	}
}

// setValidator validates set attributes by converting each element to a proto message.
type setValidator[T Protoable] struct {
	description string
}

// ProtoSetValidator creates a validator for set attributes.
// The model type T must have a ToProto method with one of these signatures:
//   - ToProto() *proto.Message
//   - ToProto() (*proto.Message, diag.Diagnostics)
//   - ToProto(context.Context) (*proto.Message, diag.Diagnostics)
//
// Example usage:
//
//	coretf.ProtoSetValidator[VpcPrefixResourceModel]()
func ProtoSetValidator[T Protoable]() validator.Set {
	return &setValidator[T]{
		description: fmt.Sprintf("validates each %T element using protovalidate", *new(T)),
	}
}

func (v *setValidator[T]) Description(ctx context.Context) string {
	return v.description
}

func (v *setValidator[T]) MarkdownDescription(ctx context.Context) string {
	return v.description
}

func (v *setValidator[T]) ValidateSet(ctx context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	// Skip validation for unknown or null values
	if req.ConfigValue.IsUnknown() || req.ConfigValue.IsNull() {
		return
	}

	// Get raw elements to handle unknown nested values
	// We MUST check for unknowns BEFORE calling ElementsAs() because:
	// - Pointer fields (*NestedModel) cannot handle unknown values
	// - ElementsAs() will fail with "target type cannot handle unknown values"
	// - We need to skip validation for elements with unknowns, not fail
	elements := req.ConfigValue.Elements()

	// Create protovalidate validator once for all elements
	validator, err := protovalidate.New()
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Protovalidate initialization failed",
			fmt.Sprintf("Failed to create protovalidate validator: %s", err.Error()),
		)
		return
	}

	// Validate each element independently
	for i, elem := range elements {
		// Check if element is unknown or null
		if elem.IsNull() {
			continue
		}
		if unknownable, ok := elem.(interface{ IsUnknown() bool }); ok && unknownable.IsUnknown() {
			continue
		}

		// Cast element to types.Object (set elements are objects for nested attributes)
		objVal, ok := elem.(basetypes.ObjectValuable)
		if !ok {
			// Element is not an object - might be a simple type
			// This shouldn't happen for nested attributes but handle gracefully
			continue
		}

		objValue, diags := objVal.ToObjectValue(ctx)
		resp.Diagnostics.Append(diags...)
		if diags.HasError() {
			continue
		}

		// Check if object has any unknown nested values
		if hasUnknownInObject(objValue) {
			// Skip validation for this element - has unknown nested values
			continue
		}

		// Object is fully known - safe to extract to struct
		var model T
		diags = objValue.As(ctx, &model, basetypes.ObjectAsOptions{})
		if diags.HasError() {
			resp.Diagnostics.Append(diags...)
			continue
		}

		// Convert to proto message using reflection
		protoMsg, diags := callToProto(ctx, &model)
		resp.Diagnostics.Append(diags...)
		if diags.HasError() {
			continue
		}

		// Skip if conversion returned nil
		if protoMsg == nil {
			continue
		}

		// Validate the proto message
		if err := validator.Validate(protoMsg); err != nil {
			resp.Diagnostics.AddAttributeError(
				req.Path.AtSetValue(elem),
				fmt.Sprintf("Validation failed for element %d", i),
				formatProtoValidateError(err),
			)
		}
	}
}

// hasUnknownInObject recursively checks if a types.Object has any unknown values.
// This works with Terraform Framework types before they're extracted to structs.
func hasUnknownInObject(obj basetypes.ObjectValue) bool {
	if obj.IsUnknown() {
		return true
	}

	attrs := obj.Attributes()
	for _, attrVal := range attrs {
		if hasUnknownInValue(attrVal) {
			return true
		}
	}

	return false
}

// hasUnknownInValue checks if an attr.Value (or nested structures) is unknown.
// This recursively checks all Terraform Framework types (Object, List, Set, Map, etc.)
func hasUnknownInValue(val attr.Value) bool {
	// Check if value is null first
	if val.IsNull() {
		return false
	}

	// Check if value has IsUnknown method
	if unknownable, ok := val.(interface{ IsUnknown() bool }); ok {
		if unknownable.IsUnknown() {
			return true
		}
	}

	// Recursively check nested objects
	if objValuable, ok := val.(basetypes.ObjectValuable); ok {
		objValue, diags := objValuable.ToObjectValue(context.Background())
		if diags.HasError() {
			// If we can't convert, assume it might be unknown
			return true
		}
		return hasUnknownInObject(objValue)
	}

	// Check lists
	if listValuable, ok := val.(basetypes.ListValuable); ok {
		listValue, diags := listValuable.ToListValue(context.Background())
		if diags.HasError() {
			return true
		}
		if listValue.IsUnknown() {
			return true
		}
		for _, elem := range listValue.Elements() {
			if hasUnknownInValue(elem) {
				return true
			}
		}
	}

	// Check sets
	if setValuable, ok := val.(basetypes.SetValuable); ok {
		setValue, diags := setValuable.ToSetValue(context.Background())
		if diags.HasError() {
			return true
		}
		if setValue.IsUnknown() {
			return true
		}
		for _, elem := range setValue.Elements() {
			if hasUnknownInValue(elem) {
				return true
			}
		}
	}

	// Check maps
	if mapValuable, ok := val.(basetypes.MapValuable); ok {
		mapValue, diags := mapValuable.ToMapValue(context.Background())
		if diags.HasError() {
			return true
		}
		if mapValue.IsUnknown() {
			return true
		}
		for _, elem := range mapValue.Elements() {
			if hasUnknownInValue(elem) {
				return true
			}
		}
	}

	return false
}

// callToProto uses reflection to call the ToProto method on a model.
// It supports three signatures:
//   - ToProto() *proto.Message
//   - ToProto() (*proto.Message, diag.Diagnostics)
//   - ToProto(context.Context) (*proto.Message, diag.Diagnostics)
func callToProto(ctx context.Context, model any) (proto.Message, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	val := reflect.ValueOf(model)
	method := val.MethodByName("ToProto")
	if !method.IsValid() {
		diagnostics.AddError(
			"ToProto method not found",
			fmt.Sprintf("Type %T does not have a ToProto method", model),
		)
		return nil, diagnostics
	}

	methodType := method.Type()
	numIn := methodType.NumIn()
	numOut := methodType.NumOut()

	var results []reflect.Value

	// Call ToProto based on its signature
	switch {
	case numIn == 0 && numOut == 1:
		// ToProto() *proto.Message
		results = method.Call(nil)

	case numIn == 0 && numOut == 2:
		// ToProto() (*proto.Message, diag.Diagnostics)
		results = method.Call(nil)

	case numIn == 1 && numOut == 2:
		// ToProto(context.Context) (*proto.Message, diag.Diagnostics)
		results = method.Call([]reflect.Value{reflect.ValueOf(ctx)})

	default:
		diagnostics.AddError(
			"Unsupported ToProto signature",
			fmt.Sprintf("Type %T has an unsupported ToProto signature with %d inputs and %d outputs", model, numIn, numOut),
		)
		return nil, diagnostics
	}

	// Extract proto message from first return value
	var protoMsg proto.Message
	if len(results) > 0 && !results[0].IsNil() {
		protoMsg = results[0].Interface().(proto.Message)
	}

	// Extract diagnostics from second return value if present
	if len(results) > 1 {
		if diagsVal, ok := results[1].Interface().(diag.Diagnostics); ok {
			diagnostics.Append(diagsVal...)
		}
	}

	return protoMsg, diagnostics
}

// formatProtoValidateError formats protovalidate errors for user consumption.
func formatProtoValidateError(err error) string {
	// For now, return the error as-is
	// TODO: Parse and reformat proto field paths to Terraform paths
	return err.Error()
}

// ValidateProtoMessage validates a proto message directly using protovalidate.
// This is useful for resource-level ValidateConfig methods.
func ValidateProtoMessage(protoMsg proto.Message) diag.Diagnostics {
	var diagnostics diag.Diagnostics

	validator, err := protovalidate.New()
	if err != nil {
		diagnostics.AddError(
			"Protovalidate initialization failed",
			fmt.Sprintf("Failed to create protovalidate validator: %s", err.Error()),
		)
		return diagnostics
	}

	if err := validator.Validate(protoMsg); err != nil {
		diagnostics.AddError(
			"Validation failed",
			formatProtoValidateError(err),
		)
	}

	return diagnostics
}

// ValidateProtoAtPath validates a proto message and adds errors at a specific path.
// This is useful for resource-level ValidateConfig methods.
func ValidateProtoAtPath(protoMsg proto.Message, attrPath path.Path) diag.Diagnostics {
	var diagnostics diag.Diagnostics

	validator, err := protovalidate.New()
	if err != nil {
		diagnostics.AddAttributeError(
			attrPath,
			"Protovalidate initialization failed",
			fmt.Sprintf("Failed to create protovalidate validator: %s", err.Error()),
		)
		return diagnostics
	}

	if err := validator.Validate(protoMsg); err != nil {
		diagnostics.AddAttributeError(
			attrPath,
			"Validation failed",
			formatProtoValidateError(err),
		)
	}

	return diagnostics
}
