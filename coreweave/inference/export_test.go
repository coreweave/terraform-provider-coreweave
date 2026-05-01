package inference

// exported for testing

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
