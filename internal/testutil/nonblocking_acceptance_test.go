package testutil_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcceptanceModeMarker(t *testing.T) {
	const failureMode = "fail"

	if mode := os.Getenv("TEST_ACCEPTANCE_MARKER_CHILD"); mode != "" {
		testutil.NonBlockingAcceptance(t)
		if mode == failureMode {
			t.Fatal("scenario failed")
		}
		return
	}

	executable, err := os.Executable()
	require.NoError(t, err)
	for _, mode := range []string{"pass", failureMode} {
		t.Run(mode, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), executable, "-test.run=^TestAcceptanceModeMarker$", "-test.v")
			command.Env = append(os.Environ(), "TEST_ACCEPTANCE_MARKER_CHILD="+mode)
			output, err := command.CombinedOutput()
			if mode == failureMode {
				require.Error(t, err, string(output))
				assert.Contains(t, string(output), "scenario failed")
			} else {
				require.NoError(t, err, string(output))
				assert.NotContains(t, string(output), "FAIL")
			}
			assert.Contains(t, string(output), "acceptance_mode nonblocking")
			assert.Contains(t, string(output), "Runs only in the non-blocking acceptance job.")
		})
	}
}
