package observability

import "regexp"

// Plan-time mirrors of the buf.validate constraints in
// coreweave/telemetryrelay/types/v1beta1. Keeping them here means a bad slug or
// a plain-http URL fails at plan with the offending attribute named, rather
// than at apply with an opaque server rejection. protovalidate still runs in
// each resource's ValidateConfig as the backstop.
const (
	// ForwardingEndpointRef.slug and ForwardingPipelineRef.slug.
	slugMinLength         = 3
	endpointSlugMaxLength = 32
	pipelineSlugMaxLength = 65

	// ForwardingEndpointSpec.display_name.
	displayNameMaxLength = 128

	// HTTPSConfig.endpoint and PrometheusRemoteWriteConfig.endpoint.
	endpointURLMaxLength = 2048

	// S3Config.uri and S3Config.region.
	s3URIMaxLength    = 2048
	s3RegionMaxLength = 64

	// ForwardingPipelineSpec.zone_slugs items.
	zoneSlugMaxLength = 32
)

var (
	slugPattern       = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]+$`)
	httpsURLPattern   = regexp.MustCompile(`^https://`)
	s3URIPattern      = regexp.MustCompile(`^s3://[a-z0-9][a-z0-9.-]*[a-z0-9](/.*)?$`)
	lowercaseZonePat  = regexp.MustCompile(`^[^A-Z]*$`)
	slugPatternDetail = "must start with an alphanumeric character and contain only alphanumeric characters and hyphens"
)
