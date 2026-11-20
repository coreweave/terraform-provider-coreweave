package model_test

import (
	"testing"
	"time"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestTelemetryStreamModel pins the stream model's conversions. Streams are
// read-only, so Set is the whole inbound mapping and ToRef is all that goes
// back out.
func TestTelemetryStreamModel(t *testing.T) {
	t.Parallel()

	created := timestamppb.New(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	streamMsg := func() *typesv1beta1.TelemetryStream {
		spec := &typesv1beta1.TelemetryStreamSpec{DisplayName: "Plain Logs"}
		spec.SetKind(typesv1beta1.StreamKind_STREAM_KIND_LOGS)

		return &typesv1beta1.TelemetryStream{
			Ref:  &typesv1beta1.TelemetryStreamRef{Slug: "logs-plain"},
			Spec: spec,
			Status: &typesv1beta1.TelemetryStreamStatus{
				CreatedAt: created,
				State:     typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE,
			},
		}
	}

	t.Run("spec and status populate the model", func(t *testing.T) {
		t.Parallel()

		var stream model.TelemetryStreamDataSource
		require.False(t, stream.Set(t.Context(), streamMsg()).HasError())

		assert.Equal(t, "logs-plain", stream.Slug.ValueString())
		assert.Equal(t, "Plain Logs", stream.DisplayName.ValueString())
		assert.Equal(t, typesv1beta1.StreamKind_STREAM_KIND_LOGS.String(), stream.Kind.ValueString())
		assert.Equal(t, typesv1beta1.TelemetryStreamState_TELEMETRY_STREAM_STATE_ACTIVE.String(), stream.State.ValueString())
		assert.Equal(t, "2026-10-01T12:00:00Z", stream.CreatedAt.ValueString())
		assert.True(t, stream.UpdatedAt.IsNull(), "a stream never updated has no updated_at")
	})

	t.Run("the slug survives the round trip", func(t *testing.T) {
		t.Parallel()

		var stream model.TelemetryStreamDataSource
		require.False(t, stream.Set(t.Context(), streamMsg()).HasError())

		assert.Equal(t, "logs-plain", stream.ToRef().GetSlug())
	})

	t.Run("a ref converts both ways", func(t *testing.T) {
		t.Parallel()

		var ref model.TelemetryStreamRef
		ref.Set(&typesv1beta1.TelemetryStreamRef{Slug: "logs-audit"})

		assert.Equal(t, "logs-audit", ref.Slug.ValueString())
		assert.Equal(t, "logs-audit", ref.ToMsg().GetSlug())
	})
}
