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
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
		name                 string
		configHasBlock       bool
		serverHasCredentials bool
		stateHasBlock        bool
		priorVersion         types.Int64
		configVersion        types.Int64
		want                 clusterv1beta1.UpdateEndpointRequest_CredentialAction
	}{
		{
			name:                 "block removed while the server holds credentials clears them",
			configHasBlock:       false,
			serverHasCredentials: true,
			stateHasBlock:        true,
			priorVersion:         types.Int64Value(1),
			configVersion:        types.Int64Null(),
			want:                 clear,
		},
		{
			// The import case. The server holds credentials, the config has no
			// block, and Terraform never had one — so there is nothing the user
			// asked to remove and an unrelated edit must not strip auth.
			name:                 "credentials with no block in prior state are left alone",
			configHasBlock:       false,
			serverHasCredentials: true,
			stateHasBlock:        false,
			priorVersion:         types.Int64Null(),
			configVersion:        types.Int64Null(),
			want:                 preserve,
		},
		{
			name:                 "block removed with nothing stored is a no-op",
			configHasBlock:       false,
			serverHasCredentials: false,
			priorVersion:         types.Int64Null(),
			configVersion:        types.Int64Null(),
			want:                 preserve,
		},
		{
			name:                 "removal wins over a bumped counter",
			configHasBlock:       false,
			serverHasCredentials: true,
			stateHasBlock:        true,
			priorVersion:         types.Int64Value(1),
			configVersion:        types.Int64Value(2),
			want:                 clear,
		},
		{
			name:                 "credentials added to an endpoint without any are sent",
			configHasBlock:       true,
			serverHasCredentials: false,
			priorVersion:         types.Int64Null(),
			configVersion:        types.Int64Value(1),
			want:                 replace,
		},
		{
			name:                 "newly added wins over an unchanged counter",
			configHasBlock:       true,
			serverHasCredentials: false,
			priorVersion:         types.Int64Value(1),
			configVersion:        types.Int64Value(1),
			want:                 replace,
		},
		{
			name:                 "a bumped counter rotates",
			configHasBlock:       true,
			serverHasCredentials: true,
			stateHasBlock:        true,
			priorVersion:         types.Int64Value(1),
			configVersion:        types.Int64Value(2),
			want:                 replace,
		},
		{
			name:                 "introducing the counter rotates",
			configHasBlock:       true,
			serverHasCredentials: true,
			stateHasBlock:        true,
			priorVersion:         types.Int64Null(),
			configVersion:        types.Int64Value(1),
			want:                 replace,
		},
		{
			name:                 "removing the counter rotates",
			configHasBlock:       true,
			serverHasCredentials: true,
			stateHasBlock:        true,
			priorVersion:         types.Int64Value(1),
			configVersion:        types.Int64Null(),
			want:                 replace,
		},
		{
			// The documented cost of the write-only design: editing a secret
			// without bumping the counter sends nothing and the apply succeeds.
			name:                 "an edited secret with an unchanged counter is a silent no-op",
			configHasBlock:       true,
			serverHasCredentials: true,
			stateHasBlock:        true,
			priorVersion:         types.Int64Value(3),
			configVersion:        types.Int64Value(3),
			want:                 preserve,
		},
		{
			name:                 "credentials held with no counter on either side preserve",
			configHasBlock:       true,
			serverHasCredentials: true,
			stateHasBlock:        true,
			priorVersion:         types.Int64Null(),
			configVersion:        types.Int64Null(),
			want:                 preserve,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := observability.CredentialAction(tt.configHasBlock, tt.stateHasBlock, tt.serverHasCredentials, tt.priorVersion, tt.configVersion)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Only REPLACE may carry credentials; an unrelated edit sends PRESERVE, and a
// payload alongside it is rejected by the server.
func TestCarriesCredentialPayload(t *testing.T) {
	t.Parallel()

	assert.True(t, observability.CarriesCredentialPayload(clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_REPLACE))
	assert.False(t, observability.CarriesCredentialPayload(clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_PRESERVE))
	assert.False(t, observability.CarriesCredentialPayload(clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_CLEAR))
}

// TestValidateCredentialsVersion pins the validator's one-directional rule.
// A counter without credentials is the dangerous shape: credentialAction reads
// the absent block as CLEAR, so the apply strips the endpoint's auth instead of
// rotating it. The reverse — credentials with no counter — is PRESERVE and
// legitimate, so it must not error.
func TestValidateCredentialsVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		version            types.Int64
		credentialsPresent bool
		wantError          bool
	}{
		{
			name:               "a counter without credentials is rejected",
			version:            types.Int64Value(2),
			credentialsPresent: false,
			wantError:          true,
		},
		{
			name:               "a counter with credentials is fine",
			version:            types.Int64Value(2),
			credentialsPresent: true,
			wantError:          false,
		},
		{
			name:               "credentials without a counter is the PRESERVE shape",
			version:            types.Int64Null(),
			credentialsPresent: true,
			wantError:          false,
		},
		{
			name:               "neither is fine",
			version:            types.Int64Null(),
			credentialsPresent: false,
			wantError:          false,
		},
		{
			// Hoisted above the fully-known gate, so the validator now sees
			// unknown counters that it never used to. It must defer, not error.
			name:               "an unknown counter defers to apply",
			version:            types.Int64Unknown(),
			credentialsPresent: false,
			wantError:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var diagnostics diag.Diagnostics
			observability.ValidateCredentialsVersion(tt.version, tt.credentialsPresent, &diagnostics)

			assert.Equal(t, tt.wantError, diagnostics.HasError())
			if tt.wantError {
				assert.Contains(t, diagnostics.Errors()[0].Summary(), "credentials_version")
			}
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
