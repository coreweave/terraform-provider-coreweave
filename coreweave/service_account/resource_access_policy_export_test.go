package serviceaccount

import (
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// NewAccessPolicyResourceWithReadBackPolicy exposes recovery timing only to tests.
func NewAccessPolicyResourceWithReadBackPolicy(timeout, initialDelay, maxDelay time.Duration) resource.Resource {
	return newAccessPolicyResource(createReadBackPolicy{
		timeout:      timeout,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
	})
}
