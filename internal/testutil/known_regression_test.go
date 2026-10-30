package testutil_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnownIssueMarker(t *testing.T) {
	if os.Getenv("TEST_KNOWN_ISSUE_CHILD") == "1" {
		testutil.KnownRegression(t, os.Getenv("TEST_KNOWN_ISSUE_TICKET"))
		return
	}

	executable, err := os.Executable()
	require.NoError(t, err)
	for _, ticket := range []string{"CKSAPI-753", "CLDAPI-1174", "", "cksapi-753", "CKSAPI-TBD", "CKSAPI-753/path", "CKSAPI-753\n"} {
		t.Run(ticket, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), executable, "-test.run=^TestKnownIssueMarker$", "-test.v")
			command.Env = append(os.Environ(), "TEST_KNOWN_ISSUE_CHILD=1", "TEST_KNOWN_ISSUE_TICKET="+ticket)
			output, err := command.CombinedOutput()
			if ticket != "CKSAPI-753" && ticket != "CLDAPI-1174" {
				require.Error(t, err, string(output))
				assert.Contains(t, string(output), "invalid known regression ticket")
				return
			}
			require.NoError(t, err, string(output))
			assert.Contains(t, string(output), "known_regression "+ticket)
			assert.Contains(t, string(output), "Known regression, tracked in "+ticket+". Runs only in the known-regressions job.")
			assert.False(t, strings.Contains(string(output), "FAIL"))
		})
	}
}
