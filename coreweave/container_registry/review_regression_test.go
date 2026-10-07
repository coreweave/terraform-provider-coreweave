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
			h := newHarness(t, kind)
			h.fake.policyError = connect.NewError(connect.CodeInvalidArgument, errors.New("invalid policy"))
			created := h.apply(h.withContent(h.config(nil, false)), tftypes.NewValue(h.typ, nil), nil)
			require.NotEmpty(t, created.Diagnostics)
			require.True(t, h.decode(created.NewState).IsNull())
			require.Empty(t, h.fake.accessUpdates)
			require.Empty(t, h.fake.lifecycleUpdates)
		})
	}
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
