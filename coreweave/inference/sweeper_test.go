package inference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestMain(m *testing.M) {
	resource.TestMain(m)
}

func init() {
	resource.AddTestSweepers("coreweave_inference_deployment", &resource.Sweeper{
		Name: "coreweave_inference_deployment",
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.ListDeployments(ctx, connect.NewRequest(&inferencev1.ListDeploymentsRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list deployments: %w", err)
			}

			for _, d := range listResp.Msg.GetItems() {
				name := d.GetSpec().GetName()
				if !strings.HasPrefix(name, accTestPrefix) {
					continue
				}
				if testutil.SweepDryRun() {
					fmt.Printf("[DRY RUN] would delete inference deployment: %s\n", name)
					continue
				}

				id := d.GetSpec().GetId()
				fmt.Printf("sweeping inference deployment: %s (%s)\n", name, id)

				_, err := client.DeleteDeployment(ctx, connect.NewRequest(&inferencev1.DeleteDeploymentRequest{Id: id}))
				if err != nil {
					return fmt.Errorf("failed to delete deployment %s: %w", name, err)
				}

				if err := testutil.WaitForDelete(ctx, 20*time.Minute, 10*time.Second,
					client.GetDeployment,
					&inferencev1.GetDeploymentRequest{Id: id},
				); err != nil {
					return fmt.Errorf("timed out waiting for deployment %s to be deleted: %w", name, err)
				}
			}

			return nil
		},
	})
}
