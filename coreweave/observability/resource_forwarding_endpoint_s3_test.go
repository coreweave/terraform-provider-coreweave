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
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	//go:embed testdata
	s3EndpointTestdata embed.FS

	s3EndpointResourceName string = resourceName(observability.NewForwardingEndpointS3Resource())
)

func init() {
	resource.AddTestSweepers(s3EndpointResourceName, &resource.Sweeper{
		Name:         s3EndpointResourceName,
		Dependencies: []string{pipelineResourceName},
		F: func(r string) error {
			setAccEnvDefaults()
			return typedEndpointSweeper(typesv1beta1.ForwardingEndpointSpec_S3_case.String())(r)
		},
	})
}

func renderS3EndpointResource(resourceName string, m *model.ForwardingEndpointS3) string {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resource := body.AppendNewBlock("resource", []string{s3EndpointResourceName, resourceName})
	resourceBody := resource.Body()

	setCommonEndpointAttributes(resourceBody, m.ForwardingEndpointCore)
	resourceBody.SetAttributeValue("uri", cty.StringVal(m.URI.ValueString()))
	resourceBody.SetAttributeValue("region", cty.StringVal(m.Region.ValueString()))

	if !m.CredentialsVersion.IsNull() {
		resourceBody.SetAttributeValue("credentials_version", cty.NumberIntVal(m.CredentialsVersion.ValueInt64()))
	}

	if m.Credentials != nil {
		credsObj := map[string]cty.Value{
			"access_key_id":     cty.StringVal(m.Credentials.AccessKeyID.ValueString()),
			"secret_access_key": cty.StringVal(m.Credentials.SecretAccessKey.ValueString()),
		}
		if !m.Credentials.SessionToken.IsNull() {
			credsObj["session_token"] = cty.StringVal(m.Credentials.SessionToken.ValueString())
		}
		resourceBody.SetAttributeValue("credentials", cty.ObjectVal(credsObj))
	}

	var buf bytes.Buffer
	if _, err := file.WriteTo(&buf); err != nil {
		panic(fmt.Sprintf("failed to write HCL: %v", err))
	}
	return buf.String()
}

type s3EndpointTestStep struct {
	TestName         string
	ResourceName     string
	Model            *model.ForwardingEndpointS3
	PreConfig        func()
	ConfigPlanChecks resource.ConfigPlanChecks
	ExtraChecks      []statecheck.StateCheck
	Options          []testStepOption
	// ExpectError asserts the apply is refused. State checks are skipped when
	// it is set, since nothing was applied to check.
	ExpectError *regexp.Regexp
}

func createS3EndpointTestStep(t *testing.T, opts s3EndpointTestStep) resource.TestStep {
	t.Helper()

	fullResourceName := fmt.Sprintf("%s.%s", s3EndpointResourceName, opts.ResourceName)

	stateChecks := make([]statecheck.StateCheck, 0, 9+len(opts.ExtraChecks))
	stateChecks = append(stateChecks,
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("slug"), knownvalue.StringExact(opts.Model.Slug.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("display_name"), knownvalue.StringExact(opts.Model.DisplayName.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("uri"), knownvalue.StringExact(opts.Model.URI.ValueString())),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("region"), knownvalue.StringExact(opts.Model.Region.ValueString())),

		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("created_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("updated_at"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String())),

		// S3 credentials are mandatory, so this is always true for a resource
		// that applied successfully.
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials_configured"), knownvalue.Bool(true)),
		statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("credentials_updated_at"), knownvalue.NotNull()),
	)

	stateChecks = append(stateChecks, opts.ExtraChecks...)

	testStep := resource.TestStep{
		PreConfig: func() {
			t.Logf("Beginning %s test: %s", s3EndpointResourceName, opts.TestName)
			if opts.PreConfig != nil {
				opts.PreConfig()
			}
		},
		Config:           renderS3EndpointResource(opts.ResourceName, opts.Model),
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

func fixtureS3Credentials() *model.S3Credentials {
	return &model.S3Credentials{
		AccessKeyID:     types.StringValue("AKIAIOSFODNN7EXAMPLE"),
		SecretAccessKey: types.StringValue("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
	}
}

// TestForwardingEndpointS3 is the umbrella for every S3 forwarding endpoint
// test. Like HTTPS, S3 endpoints are database-only records server-side, so
// these are safe against any org.
//
// Only this function and the pure-unit subtests call t.Parallel();
// resource.ParallelTest calls it internally.
func TestForwardingEndpointS3(t *testing.T) {
	t.Parallel()

	t.Run("schema", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		schemaResponse := &fwresource.SchemaResponse{}

		observability.NewForwardingEndpointS3Resource().Schema(ctx, fwresource.SchemaRequest{}, schemaResponse)
		assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

		diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
		assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
	})

	t.Run("render", func(t *testing.T) {
		t.Parallel()

		endpoint := &model.ForwardingEndpointS3{
			ForwardingEndpointCore: model.ForwardingEndpointCore{
				Slug:        types.StringValue("test-s3-endpoint"),
				DisplayName: types.StringValue("Test S3 Endpoint"),
			},
			URI:    types.StringValue("s3://my-telemetry-bucket"),
			Region: types.StringValue("us-east-1"),
		}

		expectedHCL, err := s3EndpointTestdata.ReadFile("testdata/hcl_endpoint_s3_basic.tf")
		require.NoError(t, err)

		hcl := renderS3EndpointResource("test", endpoint)
		assert.Equal(t, string(expectedHCL), hcl)
	})

	// round_trip pins the Set/ToMsg mapping. The shared status fields are
	// covered by the HTTPS suite; this asserts the S3 config and the guards
	// that keep the two endpoint types from reading each other's records.
	t.Run("round_trip", func(t *testing.T) {
		t.Parallel()

		created := timestamppb.New(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

		s3Msg := func() *typesv1beta1.ForwardingEndpoint {
			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test S3 Endpoint"}
			spec.SetS3(&typesv1beta1.S3Config{
				Uri:    "s3://my-telemetry-bucket/prefix",
				Region: "us-east-1",
			})

			return &typesv1beta1.ForwardingEndpoint{
				Ref:  &typesv1beta1.ForwardingEndpointRef{Slug: "test-s3-endpoint"},
				Spec: spec,
				Status: &typesv1beta1.ForwardingEndpointStatus{
					CreatedAt: created,
					State:     typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED,
				},
			}
		}

		t.Run("spec and status populate the model", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointS3
			require.False(t, endpoint.Set(s3Msg()).HasError())

			assert.Equal(t, "test-s3-endpoint", endpoint.Slug.ValueString())
			assert.Equal(t, "Test S3 Endpoint", endpoint.DisplayName.ValueString())
			assert.Equal(t, "s3://my-telemetry-bucket/prefix", endpoint.URI.ValueString())
			assert.Equal(t, "us-east-1", endpoint.Region.ValueString())
			assert.Equal(t, "2026-10-01T12:00:00Z", endpoint.CreatedAt.ValueString())
			assert.True(t, endpoint.UpdatedAt.IsNull(), "an endpoint never updated has no updated_at")
		})

		t.Run("uri and region survive the round trip", func(t *testing.T) {
			t.Parallel()

			var endpoint model.ForwardingEndpointS3
			require.False(t, endpoint.Set(s3Msg()).HasError())

			out, diags := endpoint.ToMsg()
			require.False(t, diags.HasError())

			s3 := out.GetSpec().GetS3()
			assert.Equal(t, "s3://my-telemetry-bucket/prefix", s3.GetUri())
			assert.Equal(t, "us-east-1", s3.GetRegion())
		})

		t.Run("an endpoint of the wrong config type is refused", func(t *testing.T) {
			t.Parallel()

			spec := &typesv1beta1.ForwardingEndpointSpec{DisplayName: "Test HTTPS Endpoint"}
			spec.SetHttps(&typesv1beta1.HTTPSConfig{Endpoint: "https://example.com/telemetry"})

			var endpoint model.ForwardingEndpointS3
			diags := endpoint.Set(&typesv1beta1.ForwardingEndpoint{
				Ref:    &typesv1beta1.ForwardingEndpointRef{Slug: "test-https-endpoint"},
				Spec:   spec,
				Status: &typesv1beta1.ForwardingEndpointStatus{},
			})

			assert.True(t, diags.HasError())
		})

		// The session token is optional, and an empty one has to read back as
		// null so that omitting it produces no diff.
		t.Run("an empty session token reads as null", func(t *testing.T) {
			t.Parallel()

			var credentials model.S3Credentials
			credentials.Set(&typesv1beta1.S3Credentials{
				AccessKeyId:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "secret",
			})

			assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", credentials.AccessKeyID.ValueString())
			assert.True(t, credentials.SessionToken.IsNull())
		})

		t.Run("a set session token is preserved", func(t *testing.T) {
			t.Parallel()

			var credentials model.S3Credentials
			credentials.Set(&typesv1beta1.S3Credentials{
				AccessKeyId:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "secret",
				SessionToken:    "token",
			})

			assert.Equal(t, "token", credentials.SessionToken.ValueString())
		})

		t.Run("a nil model converts to a nil message", func(t *testing.T) {
			t.Parallel()

			var endpoint *model.ForwardingEndpointS3
			msg, diags := endpoint.ToMsg()

			assert.False(t, diags.HasError())
			assert.Nil(t, msg)
		})
	})
}
