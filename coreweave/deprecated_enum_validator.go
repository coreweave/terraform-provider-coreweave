package coreweave

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// deprecatedEnumValueValidator emits a warning (never an error) when the
// configured string maps to a proto enum value annotated [deprecated = true].
//
// Hard rejection of deprecated values is intentionally left to the API, which
// owns enum validity. This validator only surfaces the proto's own deprecation
// metadata at plan time so operators get guidance before a failed apply. Being
// driven by the descriptor's deprecated flag (rather than a hardcoded value
// list) means it stays in sync with the proto automatically: if a value is
// un-deprecated upstream, the warning disappears on the next module bump.
type deprecatedEnumValueValidator struct {
	desc protoreflect.EnumDescriptor
}

// DeprecatedEnumValue returns a validator.String that warns when the configured
// value is a deprecated value of the given proto enum. Pass the enum's
// descriptor, e.g. inferencev1.CapacityType(0).Descriptor() — any enum instance
// returns the same whole-enum descriptor.
func DeprecatedEnumValue(desc protoreflect.EnumDescriptor) validator.String {
	return deprecatedEnumValueValidator{desc: desc}
}

func (v deprecatedEnumValueValidator) Description(_ context.Context) string {
	return "warns when the configured value is a deprecated enum value"
}

func (v deprecatedEnumValueValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v deprecatedEnumValueValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	// Mirror stringvalidator.OneOf: never validate null/unknown values.
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	name := req.ConfigValue.ValueString()
	ev := v.desc.Values().ByName(protoreflect.Name(name))
	if ev == nil {
		// Unrecognized value; OneOf (or the API) reports it.
		return
	}

	if opts, ok := ev.Options().(*descriptorpb.EnumValueOptions); ok && opts.GetDeprecated() {
		resp.Diagnostics.AddAttributeWarning(
			req.Path,
			"Deprecated value",
			fmt.Sprintf("%q is deprecated and is rejected by the API for new or updated resources. Use a non-deprecated value instead.", name),
		)
	}
}
