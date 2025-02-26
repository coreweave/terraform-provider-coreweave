package testutil

import (
	"os"
)

// SetEnvIfUnset sets the environment variable with the given name to the specified value
// if it is not already set. It returns true if the environment variable was not previously set,
// and false if it was already set.
//
// Parameters:
//   - name: The name of the environment variable.
//   - value: The value to set the environment variable to if it is not already set.
//
// Returns:
//   - bool: True if the environment variable was not previously set, false otherwise.
func SetEnvIfUnset(name, value string) bool {
	_, found := os.LookupEnv(name)
	if !found {
		os.Setenv(name, value)
	}
	return !found
}
