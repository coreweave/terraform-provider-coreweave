package inference

import (
	"context"
	"errors"
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

	getResp *inferencev1.GetDeploymentResponse
	getErr  error
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

func (s stubDeploymentServiceClient) GetDeployment(
	_ context.Context,
	_ *connect.Request[inferencev1.GetDeploymentRequest],
) (*connect.Response[inferencev1.GetDeploymentResponse], error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return connect.NewResponse(s.getResp), nil
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

func deploymentWith(status inferencev1.Status, conds ...*inferencev1.Condition) *inferencev1.Deployment {
	return &inferencev1.Deployment{
		Status: &inferencev1.DeploymentStatus{
			Status:     status,
			Conditions: conds,
		},
	}
}

func cond(condType string, status inferencev1.Condition_Status) *inferencev1.Condition {
	return &inferencev1.Condition{Type: condType, Status: status}
}

// TestResourcesAppliedRefresh checks the poller completes on ResourcesApplied=True,
// keeps waiting otherwise, and treats terminal statuses as errDeploymentFailed.
func TestResourcesAppliedRefresh(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		deployment *inferencev1.Deployment
		getErr     error
		wantState  string
		wantErr    error
	}{
		"resources applied while still creating completes": {
			deployment: deploymentWith(
				inferencev1.Status_STATUS_CREATING,
				cond(conditionTypeResourcesApplied, inferencev1.Condition_STATUS_TRUE),
			),
			wantState: resourcesAppliedState,
		},
		"resources applied once ready still completes": {
			deployment: deploymentWith(
				inferencev1.Status_STATUS_READY,
				cond(conditionTypeResourcesApplied, inferencev1.Condition_STATUS_TRUE),
				cond("Ready", inferencev1.Condition_STATUS_TRUE),
			),
			wantState: resourcesAppliedState,
		},
		"no conditions yet keeps waiting": {
			deployment: deploymentWith(inferencev1.Status_STATUS_CREATING),
			wantState:  inferencev1.Status_STATUS_CREATING.String(),
		},
		"resources not yet applied keeps waiting": {
			deployment: deploymentWith(
				inferencev1.Status_STATUS_CREATING,
				cond(conditionTypeResourcesApplied, inferencev1.Condition_STATUS_UNSPECIFIED),
			),
			wantState: inferencev1.Status_STATUS_CREATING.String(),
		},
		"failed apply is terminal": {
			deployment: deploymentWith(
				inferencev1.Status_STATUS_FAILED,
				cond(conditionTypeResourcesApplied, inferencev1.Condition_STATUS_FALSE),
			),
			wantState: inferencev1.Status_STATUS_FAILED.String(),
			wantErr:   errDeploymentFailed,
		},
		"error status is terminal": {
			deployment: deploymentWith(inferencev1.Status_STATUS_ERROR),
			wantState:  inferencev1.Status_STATUS_ERROR.String(),
			wantErr:    errDeploymentFailed,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := &InferenceDeploymentResource{
				client: &coreweave.InferenceClient{
					DeploymentServiceClient: stubDeploymentServiceClient{
						getResp: &inferencev1.GetDeploymentResponse{Deployment: tc.deployment},
						getErr:  tc.getErr,
					},
				},
			}

			_, state, err := r.resourcesAppliedRefresh(context.Background(), "dep-1")()

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if state != tc.wantState {
				t.Fatalf("state = %q, want %q", state, tc.wantState)
			}
		})
	}
}
