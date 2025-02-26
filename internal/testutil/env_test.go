package testutil_test

import (
	"os"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
)

func TestSetEnvIfUnset(t *testing.T) {
	t.Parallel()

	t.Run("When not set, sets variable", func(t *testing.T) {
		name, value := "SET_ENV_IF_UNSET_1", "test_value"

		if _, found := os.LookupEnv(name); found {
			t.Fatalf("environment variable %s is already set", name)
		}
		defer func() {
			os.Unsetenv(name)
		}()

		result := testutil.SetEnvIfUnset(name, value)
		if result != true {
			t.Errorf("expected true, got %v", result)
		}

		if os.Getenv(name) != value {
			t.Errorf("expected %s to be %s, got %s", name, value, os.Getenv(name))
		}
	})

	t.Run("When set, does not set variable", func(t *testing.T) {
		name, ignoredValue := "SET_ENV_IF_UNSET_2", "ignored_value"
		expectedValue := "actual_value"

		if _, found := os.LookupEnv(name); found {
			t.Fatalf("environment variable %s is already set", name)
		}

		if err := os.Setenv(name, expectedValue); err != nil {
			t.Fatalf("failed to set environment variable %s: %v", name, err)
		}
		defer func() {
			os.Unsetenv(name)
		}()

		result := testutil.SetEnvIfUnset(name, ignoredValue)
		if result != false {
			t.Errorf("expected false, got %v", result)
		}

		actualValue := os.Getenv(name)
		if os.Getenv(name) != actualValue {
			t.Errorf("expected %s to be %s, got %s", name, expectedValue, actualValue)
		}
	})
}
