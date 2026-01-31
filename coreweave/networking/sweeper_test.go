package networking_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
)

func init() {
	resource.AddTestSweepers("coreweave_networking_vpc", &resource.Sweeper{
		Name:         "coreweave_networking_vpc",
		Dependencies: []string{},
		F: func(zone string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			return testutil.SweepSequential(ctx, testutil.SweeperConfig[*networkingv1beta1.VPC]{
				Lister: func(ctx context.Context) ([]*networkingv1beta1.VPC, error) {
					listResp, err := client.ListVPCs(ctx, &connect.Request[networkingv1beta1.ListVPCsRequest]{})
					if err != nil {
						return nil, err
					}
					return listResp.Msg.Items, nil
				},
				NameGetter: func(vpc *networkingv1beta1.VPC) string {
					return vpc.Name
				},
				ZoneGetter: func(vpc *networkingv1beta1.VPC) string {
					return vpc.Zone
				},
				Deleter: func(ctx context.Context, vpc *networkingv1beta1.VPC) error {
					_, err := client.DeleteVPC(ctx, connect.NewRequest(&networkingv1beta1.DeleteVPCRequest{
						Id: vpc.Id,
					}))
					if coreweave.IsNotFoundError(err) {
						return nil
					} else if err != nil {
						return fmt.Errorf("failed to delete VPC: %w", err)
					}

					if err := testutil.WaitForDelete(ctx, 5*time.Minute, 15*time.Second, client.GetVPC, &networkingv1beta1.GetVPCRequest{
						Id: vpc.Id,
					}); err != nil {
						return fmt.Errorf("failed to wait for VPC deletion: %w", err)
					}

					return nil
				},
				Prefix:         AcceptanceTestPrefix,
				Zone:           zone,
				Timeout:        30 * time.Minute,
			})
		},
	})
}

func TestMain(m *testing.M) {
	resource.TestMain(m)
}
