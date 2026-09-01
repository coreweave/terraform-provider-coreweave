package workloadfederation

import (
	"slices"
	"testing"
	"time"

	controlplanev1beta1 "buf.build/gen/go/coreweave/workload-federation/protocolbuffers/go/coreweave/workload_federation/control_plane/v1beta1"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestOIDCConfigSchema(t *testing.T) {
	t.Parallel()

	response := &frameworkresource.SchemaResponse{}
	NewOIDCConfigResource().Schema(t.Context(), frameworkresource.SchemaRequest{}, response)
	require.False(t, response.Diagnostics.HasError(), response.Diagnostics.Errors())

	diagnostics := response.Schema.ValidateImplementation(t.Context())
	require.False(t, diagnostics.HasError(), diagnostics.Errors())

	attributes := response.Schema.Attributes
	assert.True(t, attributes["id"].IsComputed())
	assert.True(t, attributes["uid"].IsComputed())
	assert.True(t, attributes["name"].IsRequired())
	assert.True(t, attributes["description"].IsOptional())
	assert.True(t, attributes["issuer_url"].IsRequired())
	assert.True(t, attributes["audience"].IsRequired())
	assert.True(t, attributes["active"].IsOptional())
	assert.True(t, attributes["active"].IsComputed())

	organizationID := attributes["organization_id"].(resourceschema.StringAttribute)
	assert.Len(t, organizationID.PlanModifiers, 1)
	createdAt := attributes["created_at"].(resourceschema.StringAttribute)
	assert.Len(t, createdAt.PlanModifiers, 1)
}

func TestOIDCConfigModelToCreateRequest(t *testing.T) {
	t.Parallel()

	model := OIDCConfigResourceModel{
		Name:        types.StringValue("hcp-terraform"),
		Description: types.StringValue("HCP Terraform workload identity"),
		IssuerURL:   types.StringValue("https://app.terraform.io"),
		Audience:    types.StringValue("https://coreweave.com/iam"),
		Active:      types.BoolValue(false),
	}

	request := model.ToCreateRequest()
	assert.Equal(t, "hcp-terraform", request.GetName())
	assert.Equal(t, "HCP Terraform workload identity", request.GetDescription())
	assert.True(t, request.HasDescription())
	assert.Equal(t, "https://app.terraform.io", request.GetIssuerUrl())
	assert.Equal(t, "https://coreweave.com/iam", request.GetAudience())
	assert.True(t, request.HasActive())
	assert.False(t, request.GetActive())
}

func TestOIDCConfigModelSetFromProto(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 25, 10, 0, 0, 123, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	deactivatedAt := updatedAt.Add(time.Hour)
	config := &controlplanev1beta1.OIDCConfig{
		Uid:           "42f18b55-f86c-4480-854c-1aaed6ca3961",
		OrgUid:        "org-123",
		Name:          "github-actions",
		Description:   proto.String("CI workloads"),
		IssuerUrl:     "https://token.actions.githubusercontent.com",
		Audience:      "https://coreweave.com/iam",
		DeactivatedAt: timestamppb.New(deactivatedAt),
		CreatedAt:     timestamppb.New(createdAt),
		UpdatedAt:     timestamppb.New(updatedAt),
	}

	var model OIDCConfigResourceModel
	require.NoError(t, model.SetFromProto(config))

	assert.Equal(t, config.GetUid(), model.ID.ValueString())
	assert.Equal(t, config.GetUid(), model.UID.ValueString())
	assert.Equal(t, config.GetOrgUid(), model.OrganizationID.ValueString())
	assert.Equal(t, config.GetName(), model.Name.ValueString())
	assert.Equal(t, config.GetDescription(), model.Description.ValueString())
	assert.Equal(t, config.GetIssuerUrl(), model.IssuerURL.ValueString())
	assert.Equal(t, config.GetAudience(), model.Audience.ValueString())
	assert.False(t, model.Active.ValueBool())
	assert.Equal(t, deactivatedAt.Format(time.RFC3339), model.DeactivatedAt.ValueString())
	assert.Equal(t, createdAt.Format(time.RFC3339), model.CreatedAt.ValueString())
	assert.Equal(t, updatedAt.Format(time.RFC3339), model.UpdatedAt.ValueString())
}

func TestOIDCConfigModelSetFromProtoActiveWithoutDescription(t *testing.T) {
	t.Parallel()

	var model OIDCConfigResourceModel
	require.NoError(t, model.SetFromProto(&controlplanev1beta1.OIDCConfig{Uid: "config-id"}))

	assert.True(t, model.Description.IsNull())
	assert.True(t, model.Active.ValueBool())
	assert.True(t, model.DeactivatedAt.IsNull())
	assert.True(t, model.CreatedAt.IsNull())
	assert.True(t, model.UpdatedAt.IsNull())
}

func TestOIDCConfigModelSetFromProtoRejectsMissingID(t *testing.T) {
	t.Parallel()

	var model OIDCConfigResourceModel
	require.EqualError(t, model.SetFromProto(&controlplanev1beta1.OIDCConfig{}), "OIDC configuration ID is missing")
	assert.True(t, model.ID.IsNull())
}

func TestOIDCConfigModelToUpdateRequest(t *testing.T) {
	t.Parallel()

	state := OIDCConfigResourceModel{
		ID:          types.StringValue("42f18b55-f86c-4480-854c-1aaed6ca3961"),
		Name:        types.StringValue("old-name"),
		Description: types.StringValue("old description"),
		IssuerURL:   types.StringValue("https://old.example.com"),
		Audience:    types.StringValue("old-audience"),
		Active:      types.BoolValue(true),
	}
	plan := state
	plan.Name = types.StringValue("new-name")
	plan.Description = types.StringNull()
	plan.IssuerURL = types.StringValue("https://new.example.com")
	plan.Audience = types.StringValue("new-audience")
	plan.Active = types.BoolValue(false)

	request := plan.ToUpdateRequest(&state)

	assert.Equal(t, state.ID.ValueString(), request.GetUid())
	require.True(t, slices.Equal(
		[]string{"name", "issuer_url", "audience", "description", "active"},
		request.GetUpdateMask().GetPaths(),
	))
	assert.Equal(t, "new-name", request.GetName())
	assert.Equal(t, "https://new.example.com", request.GetIssuerUrl())
	assert.Equal(t, "new-audience", request.GetAudience())
	assert.False(t, request.HasDescription(), "a nil optional value clears the description")
	assert.True(t, request.HasActive())
	assert.False(t, request.GetActive())
}

func TestOIDCConfigModelToUpdateRequestUnchanged(t *testing.T) {
	t.Parallel()

	state := OIDCConfigResourceModel{
		ID:          types.StringValue("42f18b55-f86c-4480-854c-1aaed6ca3961"),
		Name:        types.StringValue("name"),
		Description: types.StringNull(),
		IssuerURL:   types.StringValue("https://issuer.example.com"),
		Audience:    types.StringValue("audience"),
		Active:      types.BoolValue(true),
	}

	request := state.ToUpdateRequest(&state)
	assert.Empty(t, request.GetUpdateMask().GetPaths())
	assert.Nil(t, request.Name)
	assert.Nil(t, request.IssuerUrl)
	assert.Nil(t, request.Audience)
	assert.Nil(t, request.Description)
	assert.Nil(t, request.Active)
}

func TestOIDCConfigModelToUpdateRequestSkipsUnknownValues(t *testing.T) {
	t.Parallel()

	state := OIDCConfigResourceModel{
		ID:          types.StringValue("42f18b55-f86c-4480-854c-1aaed6ca3961"),
		Name:        types.StringValue("name"),
		Description: types.StringValue("description"),
		IssuerURL:   types.StringValue("https://issuer.example.com"),
		Audience:    types.StringValue("audience"),
		Active:      types.BoolValue(true),
	}
	plan := state
	plan.Name = types.StringUnknown()
	plan.Description = types.StringUnknown()
	plan.IssuerURL = types.StringUnknown()
	plan.Audience = types.StringUnknown()
	plan.Active = types.BoolUnknown()

	// An unknown value would serialize as the zero value, so no path may enter
	// the mask: "active" in particular would deactivate the configuration.
	request := plan.ToUpdateRequest(&state)
	assert.Empty(t, request.GetUpdateMask().GetPaths())
	assert.Nil(t, request.Name)
	assert.Nil(t, request.IssuerUrl)
	assert.Nil(t, request.Audience)
	assert.Nil(t, request.Description)
	assert.Nil(t, request.Active)
}

func TestOIDCConfigModelFillUnknownFromProtoPreservesPlannedValues(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 25, 10, 0, 0, 0, time.UTC)
	// The remote object has drifted out of band on every configurable field.
	config := &controlplanev1beta1.OIDCConfig{
		Uid:           "42f18b55-f86c-4480-854c-1aaed6ca3961",
		OrgUid:        "org-123",
		Name:          "renamed-remotely",
		Description:   proto.String("changed remotely"),
		IssuerUrl:     "https://renamed.example.com",
		Audience:      "renamed-audience",
		DeactivatedAt: timestamppb.New(createdAt.Add(2 * time.Hour)),
		CreatedAt:     timestamppb.New(createdAt),
		UpdatedAt:     timestamppb.New(createdAt.Add(time.Hour)),
	}

	model := OIDCConfigResourceModel{
		ID:             types.StringValue("42f18b55-f86c-4480-854c-1aaed6ca3961"),
		OrganizationID: types.StringValue("org-123"),
		Name:           types.StringValue("configured"),
		Description:    types.StringValue("configured description"),
		IssuerURL:      types.StringValue("https://configured.example.com"),
		Audience:       types.StringValue("configured-audience"),
		Active:         types.BoolValue(true),
		DeactivatedAt:  types.StringUnknown(),
		CreatedAt:      types.StringValue(createdAt.Format(time.RFC3339)),
		UpdatedAt:      types.StringUnknown(),
	}

	require.NoError(t, model.FillUnknownFromProto(config))

	// Known planned values survive: overwriting them would contradict the plan.
	assert.Equal(t, "configured", model.Name.ValueString())
	assert.Equal(t, "configured description", model.Description.ValueString())
	assert.Equal(t, "https://configured.example.com", model.IssuerURL.ValueString())
	assert.Equal(t, "configured-audience", model.Audience.ValueString())
	assert.True(t, model.Active.ValueBool())

	// Only the unknowns are resolved from the response.
	assert.False(t, model.DeactivatedAt.IsUnknown())
	assert.Equal(t, createdAt.Add(2*time.Hour).Format(time.RFC3339), model.DeactivatedAt.ValueString())
	assert.False(t, model.UpdatedAt.IsUnknown())
	assert.Equal(t, createdAt.Add(time.Hour).Format(time.RFC3339), model.UpdatedAt.ValueString())
}

func TestOIDCConfigModelFillUnknownFromProtoRejectsMissingID(t *testing.T) {
	t.Parallel()

	var model OIDCConfigResourceModel
	require.EqualError(t, model.FillUnknownFromProto(&controlplanev1beta1.OIDCConfig{}), "OIDC configuration ID is missing")
	require.EqualError(t, model.FillUnknownFromProto(nil), "OIDC configuration is missing")
}
