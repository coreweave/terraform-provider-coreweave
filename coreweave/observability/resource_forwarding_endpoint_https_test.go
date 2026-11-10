package observability_test

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
	"testing"
	"time"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/hashicorp/hcl/v2/hclwrite"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	//go:embed testdata
	httpsEndpointTestdata embed.FS

	httpsEndpointResourceName string = resourceName(observability.NewForwardingEndpointHTTPSResource())
)

func init() {
	resource.AddTestSweepers(httpsEndpointResourceName, &resource.Sweeper{
		Name:         httpsEndpointResourceName,
		Dependencies: []string{pipelineResourceName},
		F: func(r string) error {
			setAccEnvDefaults()
			return typedEndpointSweeper(typesv1beta1.ForwardingEndpointSpec_Https_case.String())(r)
		},
	})
}

func renderHTTPSEndpointResource(resourceName string, m *model.ForwardingEndpointHTTPS) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{httpsEndpointResourceName, resourceName})
	resourceBody := resource.Body()

	setCommonEndpointAttributes(resourceBody, m.ForwardingEndpointCore)
	resourceBody.SetAttributeValue("endpoint", cty.StringVal(m.Endpoint.ValueString()))

	if !m.Compression.IsNull() {
		resourceBody.SetAttributeValue("compression", cty.StringVal(m.Compression.ValueString()))
	}

	if !m.CredentialsVersion.IsNull() {
		resourceBody.SetAttributeValue("credentials_version", cty.NumberIntVal(m.CredentialsVersion.ValueInt64()))
	}

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
		if m.Credentials.AuthHeaders != nil {
			headersMap := make(map[string]cty.Value)
			for key, value := range m.Credentials.AuthHeaders.Headers {
				headersMap[key] = cty.StringVal(value)
			}
			credsObj["auth_headers"] = cty.ObjectVal(map[string]cty.Value{
				"headers": cty.MapVal(headersMap),
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

type httpsEndpointTestStep struct {
	TestName         string
	ResourceName     string
	Model            *model.ForwardingEndpointHTTPS
	PreConfig        func()
	ConfigPlanChecks resource.ConfigPlanChecks
	ExtraChecks      []statecheck.StateCheck
	Options          []testStepOption
	// ExpectError asserts the apply is refused. State checks are skipped when
	// it is set, since nothing was applied to check.
	ExpectError *regexp.Regexp
	// CredentialsConfigured overrides the default, which infers it from the
	// config block. An imported endpoint has credentials the config never had.
	CredentialsConfigured *bool
}

func createHTTPSEndpointTestStep(t *testing.T, opts httpsEndpointTestStep) resource.TestStep {
	t.Helper()

	fullResourceName := fmt.Sprintf("%s.%s", httpsEndpointResourceName, opts.ResourceName)

	stateChecks := []statecheck.StateCheck{
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("slug"), knownvalue.StringExact(opts.Model.Slug.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("display_name"), knownvalue.StringExact(opts.Model.DisplayName.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("endpoint"), knownvalue.StringExact(opts.Model.Endpoint.ValueString())),

		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("updated_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String())),
	}

	// compression round-trips exactly: null stays null (never configured), a set
	// value reads back as itself. This is the assertion that would catch an
	// Optional+Computed regression.
	if opts.Model.Compression.IsNull() {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("compression"), knownvalue.Null()))
	} else {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("compression"), knownvalue.StringExact(opts.Model.Compression.ValueString())))
	}

	// Credentials are write-only: the server never returns them, so this
	// computed pair is the only observable record that any were set.
	credentialsConfigured := opts.Model.Credentials != nil
	if opts.CredentialsConfigured != nil {
		credentialsConfigured = *opts.CredentialsConfigured
	}
	stateChecks = append(stateChecks,
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials_configured"), knownvalue.Bool(credentialsConfigured)))
	if opts.Model.Credentials != nil {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials_updated_at"), knownvalue.NotNull()))
	}

	if opts.Model.TLS != nil {
		stateChecks = append(stateChecks,
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tls"), knownvalue.NotNull()),
			statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("tls").AtMapKey("certificate_authority_data"), knownvalue.StringExact(opts.Model.TLS.CertificateAuthorityData.ValueString())),
		)
	}

	stateChecks = append(stateChecks, opts.ExtraChecks...)

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning %s test: %s", httpsEndpointResourceName, opts.TestName)
			if opts.PreConfig != nil {
				opts.PreConfig()
			}
		},
		Config:           renderHTTPSEndpointResource(opts.ResourceName, opts.Model),
		ConfigPlanChecks: opts.ConfigPlanChecks,
		ExpectError:      opts.ExpectError,
	}

	if opts.ExpectError == nil {
		testStep.ConfigStateChecks = stateChecks
	}

	for _, option := range opts.Options {
		option(&testStep)
	}

	return testStep
}

// TestForwardingEndpointHTTPS is the umbrella for every HTTPS forwarding
// endpoint test, so `go test -run TestForwardingEndpointHTTPS
// ./coreweave/observability` reaches all of them. Unit subtests run anywhere;
// the rest need TF_ACC=1 plus COREWEAVE_API_ENDPOINT and COREWEAVE_API_TOKEN.
//
// HTTPS endpoints are database-only server-side — no connector is provisioned
// and CheckEndpoint performs no connectivity test — so these are safe to run
// against any org, including one with a production cluster footprint.
//
// Only this function and the pure-unit subtests call t.Parallel().
// resource.ParallelTest calls it internally, so a subtest that calls both
// panics with "t.Parallel called multiple times".
func TestForwardingEndpointHTTPS(t *testing.T) {
	t.Parallel()

	t.Run("schema", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		schemaResponse := &fwresource.SchemaResponse{}

		observability.NewForwardingEndpointHTTPSResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResponse)
		assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

		diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
		assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
	})

	t.Run("render", func(t *testing.T) {
		t.Parallel()

		t.Run("basic endpoint", func(t *testing.T) {
			t.Parallel()

			endpoint := &model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue("test-https-endpoint"),
					DisplayName: types.StringValue("Test HTTPS Endpoint"),
				},
				Endpoint: types.StringValue("https://example.com/telemetry"),
			}

			expectedHCL, err := httpsEndpointTestdata.ReadFile("testdata/hcl_endpoint_https_basic.tf")
			require.NoError(t, err)

			hcl := renderHTTPSEndpointResource("test", endpoint)
			assert.Equal(t, string(expectedHCL), hcl)
		})

		t.Run("with TLS", func(t *testing.T) {
			t.Parallel()

			endpoint := &model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					Slug:        types.StringValue("test-https-tls"),
					DisplayName: types.StringValue("Test HTTPS with TLS"),
				},
				Endpoint: types.StringValue("https://example.com/telemetry"),
				TLS: &model.TLSConfig{
					CertificateAuthorityData: types.StringValue("LS0tLS1CRUdJTi=="),
				},
			}

			expectedHCL, err := httpsEndpointTestdata.ReadFile("testdata/hcl_endpoint_https_with_tls.tf")
			require.NoError(t, err)

			hcl := renderHTTPSEndpointResource("test_tls", endpoint)
			assert.Equal(t, string(expectedHCL), hcl)
		})
	})

	// round_trip pins the Set/ToMsg mapping for the fields the schema exposes,
	// including the null-vs-empty choices that decide whether a plan is clean.
	t.Run("round_trip", func(t *testing.T) {
		t.Parallel()

		created := timestamppb.New(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
		updated := timestamppb.New(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))

		endpointMsg := func(mutate func(*typesv1beta1.ForwardingEndpointSpec, *typesv1beta1.ForwardingEndpointStatus)) *typesv1beta1.ForwardingEndpoint {
			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test HTTPS Endpoint"}
			spec.SetHttps(&typesv1beta1.HTTPSConfig{Endpoint: "https://example.com/telemetry"})

			status := &typesv1beta1.ForwardingEndpointStatus{
				CreatedAt: created,
				UpdatedAt: updated,
				State:     typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED,
			}

			if mutate != nil {
				mutate(spec, status)
			}

			return &typesv1beta1.ForwardingEndpoint{
				Ref:    &typesv1beta1.ForwardingEndpointRef{Slug: "test-https-endpoint"},
				Spec:   spec,
				Status: status,
			}
		}

		t.Run("spec and status populate the model", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(endpointMsg(nil)).HasError())

			assert.Equal(t, "test-https-endpoint", endpoint.Slug.ValueString())
			assert.Equal(t, "Test HTTPS Endpoint", endpoint.DisplayName.ValueString())
			assert.Equal(t, "https://example.com/telemetry", endpoint.Endpoint.ValueString())
			assert.Equal(t, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(), endpoint.State.ValueString())
			assert.Equal(t, "2026-10-01T12:00:00Z", endpoint.CreatedAt.ValueString())
			assert.Equal(t, "2026-10-02T12:00:00Z", endpoint.UpdatedAt.ValueString())
		})

		t.Run("an absent state_message reads as null", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(endpointMsg(nil)).HasError())

			assert.True(t, endpoint.StateMessage.IsNull())
		})

		t.Run("an empty state_message is distinct from an absent one", func(t *testing.T) {
			t.Parallel()

			msg := endpointMsg(func(_ *typesv1beta1.ForwardingEndpointSpec, status *typesv1beta1.ForwardingEndpointStatus) {
				status.StateMessage = proto.String("")
			})

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(msg).HasError())

			assert.False(t, endpoint.StateMessage.IsNull())
			assert.Equal(t, "", endpoint.StateMessage.ValueString())
		})

		// UNSPECIFIED is "never configured", which is Terraform's null. NONE is
		// explicitly uncompressed and has to survive the round trip as a value.
		t.Run("unspecified compression reads as null", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(endpointMsg(nil)).HasError())

			assert.True(t, endpoint.Compression.IsNull())

			msg, diags := endpoint.ToMsg()
			require.False(t, diags.HasError())
			assert.Equal(t, typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_UNSPECIFIED, msg.GetSpec().GetHttps().GetCompression())
		})

		t.Run("explicit no compression round-trips as a value", func(t *testing.T) {
			t.Parallel()

			msg := endpointMsg(func(spec *typesv1beta1.ForwardingEndpointSpec, _ *typesv1beta1.ForwardingEndpointStatus) {
				spec.SetHttps(&typesv1beta1.HTTPSConfig{
					Endpoint:    "https://example.com/telemetry",
					Compression: typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_NONE,
				})
			})

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(msg).HasError())
			assert.Equal(t, typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_NONE.String(), endpoint.Compression.ValueString())

			out, diags := endpoint.ToMsg()
			require.False(t, diags.HasError())
			assert.Equal(t, typesv1beta1.HTTPSConfig_COMPRESSION_TYPE_NONE, out.GetSpec().GetHttps().GetCompression())
		})

		// The server never returns credentials, so this pair is the only
		// evidence any were set, and credentials_version must stay untouched
		// because it belongs to the configuration.
		t.Run("credential bookkeeping comes from status alone", func(t *testing.T) {
			t.Parallel()

			msg := endpointMsg(func(_ *typesv1beta1.ForwardingEndpointSpec, status *typesv1beta1.ForwardingEndpointStatus) {
				status.CredentialsConfigured = true
				status.CredentialsUpdatedAt = updated
			})

			endpoint := model.ForwardingEndpointHTTPS{
				ForwardingEndpointCore: model.ForwardingEndpointCore{
					CredentialsVersion: types.Int64Value(7),
				},
			}
			require.False(t, endpoint.Set(msg).HasError())

			assert.True(t, endpoint.CredentialsConfigured.ValueBool())
			assert.Equal(t, "2026-10-02T12:00:00Z", endpoint.CredentialsUpdatedAt.ValueString())
			assert.Equal(t, int64(7), endpoint.CredentialsVersion.ValueInt64())
			assert.Nil(t, endpoint.Credentials, "the API never returns credentials")
		})

		t.Run("an endpoint of the wrong config type is refused", func(t *testing.T) {
			t.Parallel()

			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test S3 Endpoint"}
			spec.SetS3(&typesv1beta1.S3Config{Uri: "s3://my-telemetry-bucket", Region: "us-east-1"})

			var endpoint model.ForwardingEndpointHTTPS
			diags := endpoint.Set(&typesv1beta1.ForwardingEndpoint{
				Ref:    &typesv1beta1.ForwardingEndpointRef{Slug: "test-s3-endpoint"},
				Spec:   spec,
				Status: &typesv1beta1.ForwardingEndpointStatus{},
			})

			assert.True(t, diags.HasError())
		})

		t.Run("tls survives the round trip", func(t *testing.T) {
			t.Parallel()

			msg := endpointMsg(func(spec *typesv1beta1.ForwardingEndpointSpec, _ *typesv1beta1.ForwardingEndpointStatus) {
				spec.SetHttps(&typesv1beta1.HTTPSConfig{
					Endpoint: "https://example.com/telemetry",
					Tls:      &typesv1beta1.TLSConfig{CertificateAuthorityData: "LS0tLS1CRUdJTi=="},
				})
			})

			var endpoint model.ForwardingEndpointHTTPS
			require.False(t, endpoint.Set(msg).HasError())
			require.NotNil(t, endpoint.TLS)
			assert.Equal(t, "LS0tLS1CRUdJTi==", endpoint.TLS.CertificateAuthorityData.ValueString())

			out, diags := endpoint.ToMsg()
			require.False(t, diags.HasError())
			assert.Equal(t, "LS0tLS1CRUdJTi==", out.GetSpec().GetHttps().GetTls().GetCertificateAuthorityData())
		})

		t.Run("a nil model converts to a nil message", func(t *testing.T) {
			t.Parallel()

			var endpoint *model.ForwardingEndpointHTTPS
			msg, diags := endpoint.ToMsg()

			assert.False(t, diags.HasError())
			assert.Nil(t, msg)
		})
	})
}
