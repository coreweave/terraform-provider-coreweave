package inference

// exported for testing

var (
	// Deployment
	SetFromDeployment       = setFromDeployment
	ToCreateRequest         = toCreateRequest
	ToUpdateRequest         = toUpdateRequest
	CapacityClassFromString = capacityClassFromString
	CapacityClassToString   = capacityClassToString

	// Capacity Claim
	SetFromCapacityClaim         = setFromCapacityClaim
	ToCreateCapacityClaimRequest = toCreateCapacityClaimRequest
	ToUpdateCapacityClaimRequest = toUpdateCapacityClaimRequest
	CapacityTypeFromString       = capacityTypeFromString
	CapacityTypeToString         = capacityTypeToString

	// Gateway
	SetFromGateway         = setFromGateway
	ToCreateGatewayRequest = toCreateGatewayRequest
	ToUpdateGatewayRequest = toUpdateGatewayRequest
	APITypeFromString      = apiTypeFromString
	APITypeToString        = apiTypeToString
)
