package inference

// exported for testing

// SemverMatches reports whether v satisfies the deployment runtime version
// validator. Exposed as a function (rather than the mutable *regexp.Regexp) so
// external-package tests can exercise the pattern without being able to
// reassign it.
func SemverMatches(v string) bool { return semverPattern.MatchString(v) }

var (
	// Deployment
	SetFromDeployment = setFromDeployment
	ToCreateRequest   = toCreateRequest
	ToUpdateRequest   = toUpdateRequest

	// Capacity Claim
	SetFromCapacityClaim         = setFromCapacityClaim
	ToCreateCapacityClaimRequest = toCreateCapacityClaimRequest
	ToUpdateCapacityClaimRequest = toUpdateCapacityClaimRequest

	// Gateway
	SetFromGateway         = setFromGateway
	ToCreateGatewayRequest = toCreateGatewayRequest
	ToUpdateGatewayRequest = toUpdateGatewayRequest
)
