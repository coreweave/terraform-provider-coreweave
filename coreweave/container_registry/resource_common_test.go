package containerregistry_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/coreweave/registry/v1alpha1/registryv1alpha1connect"
	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeRegistry struct {
	lastAuth, lastUserAgent string
	uncertainAccess         bool
	client.UnimplementedRegistryServiceHandler
	mu                                  sync.Mutex
	namespace                           *api.RegistryNamespace
	access                              *api.RegistryAccessConfiguration
	lifecycle                           *api.RegistryLifecyclePolicy
	creates                             []*api.CreateRegistryNamespaceRequest
	updates                             []*api.UpdateRegistryNamespaceRequest
	deletes                             []*api.DeleteRegistryNamespaceRequest
	accessUpdates                       []*api.UpdateRegistryAccessConfigurationRequest
	lifecycleUpdates                    []*api.UpdateRegistryLifecyclePolicyRequest
	createError, readError, deleteError error
	policyError                         error
	revisionStep                        int64
	lagAccess                           bool
	pollAccessError                     error
	transientCreate                     bool
	pending, competing                  bool
	operationGets                       int
	operationFailure                    *statuspb.Status
	operationError                      error
	operationResult                     *longrunningpb.Operation
}

// completed constructs a typed immediate LRO response.
func completed(m proto.Message) *connect.Response[longrunningpb.Operation] {
	a, _ := anypb.New(m)
	return connect.NewResponse(&longrunningpb.Operation{Name: "namespaces/example-images/operations/00000000-0000-4000-8000-000000000000", Done: true, Result: &longrunningpb.Operation_Response{Response: a}})
}

// CreateRegistryNamespace records only input fields and creates the test-owned namespace.
func (f *fakeRegistry) CreateRegistryNamespace(_ context.Context, q *connect.Request[api.CreateRegistryNamespaceRequest]) (*connect.Response[longrunningpb.Operation], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !canonicalUUIDv4(q.Msg.IdempotencyKey) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("idempotency key must be a canonical UUIDv4"))
	}
	f.lastAuth = q.Header().Get("Authorization")
	f.lastUserAgent = q.Header().Get("User-Agent")
	f.creates = append(f.creates, proto.Clone(q.Msg).(*api.CreateRegistryNamespaceRequest))
	if f.transientCreate && len(f.creates) == 1 {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("retry"))
	}
	if f.createError != nil {
		return nil, f.createError
	}
	f.namespace = proto.Clone(q.Msg.RegistryNamespace).(*api.RegistryNamespace)
	f.namespace.Name = "namespaces/" + q.Msg.RegistryNamespaceId
	f.namespace.Etag = "namespace-1"
	f.namespace.State = api.RegistryNamespace_STATE_ACTIVE
	if f.pending {
		return connect.NewResponse(&longrunningpb.Operation{Name: f.namespace.Name + "/operations/00000000-0000-4000-8000-000000000000"}), nil
	}
	return completed(f.namespace), nil
}

// GetRegistryNamespace distinguishes absence from permission or transport failures.
func (f *fakeRegistry) GetRegistryNamespace(context.Context, *connect.Request[api.GetRegistryNamespaceRequest]) (*connect.Response[api.RegistryNamespace], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readError != nil {
		return nil, f.readError
	}
	if f.namespace == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("missing"))
	}
	return connect.NewResponse(proto.Clone(f.namespace).(*api.RegistryNamespace)), nil
}

// UpdateRegistryNamespace records quota pointer presence and update masks.
func (f *fakeRegistry) UpdateRegistryNamespace(_ context.Context, q *connect.Request[api.UpdateRegistryNamespaceRequest]) (*connect.Response[longrunningpb.Operation], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !canonicalUUIDv4(q.Msg.IdempotencyKey) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("idempotency key must be a canonical UUIDv4"))
	}
	f.updates = append(f.updates, proto.Clone(q.Msg).(*api.UpdateRegistryNamespaceRequest))
	f.namespace.StorageQuotaBytes = q.Msg.RegistryNamespace.StorageQuotaBytes
	f.namespace.Etag = fmt.Sprintf("namespace-%d", len(f.updates)+1)
	return completed(f.namespace), nil
}

// DeleteRegistryNamespace returns an ephemeral completed operation.
func (f *fakeRegistry) DeleteRegistryNamespace(_ context.Context, q *connect.Request[api.DeleteRegistryNamespaceRequest]) (*connect.Response[longrunningpb.Operation], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !canonicalUUIDv4(q.Msg.IdempotencyKey) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("idempotency key must be a canonical UUIDv4"))
	}
	f.deletes = append(f.deletes, proto.Clone(q.Msg).(*api.DeleteRegistryNamespaceRequest))
	if f.deleteError != nil {
		return nil, f.deleteError
	}
	f.namespace = nil
	return completed(&emptypb.Empty{}), nil
}

// GetOperation models pending operations without incorrectly serving immediate completions.
func (f *fakeRegistry) GetOperation(context.Context, *connect.Request[api.RegistryServiceGetOperationRequest]) (*connect.Response[longrunningpb.Operation], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.operationGets++
	if f.operationError != nil {
		return nil, f.operationError
	}
	if f.operationResult != nil {
		return connect.NewResponse(proto.Clone(f.operationResult).(*longrunningpb.Operation)), nil
	}
	if f.operationFailure != nil {
		return connect.NewResponse(&longrunningpb.Operation{Name: "namespaces/example-images/operations/00000000-0000-4000-8000-000000000000", Done: true, Result: &longrunningpb.Operation_Error{Error: f.operationFailure}}), nil
	}
	if f.pending {
		return connect.NewResponse(&longrunningpb.Operation{Name: "namespaces/example-images/operations/00000000-0000-4000-8000-000000000000"}), nil
	}
	return completed(f.namespace), nil
}

// GetRegistryAccessConfiguration returns desired access state.
func (f *fakeRegistry) GetRegistryAccessConfiguration(context.Context, *connect.Request[api.GetRegistryAccessConfigurationRequest]) (*connect.Response[api.RegistryAccessConfiguration], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.access == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("missing singleton"))
	}
	if len(f.accessUpdates) > 0 && f.pollAccessError != nil {
		return nil, f.pollAccessError
	}
	result := proto.Clone(f.access).(*api.RegistryAccessConfiguration)
	if len(f.accessUpdates) > 0 && f.lagAccess {
		result.Revision--
		f.lagAccess = false
	}
	return connect.NewResponse(result), nil
}

// UpdateRegistryAccessConfiguration replaces all content with an etag precondition.
func (f *fakeRegistry) UpdateRegistryAccessConfiguration(_ context.Context, q *connect.Request[api.UpdateRegistryAccessConfigurationRequest]) (*connect.Response[api.RegistryAccessConfiguration], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if q.Msg.RegistryAccessConfiguration.GetName() != q.Msg.Parent+"/accessConfiguration" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("access configuration name must match parent"))
	}
	if q.Msg.Etag != f.access.Etag {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("etag mismatch"))
	}
	if f.policyError != nil {
		return nil, f.policyError
	}
	f.accessUpdates = append(f.accessUpdates, proto.Clone(q.Msg).(*api.UpdateRegistryAccessConfigurationRequest))
	step := f.revisionStep
	if step == 0 {
		step = 1
	}
	rev := f.access.Revision + step
	f.access = proto.Clone(q.Msg.RegistryAccessConfiguration).(*api.RegistryAccessConfiguration)
	f.access.Name = q.Msg.Parent + "/accessConfiguration"
	f.access.Revision = rev
	f.access.Etag = fmt.Sprintf("access-%d", rev)
	f.access.AccessConfigState = api.RegistryAccessConfiguration_ACCESS_CONFIG_STATE_ACCEPTED
	result := proto.Clone(f.access).(*api.RegistryAccessConfiguration)
	if f.competing {
		f.access.Revision++
	}
	if f.uncertainAccess && len(f.accessUpdates) == 1 {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
	}
	return connect.NewResponse(result), nil
}

// GetRegistryLifecyclePolicy returns desired lifecycle state and optional acknowledgement.
func (f *fakeRegistry) GetRegistryLifecyclePolicy(context.Context, *connect.Request[api.GetRegistryLifecyclePolicyRequest]) (*connect.Response[api.RegistryLifecyclePolicy], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return connect.NewResponse(proto.Clone(f.lifecycle).(*api.RegistryLifecyclePolicy)), nil
}

// UpdateRegistryLifecyclePolicy returns the compact acknowledgement, not a policy object.
func (f *fakeRegistry) UpdateRegistryLifecyclePolicy(_ context.Context, q *connect.Request[api.UpdateRegistryLifecyclePolicyRequest]) (*connect.Response[longrunningpb.Operation], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !canonicalUUIDv4(q.Msg.IdempotencyKey) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("idempotency key must be a canonical UUIDv4"))
	}
	if q.Msg.Etag != f.lifecycle.Etag {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("etag mismatch"))
	}
	if f.policyError != nil {
		return nil, f.policyError
	}
	f.lifecycleUpdates = append(f.lifecycleUpdates, proto.Clone(q.Msg).(*api.UpdateRegistryLifecyclePolicyRequest))
	step := f.revisionStep
	if step == 0 {
		step = 1
	}
	rev := f.lifecycle.Revision + step
	f.lifecycle = proto.Clone(q.Msg.RegistryLifecyclePolicy).(*api.RegistryLifecyclePolicy)
	f.lifecycle.Name = q.Msg.Parent + "/lifecyclePolicy"
	f.lifecycle.Revision = rev
	f.lifecycle.AppliedRevision = &rev
	f.lifecycle.Etag = fmt.Sprintf("lifecycle-%d", rev)
	return completed(&api.UpdateRegistryLifecyclePolicyResponse{Name: f.lifecycle.Name, AppliedRevision: rev}), nil
}

type harness struct {
	t        *testing.T
	server   tfprotov6.ProviderServer
	typ      tftypes.Type
	name     string
	fake     *fakeRegistry
	computed map[string]bool
}

// noErrors checks protocol diagnostics as well as transport errors.
func noErrors(t *testing.T, d []*tfprotov6.Diagnostic) {
	t.Helper()
	for _, v := range d {
		if v.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("%s: %s", v.Summary, v.Detail)
		}
	}
}

// nullObject constructs configuration with explicitly absent attributes.
func nullObject(typ tftypes.Type) map[string]tftypes.Value {
	m := map[string]tftypes.Value{}
	for k, t := range typ.(tftypes.Object).AttributeTypes {
		m[k] = tftypes.NewValue(t, nil)
	}
	return m
}

// newHarness runs the actual protocol-v6 provider against a fake Connect HTTP server.
func newHarness(t *testing.T, kind string) *harness {
	t.Helper()
	f := newFakeRegistry()
	mux := http.NewServeMux()
	mux.Handle(client.NewRegistryServiceHandler(f))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("COREWEAVE_API_ENDPOINT", srv.URL)
	t.Setenv("COREWEAVE_API_TOKEN", "test-token")
	server, e := provider.TestProtoV6ProviderFactories["coreweave"]()
	require.NoError(t, e)
	s, e := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, e)
	noErrors(t, s.Diagnostics)
	cfg, e := tfprotov6.NewDynamicValue(s.Provider.ValueType(), tftypes.NewValue(s.Provider.ValueType(), nullObject(s.Provider.ValueType())))
	require.NoError(t, e)
	configured, e := server.ConfigureProvider(t.Context(), &tfprotov6.ConfigureProviderRequest{Config: &cfg})
	require.NoError(t, e)
	noErrors(t, configured.Diagnostics)
	name := "coreweave_container_registry_" + kind
	rs := s.ResourceSchemas[name]
	require.NotNil(t, rs)
	h := &harness{t: t, server: server, typ: rs.ValueType(), name: name, fake: f, computed: map[string]bool{}}
	for _, a := range rs.Block.Attributes {
		h.computed[a.Name] = a.Computed
	}
	return h
}

// newFakeRegistry returns an isolated API fixture with acknowledged default policies.
func newFakeRegistry() *fakeRegistry {
	rev := int64(1)
	return &fakeRegistry{namespace: &api.RegistryNamespace{Name: "namespaces/example-images", Zone: "US-LAB-01A", State: api.RegistryNamespace_STATE_ACTIVE, Etag: "namespace-1"}, access: &api.RegistryAccessConfiguration{Name: "namespaces/example-images/accessConfiguration", Revision: 1, Etag: "access-1", AccessConfigState: api.RegistryAccessConfiguration_ACCESS_CONFIG_STATE_ACCEPTED}, lifecycle: &api.RegistryLifecyclePolicy{Name: "namespaces/example-images/lifecyclePolicy", Revision: 1, AppliedRevision: &rev, Etag: "lifecycle-1"}}
}

// dv serializes a typed protocol value.
func (h *harness) dv(v tftypes.Value) *tfprotov6.DynamicValue {
	h.t.Helper()
	d, e := tfprotov6.NewDynamicValue(h.typ, v)
	require.NoError(h.t, e)
	return &d
}

// decode decodes state using the registered provider schema.
func (h *harness) decode(v *tfprotov6.DynamicValue) tftypes.Value {
	h.t.Helper()
	x, e := v.Unmarshal(h.typ)
	require.NoError(h.t, e)
	return x
}

// config creates minimally configured resource inputs.
func (h *harness) config(quota any, bootstrap bool) tftypes.Value {
	h.t.Helper()
	m := nullObject(h.typ)
	if h.name == "coreweave_container_registry_namespace" {
		m["namespace_id"] = tftypes.NewValue(tftypes.String, "example-images")
		m["zone"] = tftypes.NewValue(tftypes.String, "US-LAB-01A")
		m["storage_quota_bytes"] = tftypes.NewValue(tftypes.Number, quota)
		if bootstrap {
			typ := m["initial_access_configuration"].Type()
			m["initial_access_configuration"] = tftypes.NewValue(typ, nullObject(typ))
		}
	} else {
		m["namespace"] = tftypes.NewValue(tftypes.String, "namespaces/example-images")
	}
	return tftypes.NewValue(h.typ, m)
}

// plan models Terraform's proposed state while leaving defaults to the Framework.
func (h *harness) plan(config, state tftypes.Value, private []byte) *tfprotov6.PlanResourceChangeResponse {
	h.t.Helper()
	var m map[string]tftypes.Value
	require.NoError(h.t, config.As(&m))
	m = maps.Clone(m)
	if !state.IsNull() {
		var old map[string]tftypes.Value
		require.NoError(h.t, state.As(&old))
		for k, c := range h.computed {
			if c && m[k].IsNull() {
				m[k] = old[k]
			}
		}
	}
	p, e := h.server.PlanResourceChange(h.t.Context(), &tfprotov6.PlanResourceChangeRequest{TypeName: h.name, Config: h.dv(config), PriorState: h.dv(state), ProposedNewState: h.dv(tftypes.NewValue(h.typ, m)), PriorPrivate: private})
	require.NoError(h.t, e)
	return p
}

// apply executes the provider's real Framework plan/apply path.
func (h *harness) apply(config, state tftypes.Value, private []byte) *tfprotov6.ApplyResourceChangeResponse {
	h.t.Helper()
	p := h.plan(config, state, private)
	noErrors(h.t, p.Diagnostics)
	r, e := h.server.ApplyResourceChange(h.t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: h.name, Config: h.dv(config), PriorState: h.dv(state), PlannedState: p.PlannedState, PlannedPrivate: p.PlannedPrivate})
	require.NoError(h.t, e)
	return r
}

// destroy executes the real Framework deletion path.
func (h *harness) destroy(state *tfprotov6.DynamicValue, private []byte) *tfprotov6.ApplyResourceChangeResponse {
	h.t.Helper()
	empty := h.dv(tftypes.NewValue(h.typ, nil))
	r, e := h.server.ApplyResourceChange(h.t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: h.name, Config: empty, PriorState: state, PlannedState: empty, PlannedPrivate: private})
	require.NoError(h.t, e)
	return r
}

// TestNamespaceQuotaAndBootstrap verifies optional presence, atomic bootstrap, stable plans and ephemeral deletion.
func TestNamespaceQuotaAndBootstrap(t *testing.T) {
	h := newHarness(t, "namespace")
	cfg := h.config(nil, true)
	r := h.apply(cfg, tftypes.NewValue(h.typ, nil), nil)
	noErrors(t, r.Diagnostics)
	require.NotNil(t, h.fake.creates[0].RegistryAccessConfiguration)
	require.Empty(t, h.fake.creates[0].RegistryNamespace.Name)
	require.Nil(t, h.fake.creates[0].RegistryNamespace.StorageQuotaBytes)
	for _, quota := range []any{int64(0), int64(100), nil} {
		cfg = h.config(quota, true)
		r = h.apply(cfg, h.decode(r.NewState), r.Private)
		noErrors(t, r.Diagnostics)
		q := h.fake.updates[len(h.fake.updates)-1]
		require.Equal(t, []string{"storage_quota_bytes"}, q.UpdateMask.Paths)
		if quota == nil {
			require.Nil(t, q.RegistryNamespace.StorageQuotaBytes)
		} else {
			require.Equal(t, uint64(quota.(int64)), *q.RegistryNamespace.StorageQuotaBytes)
		}
	}
	p := h.plan(cfg, h.decode(r.NewState), r.Private)
	noErrors(t, p.Diagnostics)
	require.True(t, h.decode(r.NewState).Equal(h.decode(p.PlannedState)), "state=%s plan=%s", h.decode(r.NewState), h.decode(p.PlannedState))
	deleted := h.destroy(r.NewState, r.Private)
	noErrors(t, deleted.Diagnostics)
	require.True(t, h.decode(deleted.NewState).IsNull())
	require.False(t, h.fake.deletes[0].Force)
	require.Zero(t, h.fake.operationGets)
}

// TestFailedCreateKeepsPrivateState verifies Framework partial-create persistence without adoption.
func TestFailedCreateKeepsPrivateState(t *testing.T) {
	h := newHarness(t, "namespace")
	h.fake.pending = true
	cfg := h.config(nil, false)
	var m map[string]tftypes.Value
	require.NoError(t, cfg.As(&m))
	tt := m["timeouts"].Type()
	v := nullObject(tt)
	v["create"] = tftypes.NewValue(tftypes.String, "250ms")
	m["timeouts"] = tftypes.NewValue(tt, v)
	r := h.apply(tftypes.NewValue(h.typ, m), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, r.Diagnostics)
	require.False(t, h.decode(r.NewState).IsNull())
	require.NotEmpty(t, r.Private)
	h.fake.pending = false
	read, e := h.server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: h.name, CurrentState: r.NewState, Private: r.Private})
	require.NoError(t, e)
	noErrors(t, read.Diagnostics)
}

// TestAlreadyExistsNeverAdopts verifies matching names are not ownership evidence.
func TestAlreadyExistsNeverAdopts(t *testing.T) {
	h := newHarness(t, "namespace")
	h.fake.createError = connect.NewError(connect.CodeAlreadyExists, errors.New("exists"))
	r := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, r.Diagnostics)
	require.True(t, h.decode(r.NewState).IsNull())
}

// TestSingletonNoopAndReset checks no-op handoff and full reset semantics.
func TestSingletonNoopAndReset(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			r := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, r.Diagnostics)
			require.Empty(t, h.fake.accessUpdates)
			require.Empty(t, h.fake.lifecycleUpdates)
			p := h.plan(h.config(nil, false), h.decode(r.NewState), r.Private)
			noErrors(t, p.Diagnostics)
			require.True(t, h.decode(r.NewState).Equal(h.decode(p.PlannedState)), "state=%s plan=%s", h.decode(r.NewState), h.decode(p.PlannedState))
			d := h.destroy(r.NewState, r.Private)
			noErrors(t, d.Diagnostics)
			require.True(t, h.decode(d.NewState).IsNull())
		})
	}
}

// TestNamespaceConflictAndNonemptyDelete retains state and never upgrades to force.
func TestNamespaceConflictAndNonemptyDelete(t *testing.T) {
	h := newHarness(t, "namespace")
	r := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
	noErrors(t, r.Diagnostics)
	h.fake.namespace.Etag = "other"
	updated := h.apply(h.config(int64(10), false), h.decode(r.NewState), r.Private)
	require.NotEmpty(t, updated.Diagnostics)
	require.Empty(t, h.fake.updates)
	h.fake.deleteError = connect.NewError(connect.CodeFailedPrecondition, errors.New("nonempty"))
	d := h.destroy(r.NewState, r.Private)
	require.NotEmpty(t, d.Diagnostics)
	require.False(t, h.decode(d.NewState).IsNull())
	require.False(t, h.fake.deletes[0].Force)
}

// withContent supplies real authoritative policy content through the protocol schema.
func (h *harness) withContent(config tftypes.Value) tftypes.Value {
	h.t.Helper()
	var a map[string]tftypes.Value
	require.NoError(h.t, config.As(&a))
	a = maps.Clone(a)
	if h.name == "coreweave_container_registry_access_configuration" {
		mt := a["policy_sets"].Type().(tftypes.Map)
		pt := mt.ElementType
		ps := nullObject(pt)
		ps["identity_selector"] = tftypes.NewValue(tftypes.String, "ANONYMOUS")
		rt := ps["rules"].Type().(tftypes.Map)
		rule := tftypes.NewValue(rt.ElementType, map[string]tftypes.Value{"expression": tftypes.NewValue(tftypes.String, "true")})
		ps["rules"] = tftypes.NewValue(rt, map[string]tftypes.Value{"pull": rule})
		a["policy_sets"] = tftypes.NewValue(mt, map[string]tftypes.Value{"readers": tftypes.NewValue(pt, ps)})
	} else {
		a["enabled"] = tftypes.NewValue(tftypes.Bool, true)
		mt := a["rules"].Type().(tftypes.Map)
		rule := nullObject(mt.ElementType)
		rule["regex"] = tftypes.NewValue(tftypes.String, "ci/.*")
		rule["keep_newest"] = tftypes.NewValue(tftypes.Number, 10)
		a["rules"] = tftypes.NewValue(mt, map[string]tftypes.Value{"expire": tftypes.NewValue(mt.ElementType, rule)})
	}
	return tftypes.NewValue(h.typ, a)
}

// TestSingletonReplacementAndDestroy verifies real mutations and full-document resets.
func TestSingletonReplacementAndDestroy(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			cfg := h.withContent(h.config(nil, false))
			r := h.apply(cfg, tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, r.Diagnostics)
			if kind == "access_configuration" {
				require.Len(t, h.fake.accessUpdates, 1)
				require.Len(t, h.fake.access.PolicySets, 1)
			} else {
				require.Len(t, h.fake.lifecycleUpdates, 1)
				require.True(t, h.fake.lifecycle.Enabled)
				require.Len(t, h.fake.lifecycle.Rules, 1)
			}
			d := h.destroy(r.NewState, r.Private)
			noErrors(t, d.Diagnostics)
			if kind == "access_configuration" {
				require.Len(t, h.fake.accessUpdates, 2)
				require.Empty(t, h.fake.access.PolicySets)
				require.Nil(t, h.fake.access.RequestIpAcl)
			} else {
				require.Len(t, h.fake.lifecycleUpdates, 2)
				require.False(t, h.fake.lifecycle.Enabled)
				require.Empty(t, h.fake.lifecycle.Rules)
			}
		})
	}
}

// TestCompetingAccessRevision retains identity and recovery evidence on a concurrent write.
func TestCompetingAccessRevision(t *testing.T) {
	h := newHarness(t, "access_configuration")
	h.fake.competing = true
	r := h.apply(h.withContent(h.config(nil, false)), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, r.Diagnostics)
	require.False(t, h.decode(r.NewState).IsNull())
	require.NotEmpty(t, r.Private)
}

// TestRetryKeepsNamespaceRequest verifies authenticated transport retries do not change key or body.
func TestRetryKeepsNamespaceRequest(t *testing.T) {
	h := newHarness(t, "namespace")
	h.fake.transientCreate = true
	r := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
	noErrors(t, r.Diagnostics)
	require.Len(t, h.fake.creates, 2)
	require.True(t, proto.Equal(h.fake.creates[0], h.fake.creates[1]))
	require.NotEmpty(t, h.fake.creates[0].IdempotencyKey)
	require.Equal(t, "Bearer test-token", h.fake.lastAuth)
	require.Contains(t, h.fake.lastUserAgent, "terraform-provider-coreweave")
}

// TestImportAndParentDisappearance verifies canonical import identifiers and parent-aware child removal.
func TestImportAndParentDisappearance(t *testing.T) {
	for _, kind := range []string{"namespace", "access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			suffix := ""
			if kind == "access_configuration" {
				suffix = "/accessConfiguration"
			}
			if kind == "lifecycle_policy" {
				suffix = "/lifecyclePolicy"
			}
			bad, e := h.server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: h.name, ID: "example-images" + suffix})
			require.NoError(t, e)
			require.NotEmpty(t, bad.Diagnostics)
			imp, e := h.server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: h.name, ID: "namespaces/example-images" + suffix})
			require.NoError(t, e)
			noErrors(t, imp.Diagnostics)
			read, e := h.server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: h.name, CurrentState: imp.ImportedResources[0].State})
			require.NoError(t, e)
			noErrors(t, read.Diagnostics)
			require.Empty(t, h.fake.creates)
			require.Empty(t, h.fake.accessUpdates)
			require.Empty(t, h.fake.lifecycleUpdates)
			if kind == "access_configuration" {
				h.fake.access = nil
				missing, e := h.server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: h.name, CurrentState: read.NewState})
				require.NoError(t, e)
				require.NotEmpty(t, missing.Diagnostics)
				require.False(t, h.decode(missing.NewState).IsNull())
				h.fake.namespace = nil
				gone, e := h.server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: h.name, CurrentState: read.NewState})
				require.NoError(t, e)
				noErrors(t, gone.Diagnostics)
				require.True(t, h.decode(gone.NewState).IsNull())
			}
		})
	}
}

// TestContainerRegistryRegistrations verifies exact resource and data-source names without legacy aliases.
func TestContainerRegistryRegistrations(t *testing.T) {
	h := newHarness(t, "namespace")
	s, e := h.server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, e)
	for _, kind := range []string{"namespace", "access_configuration", "lifecycle_policy"} {
		require.Contains(t, s.ResourceSchemas, "coreweave_container_registry_"+kind)
		require.NotContains(t, s.ResourceSchemas, "coreweave_registry_"+kind)
	}
	for _, kind := range []string{"namespace", "namespaces", "zones"} {
		require.Contains(t, s.DataSourceSchemas, "coreweave_container_registry_"+kind)
		require.NotContains(t, s.DataSourceSchemas, "coreweave_registry_"+kind)
	}
}

// TestForceIsLocalAndBootstrapReplaces verifies the lifecycle plan boundaries.
func TestForceIsLocalAndBootstrapReplaces(t *testing.T) {
	h := newHarness(t, "namespace")
	cfg := h.config(nil, false)
	r := h.apply(cfg, tftypes.NewValue(h.typ, nil), nil)
	noErrors(t, r.Diagnostics)
	var m map[string]tftypes.Value
	require.NoError(t, cfg.As(&m))
	m = maps.Clone(m)
	m["force_destroy"] = tftypes.NewValue(tftypes.Bool, true)
	r = h.apply(tftypes.NewValue(h.typ, m), h.decode(r.NewState), r.Private)
	noErrors(t, r.Diagnostics)
	require.Empty(t, h.fake.updates)
	p := h.plan(h.config(nil, true), h.decode(r.NewState), r.Private)
	noErrors(t, p.Diagnostics)
	require.NotEmpty(t, p.RequiresReplace)
	d := h.destroy(r.NewState, r.Private)
	noErrors(t, d.Diagnostics)
	require.True(t, h.fake.deletes[0].Force)
}

// TestPolicyOmissionClearsOwnedContent verifies defaults do not retain prior grants or rules.
func TestPolicyOmissionClearsOwnedContent(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			cfg := h.withContent(h.config(nil, false))
			r := h.apply(cfg, tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, r.Diagnostics)
			r = h.apply(h.config(nil, false), h.decode(r.NewState), r.Private)
			noErrors(t, r.Diagnostics)
			if kind == "access_configuration" {
				require.Empty(t, h.fake.access.PolicySets)
			} else {
				require.Empty(t, h.fake.lifecycle.Rules)
				require.False(t, h.fake.lifecycle.Enabled)
			}
			p := h.plan(h.config(nil, false), h.decode(r.NewState), r.Private)
			noErrors(t, p.Diagnostics)
			require.True(t, h.decode(p.PlannedState).Equal(h.decode(r.NewState)))
		})
	}
}

// ListRegistryNamespaces serves discovery through the actual generated Connect handler.
func (f *fakeRegistry) ListRegistryNamespaces(context.Context, *connect.Request[api.ListRegistryNamespacesRequest]) (*connect.Response[api.ListRegistryNamespacesResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := &api.ListRegistryNamespacesResponse{}
	if f.namespace != nil {
		res.RegistryNamespaces = []*api.RegistryNamespace{proto.Clone(f.namespace).(*api.RegistryNamespace)}
	}
	return connect.NewResponse(res), nil
}

// ListZones serves unsorted topology to verify stable Terraform output.
func (f *fakeRegistry) ListZones(context.Context, *connect.Request[api.ListZonesRequest]) (*connect.Response[api.ListZonesResponse], error) {
	return connect.NewResponse(&api.ListZonesResponse{Zones: []*api.Zone{{Zone: "US-ZZZ", Available: false}, {Zone: "US-AAA", Available: true}}}), nil
}

// TestDiscoveryProtocol verifies all discovery schemas and non-null empty lists.
func TestDiscoveryProtocol(t *testing.T) {
	h := newHarness(t, "namespace")
	s, e := h.server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, e)
	for _, kind := range []string{"namespace", "namespaces", "zones"} {
		name := "coreweave_container_registry_" + kind
		typ := s.DataSourceSchemas[name].ValueType()
		m := nullObject(typ)
		if kind == "namespace" {
			m["name"] = tftypes.NewValue(tftypes.String, "namespaces/example-images")
		}
		v, e := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, m))
		require.NoError(t, e)
		r, e := h.server.ReadDataSource(t.Context(), &tfprotov6.ReadDataSourceRequest{TypeName: name, Config: &v})
		require.NoError(t, e)
		noErrors(t, r.Diagnostics)
		state, e := r.State.Unmarshal(typ)
		require.NoError(t, e)
		require.False(t, state.IsNull())
	}
	h.fake.namespace = nil
	name := "coreweave_container_registry_namespaces"
	typ := s.DataSourceSchemas[name].ValueType()
	cfg, e := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, nullObject(typ)))
	require.NoError(t, e)
	r, e := h.server.ReadDataSource(t.Context(), &tfprotov6.ReadDataSourceRequest{TypeName: name, Config: &cfg})
	require.NoError(t, e)
	noErrors(t, r.Diagnostics)
	v, e := r.State.Unmarshal(typ)
	require.NoError(t, e)
	var m map[string]tftypes.Value
	require.NoError(t, v.As(&m))
	var items []tftypes.Value
	require.NoError(t, m["namespaces"].As(&items))
	require.Empty(t, items)
	require.False(t, m["namespaces"].IsNull())
}

// TestDeniedReadRetainsState ensures permissions are never interpreted as disappearance.
func TestDeniedReadRetainsState(t *testing.T) {
	h := newHarness(t, "namespace")
	r := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
	noErrors(t, r.Diagnostics)
	h.fake.readError = connect.NewError(connect.CodePermissionDenied, errors.New("denied"))
	read, e := h.server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: h.name, CurrentState: r.NewState})
	require.NoError(t, e)
	require.NotEmpty(t, read.Diagnostics)
	require.True(t, h.decode(read.NewState).Equal(h.decode(r.NewState)))
}

// TestPolicyConflictDoesNotRebase rejects changes made after the Terraform refresh.
func TestPolicyConflictDoesNotRebase(t *testing.T) {
	for _, kind := range []string{"access_configuration", "lifecycle_policy"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, kind)
			r := h.apply(h.config(nil, false), tftypes.NewValue(h.typ, nil), nil)
			noErrors(t, r.Diagnostics)
			h.fake.access.Etag = "other"
			h.fake.lifecycle.Etag = "other"
			changed := h.apply(h.withContent(h.config(nil, false)), h.decode(r.NewState), r.Private)
			require.NotEmpty(t, changed.Diagnostics)
			require.Empty(t, h.fake.accessUpdates)
			require.Empty(t, h.fake.lifecycleUpdates)
		})
	}
}

// TestUncertainAccessCommitDoesNotAllocateAgain observes current policy without replay after a lost response.
func TestUncertainAccessCommitDoesNotAllocateAgain(t *testing.T) {
	h := newHarness(t, "access_configuration")
	h.fake.uncertainAccess = true
	r := h.apply(h.withContent(h.config(nil, false)), tftypes.NewValue(h.typ, nil), nil)
	require.NotEmpty(t, r.Diagnostics)
	require.Len(t, h.fake.accessUpdates, 1)
	require.Equal(t, int64(2), h.fake.access.Revision)
	require.NotEmpty(t, r.Private)
	read, e := h.server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: h.name, CurrentState: r.NewState, Private: r.Private})
	require.NoError(t, e)
	noErrors(t, read.Diagnostics)
	require.Len(t, h.fake.accessUpdates, 1)
}

// canonicalUUIDv4 mirrors the live API's mutation-key contract.
func canonicalUUIDv4(key string) bool {
	return regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(key)
}
