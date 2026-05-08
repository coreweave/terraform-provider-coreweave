package sandbox_test

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	sandboxv1beta2 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1beta2"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/coreweave/terraform-provider-coreweave/coreweave/sandbox"
)

const acceptanceTestPrefix = "test-acc-pt-"

func init() {
	resource.AddTestSweepers("coreweave_sandbox_profile_template", &resource.Sweeper{
		Name:         "coreweave_sandbox_profile_template",
		Dependencies: []string{},
		F: func(_ string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			var pageToken string
			for {
				listResp, err := client.ListProfileTemplates(ctx, connect.NewRequest(&sandboxv1beta2.ListProfileTemplatesRequest{
					PageToken: pageToken,
				}))
				if err != nil {
					return fmt.Errorf("failed to list profile templates: %w", err)
				}
				for _, pt := range listResp.Msg.GetProfileTemplates() {
					if !strings.HasPrefix(pt.GetDisplayName(), acceptanceTestPrefix) {
						log.Printf("skipping profile template %s (no test prefix)", pt.GetDisplayName())
						continue
					}
					log.Printf("sweeping profile template %s", pt.GetDisplayName())
					if testutil.SweepDryRun() {
						continue
					}
					if _, err := client.DeleteProfileTemplate(ctx, connect.NewRequest(&sandboxv1beta2.DeleteProfileTemplateRequest{
						Id: pt.GetId(),
					})); err != nil && connect.CodeOf(err) != connect.CodeNotFound {
						return fmt.Errorf("failed to delete profile template %s: %w", pt.GetDisplayName(), err)
					}
				}
				pageToken = listResp.Msg.GetNextPageToken()
				if pageToken == "" {
					break
				}
			}
			return nil
		},
	})
}

func TestProfileTemplateSchema(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	resp := &fwresource.SchemaResponse{}
	sandbox.NewProfileTemplateResource().Schema(ctx, fwresource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Schema diagnostics: %+v", resp.Diagnostics)
	}
	if d := resp.Schema.ValidateImplementation(ctx); d.HasError() {
		t.Fatalf("Schema validation diagnostics: %+v", d)
	}
}

func TestProfileTemplateResource(t *testing.T) {
	suffix := fmt.Sprintf("%x", rand.IntN(1<<24))
	displayName := acceptanceTestPrefix + suffix
	resourceName := "test_pt_" + suffix
	fullResource := "coreweave_sandbox_profile_template." + resourceName
	fullDataSource := "data.coreweave_sandbox_profile_template." + resourceName

	initialConfig := fmt.Sprintf(`
resource "coreweave_sandbox_profile_template" %q {
  display_name = %q
  description  = "initial"

  spec = {
    container_image = "ghcr.io/coreweave/sandbox-runtime:v1"
    runtime_class   = "kata-qemu"

    resource_defaults = {
      cpu_request    = "500m"
      memory_request = "1Gi"
      cpu_limit      = "2"
      memory_limit   = "4Gi"
    }
  }

  labels = {
    team = "platform"
  }
}
`, resourceName, displayName)

	updatedConfig := fmt.Sprintf(`
resource "coreweave_sandbox_profile_template" %q {
  display_name = %q
  description  = "updated"

  spec = {
    container_image = "ghcr.io/coreweave/sandbox-runtime:v2"
    runtime_class   = "kata-qemu"

    resource_defaults = {
      cpu_request    = "1"
      memory_request = "2Gi"
      cpu_limit      = "4"
      memory_limit   = "8Gi"
    }

    instance_types = ["cpu.small", "cpu.medium"]
    tags           = ["ci", "benchmark"]
  }

  labels = {
    team = "platform"
    env  = "qa"
  }
}
`, resourceName, displayName)

	dataSourceConfig := fmt.Sprintf(`
data "coreweave_sandbox_profile_template" %q {
  id = %s.id
}
`, resourceName, fullResource)

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		Steps: []resource.TestStep{
			{
				Config: initialConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResource, plancheck.ResourceActionCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("display_name"), knownvalue.StringExact(displayName)),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("description"), knownvalue.StringExact("initial")),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("spec").AtMapKey("runtime_class"), knownvalue.StringExact("kata-qemu")),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("spec").AtMapKey("resource_defaults").AtMapKey("memory_limit"), knownvalue.StringExact("4Gi")),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("labels").AtMapKey("team"), knownvalue.StringExact("platform")),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("created_at"), knownvalue.NotNull()),
				},
			},
			{
				Config: updatedConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResource, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("description"), knownvalue.StringExact("updated")),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("spec").AtMapKey("container_image"), knownvalue.StringExact("ghcr.io/coreweave/sandbox-runtime:v2")),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("spec").AtMapKey("resource_defaults").AtMapKey("cpu_limit"), knownvalue.StringExact("4")),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("spec").AtMapKey("instance_types"), knownvalue.ListExact([]knownvalue.Check{
						knownvalue.StringExact("cpu.small"),
						knownvalue.StringExact("cpu.medium"),
					})),
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("labels").AtMapKey("env"), knownvalue.StringExact("qa")),
				},
			},
			{
				Config: updatedConfig + dataSourceConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullDataSource, tfjsonpath.New("display_name"), knownvalue.StringExact(displayName)),
					statecheck.ExpectKnownValue(fullDataSource, tfjsonpath.New("description"), knownvalue.StringExact("updated")),
					statecheck.ExpectKnownValue(fullDataSource, tfjsonpath.New("labels").AtMapKey("env"), knownvalue.StringExact("qa")),
				},
			},
			{
				ResourceName:      fullResource,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: updatedConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

func TestProfileTemplate_DisplayNameForcesReplace(t *testing.T) {
	suffix := fmt.Sprintf("%x", rand.IntN(1<<24))
	first := acceptanceTestPrefix + suffix + "-a"
	second := acceptanceTestPrefix + suffix + "-b"
	resourceName := "test_pt_replace_" + suffix
	fullResource := "coreweave_sandbox_profile_template." + resourceName

	configFor := func(name string) string {
		return fmt.Sprintf(`
resource "coreweave_sandbox_profile_template" %q {
  display_name = %q

  spec = {
    runtime_class = "kata-qemu"
    resource_defaults = {
      cpu_request    = "100m"
      memory_request = "256Mi"
    }
  }
}
`, resourceName, name)
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		Steps: []resource.TestStep{
			{Config: configFor(first)},
			{
				Config: configFor(second),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fullResource, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResource, tfjsonpath.New("display_name"), knownvalue.StringExact(second)),
				},
			},
		},
	})
}
