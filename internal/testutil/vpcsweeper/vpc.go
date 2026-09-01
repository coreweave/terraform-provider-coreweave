package vpcsweeper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	networkingv1beta1 "buf.build/gen/go/coreweave/networking/protocolbuffers/go/coreweave/networking/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
)

const resourceType = "coreweave_networking_vpc"

type Client interface {
	ListVPCs(context.Context, *connect.Request[networkingv1beta1.ListVPCsRequest]) (*connect.Response[networkingv1beta1.ListVPCsResponse], error)
	GetVPC(context.Context, *connect.Request[networkingv1beta1.GetVPCRequest]) (*connect.Response[networkingv1beta1.GetVPCResponse], error)
	DeleteVPC(context.Context, *connect.Request[networkingv1beta1.DeleteVPCRequest]) (*connect.Response[networkingv1beta1.DeleteVPCResponse], error)
}

type Config struct {
	Prefix string
	Zone   string
}

func New(client Client, config Config) (testutil.SweepConfig[*networkingv1beta1.VPC], error) {
	config.Prefix = strings.TrimSpace(config.Prefix)
	if config.Prefix == "" {
		return testutil.SweepConfig[*networkingv1beta1.VPC]{}, fmt.Errorf("VPC sweep prefix must not be empty")
	}
	config.Zone = strings.TrimSpace(config.Zone)
	if config.Zone == "" {
		return testutil.SweepConfig[*networkingv1beta1.VPC]{}, fmt.Errorf("VPC sweep zone must not be empty")
	}

	return testutil.SweepConfig[*networkingv1beta1.VPC]{
		ResourceType: resourceType,
		List: func(ctx context.Context) ([]*networkingv1beta1.VPC, error) {
			response, err := client.ListVPCs(ctx, connect.NewRequest(&networkingv1beta1.ListVPCsRequest{}))
			if err != nil {
				return nil, fmt.Errorf("list VPCs: %w", err)
			}
			return response.Msg.Items, nil
		},
		Name: func(vpc *networkingv1beta1.VPC) string {
			return vpc.GetName()
		},
		Match: func(vpc *networkingv1beta1.VPC) bool {
			return strings.HasPrefix(vpc.GetName(), config.Prefix) && vpc.GetZone() == config.Zone
		},
		Delete: func(ctx context.Context, vpc *networkingv1beta1.VPC) error {
			_, err := client.DeleteVPC(ctx, connect.NewRequest(&networkingv1beta1.DeleteVPCRequest{Id: vpc.GetId()}))
			if connect.CodeOf(err) == connect.CodeNotFound {
				return nil
			}
			if err != nil {
				return fmt.Errorf("delete VPC: %w", err)
			}

			if err := testutil.WaitForDelete(ctx, 5*time.Minute, 15*time.Second, client.GetVPC, &networkingv1beta1.GetVPCRequest{Id: vpc.GetId()}); err != nil {
				return fmt.Errorf("wait for VPC deletion: %w", err)
			}
			return nil
		},
	}, nil
}
