package observability_test

import (
	"fmt"
	"regexp"
	"testing"

	clusterv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave/observability/internal/model"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
)

// Both subtests here drive a real API, so the whole function is skipped
// without TF_ACC. The offline conversion and render coverage lives in
// resource_forwarding_pipeline_test.go.
func TestForwardingPipelineAcceptance(t *testing.T) {
	t.Parallel()

	t.Run("invalid_config", testForwardingPipelineInvalidConfig)
	t.Run("lifecycle", testForwardingPipelineLifecycle)
}

// mutations from triggering Kafka rebalances in one another's status checks.
func runForwardingPipelineAcceptanceTest(t *testing.T, testCase resource.TestCase) {
	t.Helper()

	//nolint:forbidigo // serial Kafka Connect constraint, see TestForwardingPipelineAcceptance
	resource.Test(t, testCase)
}

// these are safe against any organization.
func testForwardingPipelineInvalidConfig(t *testing.T) {
	token := testToken(t)
	resourceName := "invalid_config"

	valid := func() *model.ForwardingPipeline {
		return &model.ForwardingPipeline{
			Slug:            types.StringValue(slugify("pipe-invalid", token)),
			SourceSlug:      types.StringValue(logsStreams[0]),
			DestinationSlug: types.StringValue(slugify("pipe-invalid-ep", token)),
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
			runForwardingPipelineAcceptanceTest(t, resource.TestCase{
				ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
				PreCheck:                 func() { setAccEnvDefaults() },
				CheckDestroy:             checkDestroyed(t),
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
	token := testToken(t)

	t.Run("stream_to_endpoint_matrix", func(t *testing.T) {
		streamsByStreamType := map[StreamType][]string{
			StreamTypeMetrics: metricsStreams,
			StreamTypeLogs:    matrixLogsStreams,
		}

		// Metrics streams have no endpoint type the provider can create today;
		// Prometheus remote write is the only destination the API accepts for
		// them, and that resource is not implemented yet.
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
						endpointSlug := slugify(fmt.Sprintf("p-%s-%s", streamSlug, endpointType), token)
						if len(endpointSlug) > endpointSlugMaxTestLength {
							endpointSlug = slugify(fmt.Sprintf("p-%s-%s", shorten(streamSlug), string(endpointType)), token)
						}
						if len(endpointSlug) > endpointSlugMaxTestLength {
							endpointSlug = slugify(fmt.Sprintf("p-%s-%s", shorten(streamSlug), shorten(string(endpointType))), token)
						}

						var endpoint any
						switch endpointType {
						case EndpointTypeHTTPS:
							endpoint = createHTTPSEndpoint(t, endpointSlug)
						case EndpointTypeS3:
							endpoint = createS3Endpoint(t, endpointSlug)
						}

						pipelineSlug := slugify(fmt.Sprintf("pipe-%s-%s", streamSlug, endpointType), token)

						// Always scope to the lab zone. A pipeline fans out one connector
						// per Kafka cluster holding the stream's topics, so an unscoped
						// pipeline creates connectors in whatever production zones the org
						// reaches -- which an acceptance test may not do. It also fails
						// there: a production kafkaclusterd unwraps credentials with
						// telecasterd-secrets while staging telecasterd sealed them with
						// telecasterd-secrets-stg, so a credentialed endpoint dies on
						// crypto/rsa: decryption error after the ten minute poll timeout.
						pipeline := &model.ForwardingPipeline{
							Slug:            types.StringValue(pipelineSlug),
							SourceSlug:      types.StringValue(streamSlug),
							DestinationSlug: types.StringValue(endpointSlug),
							Enabled:         types.BoolValue(true),
							ZoneSlugs:       zoneSlugSet(t, testLabZoneSlug),
						}

						runForwardingPipelineAcceptanceTest(t, resource.TestCase{
							ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
							PreCheck:                 func() { setAccEnvDefaults() },
							CheckDestroy:             checkDestroyed(t),
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
										statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("zone_slugs"), knownvalue.SetExact([]knownvalue.Check{
											knownvalue.StringExact(testLabZoneSlug),
										})),
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

		streamSlug := lifecycleCrudStream

		endpointSlug := slugify("lifecycle-ep", token)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		secondEndpointSlug := slugify("lifecycle-ep2", token)

		pipelineSlug := slugify("lifecycle-pipeline", token)
		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(pipelineSlug),
			SourceSlug:      types.StringValue(streamSlug),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}

		runForwardingPipelineAcceptanceTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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
					// The resource has no id attribute; slug is the identity the
					// import passthrough reads, so verification has to match on it.
					ImportStateVerifyIdentifierAttribute: "slug",
					// enabled is Optional with no default, so an imported pipeline
					// reads it as null regardless of what the server stored.
					//
					// updated_at moves on its own: the reconcile workflow writes
					// pipeline state once its connectors settle, which lands after
					// the apply returns, so comparing it across an import is a race
					// rather than an assertion. Observed differing by one second.
					// state and state_message are deliberately still verified.
					ImportStateVerifyIgnore: []string{"enabled", "updated_at"},
				},
				{
					// UpdatePipeline accepts a new destination, so repointing is an
					// in-place update. Forcing replacement would leave a window with
					// no pipeline at all.
					// Holds the pipeline's Terraform address steady and keeps the
					// original endpoint in the config. Renaming the address while
					// holding the slug would put two addresses on one server object,
					// and dropping the old endpoint would have Terraform delete it
					// mid-repoint; both were tried and both fail.
					PreConfig: func() { t.Log("Step 7: Repoint destination in place (no replacement)") },
					Config: renderForwardingPipelineResource(t, resourceName,
						with(pipeline, func(p *model.ForwardingPipeline) {
							p.DestinationSlug = types.StringValue(secondEndpointSlug)
						}),
						endpoint, createHTTPSEndpoint(t, secondEndpointSlug)),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionUpdate),
						},
					},
				},
			},
		})
	})

	// zone_slugs is OPTIONAL and IMMUTABLE in the proto and UpdatePipeline
	// hard-rejects a change, so an edit must plan as a replacement. Both steps
	// apply: that proves the replacement actually happens, where a PlanOnly
	// step would only prove it was planned and would pass even if the apply
	// then failed. us-lab-01a is the only registered lab zone, so the edit
	// removes the scope rather than swapping it for another zone.
	t.Run("zone_slugs_requires_replace", func(t *testing.T) {
		resourceName := "zone_slugs"
		fullResourceName := fmt.Sprintf("%s.%s", pipelineResourceName, resourceName)

		endpointSlug := slugify("zones-ep", token)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(slugify("zones-pipeline", token)),
			SourceSlug:      types.StringValue(lifecycleZoneReplaceStream),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       zoneSlugSet(t, testLabZoneSlug),
		}

		runForwardingPipelineAcceptanceTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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
					PreConfig: func() { t.Log("removing zone_slugs replaces the pipeline") },
					Config: renderForwardingPipelineResource(t, resourceName, with(pipeline, func(p *model.ForwardingPipeline) {
						p.ZoneSlugs = types.SetNull(types.StringType)
					}), endpoint),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(fullResourceName, plancheck.ResourceActionReplace),
						},
					},
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(fullResourceName, tfjsonpath.New("zone_slugs"), knownvalue.Null()),
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

		endpointSlug := slugify("slugrep-ep", token)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(slugify("slugrep-pipeline", token)),
			SourceSlug:      types.StringValue(lifecycleSlugReplaceStream),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}

		runForwardingPipelineAcceptanceTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
			Steps: []resource.TestStep{
				{
					PreConfig: func() { t.Log("create the pipeline") },
					Config:    renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
				},
				{
					PreConfig: func() { t.Log("changing slug plans a replacement") },
					Config: renderForwardingPipelineResource(t, resourceName, with(pipeline, func(p *model.ForwardingPipeline) {
						p.Slug = types.StringValue(slugify("slugrep-pipeline-2", token))
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

		endpointSlug := slugify("gone-ep", token)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		pipelineSlug := slugify("gone-pipeline", token)
		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(pipelineSlug),
			SourceSlug:      types.StringValue(lifecycleDisappearsStream),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}

		runForwardingPipelineAcceptanceTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
			Steps: []resource.TestStep{
				{
					PreConfig: func() { t.Log("create the pipeline to be deleted out of band") },
					Config:    renderForwardingPipelineResource(t, resourceName, pipeline, endpoint),
				},
				{
					PreConfig: func() {
						t.Log("delete the pipeline behind Terraform's back")
						client := testClient(t)
						_, err := client.DeletePipeline(t.Context(), &clusterv1beta1.DeletePipelineRequest{
							Ref: &typesv1beta1.ForwardingPipelineRef{Slug: pipelineSlug},
						})
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

		endpointSlug := slugify("badstream-ep", token)
		endpoint := createHTTPSEndpoint(t, endpointSlug)

		pipeline := &model.ForwardingPipeline{
			Slug:            types.StringValue(slugify("pipe-badstream", token)),
			SourceSlug:      types.StringValue("nonexistent-stream"),
			DestinationSlug: types.StringValue(endpointSlug),
			Enabled:         types.BoolValue(true),
			ZoneSlugs:       types.SetNull(types.StringType),
		}

		runForwardingPipelineAcceptanceTest(t, resource.TestCase{
			ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
			PreCheck:                 func() { setAccEnvDefaults() },
			CheckDestroy:             checkDestroyed(t),
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
