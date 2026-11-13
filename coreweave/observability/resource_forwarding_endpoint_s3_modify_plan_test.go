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

// TestS3ModifyPlanAddressChange pins that ModifyPlan reads uri, so a change
// to the key prefix alone counts as an address change.
//
// S3 credentials are mandatory, so the removed-block and no-credentials
// branches are unreachable here.
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

	// A changed slug replaces the endpoint; an unknown one cannot say yet.
	withSlug := func(base tftypes.Value, slug string) tftypes.Value {
		plan := tfsdk.Plan{Schema: schema.Schema, Raw: base}
		require.False(t, plan.SetAttribute(ctx, path.Root("slug"), types.StringValue(slug)).HasError())
		return plan.Raw
	}
	unknownSlug := func(base tftypes.Value) tftypes.Value {
		plan := tfsdk.Plan{Schema: schema.Schema, Raw: base}
		require.False(t, plan.SetAttribute(ctx, path.Root("slug"), types.StringUnknown()).HasError())
		return plan.Raw
	}

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
			name:   "slug change alongside a uri change is allowed",
			state:  withSlug(endpoint(bucket, 1), "logs-old"),
			config: withSlug(endpoint(otherBucket, 1), "logs-new"),
		},
		{
			name:   "unknown planned slug defers the uri check",
			state:  withSlug(endpoint(bucket, 1), "logs-old"),
			config: unknownSlug(endpoint(otherBucket, 1)),
		},
		{
			name:      "uri change with an unchanged slug is still refused",
			state:     withSlug(endpoint(bucket, 1), "logs-old"),
			config:    withSlug(endpoint(otherBucket, 1), "logs-old"),
			wantError: true,
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
			if tt.name == "unknown planned slug defers the uri check" {
				// Replan the same change after the slug resolves to the existing one.
				resolved := withSlug(tt.config, "logs-old")
				req.Plan.Raw = resolved
				req.Config.Raw = resolved
				resp = resource.ModifyPlanResponse{Plan: req.Plan}
				newResource().ModifyPlan(t.Context(), req, &resp)
				require.True(t, resp.Diagnostics.HasError(), "resolved in-place URI change must be refused")
				require.Contains(t, resp.Diagnostics.Errors()[0].Summary(), "Credentials must be updated when the address changes")
			}
		})
	}
}
