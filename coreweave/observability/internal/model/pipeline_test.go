package model_test

import (
	"testing"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"buf.build/go/protovalidate"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForwardingPipelineModel pins the pipeline model's conversions: the whole
// model to a message, the round trip, and the Ref and Spec sub-conversions.
func TestForwardingPipelineModel(t *testing.T) {
	t.Parallel()

	t.Run("model_to_msg", testForwardingPipelineModelToMsg)
	t.Run("round_trip", testForwardingPipelineRoundTrip)
	t.Run("ref_to_msg", testForwardingPipelineRefToMsg)
	t.Run("spec_to_msg", testForwardingPipelineSpecToMsg)
}

func testForwardingPipelineModelToMsg(t *testing.T) {
	t.Parallel()

	base := func() *model.ForwardingPipeline {
		return &model.ForwardingPipeline{
			Slug:            types.StringValue("test-pipeline"),
			SourceSlug:      types.StringValue("test-stream"),
			DestinationSlug: types.StringValue("test-endpoint"),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}
	}

	tests := []struct {
		name              string
		input             *model.ForwardingPipeline
		expectedZoneSlugs []string
		wantErr           bool
	}{
		{
			name:  nilInputReturnsNil,
			input: nil,
		},
		{
			name:  "valid model creates valid message",
			input: base(),
		},
		{
			name: "disabled pipeline is valid",
			input: with(base(), func(p *model.ForwardingPipeline) {
				p.Enabled = types.BoolValue(false)
			}),
		},
		{
			name: "omitted enabled is valid and sends false",
			input: with(base(), func(p *model.ForwardingPipeline) {
				p.Enabled = types.BoolNull()
			}),
		},
		{
			name: "zone_slugs are carried through",
			input: with(base(), func(p *model.ForwardingPipeline) {
				p.ZoneSlugs = zoneSlugSet(t, testZone, "us-west-01a")
			}),
			expectedZoneSlugs: []string{testZone, "us-west-01a"},
		},
		{
			// buf.validate marks zone_slugs repeated.unique. Terraform dedupes a
			// set written in HCL, but types.Set in Go does not, so ToMsg can still
			// emit duplicates and protovalidate is what catches them.
			name: "duplicate zone_slugs fail protovalidate",
			input: with(base(), func(p *model.ForwardingPipeline) {
				p.ZoneSlugs = types.SetValueMust(types.StringType, []attr.Value{
					types.StringValue(testZone),
					types.StringValue(testZone),
				})
			}),
			expectedZoneSlugs: []string{testZone, testZone},
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msg, diags := tt.input.ToMsg(t.Context())
			assert.False(t, diags.HasError(), "ToMsg returned diagnostics: %v", diags)

			if tt.input == nil {
				assert.Nil(t, msg)
				return
			}

			require.NotNil(t, msg)
			assert.Equal(t, tt.input.Slug.ValueString(), msg.GetRef().GetSlug())
			assert.Equal(t, tt.input.SourceSlug.ValueString(), msg.GetSpec().GetSource().GetSlug())
			assert.Equal(t, tt.input.DestinationSlug.ValueString(), msg.GetSpec().GetDestination().GetSlug())
			assert.Equal(t, tt.input.Enabled.ValueBool(), msg.GetSpec().GetEnabled())
			assert.ElementsMatch(t, tt.expectedZoneSlugs, msg.GetSpec().GetZoneSlugs())

			err := protovalidate.Validate(msg)
			if tt.wantErr {
				assert.Error(t, err, "Expected validation error but got none")
			} else {
				assert.NoError(t, err, "Message failed protovalidation: %v", err)
			}
		})
	}
}
func testForwardingPipelineRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	pipeline := &typesv1beta1.ForwardingPipeline{
		Ref: &typesv1beta1.ForwardingPipelineRef{Slug: "round-trip"},
		Spec: &typesv1beta1.ForwardingPipelineSpec{
			Source:      &typesv1beta1.TelemetryStreamRef{Slug: "logs-journald"},
			Destination: &typesv1beta1.ForwardingEndpointRef{Slug: "sink"},
			Enabled:     true,
		},
		Status: &typesv1beta1.ForwardingPipelineStatus{
			State: typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE,
		},
	}

	t.Run("omitted enabled stays null", func(t *testing.T) {
		t.Parallel()

		m := &model.ForwardingPipeline{Enabled: types.BoolNull()}
		require.False(t, m.Set(ctx, pipeline).HasError())
		assert.True(t, m.Enabled.IsNull(),
			"a null config must stay null, or the apply fails with 'provider produced inconsistent result'")
	})

	t.Run("configured enabled reflects the server", func(t *testing.T) {
		t.Parallel()

		m := &model.ForwardingPipeline{Enabled: types.BoolValue(false)}
		require.False(t, m.Set(ctx, pipeline).HasError())
		assert.True(t, m.Enabled.ValueBool())
	})

	t.Run("empty zone_slugs read back as null", func(t *testing.T) {
		t.Parallel()

		m := &model.ForwardingPipeline{}
		require.False(t, m.Set(ctx, pipeline).HasError())
		assert.True(t, m.ZoneSlugs.IsNull(),
			"empty means all zones; an empty set would assert the opposite")
	})

	t.Run("populated zone_slugs round-trip", func(t *testing.T) {
		t.Parallel()

		withZones := &typesv1beta1.ForwardingPipeline{
			Ref: pipeline.GetRef(),
			Spec: &typesv1beta1.ForwardingPipelineSpec{
				Source:      pipeline.GetSpec().GetSource(),
				Destination: pipeline.GetSpec().GetDestination(),
				ZoneSlugs:   []string{testZone},
			},
			Status: pipeline.GetStatus(),
		}

		m := &model.ForwardingPipeline{}
		require.False(t, m.Set(ctx, withZones).HasError())
		require.False(t, m.ZoneSlugs.IsNull())

		msg, diags := m.ToMsg(ctx)
		require.False(t, diags.HasError())
		assert.Equal(t, []string{testZone}, msg.GetSpec().GetZoneSlugs())
	})
}
func testForwardingPipelineRefToMsg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *model.ForwardingPipelineRef
		expected *typesv1beta1.ForwardingPipelineRef
		wantErr  bool
	}{
		{
			name:     nilInputReturnsNil,
			input:    nil,
			expected: nil,
		},
		{
			name: "valid input converts correctly",
			input: &model.ForwardingPipelineRef{
				Slug: types.StringValue("example-pipeline"),
			},
			expected: &typesv1beta1.ForwardingPipelineRef{
				Slug: "example-pipeline",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := tt.input.ToMsg()
			assert.Equal(t, tt.expected, msg)
		})
	}
}
func testForwardingPipelineSpecToMsg(t *testing.T) {
	t.Parallel()

	specBase := func() *model.ForwardingPipelineSpec {
		return &model.ForwardingPipelineSpec{
			Enabled: types.BoolValue(true),
			Source: model.TelemetryStreamRef{
				Slug: types.StringValue("example-stream"),
			},
			Destination: model.ForwardingEndpointRef{
				Slug: types.StringValue("example-destination"),
			},
		}
	}
	outputBase := func() *typesv1beta1.ForwardingPipelineSpec {
		return &typesv1beta1.ForwardingPipelineSpec{
			Enabled: true,
			Source: &typesv1beta1.TelemetryStreamRef{
				Slug: "example-stream",
			},
			Destination: &typesv1beta1.ForwardingEndpointRef{
				Slug: "example-destination",
			},
		}
	}

	tests := []struct {
		name     string
		input    *model.ForwardingPipelineSpec
		expected *typesv1beta1.ForwardingPipelineSpec
		wantErr  bool
	}{
		{
			name:     nilInputReturnsNil,
			input:    nil,
			expected: nil,
		},
		{
			name:     "valid input converts correctly",
			input:    specBase(),
			expected: outputBase(),
		},
		{
			name: "enabled false converts correctly",
			input: with(specBase(), func(s *model.ForwardingPipelineSpec) {
				s.Enabled = types.BoolValue(false)
			}),
			expected: with(outputBase(), func(s *typesv1beta1.ForwardingPipelineSpec) {
				s.Enabled = false
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := tt.input.ToMsg()
			assert.Equal(t, tt.expected, msg)
		})
	}
}
