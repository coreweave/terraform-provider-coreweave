package observability_test

import (
	"context"
	"embed"
	"fmt"
	"log"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	//go:embed testdata
	testdata embed.FS

	pipelineResourceName string = resourceName(observability.NewForwardingPipelineResource())
)

func init() {
	resource.AddTestSweepers(pipelineResourceName, &resource.Sweeper{
		Name:         pipelineResourceName,
		Dependencies: []string{},
		F: func(r string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			testutil.SetEnvDefaults()
			client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
			if err != nil {
				return fmt.Errorf("failed to build client: %w", err)
			}

			listResp, err := client.ListPipelines(ctx, connect.NewRequest(&clusterv1beta1.ListPipelinesRequest{}))
			if err != nil {
				return fmt.Errorf("failed to list forwarding pipelines: %w", err)
			}

			streamPrefixes := make([]string, len(metricsStreams)+len(logsStreams))
			copy(streamPrefixes, metricsStreams)
			copy(streamPrefixes[len(metricsStreams):], logsStreams)

			prefixPattern := fmt.Sprintf(`^(%s-)?%s`, strings.Join(streamPrefixes, "|"), AcceptanceTestPrefix)
			prefixMatcher := regexp.MustCompile(prefixPattern)

			for _, pipeline := range listResp.Msg.GetPipelines() {
				if !prefixMatcher.MatchString(pipeline.Ref.Slug) {
					log.Printf("skipping forwarding pipeline %q because it does not match regular expression %q", pipeline.Ref.Slug, prefixMatcher)
					continue
				}

				log.Printf("sweeping forwarding pipeline %q", pipeline.Ref.Slug)
				if testutil.SweepDryRun() {
					log.Printf("skipping forwarding pipeline %q because of dry-run mode", pipeline.Ref.Slug)
					continue
				}

				deleteCtx, deleteCancel := context.WithTimeout(ctx, 5*time.Minute)
				defer deleteCancel()

				if _, err := client.DeletePipeline(deleteCtx, connect.NewRequest(&clusterv1beta1.DeletePipelineRequest{
					Ref: pipeline.Ref,
				})); err != nil {
					if connect.CodeOf(err) == connect.CodeNotFound {
						log.Printf("forwarding pipeline %q already deleted", pipeline.Ref.Slug)
						continue
					}
					return fmt.Errorf("failed to delete forwarding pipeline %q: %w", pipeline.Ref.Slug, err)
				}

				// Wait for deletion to complete
				waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Minute)
				defer waitCancel()

				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()

				for {
					select {
					case <-waitCtx.Done():
						return fmt.Errorf("timeout waiting for forwarding pipeline %q to be deleted: %w", pipeline.Ref.Slug, waitCtx.Err())
					case <-ticker.C:
						_, err := client.GetPipeline(waitCtx, connect.NewRequest(&clusterv1beta1.GetPipelineRequest{
							Ref: pipeline.Ref,
						}))
						if connect.CodeOf(err) == connect.CodeNotFound {
							log.Printf("forwarding pipeline %q successfully deleted", pipeline.Ref.Slug)
							goto nextPipeline
						}
						if err != nil {
							return fmt.Errorf("error checking forwarding pipeline %q deletion status: %w", pipeline.Ref.Slug, err)
						}
					}
				}
			nextPipeline:
			}

			return nil
		},
	})
}

// Real stream slugs for testing - these exist in the test environment
var (
	metricsStreams = []string{
		"metrics-customer-cluster",
		"metrics-platform",
	}

	logsStreams = []string{
		"logs-audit-caios",
		"logs-audit-console",
		"logs-audit-kube-api",
		"logs-customer-cluster",
		"logs-events",
		"logs-journald",
	}
)

// EndpointType represents the type of forwarding endpoint
type EndpointType string

const (
	EndpointTypeHTTPS      EndpointType = "https"
	EndpointTypeS3         EndpointType = "s3"
	EndpointTypePrometheus EndpointType = "prom"
)

// StreamType represents the type of telemetry stream
type StreamType string

const (
	StreamTypeMetrics StreamType = "metrics"
	StreamTypeLogs    StreamType = "logs"
)

func shorten(name string) string {
	if len(name) < 4 {
		return name
	}
	truncated := len(name) - 2
	return fmt.Sprintf("%s%d%s", name[:1], truncated, name[len(name)-1:])
}

func createHTTPSEndpoint(t *testing.T, slug string) *model.ForwardingEndpointHTTPS {
	t.Helper()
	return &model.ForwardingEndpointHTTPS{
		ForwardingEndpointCore: model.ForwardingEndpointCore{
			Slug:        types.StringValue(slug),
			DisplayName: types.StringValue(fmt.Sprintf("Test HTTPS Endpoint - %s", slug)),
		},
		Endpoint: types.StringValue(testHTTPSEndpointURL),
	}
}

func createS3Endpoint(t *testing.T, slug string) *model.ForwardingEndpointS3 {
	t.Helper()
	return &model.ForwardingEndpointS3{
		ForwardingEndpointCore: model.ForwardingEndpointCore{
			Slug:        types.StringValue(slug),
			DisplayName: types.StringValue(fmt.Sprintf("Test S3 Endpoint - %s", slug)),
		},
		URI:         types.StringValue("s3://tf-acc-telemetry-bucket"),
		Region:      types.StringValue("us-east-1"),
		Credentials: fixtureS3Credentials(),
	}
}

func createPrometheusEndpoint(t *testing.T, slug string) *model.ForwardingEndpointPrometheus {
	t.Helper()
	return &model.ForwardingEndpointPrometheus{
		ForwardingEndpointCore: model.ForwardingEndpointCore{
			Slug:        types.StringValue(slug),
			DisplayName: types.StringValue(fmt.Sprintf("Test Prometheus Endpoint - %s", slug)),
		},
		Endpoint: types.StringValue(testPrometheusEndpointURL),
	}
}

// renderForwardingPipelineResource renders HCL for a forwarding pipeline with
// an optional endpoint dependency. When an endpoint is supplied the
// destination is a resource traversal, which is what creates the dependency
// edge CreatePipeline's synchronous existence check needs.
func renderForwardingPipelineResource(t *testing.T, resourceName string, pipeline *model.ForwardingPipeline, endpoint any) string {
	t.Helper()
	var parts []string

	var endpointResourceType string
	if endpoint != nil {
		switch endpoint := endpoint.(type) {
		case *model.ForwardingEndpointHTTPS:
			endpointResourceType = httpsEndpointResourceName
			parts = append(parts, renderHTTPSEndpointResource(resourceName, endpoint))
		case *model.ForwardingEndpointS3:
			endpointResourceType = s3EndpointResourceName
			parts = append(parts, renderS3EndpointResource(resourceName, endpoint))
		case *model.ForwardingEndpointPrometheus:
			endpointResourceType = prometheusEndpointResourceName
			parts = append(parts, renderPrometheusEndpointResource(resourceName, endpoint))
		default:
			t.Fatalf("Unknown endpoint type: %T", endpoint)
		}
	}

	var hcl strings.Builder
	hcl.WriteString(fmt.Sprintf("resource %q %q {\n", pipelineResourceName, resourceName))
	hcl.WriteString(fmt.Sprintf("  slug             = %q\n", pipeline.Slug.ValueString()))
	hcl.WriteString(fmt.Sprintf("  source_slug      = %q\n", pipeline.SourceSlug.ValueString()))

	if endpoint != nil {
		hcl.WriteString(fmt.Sprintf("  destination_slug = %s.%s.slug\n", endpointResourceType, resourceName))
	} else {
		hcl.WriteString(fmt.Sprintf("  destination_slug = %q\n", pipeline.DestinationSlug.ValueString()))
	}

	if !pipeline.Enabled.IsNull() {
		hcl.WriteString(fmt.Sprintf("  enabled          = %t\n", pipeline.Enabled.ValueBool()))
	}

	if !pipeline.ZoneSlugs.IsNull() {
		quoted := make([]string, 0, len(pipeline.ZoneSlugs.Elements()))
		for _, element := range pipeline.ZoneSlugs.Elements() {
			zone, ok := element.(types.String)
			if !ok {
				t.Fatalf("zone_slugs element is %T, want types.String", element)
			}
			quoted = append(quoted, fmt.Sprintf("%q", zone.ValueString()))
		}
		hcl.WriteString(fmt.Sprintf("  zone_slugs       = [%s]\n", strings.Join(quoted, ", ")))
	}

	hcl.WriteString("}\n")

	parts = append(parts, hcl.String())

	return strings.Join(parts, "\n")
}

// zoneSlugSet is a convenience for building the zone_slugs fixture value.
func zoneSlugSet(t *testing.T, zones ...string) types.Set {
	t.Helper()

	value, diags := types.SetValueFrom(t.Context(), types.StringType, zones)
	require.False(t, diags.HasError(), "failed to build zone_slugs: %v", diags)
	return value
}

// TestForwardingPipeline is the umbrella for every forwarding pipeline test,
// so `go test -run TestForwardingPipeline ./coreweave/observability` reaches
// all of them.
//
// ## Acceptance tests here need a prepared lab organization
//
// Unlike the endpoint suites, which are database-only server-side, a pipeline
// provisions one connector per source Kafka cluster holding the stream's
// topics. Staging telecasterd is wired to 26 production kafkaclusterd
// instances, so running these against an org with a production cluster
// footprint creates real connectors in a production zone.
//
// A fresh QA org is not a substitute: with no registered clusters,
// CreatePipeline cannot succeed at all, because the workflow fails the
// precondition that the stream has topics. These need an organization whose
// registered clusters are lab-only.
func TestForwardingPipeline(t *testing.T) {
	t.Parallel()

	t.Run("schema", testForwardingPipelineSchema)
	t.Run("model_to_msg", testForwardingPipelineModelToMsg)
	t.Run("round_trip", testForwardingPipelineRoundTrip)
	t.Run("render", testRenderForwardingPipelineResource)
	t.Run("ref_to_msg", testForwardingPipelineRefToMsg)
	t.Run("spec_to_msg", testForwardingPipelineSpecToMsg)
	t.Run("invalid_config", testForwardingPipelineInvalidConfig)
	t.Run("lifecycle", testForwardingPipelineLifecycle)
}

// testForwardingPipelineSchema validates the resource schema
func testForwardingPipelineSchema(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	schemaResponse := new(fwresource.SchemaResponse)
	observability.NewForwardingPipelineResource().Schema(ctx, fwresource.SchemaRequest{}, schemaResponse)
	assert.False(t, schemaResponse.Diagnostics.HasError(), "Schema request returned errors: %v", schemaResponse.Diagnostics)

	diagnostics := schemaResponse.Schema.ValidateImplementation(ctx)
	assert.False(t, diagnostics.HasError(), "Schema implementation is invalid: %v", diagnostics)
}

// testForwardingPipelineModelToMsg validates the full model conversion and protovalidation
func testForwardingPipelineModelToMsg(t *testing.T) {
	t.Parallel()

	base := func() *model.ForwardingPipeline {
		return &model.ForwardingPipeline{
			Slug:            types.StringValue("test-pipeline"),
			SourceSlug:      types.StringValue("test-stream"),
			DestinationSlug: types.StringValue("test-endpoint"),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}
	}

	tests := []struct {
		name              string
		input             *model.ForwardingPipeline
		expectedZoneSlugs []string
		wantErr           bool
	}{
		{
			name:  "nil input returns nil",
			input: nil,
		},
		{
			name:  "valid model creates valid message",
			input: base(),
		},
		{
			name: "disabled pipeline is valid",
			input: with(base(), func(p *model.ForwardingPipeline) {
				p.Enabled = types.BoolValue(false)
			}),
		},
		{
			name: "omitted enabled is valid and sends false",
			input: with(base(), func(p *model.ForwardingPipeline) {
				p.Enabled = types.BoolNull()
			}),
		},
		{
			name: "zone_slugs are carried through",
			input: with(base(), func(p *model.ForwardingPipeline) {
				p.ZoneSlugs = zoneSlugSet(t, "us-east-04a", "us-west-01a")
			}),
			expectedZoneSlugs: []string{"us-east-04a", "us-west-01a"},
		},
		{
			// buf.validate marks zone_slugs repeated.unique. Terraform dedupes a
			// set written in HCL, but types.Set in Go does not, so ToMsg can still
			// emit duplicates and protovalidate is what catches them.
			name: "duplicate zone_slugs fail protovalidate",
			input: with(base(), func(p *model.ForwardingPipeline) {
				p.ZoneSlugs = types.SetValueMust(types.StringType, []attr.Value{
					types.StringValue("us-east-04a"),
					types.StringValue("us-east-04a"),
				})
			}),
			expectedZoneSlugs: []string{"us-east-04a", "us-east-04a"},
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msg, diags := tt.input.ToMsg(t.Context())
			assert.False(t, diags.HasError(), "ToMsg returned diagnostics: %v", diags)

			if tt.input == nil {
				assert.Nil(t, msg)
				return
			}

			require.NotNil(t, msg)
			assert.Equal(t, tt.input.Slug.ValueString(), msg.GetRef().GetSlug())
			assert.Equal(t, tt.input.SourceSlug.ValueString(), msg.GetSpec().GetSource().GetSlug())
			assert.Equal(t, tt.input.DestinationSlug.ValueString(), msg.GetSpec().GetDestination().GetSlug())
			assert.Equal(t, tt.input.Enabled.ValueBool(), msg.GetSpec().GetEnabled())
			assert.ElementsMatch(t, tt.expectedZoneSlugs, msg.GetSpec().GetZoneSlugs())

			err := protovalidate.Validate(msg)
			if tt.wantErr {
				assert.Error(t, err, "Expected validation error but got none")
			} else {
				assert.NoError(t, err, "Message failed protovalidation: %v", err)
			}
		})
	}
}

// testForwardingPipelineRoundTrip asserts the model survives a Set/ToMsg
// round trip, including the two values whose emptiness is meaningful: an
// omitted enabled stays null, and empty zone_slugs read back as null rather
// than as an empty set asserting "no zones".
func testForwardingPipelineRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	pipeline := &typesv1beta1.ForwardingPipeline{
		Ref: &typesv1beta1.ForwardingPipelineRef{Slug: "round-trip"},
		Spec: &typesv1beta1.ForwardingPipelineSpec{
			Source:      &typesv1beta1.TelemetryStreamRef{Slug: "logs-journald"},
			Destination: &typesv1beta1.ForwardingEndpointRef{Slug: "sink"},
			Enabled:     true,
		},
		Status: &typesv1beta1.ForwardingPipelineStatus{
			State: typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE,
		},
	}

	t.Run("omitted enabled stays null", func(t *testing.T) {
		t.Parallel()

		m := &model.ForwardingPipeline{Enabled: types.BoolNull()}
		require.False(t, m.Set(ctx, pipeline).HasError())
		assert.True(t, m.Enabled.IsNull(),
			"a null config must stay null, or the apply fails with 'provider produced inconsistent result'")
	})

	t.Run("configured enabled reflects the server", func(t *testing.T) {
		t.Parallel()

		m := &model.ForwardingPipeline{Enabled: types.BoolValue(false)}
		require.False(t, m.Set(ctx, pipeline).HasError())
		assert.True(t, m.Enabled.ValueBool())
	})

	t.Run("empty zone_slugs read back as null", func(t *testing.T) {
		t.Parallel()

		m := &model.ForwardingPipeline{}
		require.False(t, m.Set(ctx, pipeline).HasError())
		assert.True(t, m.ZoneSlugs.IsNull(),
			"empty means all zones; an empty set would assert the opposite")
	})

	t.Run("populated zone_slugs round-trip", func(t *testing.T) {
		t.Parallel()

		withZones := &typesv1beta1.ForwardingPipeline{
			Ref: pipeline.GetRef(),
			Spec: &typesv1beta1.ForwardingPipelineSpec{
				Source:      pipeline.GetSpec().GetSource(),
				Destination: pipeline.GetSpec().GetDestination(),
				ZoneSlugs:   []string{"us-east-04a"},
			},
			Status: pipeline.GetStatus(),
		}

		m := &model.ForwardingPipeline{}
		require.False(t, m.Set(ctx, withZones).HasError())
		require.False(t, m.ZoneSlugs.IsNull())

		msg, diags := m.ToMsg(ctx)
		require.False(t, diags.HasError())
		assert.Equal(t, []string{"us-east-04a"}, msg.GetSpec().GetZoneSlugs())
	})
}

// testRenderForwardingPipelineResource tests the HCL rendering behavior:
// when an endpoint is provided, destination uses a resource reference (traversal),
// otherwise it uses a literal slug string.
func testRenderForwardingPipelineResource(t *testing.T) {
	t.Parallel()

	t.Run("without_endpoint_uses_literal_slug", func(t *testing.T) {
		t.Parallel()

		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue("test-pipeline"),
			SourceSlug:      types.StringValue("test-stream"),
			DestinationSlug: types.StringValue("test-endpoint"),
			Enabled:         types.BoolValue(true),
		}

		exampleHCLPipelineUsesLiterals, err := testdata.ReadFile("testdata/hcl_pipeline_uses_literals.tf")
		require.NoError(t, err)

		hcl := renderForwardingPipelineResource(t, "test", pipeline, nil)
		assert.Equal(t, string(exampleHCLPipelineUsesLiterals), hcl)
	})

	t.Run("with_endpoint_uses_resource_reference", func(t *testing.T) {
		t.Parallel()

		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue("test-pipeline"),
			SourceSlug:      types.StringValue("test-stream"),
			DestinationSlug: types.StringValue("test-endpoint"),
			Enabled:         types.BoolValue(true),
		}
		endpoint := createHTTPSEndpoint(t, "test-endpoint")

		exampleHCLEndpointUsesReferences, err := testdata.ReadFile("testdata/hcl_pipeline_uses_references.tf")
		require.NoError(t, err)

		hcl := renderForwardingPipelineResource(t, "test", pipeline, endpoint)
		assert.Equal(t, string(exampleHCLEndpointUsesReferences), hcl)
	})
}

// testForwardingPipelineRefToMsg validates the ref model conversion
func testForwardingPipelineRefToMsg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *model.ForwardingPipelineRef
		expected *typesv1beta1.ForwardingPipelineRef
		wantErr  bool
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name: "valid input converts correctly",
			input: &model.ForwardingPipelineRef{
				Slug: types.StringValue("example-pipeline"),
			},
			expected: &typesv1beta1.ForwardingPipelineRef{
				Slug: "example-pipeline",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := tt.input.ToMsg()
			assert.Equal(t, tt.expected, msg)
		})
	}
}

// testForwardingPipelineSpecToMsg validates the spec model conversion
func testForwardingPipelineSpecToMsg(t *testing.T) {
	t.Parallel()

	specBase := func() *model.ForwardingPipelineSpec {
		return &model.ForwardingPipelineSpec{
			Enabled: types.BoolValue(true),
			Source: model.TelemetryStreamRef{
				Slug: types.StringValue("example-stream"),
			},
			Destination: model.ForwardingEndpointRef{
				Slug: types.StringValue("example-destination"),
			},
		}
	}
	outputBase := func() *typesv1beta1.ForwardingPipelineSpec {
		return &typesv1beta1.ForwardingPipelineSpec{
			Enabled: true,
			Source: &typesv1beta1.TelemetryStreamRef{
				Slug: "example-stream",
			},
			Destination: &typesv1beta1.ForwardingEndpointRef{
				Slug: "example-destination",
			},
		}
	}

	tests := []struct {
		name     string
		input    *model.ForwardingPipelineSpec
		expected *typesv1beta1.ForwardingPipelineSpec
		wantErr  bool
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "valid input converts correctly",
			input:    specBase(),
			expected: outputBase(),
		},
		{
			name: "enabled false converts correctly",
			input: with(specBase(), func(s *model.ForwardingPipelineSpec) {
				s.Enabled = types.BoolValue(false)
			}),
			expected: with(outputBase(), func(s *typesv1beta1.ForwardingPipelineSpec) {
				s.Enabled = false
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := tt.input.ToMsg()
			assert.Equal(t, tt.expected, msg)
		})
	}
}

// testForwardingPipelineInvalidConfig asserts the plan-time validators reject
// what the API would otherwise only reject at apply. Nothing is created, so
// these are safe against any organization.
func testForwardingPipelineInvalidConfig(t *testing.T) {
	randomInt := rand.IntN(100)
	resourceName := "invalid_config"

	valid := func() *model.ForwardingPipeline {
		return &model.ForwardingPipeline{
			Slug:            types.StringValue(slugify("pipe-invalid", randomInt)),
			SourceSlug:      types.StringValue(logsStreams[0]),
			DestinationSlug: types.StringValue(slugify("pipe-invalid-ep", randomInt)),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}
	}

	cases := []struct {
		name        string
		model       *model.ForwardingPipeline
		expectError *regexp.Regexp
	}{
		{
			name: "slug too short",
			model: with(valid(), func(p *model.ForwardingPipeline) {
				p.Slug = types.StringValue("ab")
			}),
			expectError: regexp.MustCompile(`(?s)slug.*at least 3`),
		},
		{
			// The server canonicalises zone slugs, so an uppercase value would
			// never match what comes back and the plan would never converge.
			name: "uppercase zone slug",
			model: with(valid(), func(p *model.ForwardingPipeline) {
				p.ZoneSlugs = zoneSlugSet(t, "US-EAST-04A")
			}),
			expectError: regexp.MustCompile(`(?s)zone_slugs.*lowercase`),
		},
		{
			// An explicit empty set reads as "no zones" but means "all zones" to
			// the server, and reads back as null. Reject it and make the user omit
			// the attribute.
			name: "empty zone_slugs set",
			model: with(valid(), func(p *model.ForwardingPipeline) {
				p.ZoneSlugs = types.SetValueMust(types.StringType, []attr.Value{})
			}),
			expectError: regexp.MustCompile(`(?s)zone_slugs.*at least 1`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.ParallelTest(t, resource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				PreCheck:                 func() { testutil.SetEnvDefaults() },
				Steps: []resource.TestStep{
					{
						Config:      renderForwardingPipelineResource(t, resourceName, tc.model, nil),
						PlanOnly:    true,
						ExpectError: tc.expectError,
					},
				},
			})
		})
	}
}

func testForwardingPipelineLifecycle(t *testing.T) {
	randomInt := rand.IntN(100)

	t.Run("stream_to_endpoint_matrix", func(t *testing.T) {
		streamsByStreamType := map[StreamType][]string{
			StreamTypeMetrics: metricsStreams,
			StreamTypeLogs:    logsStreams,
		}

		// Prometheus endpoints are not registered as a resource type, so metrics
		// streams have no destination the provider can create today.
		validEndpointsByStreamType := map[StreamType][]EndpointType{
			StreamTypeMetrics: {},
			StreamTypeLogs:    {EndpointTypeHTTPS, EndpointTypeS3},
		}

		for streamType, streamSlugs := range streamsByStreamType {
			for _, streamSlug := range streamSlugs {
				for _, endpointType := range validEndpointsByStreamType[streamType] {
					testName := fmt.Sprintf("%s_to_%s", streamSlug, endpointType)

					t.Run(testName, func(t *testing.T) {
						resourceName := fmt.Sprintf("%s_to_%s", streamSlug, endpointType)
						fullResourceName := fmt.Sprintf("%s.%s", pipelineResourceName, resourceName)

						// We end up creating many endpoint slugs that are illegally long, so we need to shorten them
						endpointSlug := slugify(fmt.Sprintf("p-%s-%s", streamSlug, endpointType), randomInt)
						if len(endpointSlug) > endpointSlugMaxTestLength {
							endpointSlug = slugify(fmt.Sprintf("p-%s-%s", shorten(streamSlug), string(endpointType)), randomInt)
						}
						if len(endpointSlug) > endpointSlugMaxTestLength {
							endpointSlug = slugify(fmt.Sprintf("p-%s-%s", shorten(streamSlug), shorten(string(endpointType))), randomInt)
						}

						var endpoint any
						switch endpointType {
						case EndpointTypeHTTPS:
							endpoint = createHTTPSEndpoint(t, endpointSlug)
						case EndpointTypeS3:
							endpoint = createS3Endpoint(t, endpointSlug)
						case EndpointTypePrometheus:
							endpoint = createPrometheusEndpoint(t, endpointSlug)
						}

						pipelineSlug := slugify(fmt.Sprintf("pipe-%s-%s", streamSlug, endpointType), randomInt)

						pipeline := &model.ForwardingPipeline{
							Slug:            types.StringValue(pipelineSlug),
							SourceSlug:      types.StringValue(streamSlug),
							DestinationSlug: types.StringValue(endpointSlug),
							Enabled:         types.BoolValue(true),
							ZoneSlugs:       types.SetNull(types.StringType),
						}

						resource.ParallelTest(t, resource.TestCase{
							ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
							PreCheck:                 func() { testutil.SetEnvDefaults() },
							Steps: []resource.TestStep{
								{
									PreConfig: func() {
										t.Logf("Creating pipeline: %s -> %s, endpoint: %s", streamSlug, endpointType, endpointSlug)
									},
									Config: renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
									ConfigPlanChecks: resource.ConfigPlanChecks{
										PreApply: []plancheck.PlanCheck{
											plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
										},
									},
									ConfigStateChecks: []statecheck.StateCheck{
										statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("slug"), knownvalue.StringExact(pipelineSlug)),
										statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("source_slug"), knownvalue.StringExact(streamSlug)),
										statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("destination_slug"), knownvalue.StringExact(endpointSlug)),
										statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
										statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("zone_slugs"), knownvalue.Null()),
										statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE.String())),
									},
								},
							},
						})
					})
				}
			}
		}
	})

	t.Run("crud", func(t *testing.T) {
		resourceName := "lifecycle"
		fullResourceName := fmt.Sprintf("%s.%s", pipelineResourceName, resourceName)

		streamSlug := logsStreams[0]

		endpointSlug := slugify("lifecycle-ep", randomInt)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		secondEndpointSlug := slugify("lifecycle-ep2", randomInt)

		pipelineSlug := slugify("lifecycle-pipeline", randomInt)
		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(pipelineSlug),
			SourceSlug:      types.StringValue(streamSlug),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				{
					PreConfig: func() { t.Log("Step 1: Create pipeline (enabled)") },
					Config:    renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("state"), knownvalue.StringExact(typesv1beta1.ForwardingPipelineState_FORWARDING_PIPELINE_STATE_ACTIVE.String())),
					},
				},
				{
					PreConfig: func() { t.Log("Step 2: No-op (verify idempotency)") },
					Config:    renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionNoop),
						},
					},
				},
				{
					// enabled is stored and echoed but the forwarding workflow never
					// reads it, so this asserts the round trip only. It does not
					// assert that forwarding stopped, because it does not.
					PreConfig: func() { t.Log("Step 3: Disable pipeline (stored, not enforced)") },
					Config: renderForwardingPipelineResource(t, resourceName, with(pipeline, func(p *model.ForwardingPipeline) {
						p.Enabled = types.BoolValue(false)
					}), endpoint),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("enabled"), knownvalue.Bool(false)),
					},
				},
				{
					PreConfig: func() { t.Log("Step 4: Re-enable pipeline") },
					Config:    renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("enabled"), knownvalue.Bool(true)),
					},
				},
				{
					// Omitting enabled must stay null rather than being overwritten
					// with the server's false, or the apply fails with "provider
					// produced inconsistent result after apply".
					PreConfig: func() { t.Log("Step 5: Omit enabled entirely") },
					Config: renderForwardingPipelineResource(t, resourceName, with(pipeline, func(p *model.ForwardingPipeline) {
						p.Enabled = types.BoolNull()
					}), endpoint),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("enabled"), knownvalue.Null()),
					},
				},
				{
					PreConfig: func() { t.Log("Step 6: Import") },
					Config:    renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
				},
				{
					ResourceName:      fullResourceName,
					ImportState:       true,
					ImportStateId:     pipelineSlug,
					ImportStateVerify: true,
					// enabled is Optional with no default, so an imported pipeline
					// reads it as null regardless of what the server stored.
					ImportStateVerifyIgnore: []string{"enabled"},
				},
				{
					// UpdatePipeline accepts a new destination, so repointing is an
					// in-place update. Forcing replacement would leave a window with
					// no pipeline at all.
					PreConfig: func() { t.Log("Step 7: Repoint destination in place (no replacement)") },
					Config: renderForwardingPipelineResource(t, resourceName+"_b",
						with(pipeline, func(p *model.ForwardingPipeline) {
							p.DestinationSlug = types.StringValue(secondEndpointSlug)
						}),
						createHTTPSEndpoint(t, secondEndpointSlug)),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(
								fmt.Sprintf("%s.%s_b", pipelineResourceName, resourceName),
								plancheck.ResourceActionCreate),
						},
					},
				},
			},
		})
	})

	// zone_slugs is OPTIONAL and IMMUTABLE in the proto and UpdatePipeline
	// hard-rejects a change, so every edit must plan as a replacement. Both
	// steps are PlanOnly, so the replacement is proven without paying for it.
	t.Run("zone_slugs_requires_replace", func(t *testing.T) {
		resourceName := "zone_slugs"
		fullResourceName := fmt.Sprintf("%s.%s", pipelineResourceName, resourceName)

		endpointSlug := slugify("zones-ep", randomInt)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(slugify("zones-pipeline", randomInt)),
			SourceSlug:      types.StringValue(logsStreams[0]),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       zoneSlugSet(t, testLabZoneSlug),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				{
					PreConfig: func() { t.Log("create a zone-scoped pipeline") },
					Config:    renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("zone_slugs"),
							knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact(testLabZoneSlug)})),
					},
				},
				{
					PreConfig: func() { t.Log("changing zone_slugs plans a replacement") },
					Config: renderForwardingPipelineResource(t, resourceName, with(pipeline, func(p *model.ForwardingPipeline) {
						p.ZoneSlugs = zoneSlugSet(t, testLabZoneSlug, testSecondLabZoneSlug)
					}), endpoint),
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PostApplyPreRefresh: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
						PostApplyPostRefresh: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
					},
				},
				{
					PreConfig: func() { t.Log("removing zone_slugs also plans a replacement") },
					Config: renderForwardingPipelineResource(t, resourceName, with(pipeline, func(p *model.ForwardingPipeline) {
						p.ZoneSlugs = types.SetNull(types.StringType)
					}), endpoint),
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PostApplyPreRefresh: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
						PostApplyPostRefresh: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
					},
				},
			},
		})
	})

	// slug is the Ref: changing it is a different object. PlanOnly, so no
	// second pipeline is created.
	t.Run("slug_requires_replace", func(t *testing.T) {
		resourceName := "slug_replace"
		fullResourceName := fmt.Sprintf("%s.%s", pipelineResourceName, resourceName)

		endpointSlug := slugify("slugrep-ep", randomInt)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(slugify("slugrep-pipeline", randomInt)),
			SourceSlug:      types.StringValue(logsStreams[0]),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				{
					PreConfig: func() { t.Log("create the pipeline") },
					Config:    renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
				},
				{
					PreConfig: func() { t.Log("changing slug plans a replacement") },
					Config: renderForwardingPipelineResource(t, resourceName, with(pipeline, func(p *model.ForwardingPipeline) {
						p.Slug = types.StringValue(slugify("slugrep-pipeline-2", randomInt))
					}), endpoint),
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PostApplyPreRefresh: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
						PostApplyPostRefresh: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
					},
				},
			},
		})
	})

	// Out-of-band delete must plan a re-create. Before Read learned to call
	// RemoveResource on NotFound, refresh surfaced an error the user could not
	// act on.
	t.Run("disappears", func(t *testing.T) {
		resourceName := "disappears"
		fullResourceName := fmt.Sprintf("%s.%s", pipelineResourceName, resourceName)

		endpointSlug := slugify("gone-ep", randomInt)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		pipelineSlug := slugify("gone-pipeline", randomInt)
		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(pipelineSlug),
			SourceSlug:      types.StringValue(logsStreams[0]),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				{
					PreConfig: func() { t.Log("create the pipeline to be deleted out of band") },
					Config:    renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
				},
				{
					PreConfig: func() {
						t.Log("delete the pipeline behind Terraform's back")
						client := testClient(t)
						_, err := client.DeletePipeline(t.Context(), connect.NewRequest(&clusterv1beta1.DeletePipelineRequest{
							Ref: &typesv1beta1.ForwardingPipelineRef{Slug: pipelineSlug},
						}))
						require.NoError(t, err)
					},
					Config: renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionCreate),
						},
					},
				},
			},
		})
	})

	t.Run("invalid_stream", func(t *testing.T) {
		resourceName := "invalid_stream"

		endpointSlug := slugify("badstream-ep", randomInt)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(slugify("pipe-badstream", randomInt)),
			SourceSlug:      types.StringValue("nonexistent-stream"),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}

		resource.ParallelTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { testutil.SetEnvDefaults() },
			Steps: []resource.TestStep{
				{
					PreConfig:   func() { t.Log("Testing pipeline creation with invalid stream") },
					Config:      renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
					ExpectError: regexp.MustCompile(`(?i)(not found|invalid|failed)`),
				},
			},
		})
	})
}
