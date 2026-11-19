package inference_test

import (
	"fmt"
	"regexp"
	"slices"
	"testing"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestGatewayCWIAMOrganizations_Requests(t *testing.T) {
	t.Parallel()
	for name, organizations := range map[string]types.Set{
		"omitted":  types.SetNull(types.StringType),
		"empty":    types.SetValueMust(types.StringType, []attr.Value{}),
		"explicit": types.SetValueMust(types.StringType, []attr.Value{types.StringValue("cw1234")}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := &inference.InferenceGatewayResourceModel{
				ID:      types.StringValue("gw-123"),
				Name:    types.StringValue("cwiam-test"),
				Zones:   types.SetValueMust(types.StringType, []attr.Value{types.StringValue("US-EAST-01A")}),
				Auth:    &inference.GatewayAuthModel{CoreWeave: &inference.CoreWeaveAuthModel{Organizations: organizations}},
				Routing: &inference.GatewayRoutingModel{BodyBased: &inference.BodyBasedRoutingModel{APIType: types.StringValue("API_TYPE_OPENAI")}},
			}
			var want []string
			if name == "explicit" {
				want = []string{"cw1234"}
			}
			create, diags := inference.ToCreateGatewayRequest(t.Context(), m)
			if diags.HasError() {
				t.Fatalf("create diagnostics: %v", diags)
			}
			if create.GetCoreWeaveAuth() == nil || !slices.Equal(create.GetCoreWeaveAuth().GetOrganizations(), want) {
				t.Fatalf("create auth = %v, want organizations %v", create.GetCoreWeaveAuth(), want)
			}
			update, diags := inference.ToUpdateGatewayRequest(t.Context(), m)
			if diags.HasError() {
				t.Fatalf("update diagnostics: %v", diags)
			}
			if update.GetCoreWeaveAuth() == nil || !slices.Equal(update.GetCoreWeaveAuth().GetOrganizations(), want) {
				t.Fatalf("update auth = %v, want organizations %v", update.GetCoreWeaveAuth(), want)
			}
		})
	}
}

func TestGatewayCWIAMOrganizations_Read(t *testing.T) {
	t.Parallel()
	null := types.SetNull(types.StringType)
	empty := types.SetValueMust(types.StringType, []attr.Value{})
	explicit := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("cw1234")})
	old := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("cw5678")})
	for _, tc := range []struct {
		name  string
		prior *inference.GatewayAuthModel
		api   []string
		want  types.Set
	}{
		{"import default", nil, nil, null},
		{"import explicit", nil, []string{"cw1234"}, explicit},
		{"omitted stays null", &inference.GatewayAuthModel{CoreWeave: &inference.CoreWeaveAuthModel{Organizations: null}}, nil, null},
		{"empty stays empty", &inference.GatewayAuthModel{CoreWeave: &inference.CoreWeaveAuthModel{Organizations: empty}}, nil, empty},
		{"explicit refreshed", &inference.GatewayAuthModel{CoreWeave: &inference.CoreWeaveAuthModel{Organizations: old}}, []string{"cw1234"}, explicit},
		{"override cleared", &inference.GatewayAuthModel{CoreWeave: &inference.CoreWeaveAuthModel{Organizations: explicit}}, nil, empty},
		{"switch from wandb", &inference.GatewayAuthModel{WeightsAndBiases: &inference.WeightsAndBiasesAuthModel{}}, []string{"cw1234"}, explicit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, preserveStatus := range []bool{false, true} {
				m := &inference.InferenceGatewayResourceModel{Auth: tc.prior}
				gw := &inferencev1.Gateway{Spec: &inferencev1.GatewaySpec{
					OrganizationId: "s12345",
					Auth:           &inferencev1.GatewaySpec_CoreWeaveAuth{CoreWeaveAuth: &inferencev1.CoreWeaveAuth{Organizations: tc.api}},
				}}
				if diags := inference.SetFromGateway(m, gw, preserveStatus); diags.HasError() {
					t.Fatalf("read diagnostics: %v", diags)
				}
				if !m.Auth.CoreWeave.Organizations.Equal(tc.want) {
					t.Errorf("organizations = %v, want %v", m.Auth.CoreWeave.Organizations, tc.want)
				}
				if m.OrganizationID.ValueString() != "s12345" {
					t.Error("gateway ownership changed")
				}
				if m.Auth.WeightsAndBiases != nil {
					t.Error("W&B auth was not cleared")
				}
			}
		})
	}
}

func TestGatewayCWIAMOrganizations_Validation(t *testing.T) {
	// Plan-only tests use a placeholder token and never contact the API.
	t.Setenv("COREWEAVE_API_TOKEN", "CW-SECRET-unit-test")
	for _, tc := range []struct{ name, config, errorPattern string }{
		{"omitted", "", ""},
		{"empty", "organizations = []", ""},
		{"internal", `organizations = ["cw1234"]`, ""},
		{"staging", `organizations = ["s12345"]`, ""},
		{"qa", `organizations = ["q123ab"]`, ""},
		{"poc", `organizations = ["poc123"]`, ""},
		{"hex", `organizations = ["abc123"]`, ""},
		{"too many", `organizations = ["cw1234", "cw5678"]`, "must contain at most 1"},
		{"invalid", `organizations = ["invalid"]`, `must be a valid\s+organization ID`},
		{"empty string", `organizations = [""]`, `must be a valid\s+organization ID`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := resource.TestStep{
				Config: fmt.Sprintf(`resource "coreweave_inference_gateway" "test" {
 name = "unit-test-cwiam"
 zones = ["US-EAST-01A"]
 auth = { coreweave = { %s } }
 routing = { body_based = { api_type = "API_TYPE_OPENAI" } }
}`, tc.config),
				PlanOnly:           true,
				ExpectNonEmptyPlan: tc.errorPattern == "",
			}
			if tc.errorPattern != "" {
				step.ExpectError = regexp.MustCompile(tc.errorPattern)
			}
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, Steps: []resource.TestStep{step}})
		})
	}
}
