package inference_test

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"testing"
	"time"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- Unit tests ---

func TestInferenceGateway_Schema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	schemaReq := fwresource.SchemaRequest{}
	schemaResp := &fwresource.SchemaResponse{}

	inference.NewInferenceGatewayResource().Schema(ctx, schemaReq, schemaResp)

	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema returned errors: %v", schemaResp.Diagnostics)
	}
}

func TestSetFromGateway_CoreWeaveAuth(t *testing.T) {
	t.Parallel()

	gw := &inferencev1.Gateway{
		Spec: &inferencev1.GatewaySpec{
			Id:             "gw-123",
			Name:           "test-gw",
			OrganizationId: "org-abc",
			Zones:          []string{"US-EAST-04A"},
			Auth: &inferencev1.GatewaySpec_CoreWeaveAuth{
				CoreWeaveAuth: &inferencev1.CoreWeaveAuth{},
			},
			Routing: &inferencev1.GatewaySpec_BodyBasedRouting{
				BodyBasedRouting: &inferencev1.BodyBasedRouting{
					ApiType: inferencev1.BodyBasedRouting_API_TYPE_OPENAI,
				},
			},
		},
		Status: &inferencev1.GatewayStatus{
			Status: inferencev1.Status_STATUS_UNSPECIFIED,
		},
	}

	m := &inference.InferenceGatewayResourceModel{}
	diags := inference.SetFromGateway(m, gw, false)
	if diags.HasError() {
		t.Fatalf("SetFromGateway returned errors: %v", diags)
	}

	if m.Auth.CoreWeave == nil {
		t.Error("Auth.CoreWeave: expected non-nil")
	}
	if m.Auth.WeightsAndBiases != nil {
		t.Error("Auth.WeightsAndBiases: expected nil")
	}
	if m.ID.ValueString() != "gw-123" {
		t.Errorf("ID: got %q, want %q", m.ID.ValueString(), "gw-123")
	}
}

// TestSetFromGateway_PreserveStatusFields verifies that during Update
// (preserveStatusFields=true) the observed status fields — status, updated_at,
// conditions — retain their prior-state values (as carried by the plan via
// UseStateForUnknown) rather than being overwritten by the fresh API response,
// while spec fields are still refreshed. Read/Create (preserveStatusFields=false)
// refresh all fields.
func TestSetFromGateway_PreserveStatusFields(t *testing.T) {
	t.Parallel()

	base := func(status inferencev1.Status, name, condReason string, updatedAt *timestamppb.Timestamp) *inferencev1.Gateway {
		return &inferencev1.Gateway{
			Spec: &inferencev1.GatewaySpec{
				Id:             "gw-123",
				Name:           name,
				OrganizationId: "org-abc",
				Zones:          []string{"US-EAST-04A"},
				Auth: &inferencev1.GatewaySpec_CoreWeaveAuth{
					CoreWeaveAuth: &inferencev1.CoreWeaveAuth{},
				},
				Routing: &inferencev1.GatewaySpec_BodyBasedRouting{
					BodyBasedRouting: &inferencev1.BodyBasedRouting{
						ApiType: inferencev1.BodyBasedRouting_API_TYPE_OPENAI,
					},
				},
			},
			Status: &inferencev1.GatewayStatus{
				Status:    status,
				UpdatedAt: updatedAt,
				Conditions: []*inferencev1.Condition{
					{Type: "Ready", Status: inferencev1.Condition_STATUS_TRUE, Reason: condReason},
				},
			},
		}
	}

	prior := base(inferencev1.Status_STATUS_READY, "test-gw", "AsExpected", timestamppb.New(time.Unix(1000, 0).UTC()))
	fresh := base(inferencev1.Status_STATUS_UPDATING, "test-gw-renamed", "Reconciling", timestamppb.New(time.Unix(2000, 0).UTC()))

	// Seed the model with prior-state values (what the plan holds on Update).
	m := &inference.InferenceGatewayResourceModel{}
	if diags := inference.SetFromGateway(m, prior, false); diags.HasError() {
		t.Fatalf("seed SetFromGateway returned errors: %v", diags)
	}
	priorStatus := m.Status
	priorUpdatedAt := m.UpdatedAt
	priorConditions := m.Conditions

	// Update path: status fields must be preserved, spec fields refreshed.
	if diags := inference.SetFromGateway(m, fresh, true); diags.HasError() {
		t.Fatalf("preserve SetFromGateway returned errors: %v", diags)
	}
	if !m.Status.Equal(priorStatus) {
		t.Errorf("Status: expected preserved %v, got %v", priorStatus, m.Status)
	}
	if !m.UpdatedAt.Equal(priorUpdatedAt) {
		t.Errorf("UpdatedAt: expected preserved %v, got %v", priorUpdatedAt, m.UpdatedAt)
	}
	if !m.Conditions.Equal(priorConditions) {
		t.Errorf("Conditions: expected preserved %v, got %v", priorConditions, m.Conditions)
	}
	if m.Name.ValueString() != "test-gw-renamed" {
		t.Errorf("Name: expected spec to refresh to 'test-gw-renamed', got %q", m.Name.ValueString())
	}

	// Read path: status fields must refresh from the API response.
	if diags := inference.SetFromGateway(m, fresh, false); diags.HasError() {
		t.Fatalf("refresh SetFromGateway returned errors: %v", diags)
	}
	if m.Status.ValueString() != inferencev1.Status_STATUS_UPDATING.String() {
		t.Errorf("Status: expected refreshed %q, got %q", inferencev1.Status_STATUS_UPDATING.String(), m.Status.ValueString())
	}
	if m.UpdatedAt.Equal(priorUpdatedAt) {
		t.Errorf("UpdatedAt: expected refresh to differ from prior %v, got %v", priorUpdatedAt, m.UpdatedAt)
	}
	if m.Conditions.Equal(priorConditions) {
		t.Errorf("Conditions: expected refresh to differ from prior, got %v", m.Conditions)
	}
}

func TestSetFromGateway_WeightsAndBiasesAuth(t *testing.T) {
	t.Parallel()

	gw := &inferencev1.Gateway{
		Spec: &inferencev1.GatewaySpec{
			Id:             "gw-456",
			Name:           "wandb-gw",
			OrganizationId: "org-abc",
			Zones:          []string{"US-EAST-04A"},
			Auth: &inferencev1.GatewaySpec_WeightsAndBiasesAuth{
				WeightsAndBiasesAuth: &inferencev1.WeightsAndBiasesAuth{
					ApiKey:             "secret-key",
					ServerUrl:          "https://wandb.example.com",
					EnableUsageReports: true,
					EnableRateLimiting: false,
				},
			},
			Routing: &inferencev1.GatewaySpec_BodyBasedRouting{
				BodyBasedRouting: &inferencev1.BodyBasedRouting{
					ApiType: inferencev1.BodyBasedRouting_API_TYPE_OPENAI,
				},
			},
		},
		Status: &inferencev1.GatewayStatus{
			Status: inferencev1.Status_STATUS_UNSPECIFIED,
		},
	}

	// Pre-populate W&B model so null-preservation logic doesn't kick in.
	m := &inference.InferenceGatewayResourceModel{
		Auth: &inference.GatewayAuthModel{
			WeightsAndBiases: &inference.WeightsAndBiasesAuthModel{
				APIKey:             types.StringValue("placeholder"),
				ServerURL:          types.StringValue("placeholder"),
				EnableUsageReports: types.BoolValue(false),
				EnableRateLimiting: types.BoolValue(false),
			},
		},
	}

	diags := inference.SetFromGateway(m, gw, false)
	if diags.HasError() {
		t.Fatalf("SetFromGateway returned errors: %v", diags)
	}

	if m.Auth.CoreWeave != nil {
		t.Error("Auth.CoreWeave: expected nil")
	}
	if m.Auth.WeightsAndBiases == nil {
		t.Fatal("Auth.WeightsAndBiases: expected non-nil")
	}
	if m.Auth.WeightsAndBiases.APIKey.ValueString() != "secret-key" {
		t.Errorf("Auth.WeightsAndBiases.ApiKey: got %q, want %q", m.Auth.WeightsAndBiases.APIKey.ValueString(), "secret-key")
	}
	if m.Auth.WeightsAndBiases.ServerURL.ValueString() != "https://wandb.example.com" {
		t.Errorf("Auth.WeightsAndBiases.ServerUrl: got %q, want %q", m.Auth.WeightsAndBiases.ServerURL.ValueString(), "https://wandb.example.com")
	}
	if !m.Auth.WeightsAndBiases.EnableUsageReports.ValueBool() {
		t.Error("Auth.WeightsAndBiases.EnableUsageReports: expected true")
	}
	if m.Auth.WeightsAndBiases.EnableRateLimiting.ValueBool() {
		t.Error("Auth.WeightsAndBiases.EnableRateLimiting: expected false")
	}
}

func TestSetFromGateway_NullPreservation(t *testing.T) {
	t.Parallel()

	gw := &inferencev1.Gateway{
		Spec: &inferencev1.GatewaySpec{
			Id:             "gw-789",
			Name:           "null-gw",
			OrganizationId: "org-abc",
			Zones:          []string{"US-EAST-04A"},
			Auth: &inferencev1.GatewaySpec_WeightsAndBiasesAuth{
				WeightsAndBiasesAuth: &inferencev1.WeightsAndBiasesAuth{
					// All zero values — should remain null in state.
					ApiKey:             "",
					ServerUrl:          "",
					EnableUsageReports: false,
					EnableRateLimiting: false,
				},
			},
			Routing: &inferencev1.GatewaySpec_BodyBasedRouting{
				BodyBasedRouting: &inferencev1.BodyBasedRouting{
					ApiType: inferencev1.BodyBasedRouting_API_TYPE_OPENAI,
				},
			},
			// No endpoint configuration — should remain nil.
		},
		Status: &inferencev1.GatewayStatus{
			Status: inferencev1.Status_STATUS_UNSPECIFIED,
		},
	}

	// Model starts with null optional fields (user didn't configure them).
	m := &inference.InferenceGatewayResourceModel{
		Auth: &inference.GatewayAuthModel{
			WeightsAndBiases: &inference.WeightsAndBiasesAuthModel{
				APIKey:             types.StringNull(),
				ServerURL:          types.StringNull(),
				EnableUsageReports: types.BoolNull(),
				EnableRateLimiting: types.BoolNull(),
			},
		},
		EndpointConfiguration: nil,
	}

	diags := inference.SetFromGateway(m, gw, false)
	if diags.HasError() {
		t.Fatalf("SetFromGateway returned errors: %v", diags)
	}

	// Optional W&B fields should remain null when API returns zero values.
	if !m.Auth.WeightsAndBiases.APIKey.IsNull() {
		t.Errorf("Auth.WeightsAndBiases.ApiKey: expected null when API returns empty, got %v", m.Auth.WeightsAndBiases.APIKey)
	}
	if !m.Auth.WeightsAndBiases.ServerURL.IsNull() {
		t.Errorf("Auth.WeightsAndBiases.ServerUrl: expected null when API returns empty, got %v", m.Auth.WeightsAndBiases.ServerURL)
	}
	if !m.Auth.WeightsAndBiases.EnableUsageReports.IsNull() {
		t.Errorf("Auth.WeightsAndBiases.EnableUsageReports: expected null when API returns false, got %v", m.Auth.WeightsAndBiases.EnableUsageReports)
	}
	if !m.Auth.WeightsAndBiases.EnableRateLimiting.IsNull() {
		t.Errorf("Auth.WeightsAndBiases.EnableRateLimiting: expected null when API returns false, got %v", m.Auth.WeightsAndBiases.EnableRateLimiting)
	}

	// Endpoint configuration should remain nil.
	if m.EndpointConfiguration != nil {
		t.Errorf("EndpointConfiguration: expected nil when API returns no endpoint config, got %v", m.EndpointConfiguration)
	}
}

func TestSetFromGateway_BodyBasedRouting(t *testing.T) {
	t.Parallel()

	gw := &inferencev1.Gateway{
		Spec: &inferencev1.GatewaySpec{
			Id:    "gw-body",
			Name:  "body-gw",
			Zones: []string{"US-EAST-04A"},
			Auth: &inferencev1.GatewaySpec_CoreWeaveAuth{
				CoreWeaveAuth: &inferencev1.CoreWeaveAuth{},
			},
			Routing: &inferencev1.GatewaySpec_BodyBasedRouting{
				BodyBasedRouting: &inferencev1.BodyBasedRouting{
					ApiType: inferencev1.BodyBasedRouting_API_TYPE_OPENAI,
				},
			},
		},
		Status: &inferencev1.GatewayStatus{
			Status: inferencev1.Status_STATUS_UNSPECIFIED,
		},
	}

	m := &inference.InferenceGatewayResourceModel{}
	diags := inference.SetFromGateway(m, gw, false)
	if diags.HasError() {
		t.Fatalf("SetFromGateway returned errors: %v", diags)
	}

	if m.Routing.BodyBased == nil {
		t.Fatal("Routing.BodyBased: expected non-nil")
	}
	if m.Routing.BodyBased.APIType.ValueString() != "API_TYPE_OPENAI" {
		t.Errorf("Routing.BodyBased.ApiType: got %q, want %q", m.Routing.BodyBased.APIType.ValueString(), "API_TYPE_OPENAI")
	}
	if m.Routing.HeaderBased != nil {
		t.Error("Routing.HeaderBased: expected nil")
	}
	if m.Routing.PathBased != nil {
		t.Error("Routing.PathBased: expected nil")
	}
}

func TestSetFromGateway_HeaderBasedRouting(t *testing.T) {
	t.Parallel()

	gw := &inferencev1.Gateway{
		Spec: &inferencev1.GatewaySpec{
			Id:    "gw-header",
			Name:  "header-gw",
			Zones: []string{"US-EAST-04A"},
			Auth: &inferencev1.GatewaySpec_CoreWeaveAuth{
				CoreWeaveAuth: &inferencev1.CoreWeaveAuth{},
			},
			Routing: &inferencev1.GatewaySpec_HeaderBasedRouting{
				HeaderBasedRouting: &inferencev1.HeaderBasedRouting{
					HeaderName: "X-Model-Name",
				},
			},
		},
		Status: &inferencev1.GatewayStatus{
			Status: inferencev1.Status_STATUS_UNSPECIFIED,
		},
	}

	m := &inference.InferenceGatewayResourceModel{}
	diags := inference.SetFromGateway(m, gw, false)
	if diags.HasError() {
		t.Fatalf("SetFromGateway returned errors: %v", diags)
	}

	if m.Routing.HeaderBased == nil {
		t.Fatal("Routing.HeaderBased: expected non-nil")
	}
	if m.Routing.HeaderBased.HeaderName.ValueString() != "X-Model-Name" {
		t.Errorf("Routing.HeaderBased.HeaderName: got %q, want %q", m.Routing.HeaderBased.HeaderName.ValueString(), "X-Model-Name")
	}
	if m.Routing.BodyBased != nil {
		t.Error("Routing.BodyBased: expected nil")
	}
	if m.Routing.PathBased != nil {
		t.Error("Routing.PathBased: expected nil")
	}
}

func TestSetFromGateway_PathBasedRouting(t *testing.T) {
	t.Parallel()

	gw := &inferencev1.Gateway{
		Spec: &inferencev1.GatewaySpec{
			Id:    "gw-path",
			Name:  "path-gw",
			Zones: []string{"US-EAST-04A"},
			Auth: &inferencev1.GatewaySpec_CoreWeaveAuth{
				CoreWeaveAuth: &inferencev1.CoreWeaveAuth{},
			},
			Routing: &inferencev1.GatewaySpec_PathBasedRouting{
				PathBasedRouting: &inferencev1.PathBasedRouting{},
			},
		},
		Status: &inferencev1.GatewayStatus{
			Status: inferencev1.Status_STATUS_UNSPECIFIED,
		},
	}

	m := &inference.InferenceGatewayResourceModel{}
	diags := inference.SetFromGateway(m, gw, false)
	if diags.HasError() {
		t.Fatalf("SetFromGateway returned errors: %v", diags)
	}

	if m.Routing.PathBased == nil {
		t.Fatal("Routing.PathBased: expected non-nil")
	}
	if m.Routing.BodyBased != nil {
		t.Error("Routing.BodyBased: expected nil")
	}
	if m.Routing.HeaderBased != nil {
		t.Error("Routing.HeaderBased: expected nil")
	}
}

func TestToCreateGatewayRequest_CoreWeaveAuth(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	m := &inference.InferenceGatewayResourceModel{
		Name:  types.StringValue("cw-auth-gw"),
		Zones: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("US-EAST-04A")}),
		Auth: &inference.GatewayAuthModel{
			CoreWeave: &inference.CoreWeaveAuthModel{},
		},
		Routing: &inference.GatewayRoutingModel{
			BodyBased: &inference.BodyBasedRoutingModel{
				APIType: types.StringValue("API_TYPE_OPENAI"),
			},
		},
	}

	req, diags := inference.ToCreateGatewayRequest(ctx, m)
	if diags.HasError() {
		t.Fatalf("ToCreateGatewayRequest returned errors: %v", diags)
	}

	if req.Name != "cw-auth-gw" {
		t.Errorf("Name: got %q, want %q", req.Name, "cw-auth-gw")
	}

	cwAuth, ok := req.Auth.(*inferencev1.CreateGatewayRequest_CoreWeaveAuth)
	if !ok {
		t.Fatalf("Auth: expected *CreateGatewayRequest_CoreWeaveAuth, got %T", req.Auth)
	}
	if cwAuth.CoreWeaveAuth == nil {
		t.Error("Auth.CoreWeaveAuth: expected non-nil")
	}
}

func TestToCreateGatewayRequest_WandBAuth(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	m := &inference.InferenceGatewayResourceModel{
		Name:  types.StringValue("wandb-auth-gw"),
		Zones: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("US-EAST-04A")}),
		Auth: &inference.GatewayAuthModel{
			WeightsAndBiases: &inference.WeightsAndBiasesAuthModel{
				APIKey:             types.StringValue("my-api-key"),
				ServerURL:          types.StringValue("https://wandb.example.com"),
				EnableUsageReports: types.BoolValue(true),
				EnableRateLimiting: types.BoolValue(false),
			},
		},
		Routing: &inference.GatewayRoutingModel{
			BodyBased: &inference.BodyBasedRoutingModel{
				APIType: types.StringValue("API_TYPE_OPENAI"),
			},
		},
	}

	req, diags := inference.ToCreateGatewayRequest(ctx, m)
	if diags.HasError() {
		t.Fatalf("ToCreateGatewayRequest returned errors: %v", diags)
	}

	wbAuth, ok := req.Auth.(*inferencev1.CreateGatewayRequest_WeightsAndBiasesAuth)
	if !ok {
		t.Fatalf("Auth: expected *CreateGatewayRequest_WeightsAndBiasesAuth, got %T", req.Auth)
	}
	if wbAuth.WeightsAndBiasesAuth.GetApiKey() != "my-api-key" {
		t.Errorf("Auth.WandB.ApiKey: got %q, want %q", wbAuth.WeightsAndBiasesAuth.GetApiKey(), "my-api-key")
	}
	if wbAuth.WeightsAndBiasesAuth.GetServerUrl() != "https://wandb.example.com" {
		t.Errorf("Auth.WandB.ServerUrl: got %q, want %q", wbAuth.WeightsAndBiasesAuth.GetServerUrl(), "https://wandb.example.com")
	}
	if !wbAuth.WeightsAndBiasesAuth.GetEnableUsageReports() {
		t.Error("Auth.WandB.EnableUsageReports: expected true")
	}
	if wbAuth.WeightsAndBiasesAuth.GetEnableRateLimiting() {
		t.Error("Auth.WandB.EnableRateLimiting: expected false")
	}
}

func TestToCreateGatewayRequest_AllRoutingTypes(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("body_based", func(t *testing.T) {
		t.Parallel()

		m := &inference.InferenceGatewayResourceModel{
			Name:  types.StringValue("body-gw"),
			Zones: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("US-EAST-04A")}),
			Auth: &inference.GatewayAuthModel{
				CoreWeave: &inference.CoreWeaveAuthModel{},
			},
			Routing: &inference.GatewayRoutingModel{
				BodyBased: &inference.BodyBasedRoutingModel{
					APIType: types.StringValue("API_TYPE_OPENAI"),
				},
			},
		}

		req, diags := inference.ToCreateGatewayRequest(ctx, m)
		if diags.HasError() {
			t.Fatalf("ToCreateGatewayRequest returned errors: %v", diags)
		}

		bbr, ok := req.Routing.(*inferencev1.CreateGatewayRequest_BodyBasedRouting)
		if !ok {
			t.Fatalf("Routing: expected *CreateGatewayRequest_BodyBasedRouting, got %T", req.Routing)
		}
		if bbr.BodyBasedRouting.GetApiType() != inferencev1.BodyBasedRouting_API_TYPE_OPENAI {
			t.Errorf("Routing.BodyBased.ApiType: got %v, want API_TYPE_OPENAI", bbr.BodyBasedRouting.GetApiType())
		}
	})

	t.Run("header_based", func(t *testing.T) {
		t.Parallel()

		m := &inference.InferenceGatewayResourceModel{
			Name:  types.StringValue("header-gw"),
			Zones: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("US-EAST-04A")}),
			Auth: &inference.GatewayAuthModel{
				CoreWeave: &inference.CoreWeaveAuthModel{},
			},
			Routing: &inference.GatewayRoutingModel{
				HeaderBased: &inference.HeaderBasedRoutingModel{
					HeaderName: types.StringValue("X-Model"),
				},
			},
		}

		req, diags := inference.ToCreateGatewayRequest(ctx, m)
		if diags.HasError() {
			t.Fatalf("ToCreateGatewayRequest returned errors: %v", diags)
		}

		hbr, ok := req.Routing.(*inferencev1.CreateGatewayRequest_HeaderBasedRouting)
		if !ok {
			t.Fatalf("Routing: expected *CreateGatewayRequest_HeaderBasedRouting, got %T", req.Routing)
		}
		if hbr.HeaderBasedRouting.GetHeaderName() != "X-Model" {
			t.Errorf("Routing.HeaderBased.HeaderName: got %q, want %q", hbr.HeaderBasedRouting.GetHeaderName(), "X-Model")
		}
	})

	t.Run("path_based", func(t *testing.T) {
		t.Parallel()

		m := &inference.InferenceGatewayResourceModel{
			Name:  types.StringValue("path-gw"),
			Zones: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("US-EAST-04A")}),
			Auth: &inference.GatewayAuthModel{
				CoreWeave: &inference.CoreWeaveAuthModel{},
			},
			Routing: &inference.GatewayRoutingModel{
				PathBased: &inference.PathBasedRoutingModel{},
			},
		}

		req, diags := inference.ToCreateGatewayRequest(ctx, m)
		if diags.HasError() {
			t.Fatalf("ToCreateGatewayRequest returned errors: %v", diags)
		}

		pbr, ok := req.Routing.(*inferencev1.CreateGatewayRequest_PathBasedRouting)
		if !ok {
			t.Fatalf("Routing: expected *CreateGatewayRequest_PathBasedRouting, got %T", req.Routing)
		}
		if pbr.PathBasedRouting == nil {
			t.Error("Routing.PathBased: expected non-nil")
		}
	})
}

// wandbAuthGatewayConfig renders a minimal gateway whose W&B auth block contains
// only the attributes in wandbAttrs. Zones are hardcoded (no data source) so the
// plan stays local and never reaches the API.
func wandbAuthGatewayConfig(wandbAttrs string) string {
	return fmt.Sprintf(`
resource "coreweave_inference_gateway" "test" {
  name  = "unit-test-wandb"
  zones = ["US-EAST-04A"]

  auth = {
    weights_and_biases = {
      %s
    }
  }

  routing = {
    body_based = {
      api_type = "API_TYPE_OPENAI"
    }
  }
}
`, wandbAttrs)
}

// TestInferenceGateway_WandBAuthValidation pins the W&B auth coupling: a
// server_url may be set without an api_key (W&B custom-instance gateways are
// read back with a server_url but never an api_key, which is sensitive and not
// returned by the API), while an api_key still requires a server_url — mirroring
// the proto's server_url_requires_api_key rule.
func TestInferenceGateway_WandBAuthValidation(t *testing.T) {
	// A non-empty token lets the provider configure so a plan-only create can be
	// computed; no API call is made during plan.
	t.Setenv("COREWEAVE_API_TOKEN", "CW-SECRET-unit-test")

	t.Run("server_url without api_key is valid", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:             wandbAuthGatewayConfig(`server_url = "https://wandb.example.com"`),
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
				},
			},
		})
	})

	t.Run("api_key without server_url is invalid", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      wandbAuthGatewayConfig(`api_key = "wandb-key"`),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(`server_url" must be specified when`),
				},
			},
		})
	})
}

// --- Acceptance tests ---

func gatewayBasicConfig(name, preferredZone string) string {
	return fmt.Sprintf(`
data "coreweave_inference_gateway_parameters" "params" {}

locals {
  preferred_zone  = %q
  available_zones = data.coreweave_inference_gateway_parameters.params.zones
  zone            = local.preferred_zone != "" ? local.preferred_zone : sort(tolist(local.available_zones))[0]
}

resource "coreweave_inference_gateway" "test" {
  name  = %q
  zones = [local.zone]

  auth = {
    coreweave = {}
  }

  routing = {
    body_based = {
      api_type = "API_TYPE_OPENAI"
    }
  }

  lifecycle {
    precondition {
      condition     = local.preferred_zone == "" || contains(local.available_zones, local.preferred_zone)
      error_message = "INFERENCE_ZONE=\"${local.preferred_zone}\" is not present in gateway parameters; available: ${jsonencode(local.available_zones)}"
    }
  }
}
`, preferredZone, name)
}

func gatewayUpdatedConfig(name, preferredZone string) string {
	return fmt.Sprintf(`
data "coreweave_inference_gateway_parameters" "params" {}

locals {
  preferred_zone  = %q
  available_zones = data.coreweave_inference_gateway_parameters.params.zones
  zone            = local.preferred_zone != "" ? local.preferred_zone : sort(tolist(local.available_zones))[0]
}

resource "coreweave_inference_gateway" "test" {
  name  = %q
  zones = [local.zone]

  auth = {
    coreweave = {}
  }

  routing = {
    header_based = {
      header_name = "X-Model-Name"
    }
  }

  lifecycle {
    precondition {
      condition     = local.preferred_zone == "" || contains(local.available_zones, local.preferred_zone)
      error_message = "INFERENCE_ZONE=\"${local.preferred_zone}\" is not present in gateway parameters; available: ${jsonencode(local.available_zones)}"
    }
  }
}
`, preferredZone, name)
}

func TestInferenceGateway(t *testing.T) {
	t.Run("lifecycle", func(t *testing.T) {
		name := fmt.Sprintf("%sgw-%x", AcceptanceTestPrefix, rand.IntN(100000))
		fullResourceName := "coreweave_inference_gateway.test"
		preferredZone := preferredInferenceZone()

		// Inference acceptance tests run sequentially via resource.Test (not
		// resource.ParallelTest) because the staging environment has limited
		// per-zone capacity and parallel runs cause allocation failures.
		//nolint:forbidigo // sequential per-zone capacity constraint, see comment above
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: gatewayBasicConfig(name, preferredZone),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("name"), knownvalue.StringExact(name)),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("organization_id"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("routing").AtMapKey("body_based").AtMapKey("api_type"), knownvalue.StringExact("API_TYPE_OPENAI")),
					},
				},
				{
					Config: gatewayUpdatedConfig(name, preferredZone),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("routing").AtMapKey("header_based").AtMapKey("header_name"), knownvalue.StringExact("X-Model-Name")),
					},
				},
				{
					// Re-applying the identical config after Update must be a no-op:
					// the preserved server-observed fields (status, updated_at,
					// conditions) must not produce perpetual plan churn.
					Config: gatewayUpdatedConfig(name, preferredZone),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
				},
				{
					ResourceName:      fullResourceName,
					ImportState:       true,
					ImportStateVerify: true,
					// Server-observed fields are intentionally not refreshed into state on
					// Update (see setFromGateway preserveStatusFields); a fresh import Read
					// legitimately observes newer values, so they can't be verified here.
					ImportStateVerifyIgnore: []string{"status", "updated_at", "conditions"},
				},
			},
		})
	})
}

func TestInferenceGatewayParameters(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		resource.ParallelTest(t, resource.TestCase{
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: `data "coreweave_inference_gateway_parameters" "test" {}`,
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(
							"data.coreweave_inference_gateway_parameters.test",
							tfjsonpath.New("zones"),
							knownvalue.NotNull(),
						),
					},
				},
			},
		})
	})
}
