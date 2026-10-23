package serviceaccount

import (
	"testing"
	"time"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestResourceSchemas(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		resource frameworkresource.Resource
		required []string
		computed []string
		optional []string
	}{
		"service account": {
			resource: NewServiceAccountResource(),
			required: []string{"id", "organization_id"},
			computed: []string{"name", "uid", "creator", "system_managed", "active", "created_at", "updated_at"},
			optional: []string{"display_name"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			response := &frameworkresource.SchemaResponse{}
			test.resource.Schema(t.Context(), frameworkresource.SchemaRequest{}, response)
			require.False(t, response.Diagnostics.HasError(), response.Diagnostics.Errors())
			diagnostics := response.Schema.ValidateImplementation(t.Context())
			require.False(t, diagnostics.HasError(), diagnostics.Errors())
			for _, attribute := range test.required {
				assert.True(t, response.Schema.Attributes[attribute].IsRequired(), attribute)
			}
			for _, attribute := range test.computed {
				assert.True(t, response.Schema.Attributes[attribute].IsComputed(), attribute)
			}
			for _, attribute := range test.optional {
				assert.True(t, response.Schema.Attributes[attribute].IsOptional(), attribute)
			}
		})
	}

	response := &frameworkresource.SchemaResponse{}
	NewServiceAccountResource().Schema(t.Context(), frameworkresource.SchemaRequest{}, response)
	creator := response.Schema.Attributes["creator"].(resourceschema.StringAttribute)
	assert.Len(t, creator.PlanModifiers, 1)
	systemManaged := response.Schema.Attributes["system_managed"].(resourceschema.BoolAttribute)
	assert.Len(t, systemManaged.PlanModifiers, 1)
	active := response.Schema.Attributes["active"].(resourceschema.BoolAttribute)
	assert.Len(t, active.PlanModifiers, 1)
}

func TestServiceAccountCreateRequest(t *testing.T) {
	t.Parallel()
	model := ServiceAccountResourceModel{
		ID: types.StringValue("ci-agent"), OrganizationID: types.StringValue("a1b2c3"),
		DisplayName: types.StringValue("CI agent"),
	}
	request := model.createRequest()
	assert.Equal(t, "organizations/a1b2c3", request.GetParent())
	assert.Equal(t, "ci-agent", request.GetServiceAccountId())
	require.NotNil(t, request.GetServiceAccount())
	assert.True(t, request.GetServiceAccount().HasDisplayName())
	assert.Equal(t, "CI agent", request.GetServiceAccount().GetDisplayName())
}

func TestServiceAccountCreateRequestPreservesAbsentDisplayName(t *testing.T) {
	t.Parallel()
	model := ServiceAccountResourceModel{ID: types.StringValue("ci-agent"), OrganizationID: types.StringValue("a1b2c3"), DisplayName: types.StringNull()}
	assert.False(t, model.createRequest().GetServiceAccount().HasDisplayName())
}

func TestServiceAccountSetFromProto(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	account := &directoryv1alpha.ServiceAccount{
		Name: "organizations/a1b2c3/serviceAccounts/ci-agent", Uid: proto.String("8d39f431-715c-4c6c-916a-849292f97920"),
		OrgId: proto.String("a1b2c3"), Creator: proto.String("organizations/a1b2c3/users/user-1"),
		DisplayName: proto.String("CI agent"), SystemManaged: proto.Bool(false), Active: proto.Bool(true),
		CreateTime: timestamppb.New(created), UpdateTime: timestamppb.New(created.Add(time.Hour)),
	}
	var model ServiceAccountResourceModel
	require.NoError(t, model.setFromProto(account))
	assert.Equal(t, "ci-agent", model.ID.ValueString())
	assert.Equal(t, "a1b2c3", model.OrganizationID.ValueString())
	assert.Equal(t, account.GetName(), model.Name.ValueString())
	assert.Equal(t, account.GetUid(), model.UID.ValueString())
	assert.Equal(t, "CI agent", model.DisplayName.ValueString())
	assert.True(t, model.Active.ValueBool())
	assert.Equal(t, created.Format(time.RFC3339), model.CreatedAt.ValueString())
	assert.Equal(t, created.Add(time.Hour).Format(time.RFC3339), model.UpdatedAt.ValueString())
}

func TestServiceAccountSetFromProtoPreservesAbsentUpdateTime(t *testing.T) {
	t.Parallel()
	account := &directoryv1alpha.ServiceAccount{
		Name: "organizations/a1b2c3/serviceAccounts/ci-agent",
		Uid:  proto.String("8d39f431-715c-4c6c-916a-849292f97920"),
	}
	var model ServiceAccountResourceModel
	require.NoError(t, model.setFromProto(account))
	assert.True(t, model.UpdatedAt.IsNull())
}

func TestCanonicalNameParsing(t *testing.T) {
	t.Parallel()
	organizationID, serviceAccountID, err := parseServiceAccountName("organizations/a1b2c3/serviceAccounts/ci-agent")
	require.NoError(t, err)
	assert.Equal(t, "a1b2c3", organizationID)
	assert.Equal(t, "ci-agent", serviceAccountID)
	_, _, err = parseServiceAccountName("ci-agent")
	require.Error(t, err)
}
