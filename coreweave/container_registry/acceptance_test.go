package containerregistry_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/go-uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// acceptanceNamespace selects a unique test-owned namespace in an explicitly designated environment.
func acceptanceNamespace(t *testing.T) (string, string) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("requires TF_ACC and an explicitly designated Container Registry test environment")
	}
	zone := os.Getenv("COREWEAVE_CONTAINER_REGISTRY_TEST_ZONE")
	if zone == "" {
		t.Skip("set COREWEAVE_CONTAINER_REGISTRY_TEST_ZONE to opt into the registry acceptance suite")
	}
	for _, key := range []string{"COREWEAVE_API_ENDPOINT", "COREWEAVE_API_TOKEN", "COREWEAVE_CONTAINER_REGISTRY_TEST_PREFIX"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s must be explicitly configured", key)
		}
	}
	prefix, err := registryTestPrefix()
	if err != nil {
		t.Fatal(err)
	}
	id, err := uuid.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	return prefix + id[:8], zone
}

// namespaceConfig declares a namespace with non-forced cleanup and explicit quota presence.
func namespaceConfig(name, zone, quota string) string {
	return fmt.Sprintf(`resource "coreweave_container_registry_namespace" "test" {
 namespace_id = %q
 zone = %q
 storage_quota_bytes = %s
 force_destroy = false
}`, name, zone, quota)
}

// checkNamespaceDestroyed verifies remote absence after Terraform's automatic cleanup.
func checkNamespaceDestroyed(t *testing.T, name string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
		if err != nil {
			return err
		}
		_, err = client.ContainerRegistry.GetRegistryNamespace(ctx, connect.NewRequest(&api.GetRegistryNamespaceRequest{Name: "namespaces/" + name}))
		if coreweave.IsNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("namespace %s still exists after destroy", name)
	}
}
