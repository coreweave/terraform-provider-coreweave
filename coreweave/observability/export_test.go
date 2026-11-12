package observability

// Unexported identifiers the external test package needs.
var (
	DeleteEndpointAndWait      = deleteEndpointAndWait
	CredentialAction           = credentialAction
	CarriesCredentialPayload   = carriesCredentialPayload
	ValidateCredentialsVersion = validateCredentialsVersion
)

type CredentialInput = credentialInput
