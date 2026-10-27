package observability_test

import (
	"testing"

	clusterv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/zclconf/go-cty/cty"
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
