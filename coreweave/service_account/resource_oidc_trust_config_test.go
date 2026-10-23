package serviceaccount

import (
	"testing"

	directoryv1alpha "buf.build/gen/go/coreweave/directory/protocolbuffers/go/coreweave/directory/v1alpha"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestOIDCTrustConfigSchema(t *testing.T) {
	t.Parallel()
	response := &frameworkresource.SchemaResponse{}
	NewOIDCTrustConfigResource().Schema(t.Context(), frameworkresource.SchemaRequest{}, response)
	require.False(t, response.Diagnostics.HasError(), response.Diagnostics.Errors())
	diagnostics := response.Schema.ValidateImplementation(t.Context())
	require.False(t, diagnostics.HasError(), diagnostics.Errors())

	for _, attribute := range []string{"id", "organization_id", "service_account_id", "cel", "workload_federation_oidc_config_id"} {
		assert.True(t, response.Schema.Attributes[attribute].IsRequired(), attribute)
	}
	for _, attribute := range []string{"name", "uid", "created_at", "updated_at"} {
		assert.True(t, response.Schema.Attributes[attribute].IsComputed(), attribute)
	}
	assert.True(t, response.Schema.Attributes["description"].IsOptional())
}

func TestOIDCTrustConfigCreateRequest(t *testing.T) {
	t.Parallel()
	model := OIDCTrustConfigResourceModel{
		ID: types.StringValue("main-repo"), OrganizationID: types.StringValue("a1b2c3"), ServiceAccountID: types.StringValue("ci-agent"),
		Description: types.StringValue("main branch"), CEL: types.StringValue(`token.ref == "refs/heads/main"`),
		WorkloadFederationOIDCConfigID: types.StringValue("8d39f431-715c-4c6c-916a-849292f97920"),
	}
	request := model.createRequest()
	assert.Equal(t, "organizations/a1b2c3/serviceAccounts/ci-agent", request.GetParent())
	assert.Equal(t, "main-repo", request.GetServiceAccountOidcTrustConfigId())
	config := request.GetServiceAccountOidcTrustConfig()
	require.NotNil(t, config)
	assert.Equal(t, model.CEL.ValueString(), config.GetCel())
	assert.Equal(t, model.WorkloadFederationOIDCConfigID.ValueString(), config.GetWorkloadFederationOidcConfigUid())
	assert.True(t, config.HasDescription())
}

func TestOIDCTrustConfigSetFromProto(t *testing.T) {
	t.Parallel()
	config := &directoryv1alpha.ServiceAccountOidcTrustConfig{
		Name: "organizations/a1b2c3/serviceAccounts/ci-agent/oidcTrustConfigs/main-repo",
		Uid:  proto.String("e3bb1892-f89e-4258-9495-427ed5ace8c7"), Cel: proto.String("true"),
		WorkloadFederationOidcConfigUid: proto.String("8d39f431-715c-4c6c-916a-849292f97920"),
	}
	var model OIDCTrustConfigResourceModel
	require.NoError(t, model.setFromProto(config))
	assert.Equal(t, "a1b2c3", model.OrganizationID.ValueString())
	assert.Equal(t, "ci-agent", model.ServiceAccountID.ValueString())
	assert.Equal(t, "main-repo", model.ID.ValueString())
	assert.True(t, model.Description.IsNull())
	assert.Equal(t, "true", model.CEL.ValueString())
}

func TestOIDCTrustConfigCanonicalNameParsing(t *testing.T) {
	t.Parallel()
	organizationID, serviceAccountID, configID, err := parseOIDCTrustConfigName("organizations/a1b2c3/serviceAccounts/ci-agent/oidcTrustConfigs/main-repo")
	require.NoError(t, err)
	assert.Equal(t, "a1b2c3", organizationID)
	assert.Equal(t, "ci-agent", serviceAccountID)
	assert.Equal(t, "main-repo", configID)
	_, _, _, err = parseOIDCTrustConfigName("main-repo")
	require.Error(t, err)
}
