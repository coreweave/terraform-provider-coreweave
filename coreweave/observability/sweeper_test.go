package observability_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"golang.org/x/sync/errgroup"
)

const (
	sweeperTimeout = 30 * time.Minute
)

func TestMain(m *testing.M) {
	resource.TestMain(m)
}

// Sweeps an endpoint, observing standard rules for when to skip and log, and polling until the endpoint is deleted.
func sweepEndpoint(ctx context.Context, client *coreweave.Client, endpoint *typesv1beta1.ForwardingEndpoint) error {
	ref := endpoint.GetRef()
	logger := slog.Default().With("slug", ref.GetSlug(), "type", endpoint.GetSpec().WhichConfig().String())

	if !strings.HasPrefix(ref.GetSlug(), AcceptanceTestPrefix) {
		logger.InfoContext(ctx, "skipping endpoint sweep", "reason", "does not match acceptance test prefix", "prefix", AcceptanceTestPrefix)
		return nil
	}

	// This skip clause should be the last one evaluated, so that when dry-run mode is disabled, the set of endpoints logged here == the set of endpoints actually swept.
	if testutil.SweepDryRun() {
		logger.InfoContext(ctx, "skipping endpoint sweep", "reason", "dry-run mode is enabled")
		return nil
	}

	logger.InfoContext(ctx, "sweeping endpoint")
	if err := observability.DeleteEndpointAndWait(ctx, client, ref); err != nil {
		return err
	}

	return nil
}

// typedEndpointSweeper returns a function that sweeps all endpoints of a given type.
// This may be reused for endpoints of different types to implement discrete sweepers for each resource.
func typedEndpointSweeper(specType string) func(string) error {
	return func(_ string) error {
		testutil.SetEnvDefaults()
		ctx, cancel := context.WithTimeout(context.Background(), sweeperTimeout)
		defer cancel()

		// Cancel the context for everything if an err occurs in the errgroup; this allows any one failure to break the entire operation.
		eg, ctx := errgroup.WithContext(ctx)

		client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
		if err != nil {
			return fmt.Errorf("failed to build client: %w", err)
		}

		parallelism := 10

		endpoints := make(chan *typesv1beta1.ForwardingEndpoint, parallelism)
		// Run producer in a goroutine, so the errgroup can handle all error propagation.
		eg.Go(func() error {
			defer close(endpoints) // Producer must close the channel when done; this forces all downstream goroutines to break their loops or die.
			listResp, err := client.ListEndpoints(ctx, connect.NewRequest(&clusterv1beta1.ListEndpointsRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list endpoints: %w", err)
			}
			for _, endpoint := range listResp.Msg.Endpoints {
				if endpoint.GetSpec().WhichConfig().String() != specType {
					continue
				}
				if !strings.HasPrefix(endpoint.GetRef().GetSlug(), AcceptanceTestPrefix) {
					continue
				}
				select {
				case <-ctx.Done(): // Context being done indicates an error; break the loop and close the channel.
					return ctx.Err()
				case endpoints <- endpoint:
				}
			}
			return nil
		})

		for range parallelism {
			eg.Go(func() error {
				for toDelete := range endpoints {
					if err := sweepEndpoint(ctx, client, toDelete); err != nil {
						return err
					}
				}
				return nil
			})
		}

		return eg.Wait()
	}
}
