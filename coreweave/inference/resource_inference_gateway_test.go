package inference_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/inference"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// --- Unit tests ---

func TestInferenceGatewayResource_Schema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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
			Status: inferencev1.GatewayStatus_STATUS_UNSPECIFIED,
		},
	}

	m := &inference.InferenceGatewayResourceModel{}
	diags := inference.SetFromGateway(m, gw)
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
			Status: inferencev1.GatewayStatus_STATUS_UNSPECIFIED,
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

	diags := inference.SetFromGateway(m, gw)
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
			Status: inferencev1.GatewayStatus_STATUS_UNSPECIFIED,
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

	diags := inference.SetFromGateway(m, gw)
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
			Status: inferencev1.GatewayStatus_STATUS_UNSPECIFIED,
		},
	}

	m := &inference.InferenceGatewayResourceModel{}
	diags := inference.SetFromGateway(m, gw)
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
			Status: inferencev1.GatewayStatus_STATUS_UNSPECIFIED,
		},
	}

	m := &inference.InferenceGatewayResourceModel{}
	diags := inference.SetFromGateway(m, gw)
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
			Status: inferencev1.GatewayStatus_STATUS_UNSPECIFIED,
		},
	}

	m := &inference.InferenceGatewayResourceModel{}
	diags := inference.SetFromGateway(m, gw)
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

// --- Acceptance tests ---

func gatewayBasicConfig(name string) string {
	return fmt.Sprintf(`
data "coreweave_inference_gateway_parameters" "params" {}

resource "coreweave_inference_gateway" "test" {
  name  = %q
  zones = [data.coreweave_inference_gateway_parameters.params.zones[0]]

  auth = {
    core_weave = {}
  }

  routing = {
    body_based = {
      api_type = "API_TYPE_OPENAI"
    }
  }
}
`, name)
}

func gatewayUpdatedConfig(name string) string {
	return fmt.Sprintf(`
data "coreweave_inference_gateway_parameters" "params" {}

resource "coreweave_inference_gateway" "test" {
  name  = %q
  zones = [data.coreweave_inference_gateway_parameters.params.zones[0]]

  auth = {
    core_weave = {}
  }

  routing = {
    header_based = {
      header_name = "X-Model-Name"
    }
  }
}
`, name)
}

func TestAccInferenceGateway(t *testing.T) {
	name := fmt.Sprintf("%sgw-%x", AcceptanceTestPrefix, rand.IntN(100000))
	fullResourceName := "coreweave_inference_gateway.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: gatewayBasicConfig(name),
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
				Config: gatewayUpdatedConfig(name),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("routing").AtMapKey("header_based").AtMapKey("header_name"), knownvalue.StringExact("X-Model-Name")),
				},
			},
			{
				ResourceName:      fullResourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccInferenceGatewayParameters(t *testing.T) {
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
}
