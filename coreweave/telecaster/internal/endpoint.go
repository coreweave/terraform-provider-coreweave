package internal

import (
	"context"
	"fmt"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telecaster/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

const (
	defaultDeleteEndpointTimeout = 5 * time.Minute
)

// EndpointTypeMatcher is a function that determines if an endpoint matches the desired type.
type EndpointTypeMatcher func(*typesv1beta1.ForwardingEndpoint) bool

type deleteEndpointOptions struct {
	timeout *time.Duration
}

type DeleteEndpointOption func(*deleteEndpointOptions)

func DeleteEndpointTimeout(timeout time.Duration) DeleteEndpointOption {
	return func(opts *deleteEndpointOptions) {
		opts.timeout = &timeout
	}
}

func waitForEndpointDeleted(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef) error {
	pollConf := retry.StateChangeConf{
		Pending: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{},
		Refresh: func() (any, string, error) {
			result, err := client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: ref,
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return nil, "", nil
				}
				return nil, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			endpoint := result.Msg.GetEndpoint()
			return endpoint, endpoint.GetStatus().GetState().String(), nil
		},
		Timeout: defaultDeleteEndpointTimeout,
	}

	if _, err := pollConf.WaitForStateContext(ctx); err != nil {
		return fmt.Errorf("endpoint was not deleted: %w", err)
	}

	return nil
}

func DeleteEndpointAndWait(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef) error {
	if _, err := client.DeleteEndpoint(ctx, connect.NewRequest(&clusterv1beta1.DeleteEndpointRequest{
		Ref: ref,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			tflog.Info(ctx, "endpoint already deleted, skipping deletion", map[string]any{
				"slug": ref.Slug,
			})
			return nil
		}

		return fmt.Errorf("failed to delete endpoint %q: %w", ref.Slug, err)
	}

	return waitForEndpointDeleted(ctx, client, ref)
}
