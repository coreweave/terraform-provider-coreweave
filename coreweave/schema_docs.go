package coreweave

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// EnumMarkdownValues renders the values of an int-keyed name map as a
// backtick-quoted, comma-separated list sorted by enum value. When dropZero
// is true the zero-key entry is omitted.
func EnumMarkdownValues(m map[int32]string, dropZero bool) string {
	values := EnumValues(m, dropZero)
	for i, v := range values {
		values[i] = fmt.Sprintf("`%s`", v)
	}
	return strings.Join(values, ", ")
}

// EnumValues returns the string values of an int-keyed name map sorted by
// enum value. When dropZero is true the zero-key entry is omitted. Useful
// for building stringvalidator.OneOf(...) lists from proto enum maps.
func EnumValues(m map[int32]string, dropZero bool) []string {
	keys := make([]int32, 0, len(m))
	for k := range m {
		if dropZero && k == 0 {
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)

	values := make([]string, len(keys))
	for i, k := range keys {
		values[i] = m[k]
	}
	return values
}

// EnumValuesExcludingDeprecated returns the names of a proto enum's values
// sorted by number, omitting the zero value and any value annotated
// [deprecated = true]. Use it to offer only currently-valid enum values (e.g.
// in a stringvalidator.OneOf list or generated docs) so deprecated values are
// neither advertised nor accepted. Pass any enum instance's descriptor, e.g.
// inferencev1.CapacityType_CAPACITY_TYPE_MANAGED.Descriptor().
func EnumValuesExcludingDeprecated(desc protoreflect.EnumDescriptor) []string {
	type enumValue struct {
		number protoreflect.EnumNumber
		name   string
	}

	vals := desc.Values()
	kept := make([]enumValue, 0, vals.Len())
	for i := 0; i < vals.Len(); i++ {
		v := vals.Get(i)
		if v.Number() == 0 {
			continue
		}
		if opts, ok := v.Options().(*descriptorpb.EnumValueOptions); ok && opts.GetDeprecated() {
			continue
		}
		kept = append(kept, enumValue{number: v.Number(), name: string(v.Name())})
	}
	slices.SortFunc(kept, func(a, b enumValue) int { return int(a.number) - int(b.number) })

	names := make([]string, len(kept))
	for i, v := range kept {
		names[i] = v.name
	}
	return names
}

// EnumMarkdownValuesExcludingDeprecated renders EnumValuesExcludingDeprecated as
// a backtick-quoted, comma-separated list.
func EnumMarkdownValuesExcludingDeprecated(desc protoreflect.EnumDescriptor) string {
	values := EnumValuesExcludingDeprecated(desc)
	for i, v := range values {
		values[i] = fmt.Sprintf("`%s`", v)
	}
	return strings.Join(values, ", ")
}
