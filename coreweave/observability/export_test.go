package observability

// Unexported identifiers the external test package needs.
var (
	DeleteEndpointAndWait      = deleteEndpointAndWait
	CredentialAction           = credentialAction
	CarriesCredentialPayload   = carriesCredentialPayload
	ValidateCredentialsVersion = validateCredentialsVersion
)

// CredentialInput is credentialAction's parameter, for table-driven tests.
type CredentialInput = credentialInput
