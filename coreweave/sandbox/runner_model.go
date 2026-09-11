package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The two nested Terraform objects mirror the v1 spec and policy fields. JSON
// is only the conversion boundary: the schema stays explicit, so adding API
// fields never silently expands Terraform's writable surface.
func objectToProto(value types.Object, message proto.Message) error {
	if value.IsNull() {
		return nil
	}
	raw, err := valueToJSON(value)
	if err != nil {
		return err
	}
	// disabled is an empty protobuf message represented by a true-only bool
	// because Terraform does not support an empty nested attribute schema.
	if spec, ok := raw.(map[string]any); ok {
		if plane, ok := spec["data_plane"].(map[string]any); ok {
			if disabled, ok := plane["disabled"].(bool); ok && disabled {
				plane["disabled"] = map[string]any{}
			}
		}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return protojson.Unmarshal(encoded, message)
}

func valueToJSON(value attr.Value) (any, error) {
	if value.IsUnknown() {
		return nil, fmt.Errorf("value is still unknown")
	}
	if value.IsNull() {
		return nil, nil
	}
	switch v := value.(type) {
	case jsontypes.Normalized:
		var raw map[string]any
		decoder := json.NewDecoder(bytes.NewBufferString(v.ValueString()))
		decoder.UseNumber()
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("expected a JSON object: %w", err)
		}
		if raw == nil {
			return nil, fmt.Errorf("expected a JSON object, got null")
		}
		return raw, nil
	case types.String:
		return v.ValueString(), nil
	case types.Int64:
		return v.ValueInt64(), nil
	case types.Bool:
		return v.ValueBool(), nil
	case types.Object:
		return attributesToJSON(v.Attributes())
	case types.Map:
		for name, element := range v.Elements() {
			if element.IsNull() {
				return nil, fmt.Errorf("map entry %q must not be null", name)
			}
		}
		return attributesToJSON(v.Elements())
	case types.List:
		return elementsToJSON(v.Elements())
	case types.Set:
		return elementsToJSON(v.Elements())
	default:
		return nil, fmt.Errorf("unsupported runner attribute type %T", value)
	}
}

func attributesToJSON(attributes map[string]attr.Value) (map[string]any, error) {
	out := make(map[string]any, len(attributes))
	for name, value := range attributes {
		if value.IsNull() {
			continue
		}
		encoded, err := valueToJSON(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = encoded
	}
	return out, nil
}

func elementsToJSON(elements []attr.Value) ([]any, error) {
	out := make([]any, len(elements))
	for i, value := range elements {
		encoded, err := valueToJSON(value)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		if encoded == nil {
			return nil, fmt.Errorf("element %d must not be null", i)
		}
		out[i] = encoded
	}
	return out, nil
}

func objectFromProto(ctx context.Context, message proto.Message, previous types.Object, typ types.ObjectType) (types.Object, error) {
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(message)
	if err != nil {
		return types.ObjectNull(typ.AttrTypes), err
	}
	var raw map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return types.ObjectNull(typ.AttrTypes), err
	}
	normalizeProtoJSON(raw, message.ProtoReflect().Descriptor(), previous)
	if plane, ok := raw["data_plane"].(map[string]any); ok {
		if _, ok := plane["disabled"]; ok {
			plane["disabled"] = true
		}
	}
	value, err := jsonToValue(ctx, raw, previous, typ)
	if err != nil {
		return types.ObjectNull(typ.AttrTypes), err
	}
	return value.(types.Object), nil
}

// EmitDefaultValues is needed to read explicit false/zero settings, but an
// unspecified enum represents an omitted setting rather than a valid selection.
func normalizeProtoJSON(raw map[string]any, descriptor protoreflect.MessageDescriptor, previous types.Object) {
	fields := descriptor.Fields()
	prior := previous.Attributes()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		name := string(field.Name())
		if field.Kind() == protoreflect.EnumKind && !field.IsList() {
			if raw[name] == string(field.Enum().Values().ByNumber(0).Name()) {
				delete(raw, name)
			}
			continue
		}
		if field.Kind() != protoreflect.MessageKind || field.IsMap() {
			continue
		}
		if field.Message().FullName() == "google.protobuf.Struct" {
			continue
		}
		if field.Message().FullName() == "google.protobuf.Timestamp" {
			if old, ok := prior[name].(types.String); ok && !old.IsNull() && !old.IsUnknown() {
				oldTime, oldErr := time.Parse(time.RFC3339Nano, old.ValueString())
				newTime, newErr := time.Parse(time.RFC3339Nano, fmt.Sprint(raw[name]))
				if oldErr == nil && newErr == nil && oldTime.Equal(newTime) {
					raw[name] = old.ValueString()
				}
			}
			continue
		}
		if nested, ok := raw[name].(map[string]any); ok {
			old, _ := prior[name].(types.Object)
			normalizeProtoJSON(nested, field.Message(), old)
		}
		if elements, ok := raw[name].([]any); ok {
			oldList, _ := prior[name].(types.List)
			for i, element := range elements {
				if nested, ok := element.(map[string]any); ok {
					var old types.Object
					if i < len(oldList.Elements()) {
						old, _ = oldList.Elements()[i].(types.Object)
					}
					normalizeProtoJSON(nested, field.Message(), old)
				}
			}
		}
	}
}

func nullValue(ctx context.Context, typ attr.Type) (attr.Value, error) {
	return typ.ValueFromTerraform(ctx, tftypes.NewValue(typ.TerraformType(ctx), nil))
}

func zeroJSON(raw any) bool {
	switch v := raw.(type) {
	case string:
		return v == ""
	case bool:
		return !v
	case json.Number:
		return v.String() == "0"
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	default:
		return raw == nil
	}
}

func jsonToValue(ctx context.Context, raw any, previous attr.Value, typ attr.Type) (attr.Value, error) {
	if raw == nil {
		return nullValue(ctx, typ)
	}
	// Preserve omitted scalars/collections when proto3 emits their zero value.
	// Message presence is different: an empty policy/constraint/allowlist is a
	// real object and must not collapse to null.
	_, object := typ.(types.ObjectType)
	_, opaque := typ.(jsontypes.NormalizedType)
	if !object && !opaque && (previous == nil || previous.IsNull()) && zeroJSON(raw) {
		return nullValue(ctx, typ)
	}
	switch t := typ.(type) {
	case jsontypes.NormalizedType:
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		return jsontypes.NewNormalizedValue(string(encoded)), nil
	case basetypes.StringType:
		v, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", raw)
		}
		return types.StringValue(v), nil
	case basetypes.BoolType:
		v, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool, got %T", raw)
		}
		return types.BoolValue(v), nil
	case basetypes.Int64Type:
		// ProtoJSON quotes int64 fields. Parsing the original decimal text
		// avoids rounding presence-aware GPU limits through a float64.
		v, err := strconv.ParseInt(fmt.Sprint(raw), 10, 64)
		if err != nil {
			return nil, err
		}
		return types.Int64Value(v), nil
	case types.ObjectType:
		return jsonToObject(ctx, raw, previous, t)
	case types.MapType:
		return jsonToMap(ctx, raw, previous, t)
	case types.ListType:
		elements, err := jsonToElements(ctx, raw, previous, t.ElemType)
		if err != nil {
			return nil, err
		}
		value, diags := types.ListValue(t.ElemType, elements)
		if diags.HasError() {
			return nil, fmt.Errorf("invalid list: %v", diags.Errors())
		}
		return value, nil
	case types.SetType:
		elements, err := jsonToElements(ctx, raw, previous, t.ElemType)
		if err != nil {
			return nil, err
		}
		value, diags := types.SetValue(t.ElemType, elements)
		if diags.HasError() {
			return nil, fmt.Errorf("invalid set: %v", diags.Errors())
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported runner attribute type %T", typ)
	}
}

func jsonToObject(ctx context.Context, raw any, previous attr.Value, typ types.ObjectType) (attr.Value, error) {
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object, got %T", raw)
	}
	var prior map[string]attr.Value
	if v, ok := previous.(types.Object); ok {
		prior = v.Attributes()
	}
	attributes := make(map[string]attr.Value, len(typ.AttrTypes))
	for name, attributeType := range typ.AttrTypes {
		value, err := jsonToValue(ctx, values[name], prior[name], attributeType)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		attributes[name] = value
	}
	value, diags := types.ObjectValue(typ.AttrTypes, attributes)
	if diags.HasError() {
		return nil, fmt.Errorf("invalid object: %v", diags.Errors())
	}
	return value, nil
}

func jsonToMap(ctx context.Context, raw any, previous attr.Value, typ types.MapType) (attr.Value, error) {
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map, got %T", raw)
	}
	var prior map[string]attr.Value
	if v, ok := previous.(types.Map); ok {
		prior = v.Elements()
	}
	attributes := make(map[string]attr.Value, len(values))
	for name, rawValue := range values {
		// Map entries, including empty strings, are always present values.
		previousValue := prior[name]
		if previousValue == nil {
			previousValue = types.StringUnknown()
		}
		value, err := jsonToValue(ctx, rawValue, previousValue, typ.ElemType)
		if err != nil {
			return nil, err
		}
		attributes[name] = value
	}
	value, diags := types.MapValue(typ.ElemType, attributes)
	if diags.HasError() {
		return nil, fmt.Errorf("invalid map: %v", diags.Errors())
	}
	return value, nil
}

func jsonToElements(ctx context.Context, raw any, previous attr.Value, typ attr.Type) ([]attr.Value, error) {
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", raw)
	}
	var prior []attr.Value
	if v, ok := previous.(types.List); ok {
		prior = v.Elements()
	}
	result := make([]attr.Value, len(values))
	for i, rawValue := range values {
		var previousValue attr.Value
		if i < len(prior) {
			previousValue = prior[i]
		}
		// List/set primitive elements must remain concrete even when zero.
		if _, object := typ.(types.ObjectType); !object && previousValue == nil {
			previousValue = types.StringUnknown()
		}
		value, err := jsonToValue(ctx, rawValue, previousValue, typ)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		result[i] = value
	}
	return result, nil
}
