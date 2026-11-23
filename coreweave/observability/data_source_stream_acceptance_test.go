package observability_test

import (
	"fmt"
	"testing"

	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestTelemetryStreamAcceptance(t *testing.T) {
	t.Parallel()

	t.Run("lifecycle", func(t *testing.T) {
		streamTests := []struct {
			streamType   string
			expectedKind string
			slugs        []string
		}{
			{
				streamType:   "metrics",
				expectedKind: typesv1beta1.StreamKind_STREAM_KIND_METRICS.String(),
				slugs:        metricsStreams,
			},
			{
				streamType:   "logs",
				expectedKind: typesv1beta1.StreamKind_STREAM_KIND_LOGS.String(),
				slugs:        logsStreams,
			},
		}

		for _, tt := range streamTests {
			for _, slug := range tt.slugs {
				t.Run(fmt.Sprintf("%s/%s", tt.streamType, slug), func(t *testing.T) {
					dataSourceName := fmt.Sprintf("test_acc_%s_stream", tt.streamType)
					fullDataSourceName := fmt.Sprintf("data.%s.%s", telemetryStreamDataSourceName, dataSourceName)

					resource.ParallelTest(t, resource.TestCase{
						ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
						PreCheck:                 func() { setAccEnvDefaults() },
						Steps: []resource.TestStep{
							createStreamDataSourceTestStep(t, streamDataSourceTestStep{
								TestName:       fmt.Sprintf("%s stream: %s", tt.streamType, slug),
								DataSourceName: dataSourceName,
								Slug:           slug,
								StateChecks: []statecheck.StateCheck{
									statecheck.ExpectKnownValue(fullDataSourceName, tfjsonpath.New("kind"), knownvalue.StringExact(tt.expectedKind)),
								},
							}),
						},
					})
				})
			}
		}
	})
}
