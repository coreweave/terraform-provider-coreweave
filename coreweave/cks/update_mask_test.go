package cks

import (
	"context"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const testUpgradeVersion = "v1.34"

func TestBuildUpdateRequest(t *testing.T) {
	ctx := context.Background()

	baseModel := func() ClusterResourceModel {
		return ClusterResourceModel{
			Id:      types.StringValue("test-id"),
			VpcId:   types.StringValue("vpc-id"),
			Zone:    types.StringValue("us-east-1a"),
			Name:    types.StringValue("test-cluster"),
			Version: types.StringValue("v1.33"),
			Public:  types.BoolValue(false),
			InternalLBCidrNames: types.ListValueMust(types.StringType, []attr.Value{
				types.StringValue("cidr-1"),
			}),
			NodePortRange: types.ObjectNull(map[string]attr.Type{
				"start": types.Int32Type,
				"end":   types.Int32Type,
			}),
			AuditPolicy:          types.StringNull(),
			AdditionalServerSans: types.SetNull(types.StringType),
		}
	}

	// expectMaskPaths asserts the mask contains exactly the given paths (order-independent).
	expectMaskPaths := func(t *testing.T, got []string, want ...string) {
		t.Helper()
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("expected mask paths %v, got %v", want, got)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("expected mask paths %v, got %v", want, got)
			}
		}
	}

	t.Run("no changes produces empty request body and mask", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.Id != "test-id" {
			t.Errorf("expected id test-id, got %s", req.Id)
		}
		if req.Version != "" {
			t.Errorf("expected empty version, got %s", req.Version)
		}
		if req.Network != nil {
			t.Error("expected nil network")
		}
		if req.Oidc != nil {
			t.Error("expected nil oidc")
		}
		expectMaskPaths(t, req.UpdateMask.Paths)
	})

	t.Run("version change only populates version field", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.Version = types.StringValue(testUpgradeVersion)
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.Version != testUpgradeVersion {
			t.Errorf("expected version v1.34, got %s", req.Version)
		}
		if req.Network != nil {
			t.Error("expected nil network on version-only change")
		}
		if req.AuditPolicy != "" {
			t.Error("expected empty audit_policy on version-only change")
		}
		if req.Oidc != nil {
			t.Error("expected nil oidc on version-only change")
		}
		if req.AuthnWebhook != nil {
			t.Error("expected nil authn_webhook on version-only change")
		}
		if req.AuthzWebhook != nil {
			t.Error("expected nil authz_webhook on version-only change")
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "version")
	})

	t.Run("network change only populates network field", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.InternalLBCidrNames = types.ListValueMust(types.StringType, []attr.Value{
			types.StringValue("cidr-1"),
			types.StringValue("cidr-2"),
		})
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.Network == nil {
			t.Fatal("expected non-nil network")
		}
		if len(req.Network.InternalLbCidrNames) != 2 {
			t.Errorf("expected 2 cidr names, got %d", len(req.Network.InternalLbCidrNames))
		}
		if req.Version != "" {
			t.Error("expected empty version on network-only change")
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "network")
	})

	t.Run("version and network change together", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.Version = types.StringValue(testUpgradeVersion)
		plan.InternalLBCidrNames = types.ListValueMust(types.StringType, []attr.Value{
			types.StringValue("cidr-1"),
			types.StringValue("cidr-2"),
		})
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.Version != testUpgradeVersion {
			t.Errorf("expected version v1.34, got %s", req.Version)
		}
		if req.Network == nil {
			t.Fatal("expected non-nil network")
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "version", "network")
	})

	t.Run("public change only", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.Public = types.BoolValue(true)
		req := buildUpdateRequest(ctx, &plan, &state)
		if !req.Public {
			t.Error("expected public=true")
		}
		if req.Network != nil {
			t.Error("expected nil network")
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "public")
	})

	t.Run("oidc added", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.Oidc = &OidcResourceModel{
			IssuerURL:   types.StringValue("https://issuer.example.com"),
			ClientID:    types.StringValue("client-id"),
			SigningAlgs: types.SetNull(types.StringType),
		}
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.Oidc == nil {
			t.Fatal("expected non-nil oidc")
		}
		if req.Oidc.IssuerUrl != "https://issuer.example.com" {
			t.Errorf("expected issuer url, got %s", req.Oidc.IssuerUrl)
		}
		if req.Network != nil {
			t.Error("expected nil network on oidc-only change")
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "oidc")
	})

	t.Run("oidc removed", func(t *testing.T) {
		state := baseModel()
		state.Oidc = &OidcResourceModel{
			IssuerURL:   types.StringValue("https://issuer.example.com"),
			ClientID:    types.StringValue("client-id"),
			SigningAlgs: types.SetNull(types.StringType),
		}
		plan := baseModel()
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.Oidc != nil {
			t.Error("expected nil oidc when removing oidc")
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "oidc")
	})

	t.Run("authn_webhook added", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.AuthNWebhook = &AuthWebhookResourceModel{
			Server: types.StringValue("https://authn.example.com"),
			CA:     types.StringNull(),
		}
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.AuthnWebhook == nil {
			t.Fatal("expected non-nil authn_webhook")
		}
		if req.AuthnWebhook.Server != "https://authn.example.com" {
			t.Errorf("expected server url, got %s", req.AuthnWebhook.Server)
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "authn_webhook")
	})

	t.Run("authz_webhook added", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.AuthZWebhook = &AuthWebhookResourceModel{
			Server: types.StringValue("https://authz.example.com"),
			CA:     types.StringNull(),
		}
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.AuthzWebhook == nil {
			t.Fatal("expected non-nil authz_webhook")
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "authz_webhook")
	})

	t.Run("audit_policy change", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.AuditPolicy = types.StringValue("new-policy")
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.AuditPolicy != "new-policy" {
			t.Errorf("expected new-policy, got %s", req.AuditPolicy)
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "audit_policy")
	})

	t.Run("additional_server_sans change", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.AdditionalServerSans = types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("san1.example.com"),
		})
		req := buildUpdateRequest(ctx, &plan, &state)
		if len(req.AdditionalServerSans) != 1 || req.AdditionalServerSans[0] != "san1.example.com" {
			t.Errorf("expected [san1.example.com], got %v", req.AdditionalServerSans)
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "additional_server_sans")
	})

	t.Run("multiple fields changed", func(t *testing.T) {
		state := baseModel()
		plan := baseModel()
		plan.Version = types.StringValue(testUpgradeVersion)
		plan.Public = types.BoolValue(true)
		plan.AuditPolicy = types.StringValue("new-policy")
		req := buildUpdateRequest(ctx, &plan, &state)
		if req.Version != testUpgradeVersion {
			t.Errorf("expected version v1.34, got %s", req.Version)
		}
		if !req.Public {
			t.Error("expected public=true")
		}
		if req.AuditPolicy != "new-policy" {
			t.Errorf("expected new-policy, got %s", req.AuditPolicy)
		}
		if req.Network != nil {
			t.Error("expected nil network when only version/public/audit_policy changed")
		}
		expectMaskPaths(t, req.UpdateMask.Paths, "audit_policy", "public", "version")
	})
}
