package containerregistry_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"connectrpc.com/connect"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
)

// refresh executes Terraform's observational Read path with persisted private state.
func (h *harness) refresh(r *tfprotov6.ApplyResourceChangeResponse) *tfprotov6.ReadResourceResponse {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.t.Context(), time.Second)
	defer cancel()
	result, err := h.server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: h.name, CurrentState: r.NewState, Private: r.Private})
	require.NoError(h.t, err)
	return result
}

// shortTimeout configures complete action deadlines for interrupted-operation regressions.
func (h *harness) shortTimeout(config tftypes.Value, action string) tftypes.Value {
	h.t.Helper()
	var values map[string]tftypes.Value
	require.NoError(h.t, config.As(&values))
	typ := values["timeouts"].Type()
	timeout := nullObject(typ)
	timeout[action] = tftypes.NewValue(tftypes.String, "50ms")
	values["timeouts"] = tftypes.NewValue(typ, timeout)
	return tftypes.NewValue(h.typ, values)
}

// TestRefreshNeverReplaysForceDelete prevents a plan from deleting images after a failed destroy.
func TestRefreshNeverReplaysForceDelete(t *testing.T) {
	h := newHarness(t, "namespace")
	config := h.shortTimeout(h.config(nil, false), "delete")
	var values map[string]tftypes.Value
	require.NoError(t, config.As(&values))
	values["force_destroy"] = tftypes.NewValue(tftypes.Bool, true)
	created := h.apply(tftypes.NewValue(h.typ, values), tftypes.NewValue(h.typ, nil), nil)
	noErrors(t, created.Diagnostics)
	h.fake.deleteError = connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
	deleted := h.destroy(created.NewState, created.Private)
	require.NotEmpty(t, deleted.Diagnostics)
	require.NotEmpty(t, deleted.Private)
	requests := len(h.fake.deletes)
	h.fake.deleteError = nil
	read := h.refresh(deleted)
	noErrors(t, read.Diagnostics)
	require.Len(t, h.fake.deletes, requests)
	require.NotNil(t, h.fake.namespace)
	// Reverting the intended deletion must remain an ordinary safe apply.
	result := h.apply(h.config(nil, false), h.decode(read.NewState), read.Private)
	noErrors(t, result.Diagnostics)
	require.Len(t, h.fake.deletes, requests)
}

// TestRefreshDoesNotWaitForPendingCreate preserves partial state with a single status read.
func TestRefreshDoesNotWaitForPendingCreate(t *testing.T) {
	h := newHarness(t, "namespace")
	h.fake.pending = true
	created := h.apply(h.shortTimeout(h.config(nil, false), "create"), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, created.Diagnostics)
	before := h.fake.operationGets
	read := h.refresh(created)
	noErrors(t, read.Diagnostics)
	require.Equal(t, before+1, h.fake.operationGets)
	require.False(t, h.decode(read.NewState).IsNull())
	// A visibility gap must not silently orphan the pending namespace.
	h.fake.namespace = nil
	read = h.refresh(created)
	require.NotEmpty(t, read.Diagnostics)
	require.False(t, h.decode(read.NewState).IsNull())
	deleted := h.destroy(created.NewState, created.Private)
	require.NotEmpty(t, deleted.Diagnostics)
	require.False(t, h.decode(deleted.NewState).IsNull())
}

// TestRejectedSingletonCreateNeverOwnsExistingPolicy prevents tainted replacement from resetting an unwritten policy.
func TestRejectedSingletonCreateNeverOwnsExistingPolicy(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			for _, refreshFails := range []bool{false, true} {
				t.Run(fmt.Sprintf("refresh_fails_%t", refreshFails), func(t *testing.T) {
					h := newHarness(t, kind)
					h.fake.policyError = connect.NewError(connect.CodeInvalidArgument, errors.New("invalid policy"))
					if refreshFails {
						h.fake.policyReadErrorAfter = 1
						h.fake.policyReadError = connect.NewError(connect.CodePermissionDenied, errors.New("refresh denied"))
					}
					created := h.apply(h.withContent(h.config(nil, false)), tftypes.NewValue(h.typ, nil), nil)
					require.NotEmpty(t, created.Diagnostics)
					require.True(t, h.decode(created.NewState).IsNull())
					require.Empty(t, created.Private)
					require.Equal(t, 1, h.fake.policyReads, "a rejected write must not depend on a verification GET")
					require.Empty(t, h.fake.accessUpdates)
					require.Empty(t, h.fake.lifecycleUpdates)
				})
			}
		})
	}
}

// TestRejectedPolicyUpdateRetainsOwnership preserves existing state when rejection is followed by a failed refresh.
func TestRejectedPolicyUpdateRetainsOwnership(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			created := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, created.Diagnostics)
			h.fake.policyError = connect.NewError(connect.CodeInvalidArgument, errors.New("invalid policy"))
			h.fake.policyReads = 0
			h.fake.policyReadErrorAfter = 1
			h.fake.policyReadError = connect.NewError(connect.CodePermissionDenied, errors.New("refresh denied"))
			updated := h.apply(h.withContent(h.config(nil, false)), h.decode(created.NewState), created.Private)
			require.NotEmpty(t, updated.Diagnostics)
			require.True(t, h.decode(updated.NewState).Equal(h.decode(created.NewState)))
			require.Empty(t, updated.Private)
			require.Empty(t, h.fake.accessUpdates)
			require.Empty(t, h.fake.lifecycleUpdates)
		})
	}
}

// TestUncertainAccessCreateKeepsOwnershipWithoutRefresh preserves recovery after a lost write response and failed GET.
func TestUncertainAccessCreateKeepsOwnershipWithoutRefresh(t *testing.T) {
	h := newHarness(t, "access_configuration")
	h.fake.uncertainAccess = true
	h.fake.policyReadErrorAfter = 1
	h.fake.policyReadError = connect.NewError(connect.CodePermissionDenied, errors.New("refresh denied"))
	created := h.apply(h.withContent(h.config(nil, false)), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, created.Diagnostics)
	require.False(t, h.decode(created.NewState).IsNull())
	require.NotEmpty(t, created.Private)
	require.Len(t, h.fake.accessUpdates, 1)
	h.fake.policyReadError = nil
	read := h.refresh(created)
	noErrors(t, read.Diagnostics)
	var state map[string]tftypes.Value
	require.NoError(t, h.decode(read.NewState).As(&state))
	require.True(t, state["revision"].Equal(tftypes.NewValue(tftypes.Number, 2)))
	require.Len(t, h.fake.accessUpdates, 1)
}

// TestSupersededAccessRecoveryClearsOnRefresh allows a new reviewed plan after another writer wins.
func TestSupersededAccessRecoveryClearsOnRefresh(t *testing.T) {
	h := newHarness(t, "access_configuration")
	h.fake.competing = true
	created := h.apply(h.withContent(h.config(nil, false)), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, created.Diagnostics)
	read := h.refresh(created)
	noErrors(t, read.Diagnostics)
	h.fake.competing = false
	result := h.apply(h.withContent(h.config(nil, false)), h.decode(read.NewState), read.Private)
	noErrors(t, result.Diagnostics)
	require.Len(t, h.fake.accessUpdates, 1)
}

// TestPolicyRevisionJumpAndLag accepts server-assigned revisions and waits through an older observation.
func TestPolicyRevisionJumpAndLag(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			h.fake.revisionStep = 5
			h.fake.lagAccess = true
			created := h.apply(h.withContent(h.config(nil, false)), tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, created.Diagnostics)
		})
	}
}

// TestNonActiveParentDoesNotBlockDestroy allows cleanup after provisioning failure.
func TestNonActiveParentDoesNotBlockDestroy(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			created := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, created.Diagnostics)
			h.fake.namespace.State = api.RegistryNamespace_STATE_FAILED
			deleted := h.destroy(created.NewState, created.Private)
			noErrors(t, deleted.Diagnostics)
			require.True(t, h.decode(deleted.NewState).IsNull())
		})
	}
}

// TestNonActiveNamespaceAllowsForceDestroyUpdate persists local cleanup settings without mutating remote quota.
func TestNonActiveNamespaceAllowsForceDestroyUpdate(t *testing.T) {
	for _, status := range []api.RegistryNamespace_State{api.RegistryNamespace_STATE_FAILED, api.RegistryNamespace_STATE_UNSPECIFIED} {
		t.Run(status.String(), func(t *testing.T) {
			h := newHarness(t, "namespace")
			created := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, created.Diagnostics)
			h.fake.namespace.State = status
			var values map[string]tftypes.Value
			require.NoError(t, h.config(nil, false).As(&values))
			values["force_destroy"] = tftypes.NewValue(tftypes.Bool, true)
			updated := h.apply(tftypes.NewValue(h.typ, values), h.decode(created.NewState), created.Private)
			noErrors(t, updated.Diagnostics)
			var state map[string]tftypes.Value
			require.NoError(t, h.decode(updated.NewState).As(&state))
			require.True(t, state["force_destroy"].Equal(tftypes.NewValue(tftypes.Bool, true)))
			require.True(t, state["status"].Equal(tftypes.NewValue(tftypes.String, status.String())))
			require.Empty(t, h.fake.updates)
			deleted := h.destroy(updated.NewState, updated.Private)
			noErrors(t, deleted.Diagnostics)
			require.True(t, h.decode(deleted.NewState).IsNull())
			require.Len(t, h.fake.deletes, 1)
			require.True(t, h.fake.deletes[0].Force)
		})
	}
}

// TestNonActiveNamespaceQuotaUpdateRequiresActive retains the completion check for remote quota mutations.
func TestNonActiveNamespaceQuotaUpdateRequiresActive(t *testing.T) {
	h := newHarness(t, "namespace")
	created := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
	noErrors(t, created.Diagnostics)
	h.fake.namespace.State = api.RegistryNamespace_STATE_FAILED
	updated := h.apply(h.config(int64(1024), false), h.decode(created.NewState), created.Private)
	require.NotEmpty(t, updated.Diagnostics)
	require.Contains(t, updated.Diagnostics[0].Detail, "STATE_FAILED after completion")
	require.Len(t, h.fake.updates, 1)
}

// TestPollingPermissionFailureKeepsAcceptedEvidence distinguishes a failed GET from a rejected write.
func TestPollingPermissionFailureKeepsAcceptedEvidence(t *testing.T) {
	h := newHarness(t, "access_configuration")
	h.fake.pollAccessError = connect.NewError(connect.CodePermissionDenied, errors.New("poll denied"))
	created := h.apply(h.withContent(h.config(nil, false)), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, created.Diagnostics)
	require.NotEmpty(t, created.Private)
	h.fake.pollAccessError = nil
	read := h.refresh(created)
	noErrors(t, read.Diagnostics)
	require.Len(t, h.fake.accessUpdates, 1)
}

// TestTerminalCreateFailureReleasesInvisibleState permits a fresh attempt after a definite server failure.
func TestTerminalCreateFailureReleasesInvisibleState(t *testing.T) {
	h := newHarness(t, "namespace")
	h.fake.pending = true
	created := h.apply(h.shortTimeout(h.config(nil, false), "create"), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, created.Diagnostics)
	h.fake.namespace = nil
	h.fake.operationFailure = &statuspb.Status{Code: int32(connect.CodeFailedPrecondition), Message: "provisioning failed"}
	read := h.refresh(created)
	noErrors(t, read.Diagnostics)
	require.True(t, h.decode(read.NewState).IsNull())
}

// TestUnacknowledgedLifecycleHandoffIsBounded keeps refresh responsive when the default policy has no acknowledgement.
func TestUnacknowledgedLifecycleHandoffIsBounded(t *testing.T) {
	h := newHarness(t, "lifecycle_policy")
	h.fake.lifecycle.AppliedRevision = nil
	created := h.apply(h.shortTimeout(h.config(nil, false), "create"), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, created.Diagnostics)
	require.False(t, h.decode(created.NewState).IsNull())
	require.Empty(t, h.fake.lifecycleUpdates)
	read := h.refresh(created)
	noErrors(t, read.Diagnostics)
	require.False(t, h.decode(read.NewState).IsNull())
}

// TestAbsentCreateWithTerminalOrExpiredOperation releases absent state without replaying writes.
func TestAbsentCreateWithTerminalOrExpiredOperation(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired_%t", expired), func(t *testing.T) {
			h := newHarness(t, "namespace")
			h.fake.pending = true
			created := h.apply(h.shortTimeout(h.config(nil, false), "create"), tftypes.NewValue(h.typ, nil), nil)
			require.NotEmpty(t, created.Diagnostics)
			if expired {
				h.fake.operationError = connect.NewError(connect.CodeNotFound, errors.New("expired terminal operation"))
			} else {
				h.fake.operationResult = completed(h.fake.namespace).Msg
			}
			h.fake.namespace = nil
			read := h.refresh(created)
			noErrors(t, read.Diagnostics)
			require.True(t, h.decode(read.NewState).IsNull())
			require.Len(t, h.fake.creates, 1)
			require.Empty(t, h.fake.deletes)
		})
	}
}

// TestNamespaceUpdateAfterRecoveryRequiresReplan prevents another mutation under the stale plan.
func TestNamespaceUpdateAfterRecoveryRequiresReplan(t *testing.T) {
	h := newHarness(t, "namespace")
	h.fake.pending = true
	created := h.apply(h.shortTimeout(h.config(nil, false), "create"), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, created.Diagnostics)
	h.fake.pending = false
	h.fake.namespace.Etag = "completed-later"
	updated := h.apply(h.config(int64(1024), false), h.decode(created.NewState), created.Private)
	require.NotEmpty(t, updated.Diagnostics)
	require.Contains(t, updated.Diagnostics[0].Detail, "refresh and replan")
	require.Empty(t, h.fake.updates)
	var state map[string]tftypes.Value
	require.NoError(t, h.decode(updated.NewState).As(&state))
	var etag string
	require.NoError(t, state["etag"].As(&etag))
	require.Equal(t, "completed-later", etag)
}

// TestFailedPolicyUpdateRetainsPriorState never saves desired content without an observation.
func TestFailedPolicyUpdateRetainsPriorState(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			created := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, created.Diagnostics)
			h.fake.readError = connect.NewError(connect.CodePermissionDenied, errors.New("parent read denied"))
			h.fake.policyReadError = connect.NewError(connect.CodePermissionDenied, errors.New("policy refresh denied"))
			h.fake.policyReads = 0
			failed := h.apply(h.withContent(h.config(nil, false)), h.decode(created.NewState), created.Private)
			require.NotEmpty(t, failed.Diagnostics)
			require.True(t, h.decode(failed.NewState).Equal(h.decode(created.NewState)))
			require.Equal(t, 1, h.fake.policyReads, "fallback refresh must be attempted")
			require.Empty(t, h.fake.accessUpdates)
			require.Empty(t, h.fake.lifecycleUpdates)
		})
	}
}

// TestFailedPolicyUpdateKeepsObservedWrite preserves returned content when rollout times out.
func TestFailedPolicyUpdateKeepsObservedWrite(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			config := h.shortTimeout(h.config(nil, false), "update")
			created := h.apply(config, tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, created.Diagnostics)
			h.fake.policyUnacknowledged = true
			desired := h.withContent(config)
			failed := h.apply(desired, h.decode(created.NewState), created.Private)
			require.NotEmpty(t, failed.Diagnostics)
			require.NotEmpty(t, failed.Private)
			var state, content map[string]tftypes.Value
			require.NoError(t, h.decode(failed.NewState).As(&state))
			require.NoError(t, desired.As(&content))
			field := "policy_sets"
			if kind == "lifecycle_policy" {
				field = "rules"
			}
			require.True(t, state[field].Equal(content[field]), "observed write must survive failed polling")
			require.True(t, state["revision"].Equal(tftypes.NewValue(tftypes.Number, 2)))
			require.Equal(t, 1, len(h.fake.accessUpdates)+len(h.fake.lifecycleUpdates))
		})
	}
}

// TestFailedPolicyRecoveryKeepsNewObservation never overwrites recovered state with the plan.
func TestFailedPolicyRecoveryKeepsNewObservation(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			config := h.shortTimeout(h.config(nil, false), "create")
			h.fake.policyUnacknowledged = true
			created := h.apply(h.withContent(config), tftypes.NewValue(h.typ, nil), nil)
			require.NotEmpty(t, created.Diagnostics)
			require.NotEmpty(t, created.Private)
			// An external writer changes metadata while recovery is still pending.
			h.fake.access.Revision++
			h.fake.access.Etag = "access-3"
			h.fake.lifecycle.Revision++
			h.fake.lifecycle.Etag = "lifecycle-3"
			h.fake.policyReads = 0
			h.fake.policyReadErrorAfter = 1
			h.fake.policyReadError = connect.NewError(connect.CodePermissionDenied, errors.New("fallback refresh denied"))
			failed := h.apply(config, h.decode(created.NewState), created.Private)
			require.NotEmpty(t, failed.Diagnostics)
			var state map[string]tftypes.Value
			require.NoError(t, h.decode(failed.NewState).As(&state))
			require.True(t, state["revision"].Equal(tftypes.NewValue(tftypes.Number, 3)))
			require.True(t, state["etag"].Equal(tftypes.NewValue(tftypes.String, map[string]string{"access_configuration": "access-3", "lifecycle_policy": "lifecycle-3"}[kind])))
			require.Equal(t, 2, h.fake.policyReads)
		})
	}
}

// TestFailedAccessUpdateKeepsResponse preserves accepted content without a successful polling GET.
func TestFailedAccessUpdateKeepsResponse(t *testing.T) {
	h := newHarness(t, "access_configuration")
	created := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
	noErrors(t, created.Diagnostics)
	h.fake.policyReads = 0
	h.fake.policyReadErrorAfter = 1
	h.fake.policyReadError = connect.NewError(connect.CodePermissionDenied, errors.New("poll and refresh denied"))
	desired := h.withContent(h.config(nil, false))
	failed := h.apply(desired, h.decode(created.NewState), created.Private)
	require.NotEmpty(t, failed.Diagnostics)
	require.NotEmpty(t, failed.Private)
	var state, content map[string]tftypes.Value
	require.NoError(t, h.decode(failed.NewState).As(&state))
	require.NoError(t, desired.As(&content))
	require.True(t, state["policy_sets"].Equal(content["policy_sets"]))
	require.True(t, state["revision"].Equal(tftypes.NewValue(tftypes.Number, 2)))
	require.Equal(t, 3, h.fake.policyReads)
	require.Len(t, h.fake.accessUpdates, 1)
}
