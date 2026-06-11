package inference

// exported for testing

var (
	// Validation
	SemverPattern = semverPattern

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
