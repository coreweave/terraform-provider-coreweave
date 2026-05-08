package sandbox

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// stringOrNull returns a null types.String when s is empty, otherwise the value.
// API responses use empty strings for unset fields; mapping them to null prevents
// Terraform from showing spurious diffs against unconfigured optional attributes.
func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// timestampStringsEqual compares two RFC3339 timestamp strings for semantic
// equality, parsing each side and comparing the resulting time.Time. Falls back
// to byte equality if either side fails to parse. Null/unknown values must
// match exactly.
func timestampStringsEqual(a, b types.String) bool {
	if a.IsNull() != b.IsNull() || a.IsUnknown() != b.IsUnknown() {
		return false
	}
	if a.IsNull() || a.IsUnknown() {
		return true
	}
	at, errA := time.Parse(time.RFC3339Nano, a.ValueString())
	bt, errB := time.Parse(time.RFC3339Nano, b.ValueString())
	if errA != nil || errB != nil {
		return a.ValueString() == b.ValueString()
	}
	return at.Equal(bt)
}

// timestampString converts a protobuf timestamp to an RFC3339 types.String, or null.
func timestampString(t *timestamppb.Timestamp) types.String {
	if t == nil {
		return types.StringNull()
	}
	return types.StringValue(t.AsTime().Format(rfc3339Nano))
}

const rfc3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

// stringListToSlice extracts a []string from a types.List of strings. A null or
// unknown list yields a nil slice.
func stringListToSlice(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	out := make([]string, 0, len(list.Elements()))
	d := list.ElementsAs(ctx, &out, false)
	return out, d
}

// stringSliceToList converts a []string from the API into a types.List, preserving
// "null vs empty" by returning a null list when the API returned nothing AND the
// prior plan/state value was null. If the prior value was a known list (even empty),
// we round-trip through the API value to keep refresh stable.
func stringSliceToList(_ context.Context, in []string, prior types.List) (types.List, diag.Diagnostics) {
	if len(in) == 0 && (prior.IsNull() || prior.IsUnknown()) {
		return types.ListNull(types.StringType), nil
	}
	vals := make([]attr.Value, 0, len(in))
	for _, s := range in {
		vals = append(vals, types.StringValue(s))
	}
	return types.ListValue(types.StringType, vals)
}

// stringMapToMap extracts a map[string]string from a types.Map.
func stringMapToMap(ctx context.Context, m types.Map) (map[string]string, diag.Diagnostics) {
	if m.IsNull() || m.IsUnknown() {
		return nil, nil
	}
	out := map[string]string{}
	d := m.ElementsAs(ctx, &out, false)
	return out, d
}

// stringMapFromMap converts a map[string]string from the API into a types.Map,
// preserving "null vs empty" against the prior plan/state value.
func stringMapFromMap(_ context.Context, in map[string]string, prior types.Map) (types.Map, diag.Diagnostics) {
	if len(in) == 0 && (prior.IsNull() || prior.IsUnknown()) {
		return types.MapNull(types.StringType), nil
	}
	vals := make(map[string]attr.Value, len(in))
	for k, v := range in {
		vals[k] = types.StringValue(v)
	}
	return types.MapValue(types.StringType, vals)
}
