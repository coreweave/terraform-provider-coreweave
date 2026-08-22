package calleridentity_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const callerIdentityDataSourceType = "coreweave_caller_identity"

type callerIdentityRequest struct {
	method        string
	path          string
	authorization string
	userAgent     string
}

func TestCallerIdentityDataSourceRead(t *testing.T) {
	requests := make(chan callerIdentityRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- callerIdentityRequest{
			method:        r.Method,
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			userAgent:     r.Header.Get("User-Agent"),
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"principal":{"uid":"principal-123","orgUid":"org-456"}}`))
	}))
	t.Cleanup(server.Close)

	t.Setenv(provider.CoreweaveApiEndpointEnvVar, server.URL)
	t.Setenv(provider.CoreweaveApiTokenEnvVar, "fake-token")

	protocolServer, err := provider.TestProtoV6ProviderFactories["coreweave"]()
	require.NoError(t, err)

	schemaResponse, err := protocolServer.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	requireNoCallerIdentityDiagnosticErrors(t, schemaResponse.Diagnostics)

	dataSourceSchema, ok := schemaResponse.DataSourceSchemas[callerIdentityDataSourceType]
	require.True(t, ok)
	require.Len(t, dataSourceSchema.Block.Attributes, 2)

	for _, attribute := range dataSourceSchema.Block.Attributes {
		assert.Contains(t, []string{"organization_id", "principal_id"}, attribute.Name)
		assert.True(t, attribute.Computed)
		assert.False(t, attribute.Optional)
		assert.False(t, attribute.Required)
	}

	providerConfig := callerIdentityNullDynamicValue(t, schemaResponse.Provider.ValueType())
	configureResponse, err := protocolServer.ConfigureProvider(t.Context(), &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "1.14.0",
		Config:           providerConfig,
	})
	require.NoError(t, err)
	requireNoCallerIdentityDiagnosticErrors(t, configureResponse.Diagnostics)

	readResponse, err := protocolServer.ReadDataSource(t.Context(), &tfprotov6.ReadDataSourceRequest{
		TypeName: callerIdentityDataSourceType,
		Config:   callerIdentityNullDynamicValue(t, dataSourceSchema.ValueType()),
	})
	require.NoError(t, err)
	requireNoCallerIdentityDiagnosticErrors(t, readResponse.Diagnostics)

	state, err := readResponse.State.Unmarshal(dataSourceSchema.ValueType())
	require.NoError(t, err)

	var attributes map[string]tftypes.Value
	require.NoError(t, state.As(&attributes))
	assertCallerIdentityStringValue(t, attributes["organization_id"], "org-456")
	assertCallerIdentityStringValue(t, attributes["principal_id"], "principal-123")

	request := <-requests
	assert.Equal(t, http.MethodGet, request.method)
	assert.Equal(t, "/v1beta1/auth/whoami", request.path)
	assert.Equal(t, "Bearer fake-token", request.authorization)
	assert.Contains(t, request.userAgent, "Terraform/1.14.0")
	assert.Contains(t, request.userAgent, "terraform-provider-coreweave/test")
}

func TestAccCallerIdentityDataSource(t *testing.T) {
	resource.ParallelTest(t, resource.TestCase{
		PreCheck: func() {
			if os.Getenv(provider.CoreweaveApiTokenEnvVar) == "" {
				t.Fatal("COREWEAVE_API_TOKEN must be set")
			}
		},
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "coreweave_caller_identity" "current" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.coreweave_caller_identity.current", "organization_id"),
					resource.TestCheckResourceAttrSet("data.coreweave_caller_identity.current", "principal_id"),
				),
			},
		},
	})
}

func callerIdentityNullDynamicValue(t *testing.T, valueType tftypes.Type) *tfprotov6.DynamicValue {
	t.Helper()

	objectType, ok := valueType.(tftypes.Object)
	require.True(t, ok)

	attributes := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for name, attributeType := range objectType.AttributeTypes {
		attributes[name] = tftypes.NewValue(attributeType, nil)
	}

	dynamicValue, err := tfprotov6.NewDynamicValue(valueType, tftypes.NewValue(objectType, attributes))
	require.NoError(t, err)

	return &dynamicValue
}

func requireNoCallerIdentityDiagnosticErrors(t *testing.T, diagnostics []*tfprotov6.Diagnostic) {
	t.Helper()

	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("unexpected diagnostic: %s: %s", diagnostic.Summary, diagnostic.Detail)
		}
	}
}

func assertCallerIdentityStringValue(t *testing.T, value tftypes.Value, expected string) {
	t.Helper()

	var actual string
	require.NoError(t, value.As(&actual))
	assert.Equal(t, expected, actual)
}
