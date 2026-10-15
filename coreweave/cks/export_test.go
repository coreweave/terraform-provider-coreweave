package cks

// Exports for tests in package cks_test.

const (
	KubeAPIAccessEndpoint  = testEndpoint
	KubeAPIAccessCACertPEM = testCACertPEM
)

var (
	KubeAPIAccessAttrTypes       = kubeAPIAccessAttrTypes
	PublicKubeAPIAccessAttrTypes = publicKubeAPIAccessAttrTypes

	KubeAPIAccessCIDRFixture  = kubeAPIAccessCIDRFixture
	KubeAPIAccessToProto      = kubeAPIAccessToProto
	RequireKubeAPIAccessProto = requireAccessProto
	KubeAPIAccessProto        = testAccess
	KubeAPIAccessCIDRSet      = testCIDRSet
	KubeAPIPublicAccessObject = testPublic
	KubeAPIAccessObject       = testOuter
)
