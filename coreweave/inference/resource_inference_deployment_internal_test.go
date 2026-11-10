package inference

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"buf.build/gen/go/coreweave/inference/connectrpc/go/v2/coreweave/inference/v1alpha1/inferencev1alpha1connect"
	inferencev1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect/v2"
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
	_ *inferencev1.GetDeploymentParametersRequest,
) (*inferencev1.GetDeploymentParametersResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func (s stubDeploymentServiceClient) GetDeployment(
	_ context.Context,
	_ *inferencev1.GetDeploymentRequest,
) (*inferencev1.GetDeploymentResponse, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.getResp, nil
}

func paramsWithEngines(engines ...string) *inferencev1.GetDeploymentParametersResponse {
	opts := make(map[string]*inferencev1.DeploymentRuntimeParameters_RuntimeEngineOptions, len(engines))
	for _, e := range engines {
		opts[e] = &inferencev1.DeploymentRuntimeParameters_RuntimeEngineOptions{}
	}
	return &inferencev1.GetDeploymentParametersResponse{
		RuntimeParameters: &inferencev1.DeploymentRuntimeParameters{RuntimeEngineOptions: opts},
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

// TestUpdateStateChangeConf guards the non-blocking update contract: resources
// applied during a rollout complete the waiter, while readiness alone does not.
func TestUpdateStateChangeConf(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status    inferencev1.Status
		applied   inferencev1.Condition_Status
		wantState string
		wantErr   error
	}{
		"applied while updating completes": {inferencev1.Status_STATUS_UPDATING, inferencev1.Condition_STATUS_TRUE, resourcesAppliedState, nil},
		"applied while creating completes": {inferencev1.Status_STATUS_CREATING, inferencev1.Condition_STATUS_TRUE, resourcesAppliedState, nil},
		"updating without applied waits":   {inferencev1.Status_STATUS_UPDATING, inferencev1.Condition_STATUS_FALSE, inferencev1.Status_STATUS_UPDATING.String(), nil},
		"ready without applied waits":      {inferencev1.Status_STATUS_READY, inferencev1.Condition_STATUS_FALSE, inferencev1.Status_STATUS_READY.String(), nil},
		"failed despite applied fails":     {inferencev1.Status_STATUS_FAILED, inferencev1.Condition_STATUS_TRUE, inferencev1.Status_STATUS_FAILED.String(), errDeploymentFailed},
		"error despite applied fails":      {inferencev1.Status_STATUS_ERROR, inferencev1.Condition_STATUS_TRUE, inferencev1.Status_STATUS_ERROR.String(), errDeploymentFailed},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := &InferenceDeploymentResource{client: &coreweave.InferenceClient{
				DeploymentServiceClient: stubDeploymentServiceClient{
					getResp: &inferencev1.GetDeploymentResponse{Deployment: deploymentWith(tc.status,
						cond(conditionTypeResourcesApplied, tc.applied))},
				},
			}}
			conf := r.updateStateChangeConf(context.Background(), "dep-1")
			if len(conf.Target) != 1 || conf.Target[0] != resourcesAppliedState {
				t.Fatalf("Target = %v, want [%q]", conf.Target, resourcesAppliedState)
			}
			if conf.Delay != 0 {
				t.Fatalf("Delay = %v, want no rollout delay", conf.Delay)
			}
			if conf.Timeout != 20*time.Minute {
				t.Fatalf("Timeout = %v, want 20m", conf.Timeout)
			}
			_, state, err := conf.Refresh()
			if !errors.Is(err, tc.wantErr) || state != tc.wantState {
				t.Fatalf("Refresh = (%q, %v), want (%q, %v)", state, err, tc.wantState, tc.wantErr)
			}
		})
	}
}

// TestDeleteStateChangeConf verifies Delete tolerates every live status as Pending — including
// STATUS_ERROR and STATUS_FAILED — so a broken deployment can still be destroyed. Omitting a
// live state makes StateChangeConf reject the first poll as an unexpected state and fail Delete.
func TestDeleteStateChangeConf(t *testing.T) {
	t.Parallel()

	r := &InferenceDeploymentResource{}
	conf := r.deleteStateChangeConf(context.Background(), "dep-1")

	wantPending := map[string]bool{
		inferencev1.Status_STATUS_READY.String():       true,
		inferencev1.Status_STATUS_UPDATING.String():    true,
		inferencev1.Status_STATUS_CREATING.String():    true,
		inferencev1.Status_STATUS_DELETING.String():    true,
		inferencev1.Status_STATUS_ERROR.String():       true,
		inferencev1.Status_STATUS_FAILED.String():      true,
		inferencev1.Status_STATUS_UNSPECIFIED.String(): true,
	}
	got := make(map[string]bool, len(conf.Pending))
	for _, s := range conf.Pending {
		got[s] = true
	}
	for want := range wantPending {
		if !got[want] {
			t.Errorf("Pending missing %q; got %v", want, conf.Pending)
		}
	}
	if len(conf.Target) != 1 || conf.Target[0] != deletedState {
		t.Fatalf("Target = %v, want [%q]", conf.Target, deletedState)
	}
	if conf.Refresh == nil {
		t.Fatalf("Refresh must be set")
	}
}

// TestDeletedRefresh checks Delete's poller reports the synthetic deleted state on
// NotFound, surfaces any live status (so it can be tolerated as Pending), and
// propagates non-NotFound poll errors.
func TestDeletedRefresh(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		deployment *inferencev1.Deployment
		getErr     error
		wantState  string
		wantErr    bool
	}{
		"not found completes deletion": {
			getErr:    connect.NewError(connect.CodeNotFound, "gone"),
			wantState: deletedState,
		},
		"still ready keeps waiting": {
			deployment: deploymentWith(inferencev1.Status_STATUS_READY),
			wantState:  inferencev1.Status_STATUS_READY.String(),
		},
		"still updating keeps waiting": {
			deployment: deploymentWith(inferencev1.Status_STATUS_UPDATING),
			wantState:  inferencev1.Status_STATUS_UPDATING.String(),
		},
		"deleting keeps waiting": {
			deployment: deploymentWith(inferencev1.Status_STATUS_DELETING),
			wantState:  inferencev1.Status_STATUS_DELETING.String(),
		},
		"error keeps waiting so a broken deployment can be destroyed": {
			deployment: deploymentWith(inferencev1.Status_STATUS_ERROR),
			wantState:  inferencev1.Status_STATUS_ERROR.String(),
		},
		"failed keeps waiting so a broken deployment can be destroyed": {
			deployment: deploymentWith(inferencev1.Status_STATUS_FAILED),
			wantState:  inferencev1.Status_STATUS_FAILED.String(),
		},
		"non-notfound error surfaces": {
			getErr:    connect.NewError(connect.CodeUnavailable, "boom"),
			wantState: inferencev1.Status_STATUS_UNSPECIFIED.String(),
			wantErr:   true,
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

			_, state, err := r.deletedRefresh(context.Background(), "dep-1")()

			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got none")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if state != tc.wantState {
				t.Fatalf("state = %q, want %q", state, tc.wantState)
			}
		})
	}
}
