package observability_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	clusterv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/zclconf/go-cty/cty"
	"golang.org/x/sync/errgroup"
)

// setCommonEndpointAttributes sets common writable attributes from ForwardingEndpointModelCore
// that are present in all endpoint types (slug, display_name).
func setCommonEndpointAttributes(body *hclwrite.Body, core model.ForwardingEndpointCore) {
	body.SetAttributeValue("slug", cty.StringVal(core.Slug.ValueString()))
	body.SetAttributeValue("display_name", cty.StringVal(core.DisplayName.ValueString()))
}

// TestEndpointCredentialAction covers every branch of the credential_action
// decision. Credentials are write-only, so they are null in both plan and
// state and can never produce a diff themselves; these four inputs are
// everything the provider has to go on, which makes the mapping worth pinning
// down here rather than only against a live API.
func TestEndpointCredentialAction(t *testing.T) {
	t.Parallel()

	const (
		clear    = clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_CLEAR
		preserve = clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_PRESERVE
		replace  = clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_REPLACE
	)

	tests := []struct {
		name            string
		hasCredentials  bool
		priorConfigured bool
		priorVersion    types.Int64
		configVersion   types.Int64
		want            clusterv1beta1.UpdateEndpointRequest_CredentialAction
	}{
		{
			name:            "block removed while the server holds credentials clears them",
			hasCredentials:  false,
			priorConfigured: true,
			priorVersion:    types.Int64Value(1),
			configVersion:   types.Int64Null(),
			want:            clear,
		},
		{
			name:            "block removed with nothing stored is a no-op",
			hasCredentials:  false,
			priorConfigured: false,
			priorVersion:    types.Int64Null(),
			configVersion:   types.Int64Null(),
			want:            preserve,
		},
		{
			name:            "removal wins over a bumped counter",
			hasCredentials:  false,
			priorConfigured: true,
			priorVersion:    types.Int64Value(1),
			configVersion:   types.Int64Value(2),
			want:            clear,
		},
		{
			name:            "credentials added to an endpoint without any are sent",
			hasCredentials:  true,
			priorConfigured: false,
			priorVersion:    types.Int64Null(),
			configVersion:   types.Int64Value(1),
			want:            replace,
		},
		{
			name:            "newly added wins over an unchanged counter",
			hasCredentials:  true,
			priorConfigured: false,
			priorVersion:    types.Int64Value(1),
			configVersion:   types.Int64Value(1),
			want:            replace,
		},
		{
			name:            "a bumped counter rotates",
			hasCredentials:  true,
			priorConfigured: true,
			priorVersion:    types.Int64Value(1),
			configVersion:   types.Int64Value(2),
			want:            replace,
		},
		{
			name:            "introducing the counter rotates",
			hasCredentials:  true,
			priorConfigured: true,
			priorVersion:    types.Int64Null(),
			configVersion:   types.Int64Value(1),
			want:            replace,
		},
		{
			name:            "removing the counter rotates",
			hasCredentials:  true,
			priorConfigured: true,
			priorVersion:    types.Int64Value(1),
			configVersion:   types.Int64Null(),
			want:            replace,
		},
		{
			// The documented cost of the write-only design: editing a secret
			// without bumping the counter sends nothing and the apply succeeds.
			name:            "an edited secret with an unchanged counter is a silent no-op",
			hasCredentials:  true,
			priorConfigured: true,
			priorVersion:    types.Int64Value(3),
			configVersion:   types.Int64Value(3),
			want:            preserve,
		},
		{
			name:            "credentials held with no counter on either side preserve",
			hasCredentials:  true,
			priorConfigured: true,
			priorVersion:    types.Int64Null(),
			configVersion:   types.Int64Null(),
			want:            preserve,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := observability.CredentialAction(tt.hasCredentials, tt.priorConfigured, tt.priorVersion, tt.configVersion)
			assert.Equal(t, tt.want, got)
		})
	}
}

const (
	sweeperTimeout = 30 * time.Minute
)

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
		setAccEnvDefaults()
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
			listResp, err := client.ListEndpoints(ctx, &clusterv1beta1.ListEndpointsRequest{})
			if err != nil {
				return fmt.Errorf("failed to list endpoints: %w", err)
			}
			for _, endpoint := range listResp.Endpoints {
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
