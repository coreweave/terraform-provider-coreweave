package model

import (
	"context"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type TelemetryStreamRef struct {
	Slug types.String `tfsdk:"slug"`
}

func (r *TelemetryStreamRef) Set(ref *typesv1beta1.TelemetryStreamRef) {
	r.Slug = types.StringValue(ref.Slug)
}

func (r *TelemetryStreamRef) ToMsg() (msg *typesv1beta1.TelemetryStreamRef) {
	if r == nil {
		return nil
	}

	msg = &typesv1beta1.TelemetryStreamRef{
		Slug: r.Slug.ValueString(),
	}

	return msg
}

type TelemetryStreamSpec struct {
	DisplayName types.String           `tfsdk:"display_name"`
	Kind        types.String           `tfsdk:"kind"`
	Filter      *TelemetryStreamFilter `tfsdk:"filter"`
}

// TelemetryStreamFilter is map(set(string)) on both sides. The proto wraps the
// values in a StringList message only because proto maps cannot hold a
// `repeated` field directly; that wrapper is an encoding detail and has no
// business reaching HCL, where it would force users to write
// `filter.include["app"].values` instead of `filter.include["app"]`.
//
// Set rather than List: label-selector values are an unordered bag, and the
// API makes no ordering promise, so a List would produce a phantom diff
// whenever the server returned them in a different order.
type TelemetryStreamFilter struct {
	Include map[string][]string `tfsdk:"include"`
	Exclude map[string][]string `tfsdk:"exclude"`
}

func (f *TelemetryStreamFilter) Set(filter *typesv1beta1.LabelSelector) {
	if f == nil || filter == nil {
		return
	}

	f.Include = flattenStringLists(filter.GetInclude())
	f.Exclude = flattenStringLists(filter.GetExclude())
}

// flattenStringLists unwraps the proto's StringList map values. It returns nil
// for an empty map so an absent selector side reads as null rather than as an
// empty map asserting "no labels selected".
func flattenStringLists(in map[string]*typesv1beta1.StringList) map[string][]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string][]string, len(in))
	for key, list := range in {
		values := make([]string, len(list.GetValues()))
		copy(values, list.GetValues())
		out[key] = values
	}
	return out
}

func (s *TelemetryStreamSpec) Set(spec *typesv1beta1.TelemetryStreamSpec) {
	s.DisplayName = types.StringValue(spec.GetDisplayName())
	s.Kind = types.StringValue(spec.GetKind().String())
	if spec.Filter != nil {
		s.Filter = new(TelemetryStreamFilter)
		s.Filter.Set(spec.Filter)
	} else {
		s.Filter = nil
	}
}

type TelemetryStreamStatus struct {
	CreatedAt    timetypes.RFC3339      `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339      `tfsdk:"updated_at"`
	StateString  types.String           `tfsdk:"state"`
	StateMessage types.String           `tfsdk:"state_message"`
	ZonesActive  map[string]*ActiveZone `tfsdk:"zones_active"`
}

type ActiveZone struct {
	Clusters types.List `tfsdk:"clusters"`
}

func (a *ActiveZone) Set(zone *typesv1beta1.TelemetryStreamStatus_ZoneClusterStatus) (diagnostics diag.Diagnostics) {
	clustersRaw := make([]ActiveCluster, len(zone.Clusters))
	for i, cluster := range zone.Clusters {
		clustersRaw[i] = ActiveCluster{
			Id: types.StringValue(cluster.Id),
		}
	}
	a.Clusters, diagnostics = types.ListValueFrom(context.Background(), types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"id": types.StringType,
		},
	}, clustersRaw)
	return
}

type ActiveCluster struct {
	Id types.String `tfsdk:"id"`
}

func (s *TelemetryStreamStatus) Set(status *typesv1beta1.TelemetryStreamStatus) {
	s.CreatedAt = timestampToTimeValue(status.GetCreatedAt())
	s.UpdatedAt = timestampToTimeValue(status.GetUpdatedAt())
	s.StateString = types.StringValue(status.GetState().String())
	s.StateMessage = streamStateMessage(status)
}

// streamStateMessage preserves the distinction between "the server sent no
// message" and "the server sent an empty one".
func streamStateMessage(status *typesv1beta1.TelemetryStreamStatus) types.String {
	if status == nil || !status.HasStateMessage() {
		return types.StringNull()
	}
	return types.StringValue(status.GetStateMessage())
}

// TelemetryStreamDataSource is a flattened model for the stream data source
// that combines ref, spec, and status fields at the top level, similar to endpoint resources.
type TelemetryStreamDataSource struct {
	Slug types.String `tfsdk:"slug"`

	DisplayName types.String           `tfsdk:"display_name"`
	Kind        types.String           `tfsdk:"kind"`
	Filter      *TelemetryStreamFilter `tfsdk:"filter"`

	CreatedAt    timetypes.RFC3339      `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339      `tfsdk:"updated_at"`
	State        types.String           `tfsdk:"state"`
	StateMessage types.String           `tfsdk:"state_message"`
	ZonesActive  map[string]*ActiveZone `tfsdk:"zones_active"`
}

func (m *TelemetryStreamDataSource) Set(stream *typesv1beta1.TelemetryStream) (diagnostics diag.Diagnostics) {
	if stream == nil {
		return diagnostics
	}

	status := stream.GetStatus()

	m.Slug = types.StringValue(stream.GetRef().GetSlug())

	m.DisplayName = types.StringValue(stream.GetSpec().GetDisplayName())
	m.Kind = types.StringValue(stream.GetSpec().GetKind().String())
	if stream.GetSpec().GetFilter() != nil {
		m.Filter = new(TelemetryStreamFilter)
		m.Filter.Set(stream.GetSpec().GetFilter())
	} else {
		m.Filter = nil
	}

	m.CreatedAt = timestampToTimeValue(status.GetCreatedAt())
	m.UpdatedAt = timestampToTimeValue(status.GetUpdatedAt())
	m.State = types.StringValue(status.GetState().String())
	m.StateMessage = streamStateMessage(status)

	m.ZonesActive = make(map[string]*ActiveZone, len(status.GetZonesActive()))
	for _, zone := range status.GetZonesActive() {
		az := new(ActiveZone)
		diagnostics.Append(az.Set(zone)...)
		m.ZonesActive[zone.GetZoneSlug()] = az
	}

	return diagnostics
}

func (m *TelemetryStreamDataSource) ToRef() *typesv1beta1.TelemetryStreamRef {
	if m == nil {
		return nil
	}

	return &typesv1beta1.TelemetryStreamRef{
		Slug: m.Slug.ValueString(),
	}
}
