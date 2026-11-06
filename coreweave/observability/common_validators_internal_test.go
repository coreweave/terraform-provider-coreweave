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

// slugField is the proto field name shared by every ref message.
const slugField protoreflect.Name = "slug"

// stringRules reads the buf.validate string constraints declared on a field.
func stringRules(t *testing.T, message protoreflect.ProtoMessage, field protoreflect.Name) *validate.StringRules {
	t.Helper()

	descriptor := message.ProtoReflect().Descriptor().Fields().ByName(field)
	require.NotNilf(t, descriptor, "no field %s on %s", field, message.ProtoReflect().Descriptor().FullName())

	rules, ok := proto.GetExtension(descriptor.Options(), validate.E_Field).(*validate.FieldRules)
	require.Truef(t, ok, "%s carries no buf.validate.field option", field)

	return rules.GetString()
}

// TestAuthHeaderValidatorsMirrorProto covers the headers map, whose pair bounds
// are MapRules rather than the StringRules the table test reads.
func TestAuthHeaderValidatorsMirrorProto(t *testing.T) {
	t.Parallel()

	message := &typesv1beta1.AuthHeadersCredentials{}
	descriptor := message.ProtoReflect().Descriptor().Fields().ByName("headers")
	require.NotNil(t, descriptor, "no headers field on AuthHeadersCredentials")

	rules, ok := proto.GetExtension(descriptor.Options(), validate.E_Field).(*validate.FieldRules)
	require.True(t, ok, "headers carries no buf.validate.field option")

	mapRules := rules.GetMap()
	require.NotNil(t, mapRules, "headers carries no map rules")

	assert.Equal(t, uint64(authHeadersMinPairs), mapRules.GetMinPairs(), "min_pairs drifted from the proto")
	assert.Equal(t, uint64(authHeadersMaxPairs), mapRules.GetMaxPairs(), "max_pairs drifted from the proto")

	keyRules := mapRules.GetKeys().GetString()
	require.NotNil(t, keyRules, "headers keys carry no string rules")
	assert.Equal(t, uint64(authHeaderNameMaxLength), keyRules.GetMaxLen(), "key max_len drifted from the proto")
	assert.Equal(t, authHeaderNamePat.String(), keyRules.GetPattern(), "key pattern drifted from the proto")
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
			field:   slugField,
			minLen:  slugMinLength,
			maxLen:  endpointSlugMaxLength,
			pattern: slugPattern.String(),
		},
		{
			name:    "pipeline slug",
			message: &typesv1beta1.ForwardingPipelineRef{},
			field:   slugField,
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
		{
			name:    "stream slug",
			message: &typesv1beta1.TelemetryStreamRef{},
			field:   slugField,
			minLen:  slugMinLength,
			maxLen:  streamSlugMaxLength,
			pattern: slugPattern.String(),
		},
		{
			name:    "basic auth username",
			message: &typesv1beta1.BasicAuthCredentials{},
			field:   "username",
			minLen:  1,
			maxLen:  basicAuthUsernameMaxLength,
		},
		{
			name:    "basic auth password",
			message: &typesv1beta1.BasicAuthCredentials{},
			field:   "password",
			minLen:  1,
			maxLen:  basicAuthPasswordMaxLength,
		},
		{
			name:    "s3 access_key_id",
			message: &typesv1beta1.S3Credentials{},
			field:   "access_key_id",
			minLen:  s3AccessKeyIDMinLength,
			maxLen:  s3AccessKeyIDMaxLength,
			pattern: s3AccessKeyIDPat.String(),
		},
		{
			name:    "s3 secret_access_key",
			message: &typesv1beta1.S3Credentials{},
			field:   "secret_access_key",
			minLen:  1,
			maxLen:  s3SecretAccessKeyMaxLength,
		},
		{
			name:    "s3 session_token",
			message: &typesv1beta1.S3Credentials{},
			field:   "session_token",
			maxLen:  s3SessionTokenMaxLength,
		},
		{
			name:    "tls certificate_authority_data",
			message: &typesv1beta1.TLSConfig{},
			field:   "certificate_authority_data",
			maxLen:  tlsCADataMaxLength,
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
