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

// TestHTTPSModifyPlanCredentialArms checks that the auth-method guard reads the
// selected arm without decoding its secret values. A header value supplied by
// another resource is unknown at plan time and cannot convert to
// map[string]string, so decoding the model here rejected valid rotations.
func TestHTTPSModifyPlanCredentialArms(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	newResource := func() *observability.HTTPSForwardingEndpointResource {
		r, ok := observability.NewForwardingEndpointHTTPSResource().(*observability.HTTPSForwardingEndpointResource)
		require.True(t, ok, "unexpected resource type")
		return r
	}

	var schema resource.SchemaResponse
	newResource().Schema(ctx, resource.SchemaRequest{}, &schema)
	require.False(t, schema.Diagnostics.HasError(), "%v", schema.Diagnostics)

	// credentials builds a raw value holding one credential arm plus the counter.
	credentials := func(arm path.Path, value any, version int64) tftypes.Value {
		plan := tfsdk.Plan{
			Schema: schema.Schema,
			Raw:    tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), nil),
		}
		require.False(t, plan.SetAttribute(ctx, arm, value).HasError())
		require.False(t, plan.SetAttribute(ctx, path.Root("credentials_version"), types.Int64Value(version)).HasError())
		return plan.Raw
	}

	headersPath := path.Root("credentials").AtName("auth_headers").AtName("headers")
	passwordPath := path.Root("credentials").AtName("basic_auth").AtName("password")

	headers := func(value types.String) types.Map {
		m, diags := types.MapValueFrom(ctx, types.StringType, map[string]types.String{"X-API-Key": value})
		require.False(t, diags.HasError(), "%v", diags)
		return m
	}

	var (
		headersKnown   = credentials(headersPath, headers(types.StringValue("old")), 1)
		headersRotated = credentials(headersPath, headers(types.StringValue("new")), 2)
		headersUnknown = credentials(headersPath, headers(types.StringUnknown()), 2)
		basicKnown     = credentials(passwordPath, types.StringValue("old"), 1)
	)

	// unknownObject models a whole object resolved from another resource: the
	// attribute at p is unknown rather than its leaves.
	unknownObject := func(base tftypes.Value, p path.Path) tftypes.Value {
		attribute, diags := schema.Schema.AttributeAtPath(ctx, p)
		require.False(t, diags.HasError(), "%v", diags)
		object, ok := attribute.GetType().(types.ObjectType)
		require.True(t, ok, "%s is not an object attribute", p)

		plan := tfsdk.Plan{Schema: schema.Schema, Raw: base}
		require.False(t, plan.SetAttribute(ctx, p, types.ObjectUnknown(object.AttrTypes)).HasError())
		return plan.Raw
	}
	basicUnknownTLS := unknownObject(basicKnown, path.Root("tls"))
	basicUnknownCredentials := unknownObject(basicKnown, path.Root("credentials"))

	tests := []struct {
		name      string
		state     tftypes.Value
		config    tftypes.Value
		wantError bool
	}{
		{
			// An unknown secret is not an unknown authentication method.
			name:   "rotating the same arm with an unknown header value is allowed",
			state:  headersKnown,
			config: headersUnknown,
		},
		{
			name:   "rotating the same arm with a known header value is allowed",
			state:  headersKnown,
			config: headersRotated,
		},
		{
			// An unknown value must not let a real method switch through.
			name:      "switching arms is still refused when the new value is unknown",
			state:     basicKnown,
			config:    headersUnknown,
			wantError: true,
		},
		{
			name:      "switching arms is refused when both values are known",
			state:     basicKnown,
			config:    headersRotated,
			wantError: true,
		},
		{
			name:   "unchanged auth method with unknown TLS object",
			state:  basicKnown,
			config: basicUnknownTLS,
		},
		{
			// The arm cannot be read, so the guard defers rather than guesses.
			name:   "unknown credentials object defers the arm check",
			state:  basicKnown,
			config: basicUnknownCredentials,
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
				require.True(t, resp.Diagnostics.HasError(), "expected the method-change diagnostic")
				require.Contains(t, resp.Diagnostics.Errors()[0].Summary(), "Credential type cannot be changed")
				return
			}
			require.False(t, resp.Diagnostics.HasError(), "%v", resp.Diagnostics)
		})
	}
}

// TestHTTPSModifyPlanAddressChange checks that an address edit cannot preserve
// credentials the API will no longer accept, while leaving every case that
// carries no stored credentials across the change alone.
func TestHTTPSModifyPlanAddressChange(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	newResource := func() *observability.HTTPSForwardingEndpointResource {
		r, ok := observability.NewForwardingEndpointHTTPSResource().(*observability.HTTPSForwardingEndpointResource)
		require.True(t, ok, "unexpected resource type")
		return r
	}

	var schema resource.SchemaResponse
	newResource().Schema(ctx, resource.SchemaRequest{}, &schema)
	require.False(t, schema.Diagnostics.HasError(), "%v", schema.Diagnostics)

	// endpoint builds a value with an address, the rotation counter, whether the
	// server holds credentials, and optionally a basic_auth block.
	endpoint := func(address string, version int64, configured, withBlock bool) tftypes.Value {
		plan := tfsdk.Plan{
			Schema: schema.Schema,
			Raw:    tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), nil),
		}
		set := func(p path.Path, v any) {
			require.False(t, plan.SetAttribute(ctx, p, v).HasError())
		}
		set(path.Root("endpoint"), types.StringValue(address))
		set(path.Root("credentials_version"), types.Int64Value(version))
		set(path.Root("credentials_configured"), types.BoolValue(configured))
		if withBlock {
			set(path.Root("credentials").AtName("basic_auth").AtName("password"), types.StringValue("secret"))
		}
		return plan.Raw
	}

	const (
		oldAddress = "https://logs.example.com/v1"
		newAddress = "https://logs.example.com/v2"
	)

	tests := []struct {
		name      string
		state     tftypes.Value
		config    tftypes.Value
		wantError bool
	}{
		{
			name:      "address change with an unchanged counter is refused",
			state:     endpoint(oldAddress, 1, true, true),
			config:    endpoint(newAddress, 1, true, true),
			wantError: true,
		},
		{
			name:   "address change with a bumped counter is allowed",
			state:  endpoint(oldAddress, 1, true, true),
			config: endpoint(newAddress, 2, true, true),
		},
		{
			// Dropping the block clears the credentials, so none are preserved.
			name:   "address change that clears credentials is allowed",
			state:  endpoint(oldAddress, 1, true, true),
			config: endpoint(newAddress, 1, true, false),
		},
		{
			// Adopted after import: nothing in config can resend them.
			name:      "address change with no block to resend from is refused",
			state:     endpoint(oldAddress, 1, true, false),
			config:    endpoint(newAddress, 1, true, false),
			wantError: true,
		},
		{
			// Adopting after import sends the credentials, so the counter need
			// not move: credentialAction already selects REPLACE here.
			name:   "address change while adopting credentials is allowed",
			state:  endpoint(oldAddress, 1, true, false),
			config: endpoint(newAddress, 1, true, true),
		},
		{
			name:   "address change with no stored credentials is allowed",
			state:  endpoint(oldAddress, 1, false, false),
			config: endpoint(newAddress, 1, false, false),
		},
		{
			name:   "unchanged address is allowed",
			state:  endpoint(oldAddress, 1, true, true),
			config: endpoint(oldAddress, 1, true, true),
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
