package serviceaccount

import (
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// NewServiceAccountResourceWithReadBackPolicy exposes recovery timing only to tests.
func NewServiceAccountResourceWithReadBackPolicy(timeout, initialDelay, maxDelay time.Duration) resource.Resource {
	return newServiceAccountResource(serviceAccountReadBackPolicy{
		timeout:      timeout,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
	})
}
