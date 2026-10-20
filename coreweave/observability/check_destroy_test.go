package observability_test

import (
	"fmt"
	"testing"

	clusterv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// checkDestroyed asserts that every forwarding endpoint and pipeline the test
// created is actually gone from the API.
//
// Terraform reporting a clean destroy is not evidence the server agrees. A
// Delete that returns before the resource is really removed, or a poll that
// exits early, both leave Terraform believing the resource is gone. Without
// this the leftover is invisible: the sweeper silently removes it on the next
// run and the delete bug never surfaces.
//
// It reads slugs out of the destroyed state rather than taking them as
// arguments, so test cases that build several resources at once — a pipeline
// and the endpoint it forwards to — are covered without listing each one.
func checkDestroyed(t *testing.T) resource.TestCheckFunc {
	t.Helper()

	return func(state *terraform.State) error {
		ctx := t.Context()

		client, err := provider.BuildClient(ctx, provider.CoreweaveProviderModel{}, "", "")
		if err != nil {
			return fmt.Errorf("building client: %w", err)
		}

		for _, module := range state.Modules {
			for address, res := range module.Resources {
				if res.Primary == nil {
					continue
				}
				slug := res.Primary.Attributes["slug"]
				if slug == "" {
					continue
				}

				var lookup error
				switch res.Type {
				case httpsEndpointResourceName, s3EndpointResourceName:
					_, lookup = client.GetEndpoint(ctx, &clusterv1beta1.GetEndpointRequest{
						Ref: &typesv1beta1.ForwardingEndpointRef{Slug: slug},
					})
				case pipelineResourceName:
					_, lookup = client.GetPipeline(ctx, &clusterv1beta1.GetPipelineRequest{
						Ref: &typesv1beta1.ForwardingPipelineRef{Slug: slug},
					})
				default:
					// Data sources and any other provider's resources.
					continue
				}

				if lookup == nil {
					return fmt.Errorf("%s %q still exists after destroy", address, slug)
				}
				if !coreweave.IsNotFoundError(lookup) {
					return fmt.Errorf("checking %s %q was destroyed: %w", address, slug, lookup)
				}
			}
		}

		return nil
	}
}
