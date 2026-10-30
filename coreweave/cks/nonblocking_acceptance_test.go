//go:build nonblocking

package cks_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
)

func TestNonBlockingAcceptance(t *testing.T) {
	t.Run("cluster_lifecycle", func(t *testing.T) {
		testutil.NonBlockingAcceptance(t)
		testClusterResource(t)
	})
}
