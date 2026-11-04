package testutil

import "testing"

// NonBlockingAcceptance marks a test for the non-blocking acceptance job.
func NonBlockingAcceptance(t *testing.T) {
	t.Helper()
	t.Attr("acceptance_mode", "nonblocking")
	t.Log("Runs only in the non-blocking acceptance job.")
}
