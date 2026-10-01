package inference

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"buf.build/gen/go/coreweave/inference/connectrpc/go/coreweave/inference/v1alpha1/inferencev1alpha1connect"
	v1 "buf.build/gen/go/coreweave/inference/protocolbuffers/go/coreweave/inference/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type hotLoadTestHandler struct {
	inferencev1alpha1connect.UnimplementedHotLoadServiceHandler
	operation *v1.HotLoad
	getErr    error
	cancelErr error
	canceled  bool
	createErr error
}

func (h *hotLoadTestHandler) CreateHotLoad(_ context.Context, req *connect.Request[v1.CreateHotLoadRequest]) (*connect.Response[v1.CreateHotLoadResponse], error) {
	if !operationUUIDPattern.MatchString(req.Msg.HotLoadId) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("missing stable operation UUID"))
	}
	h.operation = &v1.HotLoad{Spec: &v1.HotLoadSpec{Id: req.Msg.HotLoadId, DeploymentId: req.Msg.DeploymentId, Identity: req.Msg.Identity, Type: req.Msg.Type, PromptCachePolicy: req.Msg.PromptCachePolicy}, Status: &v1.HotLoadStatus{State: v1.HotLoadState_HOT_LOAD_STATE_PENDING}}
	if h.createErr != nil {
		return nil, h.createErr
	}
	return connect.NewResponse(&v1.CreateHotLoadResponse{HotLoad: h.operation}), nil
}

func (h *hotLoadTestHandler) GetHotLoad(_ context.Context, _ *connect.Request[v1.GetHotLoadRequest]) (*connect.Response[v1.GetHotLoadResponse], error) {
	if h.getErr != nil {
		return nil, h.getErr
	}
	return connect.NewResponse(&v1.GetHotLoadResponse{HotLoad: h.operation}), nil
}

func (h *hotLoadTestHandler) CancelHotLoad(_ context.Context, _ *connect.Request[v1.CancelHotLoadRequest]) (*connect.Response[v1.CancelHotLoadResponse], error) {
	h.canceled = true
	if h.cancelErr != nil {
		return nil, h.cancelErr
	}
	h.operation.Status.State = v1.HotLoadState_HOT_LOAD_STATE_CANCELED
	return connect.NewResponse(&v1.CancelHotLoadResponse{HotLoad: h.operation}), nil
}

func hotLoadResourceFixture(t *testing.T, createErr error) (*InferenceHotLoadResource, *hotLoadTestHandler, tfsdk.State) {
	t.Helper()
	h := &hotLoadTestHandler{createErr: createErr}
	mux := http.NewServeMux()
	url, handler := inferencev1alpha1connect.NewHotLoadServiceHandler(h)
	mux.Handle(url, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r := &InferenceHotLoadResource{client: &coreweave.InferenceClient{HotLoadServiceClient: inferencev1alpha1connect.NewHotLoadServiceClient(srv.Client(), srv.URL)}}
	var schemaResp resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schemaResp)
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	m := &InferenceHotLoadResourceModel{
		DeploymentID: types.StringValue("22222222-2222-4222-8222-222222222222"), Identity: types.StringValue("step1"),
		Type: types.StringValue("SNAPSHOT_TYPE_FULL"), PromptCachePolicy: types.StringValue("PROMPT_CACHE_POLICY_PRESERVE"),
		SnapshotChain: types.ListUnknown(types.ObjectType{AttrTypes: snapshotChainTypes}),
		Conditions:    types.ListUnknown(types.ObjectType{AttrTypes: conditionAttrTypes}),
	}
	if diags := plan.Set(t.Context(), m); diags.HasError() {
		t.Fatal(diags)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	return r, h, resp.State
}

func TestHotLoadResourceLifecycle(t *testing.T) {
	r, h, state := hotLoadResourceFixture(t, nil)
	h.operation.Status.State = v1.HotLoadState_HOT_LOAD_STATE_COMPLETED
	read := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var got InferenceHotLoadResourceModel
	if diags := read.State.Get(t.Context(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	if got.State.ValueString() != "HOT_LOAD_STATE_COMPLETED" || got.Identity.ValueString() != "step1" {
		t.Fatalf("bad refreshed state: %#v", got)
	}
	deleted := resource.DeleteResponse{State: read.State}
	r.Delete(t.Context(), resource.DeleteRequest{State: read.State}, &deleted)
	if deleted.Diagnostics.HasError() || h.canceled {
		t.Fatal("terminal operation must not be canceled", deleted.Diagnostics)
	}
}

func TestHotLoadDestroy(t *testing.T) {
	for name, tc := range map[string]struct {
		getErr, cancelErr error
		state             v1.HotLoadState
		cancel, fail      bool
	}{
		"pending":       {state: v1.HotLoadState_HOT_LOAD_STATE_PENDING, cancel: true},
		"updating":      {state: v1.HotLoadState_HOT_LOAD_STATE_IN_PROGRESS, cancel: true},
		"canceled":      {state: v1.HotLoadState_HOT_LOAD_STATE_CANCELED},
		"failed":        {state: v1.HotLoadState_HOT_LOAD_STATE_FAILED},
		"missing":       {getErr: connect.NewError(connect.CodeNotFound, errors.New("missing"))},
		"read error":    {getErr: connect.NewError(connect.CodeUnavailable, errors.New("offline")), fail: true},
		"cancel error":  {state: v1.HotLoadState_HOT_LOAD_STATE_PENDING, cancelErr: connect.NewError(connect.CodeUnavailable, errors.New("offline")), cancel: true, fail: true},
		"unknown state": {state: v1.HotLoadState_HOT_LOAD_STATE_UNSPECIFIED, fail: true},
	} {
		t.Run(name, func(t *testing.T) {
			r, h, state := hotLoadResourceFixture(t, nil)
			h.operation.Status.State, h.getErr, h.cancelErr = tc.state, tc.getErr, tc.cancelErr
			resp := resource.DeleteResponse{State: state}
			r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.fail || h.canceled != tc.cancel {
				t.Fatalf("cancel=%v diagnostics=%v", h.canceled, resp.Diagnostics)
			}
		})
	}
}

func TestHotLoadCreateAfterLostResponse(t *testing.T) {
	_, h, _ := hotLoadResourceFixture(t, connect.NewError(connect.CodeAlreadyExists, errors.New("already accepted")))
	if h.operation == nil {
		t.Fatal("accepted operation was not recovered")
	}
}

func TestHotLoadConfigValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		kind     types.String
		metadata *IncrementalSnapshotModel
		fail     bool
	}{
		"full":                         {kind: types.StringValue("SNAPSHOT_TYPE_FULL")},
		"incremental missing metadata": {kind: types.StringValue("SNAPSHOT_TYPE_INCREMENTAL"), fail: true},
		"unknown type":                 {kind: types.StringUnknown()},
		"full with metadata":           {kind: types.StringValue("SNAPSHOT_TYPE_FULL"), metadata: &IncrementalSnapshotModel{}, fail: true},
		"incremental": {kind: types.StringValue("SNAPSHOT_TYPE_INCREMENTAL"), metadata: &IncrementalSnapshotModel{
			PreviousSnapshotIdentity: types.StringValue("step0"), CompressionFormat: types.StringValue("COMPRESSION_FORMAT_ZSTD"), ChecksumFormat: types.StringValue("CHECKSUM_FORMAT_ADLER32"),
		}},
	} {
		t.Run(name, func(t *testing.T) {
			r := &InferenceHotLoadResource{}
			var s resource.SchemaResponse
			r.Schema(t.Context(), resource.SchemaRequest{}, &s)
			plan := tfsdk.Plan{Schema: s.Schema}
			m := InferenceHotLoadResourceModel{Type: tc.kind, IncrementalSnapshotMetadata: tc.metadata,
				SnapshotChain: types.ListNull(types.ObjectType{AttrTypes: snapshotChainTypes}), Conditions: types.ListNull(types.ObjectType{AttrTypes: conditionAttrTypes})}
			if diags := plan.Set(t.Context(), &m); diags.HasError() {
				t.Fatal(diags)
			}
			var resp resource.ValidateConfigResponse
			r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s.Schema, Raw: plan.Raw}}, &resp)
			if resp.Diagnostics.HasError() != tc.fail {
				t.Fatalf("validation diagnostics: %v", resp.Diagnostics)
			}
			if tc.metadata != nil && !tc.fail {
				in := m.request()
				if in.GetIncrementalSnapshotMetadata().GetPreviousSnapshotIdentity() != "step0" || in.GetIncrementalSnapshotMetadata().GetCompressionFormat() != v1.CompressionFormat_COMPRESSION_FORMAT_ZSTD {
					t.Fatal("incremental metadata lost")
				}
			}
		})
	}
}

func TestHotLoadReadMissingAndError(t *testing.T) {
	for name, code := range map[string]connect.Code{"missing": connect.CodeNotFound, "offline": connect.CodeUnavailable} {
		t.Run(name, func(t *testing.T) {
			r, h, state := hotLoadResourceFixture(t, nil)
			h.getErr = connect.NewError(code, errors.New(name))
			resp := resource.ReadResponse{State: state}
			r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
			if code == connect.CodeNotFound {
				if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
					t.Fatal("missing operation should leave state", resp.Diagnostics)
				}
			} else if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
				t.Fatal("API error must retain state", resp.Diagnostics)
			}
		})
	}
}

func TestHotLoadImport(t *testing.T) {
	r, _, state := hotLoadResourceFixture(t, nil)
	resp := resource.ImportStateResponse{State: state}
	const id = "33333333-3333-4333-8333-333333333333"
	r.ImportState(t.Context(), resource.ImportStateRequest{ID: id}, &resp)
	var got types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(t.Context(), path.Root("id"), &got)...)
	if resp.Diagnostics.HasError() || got.ValueString() != id {
		t.Fatal(resp.Diagnostics, got)
	}
}

func TestHotLoadPathValidation(t *testing.T) {
	for value, fail := range map[string]bool{"": false, "runs/model": false, "model..v2": false, "..": true, "../run": true, "run/../step": true, "run/..": true} {
		t.Run(value, func(t *testing.T) {
			var resp validator.StringResponse
			hotLoadPathValidator{}.ValidateString(t.Context(), validator.StringRequest{ConfigValue: types.StringValue(value)}, &resp)
			if resp.Diagnostics.HasError() != fail {
				t.Fatalf("%q diagnostics: %v", value, resp.Diagnostics)
			}
		})
	}
}
