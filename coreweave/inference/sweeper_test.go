package inference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const AcceptanceTestPrefix = "test-acc-inf-"

func TestMain(m *testing.M) {
	resource.TestMain(m)
}

func init() {
	resource.AddTestSweepers("coreweave_inference_deployment", &resource.Sweeper{
		Name:         "coreweave_inference_deployment",
		Dependencies: []string{},
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.Inference.ListDeployments(ctx, connect.NewRequest(&inferencev1.ListDeploymentsRequest{}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					fmt.Println("no deployments found. skipping sweeper.")
					return nil
				}
				return fmt.Errorf("failed to list deployments: %w", err)
			}

			for _, d := range listResp.Msg.GetItems() {
				name := d.GetSpec().GetName()
				if !strings.HasPrefix(name, AcceptanceTestPrefix) {
					continue
				}
				if testutil.SweepDryRun() {
					fmt.Printf("[DRY RUN] would delete inference deployment: %s\n", name)
					continue
				}

				id := d.GetSpec().GetId()
				fmt.Printf("sweeping inference deployment: %s (%s)\n", name, id)

				_, err := client.Inference.DeleteDeployment(ctx, connect.NewRequest(&inferencev1.DeleteDeploymentRequest{Id: id}))
				if err != nil {
					return fmt.Errorf("failed to delete deployment %s: %w", name, err)
				}

				if err := testutil.WaitForDelete(ctx, 20*time.Minute, 10*time.Second,
					client.Inference.GetDeployment,
					&inferencev1.GetDeploymentRequest{Id: id},
				); err != nil {
					return fmt.Errorf("timed out waiting for deployment %s to be deleted: %w", name, err)
				}
			}

			return nil
		},
	})

	resource.AddTestSweepers("coreweave_inference_capacity_claim", &resource.Sweeper{
		Name:         "coreweave_inference_capacity_claim",
		Dependencies: []string{"coreweave_inference_deployment"},
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.Inference.ListCapacityClaims(ctx, connect.NewRequest(&inferencev1.ListCapacityClaimsRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list capacity claims: %w", err)
			}

			for _, d := range listResp.Msg.GetCapacityClaims() {
				name := d.GetSpec().GetName()
				if !strings.HasPrefix(name, AcceptanceTestPrefix) {
					continue
				}
				if testutil.SweepDryRun() {
					fmt.Printf("[DRY RUN] would delete inference capacity claim: %s\n", name)
					continue
				}

				id := d.GetSpec().GetId()
				fmt.Printf("sweeping inference capacity claim: %s (%s)\n", name, id)

				_, err := client.Inference.DeleteCapacityClaim(ctx, connect.NewRequest(&inferencev1.DeleteCapacityClaimRequest{Id: id}))
				if err != nil {
					return fmt.Errorf("failed to delete capacity claim %s: %w", name, err)
				}

				if err := testutil.WaitForDelete(ctx, 20*time.Minute, 10*time.Second,
					client.Inference.GetCapacityClaim,
					&inferencev1.GetCapacityClaimRequest{Id: id},
				); err != nil {
					return fmt.Errorf("timed out waiting for capacity claim %s to be deleted: %w", name, err)
				}
			}

			return nil
		},
	})

	resource.AddTestSweepers("coreweave_inference_gateway", &resource.Sweeper{
		Name:         "coreweave_inference_gateway",
		Dependencies: []string{"coreweave_inference_deployment"},
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.Inference.ListGateways(ctx, connect.NewRequest(&inferencev1.ListGatewaysRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list gateways: %w", err)
			}

			for _, d := range listResp.Msg.GetItems() {
				name := d.GetSpec().GetName()
				if !strings.HasPrefix(name, AcceptanceTestPrefix) {
					continue
				}
				if testutil.SweepDryRun() {
					fmt.Printf("[DRY RUN] would delete inference gateway: %s\n", name)
					continue
				}

				id := d.GetSpec().GetId()
				fmt.Printf("sweeping inference gateway: %s (%s)\n", name, id)

				_, err := client.Inference.DeleteGateway(ctx, connect.NewRequest(&inferencev1.DeleteGatewayRequest{Id: id}))
				if err != nil {
					return fmt.Errorf("failed to delete gateway %s: %w", name, err)
				}

				if err := testutil.WaitForDelete(ctx, 20*time.Minute, 10*time.Second,
					client.Inference.GetGateway,
					&inferencev1.GetGatewayRequest{Id: id},
				); err != nil {
					return fmt.Errorf("timed out waiting for gateway %s to be deleted: %w", name, err)
				}
			}

			return nil
		},
	})
}
