package observability_test

import (
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

func TestEndpointSlugPlanModifierRequiresReplacement(t *testing.T) {
	t.Parallel()
	for name, r := range map[string]resource.Resource{
		"https": observability.NewForwardingEndpointHTTPSResource(),
		"s3":    observability.NewForwardingEndpointS3Resource(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			var schemaResp resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
			require.False(t, schemaResp.Diagnostics.HasError(), "%v", schemaResp.Diagnostics)
			slug, ok := schemaResp.Schema.Attributes["slug"].(schema.StringAttribute)
			require.True(t, ok)
			require.NotEmpty(t, slug.PlanModifiers)

			value := func(s string) tftypes.Value {
				p := tfsdk.Plan{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
				require.False(t, p.SetAttribute(ctx, path.Root("slug"), types.StringValue(s)).HasError())
				return p.Raw
			}
			state := tfsdk.State{Schema: schemaResp.Schema, Raw: value("logs-old")}
			for _, tc := range []struct {
				slug    string
				replace bool
			}{{"logs-new", true}, {"logs-old", false}} {
				plan := tfsdk.Plan{Schema: schemaResp.Schema, Raw: value(tc.slug)}
				req := planmodifier.StringRequest{State: state, Plan: plan, StateValue: types.StringValue("logs-old"), PlanValue: types.StringValue(tc.slug)}
				var resp planmodifier.StringResponse
				for _, modifier := range slug.PlanModifiers {
					modifier.PlanModifyString(ctx, req, &resp)
				}
				require.False(t, resp.Diagnostics.HasError(), "%v", resp.Diagnostics)
				require.Equal(t, tc.replace, resp.RequiresReplace, "planned slug %q", tc.slug)
			}
		})
	}
}
