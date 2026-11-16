package model

import (
	"context"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
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

type ActiveZone struct {
	Clusters types.List `tfsdk:"clusters"`
}

func (a *ActiveZone) Set(ctx context.Context, zone *typesv1beta1.TelemetryStreamStatus_ZoneClusterStatus) (diagnostics diag.Diagnostics) {
	clustersRaw := make([]ActiveCluster, len(zone.Clusters))
	for i, cluster := range zone.Clusters {
		clustersRaw[i] = ActiveCluster{
			ID: types.StringValue(cluster.Id),
		}
	}
	a.Clusters, diagnostics = types.ListValueFrom(ctx, types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"id": types.StringType,
		},
	}, clustersRaw)
	return
}

type ActiveCluster struct {
	ID types.String `tfsdk:"id"`
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

	DisplayName types.String `tfsdk:"display_name"`
	Kind        types.String `tfsdk:"kind"`

	CreatedAt    timetypes.RFC3339      `tfsdk:"created_at"`
	UpdatedAt    timetypes.RFC3339      `tfsdk:"updated_at"`
	State        types.String           `tfsdk:"state"`
	StateMessage types.String           `tfsdk:"state_message"`
	ZonesActive  map[string]*ActiveZone `tfsdk:"zones_active"`
}

func (m *TelemetryStreamDataSource) Set(ctx context.Context, stream *typesv1beta1.TelemetryStream) (diagnostics diag.Diagnostics) {
	if stream == nil {
		return diagnostics
	}

	status := stream.GetStatus()

	m.Slug = types.StringValue(stream.GetRef().GetSlug())

	m.DisplayName = types.StringValue(stream.GetSpec().GetDisplayName())
	m.Kind = types.StringValue(stream.GetSpec().GetKind().String())

	m.CreatedAt = timestampToTimeValue(status.GetCreatedAt())
	m.UpdatedAt = timestampToTimeValue(status.GetUpdatedAt())
	m.State = types.StringValue(status.GetState().String())
	m.StateMessage = streamStateMessage(status)

	m.ZonesActive = make(map[string]*ActiveZone, len(status.GetZonesActive()))
	for _, zone := range status.GetZonesActive() {
		az := new(ActiveZone)
		diagnostics.Append(az.Set(ctx, zone)...)
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
