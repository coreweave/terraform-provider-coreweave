package testutil

import (
	"regexp"
	"testing"
)

var knownRegressionTicket = regexp.MustCompile(`^[A-Z]+-[0-9]+$`)

// KnownRegression marks a test for the non-blocking known-regressions job.
func KnownRegression(t *testing.T, ticket string) {
	t.Helper()
	if !knownRegressionTicket.MatchString(ticket) {
		t.Fatalf("invalid known regression ticket %q: expected ^[A-Z]+-[0-9]+$", ticket)
	}
	t.Attr("known_regression", ticket)
	t.Logf("Known regression, tracked in %s. Runs only in the known-regressions job.", ticket)
}
