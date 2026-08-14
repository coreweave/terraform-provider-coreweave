package inference

import (
	"context"
	"strings"
	"testing"

	"buf.build/gen/go/coreweave/inference/connectrpc/go/coreweave/inference/v1alpha1/inferencev1alpha1connect"
	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
)

// stubDeploymentServiceClient satisfies the Connect DeploymentServiceClient by
// embedding the interface (all other methods are unused and would panic if
// called) and overriding only GetDeploymentParameters, so validateEngineAvailable
// can be exercised without a live API.
type stubDeploymentServiceClient struct {
	inferencev1alpha1connect.DeploymentServiceClient
	resp *inferencev1.GetDeploymentParametersResponse
	err  error
}

func (s stubDeploymentServiceClient) GetDeploymentParameters(
	_ context.Context,
	_ *connect.Request[inferencev1.GetDeploymentParametersRequest],
) (*connect.Response[inferencev1.GetDeploymentParametersResponse], error) {
	if s.err != nil {
		return nil, s.err
	}
	return connect.NewResponse(s.resp), nil
}

func paramsWithEngines(engines ...string) *inferencev1.GetDeploymentParametersResponse {
	versions := make(map[string]*inferencev1.DeploymentRuntimeParameters_RuntimeVersions, len(engines))
	for _, e := range engines {
		versions[e] = &inferencev1.DeploymentRuntimeParameters_RuntimeVersions{}
	}
	return &inferencev1.GetDeploymentParametersResponse{
		RuntimeParameters: &inferencev1.DeploymentRuntimeParameters{RuntimeVersions: versions},
	}
}

func TestValidateEngineAvailable(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		engine          string
		resp            *inferencev1.GetDeploymentParametersResponse
		wantErr         bool
		wantDetailMatch string
	}{
		// An engine advertised only in the server response (not any hardcoded
		// list) is accepted — the point of validating against the live API.
		"engine present only in server response": {
			engine: "dynamo-sglang",
			resp:   paramsWithEngines("vllm", "dynamo-vllm", "dynamo-sglang"),
		},
		// An unadvertised engine errors, listing the allowed values sorted.
		"absent engine reports sorted allowed values": {
			engine:          "sglang",
			resp:            paramsWithEngines("vllm", "dynamo-vllm", "dynamo-sglang"),
			wantErr:         true,
			wantDetailMatch: `engine "sglang" is not available; must be one of: dynamo-sglang, dynamo-vllm, vllm`,
		},
		// An empty advertised set bypasses the check (server is the authority).
		"empty response bypasses validation": {
			engine: "anything",
			resp:   paramsWithEngines(),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := &InferenceDeploymentResource{
				client: &coreweave.InferenceClient{
					DeploymentServiceClient: stubDeploymentServiceClient{resp: tc.resp},
				},
			}

			diags := r.validateEngineAvailable(context.Background(), tc.engine)

			if tc.wantErr {
				if !diags.HasError() {
					t.Fatalf("expected an error diagnostic, got none")
				}
				if got := diags.Errors()[0].Detail(); !strings.Contains(got, tc.wantDetailMatch) {
					t.Fatalf("diagnostic detail = %q, want it to contain %q", got, tc.wantDetailMatch)
				}
				return
			}
			if diags.HasError() {
				t.Fatalf("unexpected error diagnostic: %v", diags)
			}
		})
	}
}
