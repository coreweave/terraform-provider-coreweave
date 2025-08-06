package testutil

import (
	"os"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
)

// SetEnvDefaults sets default values for environment variables used in tests.
func SetEnvDefaults() {
	defaultPairs := map[string]string{
		provider.CoreweaveApiTokenEnvVar:    "test",
		provider.CoreweaveApiEndpointEnvVar: "http://172.17.111.5",
	}

	for name, value := range defaultPairs {
		if _, found := os.LookupEnv(name); !found {
			_ = os.Setenv(name, value)
		}
	}
}
