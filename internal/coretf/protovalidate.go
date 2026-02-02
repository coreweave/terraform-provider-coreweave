package coretf

import (
	"context"
	"fmt"
	"reflect"

	"buf.build/go/protovalidate"
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

	// Extract the model from the object
	var model T
	diags := req.ConfigValue.As(ctx, &model, basetypes.ObjectAsOptions{})
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}

	// Check if the model has any unknown fields
	if hasUnknownFields(reflect.ValueOf(model)) {
		// Skip validation if any fields are unknown
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

	// Extract elements from the set
	var models []T
	diags := req.ConfigValue.ElementsAs(ctx, &models, false)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}

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
	for i, model := range models {
		// Check if this element has unknown fields
		if hasUnknownFields(reflect.ValueOf(model)) {
			// Skip this element, continue with others
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
				req.Path.AtSetValue(req.ConfigValue.Elements()[i]),
				fmt.Sprintf("Validation failed for element %d", i),
				formatProtoValidateError(err),
			)
		}
	}
}

// hasUnknownFields checks if a struct has any fields that are unknown.
// This handles types.String, types.Bool, types.Int64, etc.
func hasUnknownFields(v reflect.Value) bool {
	// Dereference pointers
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return false
	}

	// Check each field
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)

		// Check if field has IsUnknown method
		if field.CanInterface() {
			if unknownable, ok := field.Interface().(interface{ IsUnknown() bool }); ok {
				if unknownable.IsUnknown() {
					return true
				}
			}
		}

		// Recursively check nested structs
		if field.Kind() == reflect.Struct || field.Kind() == reflect.Ptr {
			if hasUnknownFields(field) {
				return true
			}
		}

		// Check slices of structs
		if field.Kind() == reflect.Slice {
			for j := 0; j < field.Len(); j++ {
				if hasUnknownFields(field.Index(j)) {
					return true
				}
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
