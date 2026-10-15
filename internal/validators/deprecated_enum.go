package validators

import (
	"context"
	"fmt"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// deprecatedEnumValueValidator surfaces proto enum values annotated
// [deprecated = true]. It reads the deprecated flag from the descriptor at
// runtime, so it stays in sync with the proto automatically (no hardcoded value
// list). When reject is false it emits a warning; when true it emits an error.
//
// "deprecated" in the proto does not by itself mean the API rejects the value,
// so the warning form makes no claim about API behavior. The reject form is
// opt-in for attributes where the caller knows the value is no longer accepted
// (e.g. the API rejects it) and allowing it would produce a non-convergent plan.
type deprecatedEnumValueValidator struct {
	desc     protoreflect.EnumDescriptor
	reject   bool
	guidance string
}

// DeprecatedEnumValue returns a validator.String that warns when the configured
// value is a deprecated value of the given proto enum. Pass any enum instance's
// descriptor, e.g. inferencev1.CapacityType_CAPACITY_TYPE_MANAGED.Descriptor().
func DeprecatedEnumValue(desc protoreflect.EnumDescriptor) validator.String {
	return deprecatedEnumValueValidator{desc: desc}
}

// RejectDeprecatedEnumValue returns a validator.String that errors when the
// configured value is a deprecated value of the given proto enum. Use it where a
// deprecated value is no longer accepted and must be rejected at plan time
// rather than failing at apply.
//
// guidance is appended to the error to tell the user what to do — e.g. the
// specific replacement value and any migration notes. The proto deprecation flag
// does not encode a replacement, so this is the caller's responsibility. When
// guidance is empty, the error falls back to listing the enum's non-deprecated
// values.
func RejectDeprecatedEnumValue(desc protoreflect.EnumDescriptor, guidance string) validator.String {
	return deprecatedEnumValueValidator{desc: desc, reject: true, guidance: guidance}
}

func (v deprecatedEnumValueValidator) Description(_ context.Context) string {
	if v.reject {
		return "errors when the configured value is a deprecated enum value"
	}
	return "warns when the configured value is a deprecated enum value"
}

func (v deprecatedEnumValueValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v deprecatedEnumValueValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	// Mirror stringvalidator.OneOf: never validate null/unknown. Guard nil desc.
	if v.desc == nil || req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	name := req.ConfigValue.ValueString()
	ev := v.desc.Values().ByName(protoreflect.Name(name))
	if ev == nil {
		// Unrecognized value; OneOf (or the API) reports it.
		return
	}
	if opts, ok := ev.Options().(*descriptorpb.EnumValueOptions); !ok || !opts.GetDeprecated() {
		return
	}

	if v.reject {
		detail := fmt.Sprintf("%q is deprecated and is no longer an accepted value.", name)
		if v.guidance != "" {
			detail += " " + v.guidance
		} else {
			detail += fmt.Sprintf(" Use one of: %s.", coreweave.EnumMarkdownValuesExcludingDeprecated(v.desc))
		}
		resp.Diagnostics.AddAttributeError(req.Path, "Deprecated value", detail)
		return
	}

	resp.Diagnostics.AddAttributeWarning(
		req.Path,
		"Deprecated value",
		fmt.Sprintf("%q is deprecated.", name),
	)
}
