package serviceaccount

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
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
	assert.False(t, model.DisplayName.IsNull())
	assert.Equal(t, "", model.DisplayName.ValueString())
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

func TestServiceAccountUpdateRetainsSuccessfulDisplayNameWhenDeactivationFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/coreweave.directory.v1alpha.InternalDirectoryService/UpdateServiceAccount":
			_, _ = w.Write([]byte(`{"name":"serviceAccounts/sa-abc","uid":"sa-abc","displayName":"New name","systemManaged":false,"active":true}`))
		case "/coreweave.directory.v1alpha.InternalDirectoryService/DeactivateServiceAccount":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"permission_denied","message":"deactivation unavailable"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := coreweave.NewClient(
		server.URL,
		"https://objects.example.test",
		time.Second,
		auth.NewStaticTokenSource("test-token"),
		"test-user-agent",
	)
	require.NoError(t, err)
	resourceUnderTest := &ServiceAccountResource{client: client}

	var schemaResponse frameworkresource.SchemaResponse
	resourceUnderTest.Schema(t.Context(), frameworkresource.SchemaRequest{}, &schemaResponse)
	require.False(t, schemaResponse.Diagnostics.HasError(), schemaResponse.Diagnostics.Errors())

	stateModel := ServiceAccountResourceModel{
		ID:             types.StringValue("serviceAccounts/sa-abc"),
		Name:           types.StringValue("serviceAccounts/sa-abc"),
		UID:            types.StringValue("sa-abc"),
		OrganizationID: types.StringNull(),
		Creator:        types.StringNull(),
		DisplayName:    types.StringValue("Old name"),
		SystemManaged:  types.BoolValue(false),
		Active:         types.BoolValue(true),
		CreatedAt:      types.StringNull(),
		UpdatedAt:      types.StringNull(),
	}
	planModel := stateModel
	planModel.DisplayName = types.StringValue("New name")
	planModel.Active = types.BoolValue(false)

	state := tfsdk.State{Schema: schemaResponse.Schema}
	require.False(t, state.Set(t.Context(), &stateModel).HasError())
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	require.False(t, plan.Set(t.Context(), &planModel).HasError())
	response := &frameworkresource.UpdateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}

	resourceUnderTest.Update(t.Context(), frameworkresource.UpdateRequest{Plan: plan, State: state}, response)

	require.True(t, response.Diagnostics.HasError(), "deactivation failure must be reported")
	var retained ServiceAccountResourceModel
	require.False(t, response.State.Get(t.Context(), &retained).HasError())
	assert.Equal(t, "New name", retained.DisplayName.ValueString())
	assert.True(t, retained.Active.ValueBool(), "state must retain the activation value returned by the successful first mutation")
}

func ptr[T any](value T) *T {
	return &value
}
