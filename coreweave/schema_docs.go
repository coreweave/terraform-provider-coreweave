package coreweave

import (
	"fmt"
	"slices"
	"strings"
)

// EnumMarkdownValues renders the values of an int-keyed name map as a
// backtick-quoted, comma-separated list sorted by key. When dropZero is true
// the zero-key entry is omitted.
func EnumMarkdownValues(m map[int32]string, dropZero bool) string {
	// the values should be sorted by their enum (string) value, not the string value.
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
		values[i] = fmt.Sprintf("`%s`", m[k])
	}
	return strings.Join(values, ", ")
}
