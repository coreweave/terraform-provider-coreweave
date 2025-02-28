package testutil

import (
	"fmt"
	"os"
	"strconv"
)

func SweepDryRun() bool {
	dryRunStr, ok := os.LookupEnv("SWEEP_DRY_RUN")
	if !ok {
		return false
	}
	dryRun, err := strconv.ParseBool(dryRunStr)
	if err != nil {
		panic(fmt.Errorf("failed to parse SWEEP_DRY_RUN as bool: %w", err))
	}
	return dryRun
}
