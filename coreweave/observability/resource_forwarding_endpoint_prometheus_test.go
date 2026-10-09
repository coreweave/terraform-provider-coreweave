package observability_test

import (
	"bytes"
	"embed"
	"fmt"
	"testing"

	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/hcl/v2/hclwrite"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

var (
	//go:embed testdata
	prometheusEndpointTestdata embed.FS

	prometheusEndpointResourceName string = resourceName(observability.NewForwardingEndpointPrometheusResource())
)

func init() {
	// The resource is unregistered, but the sweeper stays: an earlier branch may
	// have left Prometheus endpoints in the test org, and the server can grow
	// support before the provider does.
	resource.AddTestSweepers(prometheusEndpointResourceName, &resource.Sweeper{
		Name:         prometheusEndpointResourceName,
		Dependencies: []string{pipelineResourceName},
		F: func(r string) error {
			testutil.SetEnvDefaults()
			return typedEndpointSweeper(typesv1beta1.ForwardingEndpointSpec_Prometheus_case.String())(r)
		},
	})
}

func renderPrometheusEndpointResource(resourceName string, m *model.ForwardingEndpointPrometheus) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{prometheusEndpointResourceName, resourceName})
	resourceBody := resource.Body()

	setCommonEndpointAttributes(resourceBody, m.ForwardingEndpointCore)
	resourceBody.SetAttributeValue("endpoint", cty.StringVal(m.Endpoint.ValueString()))

	if m.TLS != nil {
		resourceBody.SetAttributeValue("tls", cty.ObjectVal(map[string]cty.Value{
			"certificate_authority_data": cty.StringVal(m.TLS.CertificateAuthorityData.ValueString()),
		}))
	}

	if m.Credentials != nil {
		credsObj := make(map[string]cty.Value)
		if m.Credentials.BasicAuth != nil {
			credsObj["basic_auth"] = cty.ObjectVal(map[string]cty.Value{
				"username": cty.StringVal(m.Credentials.BasicAuth.Username.ValueString()),
				"password": cty.StringVal(m.Credentials.BasicAuth.Password.ValueString()),
			})
		}
		resourceBody.SetAttributeValue("credentials", cty.ObjectVal(credsObj))
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(fmt.Sprintf("failed to write HCL: %v", err))
	}
	return buf.String()
}

// TestForwardingEndpointPrometheus covers only what can be exercised without
// the server: the resource is deliberately not registered in provider.go
// because CreateEndpoint answers "endpoint type is not implemented yet:
// prometheus". Acceptance subtests mirroring the HTTPS suite belong here the
// moment both of those change.
func TestForwardingEndpointPrometheus(t *testing.T) {
	t.Parallel()

	t.Run("schema", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		schemaResponse := &fwresource.SchemaResponse{}

		observability.NewForwardingEndpointPrometheusResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResponse)
		assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

		diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
		assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
	})

	t.Run("render", func(t *testing.T) {
		t.Parallel()

		endpoint := &model.ForwardingEndpointPrometheus{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue("test-prometheus-endpoint"),
				DisplayName: types.StringValue("Test Prometheus Endpoint"),
			},
			Endpoint: types.StringValue("https://prometheus.example.com/api/v1/write"),
		}

		expectedHCL, err := prometheusEndpointTestdata.ReadFile("testdata/hcl_endpoint_prometheus_basic.tf")
		require.NoError(t, err)

		hcl := renderPrometheusEndpointResource("test", endpoint)
		assert.Equal(t, string(expectedHCL), hcl)
	})

	t.Run("unregistered", func(t *testing.T) {
		t.Parallel()

		// Guard the decision: if someone registers the resource, this fails and
		// they have to confirm the server implements the type first.
		for _, newResource := range (&provider.CoreweaveProvider{}).Resources(t.Context()) {
			metadataResp := new(fwresource.MetadataResponse)
			newResource().Metadata(t.Context(), fwresource.MetadataRequest{ProviderTypeName: "coreweave"}, metadataResp)
			assert.NotEqual(t, prometheusEndpointResourceName, metadataResp.TypeName,
				"the Prometheus endpoint resource is registered, but CreateEndpoint still rejects the type")
		}
	})
}
