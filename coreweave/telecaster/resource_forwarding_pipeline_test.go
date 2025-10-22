package telecaster_test

import (
	"testing"

	telecastertypesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/cw/telecaster/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/telecaster"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
)

func TestForwardingPipelineResourceModelRef_ToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *telecaster.ForwardingPipelineRefModel
		expected *telecastertypesv1beta1.ForwardingPipelineRef
		wantErr  bool
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name: "valid input converts correctly",
			input: &telecaster.ForwardingPipelineRefModel{
				Slug: types.StringValue("example-pipeline"),
			},
			expected: &telecastertypesv1beta1.ForwardingPipelineRef{
				Slug: "example-pipeline",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.input.ToProto())
		})
	}
}

func TestForwardingPipelineResourceSpecModel_ToProto(t *testing.T) {
	t.Parallel()

	specBase := func() *telecaster.ForwardingPipelineSpecModel {
		return &telecaster.ForwardingPipelineSpecModel{
			Enabled: types.BoolValue(true),
			Source: telecaster.TelemetryStreamRefModel{
				Slug: types.StringValue("example-stream"),
			},
			Destination: telecaster.ForwardingEndpointRefModel{
				Slug: types.StringValue("example-destination"),
			},
		}
	}
	outputBase := func() *telecastertypesv1beta1.ForwardingPipelineSpec {
		return &telecastertypesv1beta1.ForwardingPipelineSpec{
			Enabled: true,
			Source: &telecastertypesv1beta1.TelemetryStreamRef{
				Slug: "example-stream",
			},
			Destination: &telecastertypesv1beta1.ForwardingEndpointRef{
				Slug: "example-destination",
			},
		}
	}

	tests := []struct {
		name     string
		input    *telecaster.ForwardingPipelineSpecModel
		expected *telecastertypesv1beta1.ForwardingPipelineSpec
		wantErr  bool
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "valid input converts correctly",
			input:    specBase(),
			expected: outputBase(),
		},
		{
			name: "enabled nil converts correctly",
			input: with(specBase(), func(s *telecaster.ForwardingPipelineSpecModel) {
				s.Enabled = types.BoolNull()
			}),
			expected: with(outputBase(), func(s *telecastertypesv1beta1.ForwardingPipelineSpec) {
				s.Enabled = false
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.input.ToProto())
		})
	}
}

func TestForwardingPipelineResourceSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaRequest := &resource.SchemaRequest{}
	schemaResponse := &resource.SchemaResponse{}

	telecaster.NewForwardingPipelineResource().Schema(ctx, *schemaRequest, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}
