package observability

import (
	"testing"

	validate "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// stringRules reads the buf.validate string constraints declared on a field.
func stringRules(t *testing.T, message protoreflect.ProtoMessage, field protoreflect.Name) *validate.StringRules {
	t.Helper()

	descriptor := message.ProtoReflect().Descriptor().Fields().ByName(field)
	require.NotNilf(t, descriptor, "no field %s on %s", field, message.ProtoReflect().Descriptor().FullName())

	rules, ok := proto.GetExtension(descriptor.Options(), validate.E_Field).(*validate.FieldRules)
	require.Truef(t, ok, "%s carries no buf.validate.field option", field)

	return rules.GetString()
}

// TestValidatorsMirrorProto pins common_validators.go to the constraints it
// copies. Drift is silent and one-sided: if the proto relaxes a limit the
// provider keeps rejecting configs the server would accept, and protovalidate
// in ValidateConfig only backstops the opposite direction.
func TestValidatorsMirrorProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message protoreflect.ProtoMessage
		field   protoreflect.Name
		minLen  uint64
		maxLen  uint64
		pattern string
	}{
		{
			name:    "endpoint slug",
			message: &typesv1beta1.ForwardingEndpointRef{},
			field:   "slug",
			minLen:  slugMinLength,
			maxLen:  endpointSlugMaxLength,
			pattern: slugPattern.String(),
		},
		{
			name:    "pipeline slug",
			message: &typesv1beta1.ForwardingPipelineRef{},
			field:   "slug",
			minLen:  slugMinLength,
			maxLen:  pipelineSlugMaxLength,
			pattern: slugPattern.String(),
		},
		{
			name:    "endpoint display_name",
			message: &typesv1beta1.ForwardingEndpointSpec{},
			field:   "display_name",
			maxLen:  displayNameMaxLength,
		},
		{
			name:    "https endpoint url",
			message: &typesv1beta1.HTTPSConfig{},
			field:   "endpoint",
			maxLen:  endpointURLMaxLength,
		},
		{
			name:    "s3 uri",
			message: &typesv1beta1.S3Config{},
			field:   "uri",
			maxLen:  s3URIMaxLength,
		},
		{
			name:    "s3 region",
			message: &typesv1beta1.S3Config{},
			field:   "region",
			maxLen:  s3RegionMaxLength,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rules := stringRules(t, tc.message, tc.field)
			if tc.minLen > 0 {
				assert.Equal(t, tc.minLen, rules.GetMinLen(), "min_len drifted from the proto")
			}
			assert.Equal(t, tc.maxLen, rules.GetMaxLen(), "max_len drifted from the proto")
			if tc.pattern != "" {
				assert.Equal(t, tc.pattern, rules.GetPattern(), "pattern drifted from the proto")
			}
		})
	}
}
