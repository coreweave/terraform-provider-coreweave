package inference_test

import (
	"context"
	"fmt"
	"math/rand/v2"
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
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testInstanceID = "gb200-4x"

// --- Unit tests ---

func TestInferenceCapacityClaimResource_Schema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schemaReq := fwresource.SchemaRequest{}
	schemaResp := &fwresource.SchemaResponse{}

	inference.NewInferenceCapacityClaimResource().Schema(ctx, schemaReq, schemaResp)

	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema returned errors: %v", schemaResp.Diagnostics)
	}
}

func TestCapacityTypeRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input    string
		proto    inferencev1.CapacityClaimResources_CapacityType
		expected string
	}{
		{"SERVERLESS", inferencev1.CapacityClaimResources_CAPACITY_TYPE_SERVERLESS, "SERVERLESS"},
		{"CUSTOMER", inferencev1.CapacityClaimResources_CAPACITY_TYPE_CUSTOMER, "CUSTOMER"},
		{"unknown", inferencev1.CapacityClaimResources_CAPACITY_TYPE_UNSPECIFIED, ""},
	}

	for _, tc := range cases {
		got := inference.CapacityTypeFromString(tc.input)
		if got != tc.proto {
			t.Errorf("CapacityTypeFromString(%q): got %v, want %v", tc.input, got, tc.proto)
		}
		str := inference.CapacityTypeToString(tc.proto)
		if str != tc.expected {
			t.Errorf("CapacityTypeToString(%v): got %q, want %q", tc.proto, str, tc.expected)
		}
	}
}

func TestSetFromCapacityClaim_Fields(t *testing.T) {
	t.Parallel()

	now := timestamppb.New(time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC))

	cc := (inferencev1.CapacityClaim_builder{
		Spec: (inferencev1.CapacityClaimSpec_builder{
			Id:             "cc-123",
			Name:           "my-capacity-claim",
			OrganizationId: "org-456",
			Resources: (inferencev1.CapacityClaimResources_builder{
				InstanceId:    testInstanceID,
				InstanceCount: 3,
				CapacityType:  inferencev1.CapacityClaimResources_CAPACITY_TYPE_CUSTOMER,
				Zones:         []string{"US-WEST-04A", "US-EAST-01A"},
			}).Build(),
		}).Build(),
		Status: (inferencev1.CapacityClaimStatus_builder{
			Status:             inferencev1.CapacityClaimStatus_STATUS_UNSPECIFIED,
			CreatedAt:          now,
			UpdatedAt:          now,
			AllocatedInstances: 2,
			PendingInstances:   1,
			Conditions: []*inferencev1.Condition{
				(inferencev1.Condition_builder{
					Type:           "Ready",
					Status:         inferencev1.Condition_STATUS_TRUE,
					LastUpdateTime: now,
					Reason:         "AllAllocated",
					Message:        "All instances allocated",
				}).Build(),
			},
		}).Build(),
	}).Build()

	var m inference.InferenceCapacityClaimResourceModel
	diags := inference.SetFromCapacityClaim(&m, cc)
	if diags.HasError() {
		t.Fatalf("SetFromCapacityClaim returned errors: %v", diags)
	}

	if m.ID.ValueString() != "cc-123" {
		t.Errorf("ID: got %q, want %q", m.ID.ValueString(), "cc-123")
	}
	if m.Name.ValueString() != "my-capacity-claim" {
		t.Errorf("Name: got %q, want %q", m.Name.ValueString(), "my-capacity-claim")
	}
	if m.OrganizationID.ValueString() != "org-456" {
		t.Errorf("OrganizationID: got %q, want %q", m.OrganizationID.ValueString(), "org-456")
	}
	if m.Status.ValueString() != inferencev1.CapacityClaimStatus_STATUS_UNSPECIFIED.String() {
		t.Errorf("Status: got %q, want %q", m.Status.ValueString(), inferencev1.CapacityClaimStatus_STATUS_UNSPECIFIED.String())
	}
	if m.AllocatedInstances.ValueInt64() != 2 {
		t.Errorf("AllocatedInstances: got %d, want 2", m.AllocatedInstances.ValueInt64())
	}
	if m.PendingInstances.ValueInt64() != 1 {
		t.Errorf("PendingInstances: got %d, want 1", m.PendingInstances.ValueInt64())
	}
	if m.CreatedAt.ValueString() != "2026-03-24T12:00:00Z" {
		t.Errorf("CreatedAt: got %q, want %q", m.CreatedAt.ValueString(), "2026-03-24T12:00:00Z")
	}
	if m.UpdatedAt.ValueString() != "2026-03-24T12:00:00Z" {
		t.Errorf("UpdatedAt: got %q, want %q", m.UpdatedAt.ValueString(), "2026-03-24T12:00:00Z")
	}

	// Resources
	if m.Resources.InstanceID.ValueString() != testInstanceID {
		t.Errorf("Resources.InstanceID: got %q, want %q", m.Resources.InstanceID.ValueString(), testInstanceID)
	}
	if m.Resources.InstanceCount.ValueInt64() != 3 {
		t.Errorf("Resources.InstanceCount: got %d, want 3", m.Resources.InstanceCount.ValueInt64())
	}
	if m.Resources.CapacityType.ValueString() != "CUSTOMER" {
		t.Errorf("Resources.CapacityType: got %q, want %q", m.Resources.CapacityType.ValueString(), "CUSTOMER")
	}

	var zones []string
	diags = m.Resources.Zones.ElementsAs(context.Background(), &zones, false)
	if diags.HasError() {
		t.Fatalf("failed to extract zones: %v", diags)
	}
	if len(zones) != 2 {
		t.Fatalf("Zones: expected 2, got %d", len(zones))
	}

	// Conditions
	if m.Conditions.IsNull() || m.Conditions.IsUnknown() {
		t.Fatal("Conditions: expected known, non-null value")
	}
	condElems := m.Conditions.Elements()
	if len(condElems) != 1 {
		t.Fatalf("Conditions: expected 1 element, got %d", len(condElems))
	}
}

func TestToCreateCapacityClaimRequest_Fields(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	zones := types.SetValueMust(types.StringType, []attr.Value{
		types.StringValue("US-WEST-04A"),
		types.StringValue("US-EAST-01A"),
	})

	m := &inference.InferenceCapacityClaimResourceModel{
		Name: types.StringValue("my-claim"),
		Resources: &inference.CapacityClaimResourcesModel{
			InstanceID:    types.StringValue(testInstanceID),
			InstanceCount: types.Int64Value(5),
			CapacityType:  types.StringValue("CUSTOMER"),
			Zones:         zones,
		},
	}

	req, diags := inference.ToCreateCapacityClaimRequest(ctx, m)
	if diags.HasError() {
		t.Fatalf("ToCreateCapacityClaimRequest returned errors: %v", diags)
	}

	if req.GetName() != "my-claim" {
		t.Errorf("Name: got %q, want %q", req.GetName(), "my-claim")
	}
	if req.GetResources().GetInstanceId() != testInstanceID {
		t.Errorf("InstanceID: got %q, want %q", req.GetResources().GetInstanceId(), testInstanceID)
	}
	if req.GetResources().GetInstanceCount() != 5 {
		t.Errorf("InstanceCount: got %d, want 5", req.GetResources().GetInstanceCount())
	}
	if req.GetResources().GetCapacityType() != inferencev1.CapacityClaimResources_CAPACITY_TYPE_CUSTOMER {
		t.Errorf("CapacityType: got %v, want CAPACITY_TYPE_CUSTOMER", req.GetResources().GetCapacityType())
	}
	if len(req.GetResources().GetZones()) != 2 {
		t.Fatalf("Zones: expected 2, got %d", len(req.GetResources().GetZones()))
	}
}

func TestToUpdateCapacityClaimRequest_Fields(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	zones := types.SetValueMust(types.StringType, []attr.Value{
		types.StringValue("US-WEST-04A"),
	})

	m := &inference.InferenceCapacityClaimResourceModel{
		ID:   types.StringValue("cc-789"),
		Name: types.StringValue("my-claim"),
		Resources: &inference.CapacityClaimResourcesModel{
			InstanceID:    types.StringValue(testInstanceID),
			InstanceCount: types.Int64Value(10),
			CapacityType:  types.StringValue("SERVERLESS"),
			Zones:         zones,
		},
	}

	req, diags := inference.ToUpdateCapacityClaimRequest(ctx, m)
	if diags.HasError() {
		t.Fatalf("ToUpdateCapacityClaimRequest returned errors: %v", diags)
	}

	if req.GetId() != "cc-789" {
		t.Errorf("Id: got %q, want %q", req.GetId(), "cc-789")
	}
	if req.GetResources().GetInstanceCount() != 10 {
		t.Errorf("InstanceCount: got %d, want 10", req.GetResources().GetInstanceCount())
	}
	if req.GetResources().GetCapacityType() != inferencev1.CapacityClaimResources_CAPACITY_TYPE_SERVERLESS {
		t.Errorf("CapacityType: got %v, want CAPACITY_TYPE_SERVERLESS", req.GetResources().GetCapacityType())
	}
	if len(req.GetResources().GetZones()) != 1 {
		t.Fatalf("Zones: expected 1, got %d", len(req.GetResources().GetZones()))
	}

	// Update request should NOT carry name (name triggers replace).
	// The proto field is Id + Resources only.
	if req.GetResources().GetInstanceId() != testInstanceID {
		t.Errorf("InstanceID: got %q, want %q", req.GetResources().GetInstanceId(), testInstanceID)
	}
}

// --- Acceptance tests ---

func capacityClaimConfig(name string, instanceCount int) string {
	return fmt.Sprintf(`
data "coreweave_inference_capacity_claim_parameters" "params" {}

locals {
  zones_map = data.coreweave_inference_capacity_claim_parameters.params.zone_instance_types
  zone      = keys(local.zones_map)[0]
  instance  = local.zones_map[local.zone].instance_ids[0]
}

resource "coreweave_inference_capacity_claim" "test" {
  name = %q

  resources = {
    instance_id    = local.instance
    instance_count = %d
    capacity_type  = "CUSTOMER"
    zones          = [local.zone]
  }
}
`, name, instanceCount)
}

func TestAccInferenceCapacityClaim(t *testing.T) {
	name := fmt.Sprintf("%scc-%x", AcceptanceTestPrefix, rand.IntN(100000))
	fullResourceName := "coreweave_inference_capacity_claim.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: capacityClaimConfig(name, 1),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("name"), knownvalue.StringExact(name)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("organization_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("status"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("allocated_instances"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("pending_instances"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("resources").AtMapKey("instance_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("resources").AtMapKey("instance_count"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("resources").AtMapKey("capacity_type"), knownvalue.StringExact("CUSTOMER")),
				},
			},
			{
				Config: capacityClaimConfig(name, 2),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("resources").AtMapKey("instance_count"), knownvalue.Int64Exact(2)),
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

func TestAccInferenceCapacityClaimParameters(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testutil.SetEnvDefaults() },
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "coreweave_inference_capacity_claim_parameters" "test" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						"data.coreweave_inference_capacity_claim_parameters.test",
						tfjsonpath.New("zone_instance_types"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}
