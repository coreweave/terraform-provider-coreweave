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

type TelemetryStreamFilter struct {
	Include map[string]StringList `tfsdk:"include"`
	Exclude map[string]StringList `tfsdk:"exclude"`
}

type StringList struct {
	Values types.List `tfsdk:"values"`
}

func (s *StringList) Set(values []string) (diagnostics diag.Diagnostics) {
	listValues, diags := types.ListValueFrom(context.Background(), types.StringType, values)
	diagnostics.Append(diags...)
	s.Values = listValues
	return
}

func (f *TelemetryStreamFilter) Set(filter *typesv1beta1.LabelSelector) (diagnostics diag.Diagnostics) {
	include := make(map[string]StringList, len(filter.Include))
	exclude := make(map[string]StringList, len(filter.Exclude))

	for key, values := range filter.Include {
		sl := new(StringList)
		diagnostics.Append(sl.Set(values.Values)...)
		include[key] = *sl
	}
	for key, values := range filter.Exclude {
		sl := new(StringList)
		diagnostics.Append(sl.Set(values.Values)...)
		exclude[key] = *sl
	}

	f.Include = include
	f.Exclude = exclude
	return
}

func (s *TelemetryStreamSpec) Set(spec *typesv1beta1.TelemetryStreamSpec) (diagnostics diag.Diagnostics) {
	s.DisplayName = types.StringValue(spec.DisplayName)
	s.Kind = types.StringValue(spec.GetKind().String())
	if spec.Filter != nil {
		s.Filter = new(TelemetryStreamFilter)
		diagnostics.Append(s.Filter.Set(spec.Filter)...)
	} else {
		s.Filter = nil
	}
	return
}

type TelemetryStreamStatus struct {
	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	StateString  types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
	ZonesActive  map[string]*ActiveZone         `tfsdk:"zones_active"`
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
	s.CreatedAt = timestampToTimeValue(status.CreatedAt)
	s.UpdatedAt = timestampToTimeValue(status.UpdatedAt)
	s.StateCode = types.Int32Value(int32(status.State.Number()))
	s.StateString = types.StringValue(status.State.String())
	s.StateMessage = types.StringPointerValue(status.StateMessage)
}

// TelemetryStreamDataSource is a flattened model for the stream data source
// that combines ref, spec, and status fields at the top level, similar to endpoint resources.
type TelemetryStreamDataSource struct {
	Slug types.String `tfsdk:"slug"`

	DisplayName types.String           `tfsdk:"display_name"`
	Kind        types.String           `tfsdk:"kind"`
	Filter      *TelemetryStreamFilter `tfsdk:"filter"`

	CreatedAt    timetypes.RFC3339 `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339 `tfsdk:"updated_at"`
	StateCode    types.Int32       `tfsdk:"state_code"`
	State        types.String      `tfsdk:"state"`
	StateMessage types.String      `tfsdk:"state_message"`
	ZonesActive  map[string]*ActiveZone         `tfsdk:"zones_active"`
}

func (m *TelemetryStreamDataSource) Set(stream *typesv1beta1.TelemetryStream) (diagnostics diag.Diagnostics) {
	if stream == nil {
		return
	}

	m.Slug = types.StringValue(stream.Ref.Slug)

	m.DisplayName = types.StringValue(stream.Spec.DisplayName)
	m.Kind = types.StringValue(stream.Spec.GetKind().String())
	if stream.Spec.Filter != nil {
		m.Filter = new(TelemetryStreamFilter)
		diagnostics.Append(m.Filter.Set(stream.Spec.Filter)...)
	} else {
		m.Filter = nil
	}

	m.CreatedAt = timestampToTimeValue(stream.Status.CreatedAt)
	m.UpdatedAt = timestampToTimeValue(stream.Status.UpdatedAt)
	m.StateCode = types.Int32Value(int32(stream.Status.State.Number()))
	m.State = types.StringValue(stream.Status.State.String())
	m.StateMessage = types.StringPointerValue(stream.Status.StateMessage)

	m.ZonesActive = make(map[string]*ActiveZone, len(stream.Status.ZonesActive))
	for _, zone := range stream.Status.ZonesActive {
		az := new(ActiveZone)
		diagnostics.Append(az.Set(zone)...)
		m.ZonesActive[zone.ZoneSlug] = az
	}

	return
}

func (m *TelemetryStreamDataSource) ToRef() *typesv1beta1.TelemetryStreamRef {
	if m == nil {
		return nil
	}

	return &typesv1beta1.TelemetryStreamRef{
		Slug: m.Slug.ValueString(),
	}
}
