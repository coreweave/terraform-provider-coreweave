package coreweave

import (
	"fmt"
	"slices"
	"strings"
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
