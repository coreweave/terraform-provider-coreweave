package observability_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

// TestS3ModifyPlanAddressChange covers the S3 half of the address guard. The
// decision logic is shared with HTTPS and tested there; what this pins is the
// wiring — that ModifyPlan is reached at all, and that it reads uri rather
// than endpoint, so a key-prefix edit counts as an address change.
//
// S3 credentials are mandatory, so the branches for a removed block and for an
// endpoint with no stored credentials are unreachable here.
func TestS3ModifyPlanAddressChange(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	newResource := func() *observability.S3ForwardingEndpointResource {
		r, ok := observability.NewForwardingEndpointS3Resource().(*observability.S3ForwardingEndpointResource)
		require.True(t, ok, "unexpected resource type")
		return r
	}

	var schema resource.SchemaResponse
	newResource().Schema(ctx, resource.SchemaRequest{}, &schema)
	require.False(t, schema.Diagnostics.HasError(), "%v", schema.Diagnostics)

	// endpoint builds a value with a uri, the rotation counter, and the
	// mandatory credentials block.
	endpoint := func(uri string, version int64) tftypes.Value {
		plan := tfsdk.Plan{
			Schema: schema.Schema,
			Raw:    tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), nil),
		}
		set := func(p path.Path, v any) {
			require.False(t, plan.SetAttribute(ctx, p, v).HasError())
		}
		set(path.Root("uri"), types.StringValue(uri))
		set(path.Root("credentials_version"), types.Int64Value(version))
		set(path.Root("credentials_configured"), types.BoolValue(true))
		set(path.Root("credentials").AtName("access_key_id"), types.StringValue("AKIA"))
		set(path.Root("credentials").AtName("secret_access_key"), types.StringValue("secret"))
		return plan.Raw
	}

	const (
		bucket       = "s3://logs-bucket/audit"
		otherBucket  = "s3://other-bucket/audit"
		prefixedOnly = "s3://logs-bucket/audit/v2"
	)

	tests := []struct {
		name      string
		state     tftypes.Value
		config    tftypes.Value
		wantError bool
	}{
		{
			name:      "uri change with an unchanged counter is refused",
			state:     endpoint(bucket, 1),
			config:    endpoint(otherBucket, 1),
			wantError: true,
		},
		{
			// Eric's case: the bucket is the same, only the key prefix moves.
			name:      "key-prefix change with an unchanged counter is refused",
			state:     endpoint(bucket, 1),
			config:    endpoint(prefixedOnly, 1),
			wantError: true,
		},
		{
			name:   "uri change with a bumped counter is allowed",
			state:  endpoint(bucket, 1),
			config: endpoint(otherBucket, 2),
		},
		{
			name:   "unchanged uri is allowed",
			state:  endpoint(bucket, 1),
			config: endpoint(bucket, 1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := tfsdk.Plan{Schema: schema.Schema, Raw: tt.config}
			req := resource.ModifyPlanRequest{
				State:  tfsdk.State{Schema: schema.Schema, Raw: tt.state},
				Config: tfsdk.Config{Schema: schema.Schema, Raw: tt.config},
				Plan:   plan,
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			newResource().ModifyPlan(t.Context(), req, &resp)

			if tt.wantError {
				require.True(t, resp.Diagnostics.HasError(), "expected the address-change diagnostic")
				require.Contains(t, resp.Diagnostics.Errors()[0].Summary(), "Credentials must be updated when the address changes")
				return
			}
			require.False(t, resp.Diagnostics.HasError(), "%v", resp.Diagnostics)
		})
	}
}
