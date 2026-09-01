package serviceaccount

import (
	"testing"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceAccountSchema(t *testing.T) {
	t.Parallel()

	response := &frameworkresource.SchemaResponse{}
	NewServiceAccountResource().Schema(t.Context(), frameworkresource.SchemaRequest{}, response)
	require.False(t, response.Diagnostics.HasError(), response.Diagnostics.Errors())
	diagnostics := response.Schema.ValidateImplementation(t.Context())
	require.False(t, diagnostics.HasError(), diagnostics.Errors())

	attributes := response.Schema.Attributes
	assert.True(t, attributes["id"].IsComputed())
	assert.True(t, attributes["name"].IsComputed())
	assert.True(t, attributes["uid"].IsComputed())
	assert.True(t, attributes["display_name"].IsOptional())
	assert.True(t, attributes["display_name"].IsComputed())
	assert.Len(t, attributes["display_name"].(resourceschema.StringAttribute).PlanModifiers, 1)
	assert.True(t, attributes["active"].IsOptional())
	assert.True(t, attributes["active"].IsComputed())
	assert.Len(t, attributes["active"].(resourceschema.BoolAttribute).PlanModifiers, 1)
}

func TestServiceAccountModelToRequests(t *testing.T) {
	t.Parallel()

	model := ServiceAccountResourceModel{DisplayName: types.StringValue("Terraform automation")}
	createRequest := model.ToCreateRequest()
	require.NotNil(t, createRequest.ServiceAccount)
	require.NotNil(t, createRequest.ServiceAccount.DisplayName)
	assert.Equal(t, "Terraform automation", *createRequest.ServiceAccount.DisplayName)

	state := ServiceAccountResourceModel{
		ID:          types.StringValue("serviceAccounts/sa-old"),
		DisplayName: types.StringValue("Old name"),
	}
	plan := state
	plan.DisplayName = types.StringValue("New name")
	updateRequest := plan.ToDisplayNameUpdateRequest(&state)
	require.NotNil(t, updateRequest)
	assert.Equal(t, "serviceAccounts/sa-old", updateRequest.ServiceAccount.Name)
	assert.Equal(t, "displayName", updateRequest.UpdateMask)
	require.NotNil(t, updateRequest.ServiceAccount.DisplayName)
	assert.Equal(t, "New name", *updateRequest.ServiceAccount.DisplayName)

	plan.DisplayName = types.StringValue("")
	updateRequest = plan.ToDisplayNameUpdateRequest(&state)
	require.NotNil(t, updateRequest)
	require.NotNil(t, updateRequest.ServiceAccount.DisplayName)
	assert.Empty(t, *updateRequest.ServiceAccount.DisplayName, "an explicit empty string clears display_name")
}

func TestServiceAccountModelTreatsAbsentBooleanFieldsAsFalse(t *testing.T) {
	t.Parallel()

	var model ServiceAccountResourceModel
	require.NoError(t, model.SetFromAPI(&coreweave.ServiceAccount{
		Name: "serviceAccounts/sa-abc",
		UID:  ptr("sa-abc"),
	}))
	assert.False(t, model.SystemManaged.ValueBool())
	assert.False(t, model.Active.ValueBool())
}

func TestServiceAccountModelSetFromAPI(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	account := &coreweave.ServiceAccount{
		Name:           "serviceAccounts/sa-abc",
		UID:            ptr("sa-abc"),
		OrganizationID: ptr("org-123"),
		Creator:        ptr("users/u-123"),
		DisplayName:    ptr("Terraform automation"),
		SystemManaged:  ptr(false),
		Active:         ptr(false),
		CreateTime:     &createdAt,
		UpdateTime:     &updatedAt,
	}

	var model ServiceAccountResourceModel
	require.NoError(t, model.SetFromAPI(account))
	assert.Equal(t, account.Name, model.ID.ValueString())
	assert.Equal(t, account.Name, model.Name.ValueString())
	assert.Equal(t, *account.UID, model.UID.ValueString())
	assert.Equal(t, *account.OrganizationID, model.OrganizationID.ValueString())
	assert.Equal(t, *account.Creator, model.Creator.ValueString())
	assert.Equal(t, *account.DisplayName, model.DisplayName.ValueString())
	assert.False(t, model.SystemManaged.ValueBool())
	assert.False(t, model.Active.ValueBool())
	assert.Equal(t, createdAt.Format(time.RFC3339), model.CreatedAt.ValueString())
	assert.Equal(t, updatedAt.Format(time.RFC3339), model.UpdatedAt.ValueString())
}

func TestServiceAccountModelRejectsInvalidOrSystemManagedAPIValues(t *testing.T) {
	t.Parallel()

	var model ServiceAccountResourceModel
	assert.EqualError(t, model.SetFromAPI(&coreweave.ServiceAccount{}), "service account resource name is missing")
	assert.EqualError(t, model.SetFromAPI(&coreweave.ServiceAccount{Name: "serviceAccounts/sa-abc"}), "service account UID is missing")
	assert.EqualError(t, model.SetFromAPI(&coreweave.ServiceAccount{
		Name:          "serviceAccounts/sa-abc",
		UID:           ptr("sa-abc"),
		SystemManaged: ptr(true),
		Active:        ptr(true),
	}), "system-managed service accounts cannot be managed by Terraform")
}

func ptr[T any](value T) *T {
	return &value
}
