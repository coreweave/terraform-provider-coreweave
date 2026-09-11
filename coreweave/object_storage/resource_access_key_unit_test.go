package objectstorage_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/gen/go/coreweave/cwobject/connectrpc/go/cwobject/v1/cwobjectv1connect"
	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const accessKeyTypeName = "coreweave_object_storage_access_key"
const accessKeyAddress = accessKeyTypeName + ".test"

type accessKeyFake struct {
	cwobjectv1connect.UnimplementedCWObjectHandler
	mu                                  sync.Mutex
	keys                                map[string]*cwobjectv1.AccessKeyInfo
	creates                             int
	revocations                         []string
	createError, readError, deleteError error
	missingInfo                         bool
	missingSecret                       bool
}

func newAccessKeyFake(t *testing.T) *accessKeyFake {
	t.Helper()
	fake := &accessKeyFake{keys: map[string]*cwobjectv1.AccessKeyInfo{"unrelated": {AccessKeyId: "unrelated", PrincipalName: "coreweave/caller", OrgId: "org-test", Status: "ACTIVE"}}}
	mux := http.NewServeMux()
	mux.Handle(cwobjectv1connect.NewCWObjectHandler(fake))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("COREWEAVE_API_ENDPOINT", srv.URL)
	t.Setenv("COREWEAVE_API_TOKEN", "test-token")
	return fake
}

func (f *accessKeyFake) CreateAccessKeyFromJWT(_ context.Context, req *connect.Request[cwobjectv1.CreateAccessKeyFromJWTRequest]) (*connect.Response[cwobjectv1.CreateAccessKeyFromJWTResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	if f.createError != nil {
		return nil, f.createError
	}
	if req.Msg.DurationSeconds == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("duration required"))
	}
	id := fmt.Sprintf("managed-%d", f.creates)
	expiry := timestamppb.New(time.Time{})
	if req.Msg.DurationSeconds.Value > 0 {
		expiry = timestamppb.New(time.Now().Add(time.Duration(req.Msg.DurationSeconds.Value) * time.Second))
	}
	f.keys[id] = &cwobjectv1.AccessKeyInfo{AccessKeyId: id, PrincipalName: "coreweave/caller", OrgId: "org-test", Status: "ACTIVE", Expiry: expiry, Attributes: maps.Clone(req.Msg.Attributes)}
	secret := "creation-secret-" + id
	if f.missingSecret {
		secret = ""
	}
	return connect.NewResponse(&cwobjectv1.CreateAccessKeyFromJWTResponse{AccessKeyId: id, SecretKey: secret, PrincipalName: "coreweave/caller", Expiry: expiry, Attributes: maps.Clone(req.Msg.Attributes)}), nil
}
func (f *accessKeyFake) GetAccessKeyInfo(_ context.Context, req *connect.Request[cwobjectv1.GetAccessKeyInfoRequest]) (*connect.Response[cwobjectv1.GetAccessKeyInfoResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readError != nil {
		return nil, f.readError
	}
	if f.missingInfo {
		return connect.NewResponse(&cwobjectv1.GetAccessKeyInfoResponse{}), nil
	}
	key, ok := f.keys[req.Msg.AccessKeyId]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("missing"))
	}
	return connect.NewResponse(&cwobjectv1.GetAccessKeyInfoResponse{Info: proto.Clone(key).(*cwobjectv1.AccessKeyInfo)}), nil
}
func (f *accessKeyFake) RevokeAccessKeyByAccessKey(_ context.Context, req *connect.Request[cwobjectv1.RevokeAccessKeyByAccessKeyRequest]) (*connect.Response[emptypb.Empty], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revocations = append(f.revocations, req.Msg.AccessKey)
	if f.deleteError != nil {
		return nil, f.deleteError
	}
	if _, ok := f.keys[req.Msg.AccessKey]; !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("missing"))
	}
	f.keys[req.Msg.AccessKey].Status = "DELETED"
	return connect.NewResponse(&emptypb.Empty{}), nil
}

type accessKeyHarness struct {
	t        *testing.T
	server   tfprotov6.ProviderServer
	typ      tftypes.Type
	fake     *accessKeyFake
	computed map[string]bool
}

func newAccessKeyHarness(t *testing.T) *accessKeyHarness {
	t.Helper()
	fake := newAccessKeyFake(t)
	server, err := provider.TestProtoV6ProviderFactories["coreweave"]()
	require.NoError(t, err)
	s, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	requireNoDiagErrors(t, s.Diagnostics, "schema")
	cfg, err := tfprotov6.NewDynamicValue(s.Provider.ValueType(), nullAttributesOf(t, s.Provider.ValueType()))
	require.NoError(t, err)
	configured, err := server.ConfigureProvider(t.Context(), &tfprotov6.ConfigureProviderRequest{Config: &cfg})
	require.NoError(t, err)
	requireNoDiagErrors(t, configured.Diagnostics, "configure")
	rs := s.ResourceSchemas[accessKeyTypeName]
	require.NotNil(t, rs)
	h := &accessKeyHarness{t: t, server: server, typ: rs.ValueType(), fake: fake, computed: map[string]bool{}}
	for _, a := range rs.Block.Attributes {
		h.computed[a.Name] = a.Computed
		if a.Name == "secret_key" {
			require.True(t, a.Sensitive)
		}
	}
	return h
}
func (h *accessKeyHarness) dv(v tftypes.Value) *tfprotov6.DynamicValue {
	h.t.Helper()
	d, err := tfprotov6.NewDynamicValue(h.typ, v)
	require.NoError(h.t, err)
	return &d
}
func (h *accessKeyHarness) decode(v *tfprotov6.DynamicValue) tftypes.Value {
	h.t.Helper()
	d, err := v.Unmarshal(h.typ)
	require.NoError(h.t, err)
	return d
}
func (h *accessKeyHarness) config(duration any, attributes any) tftypes.Value {
	h.t.Helper()
	var m map[string]tftypes.Value
	require.NoError(h.t, nullAttributesOf(h.t, h.typ).As(&m))
	m["duration_seconds"] = tftypes.NewValue(tftypes.Number, duration)
	m["attributes"] = tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, attributes)
	return tftypes.NewValue(h.typ, m)
}
func (h *accessKeyHarness) plan(config, state tftypes.Value) *tfprotov6.PlanResourceChangeResponse {
	h.t.Helper()
	var m map[string]tftypes.Value
	require.NoError(h.t, config.As(&m))
	if !state.IsNull() {
		var old map[string]tftypes.Value
		require.NoError(h.t, state.As(&old))
		for name, computed := range h.computed {
			if computed && m[name].IsNull() {
				m[name] = old[name]
			}
		}
	}
	resp, err := h.server.PlanResourceChange(h.t.Context(), &tfprotov6.PlanResourceChangeRequest{TypeName: accessKeyTypeName, Config: h.dv(config), PriorState: h.dv(state), ProposedNewState: h.dv(tftypes.NewValue(h.typ, m))})
	require.NoError(h.t, err)
	return resp
}
func (h *accessKeyHarness) create(config tftypes.Value) *tfprotov6.ApplyResourceChangeResponse {
	h.t.Helper()
	empty := tftypes.NewValue(h.typ, nil)
	p := h.plan(config, empty)
	requireNoDiagErrors(h.t, p.Diagnostics, "plan")
	r, err := h.server.ApplyResourceChange(h.t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: accessKeyTypeName, Config: h.dv(config), PriorState: h.dv(empty), PlannedState: p.PlannedState})
	require.NoError(h.t, err)
	return r
}
func (h *accessKeyHarness) read(state *tfprotov6.DynamicValue) *tfprotov6.ReadResourceResponse {
	h.t.Helper()
	r, err := h.server.ReadResource(h.t.Context(), &tfprotov6.ReadResourceRequest{TypeName: accessKeyTypeName, CurrentState: state})
	require.NoError(h.t, err)
	return r
}
func (h *accessKeyHarness) destroy(state *tfprotov6.DynamicValue) *tfprotov6.ApplyResourceChangeResponse {
	h.t.Helper()
	empty := h.dv(tftypes.NewValue(h.typ, nil))
	r, err := h.server.ApplyResourceChange(h.t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: accessKeyTypeName, Config: empty, PriorState: state, PlannedState: empty})
	require.NoError(h.t, err)
	return r
}
func (h *accessKeyHarness) fields(state *tfprotov6.DynamicValue) map[string]tftypes.Value {
	h.t.Helper()
	var m map[string]tftypes.Value
	require.NoError(h.t, h.decode(state).As(&m))
	return m
}

func TestAccessKeyLifecycle(t *testing.T) {
	for _, duration := range []int64{0, 3600} {
		t.Run(fmt.Sprint(duration), func(t *testing.T) {
			h := newAccessKeyHarness(t)
			config := h.config(duration, nil)
			created := h.create(config)
			requireNoDiagErrors(t, created.Diagnostics, "create")
			m := h.fields(created.NewState)
			require.False(t, m["secret_key"].IsNull())
			require.Equal(t, duration == 0, m["expiry"].IsNull())
			read := h.read(created.NewState)
			requireNoDiagErrors(t, read.Diagnostics, "read")
			require.True(t, h.decode(created.NewState).Equal(h.decode(read.NewState)))
			plan := h.plan(config, h.decode(read.NewState))
			requireNoDiagErrors(t, plan.Diagnostics, "unchanged plan")
			require.Empty(t, plan.RequiresReplace)
			require.True(t, h.decode(read.NewState).Equal(h.decode(plan.PlannedState)))
			for _, replacement := range []tftypes.Value{h.config(duration+1, nil), h.config(duration, map[string]tftypes.Value{"app": tftypes.NewValue(tftypes.String, "test")})} {
				p := h.plan(replacement, h.decode(read.NewState))
				requireNoDiagErrors(t, p.Diagnostics, "replace plan")
				require.NotEmpty(t, p.RequiresReplace)
			}
			gone := h.destroy(read.NewState)
			requireNoDiagErrors(t, gone.Diagnostics, "delete")
			require.True(t, h.decode(gone.NewState).IsNull())
			require.Equal(t, []string{"managed-1"}, h.fake.revocations)
			require.Contains(t, h.fake.keys, "unrelated")
		})
	}
}

func TestAccessKeyReadStates(t *testing.T) {
	for _, status := range []string{"EXPIRED", "FULL_SUSPENDED", "UNKNOWN"} {
		t.Run(status, func(t *testing.T) {
			h := newAccessKeyHarness(t)
			created := h.create(h.config(0, nil))
			requireNoDiagErrors(t, created.Diagnostics, "create")
			h.fake.keys["managed-1"].Status = status
			if status == "EXPIRED" {
				h.fake.keys["managed-1"].Expiry = timestamppb.New(time.Now().Add(-time.Hour))
			}
			read := h.read(created.NewState)
			requireNoDiagErrors(t, read.Diagnostics, "read")
			require.NotEmpty(t, read.Diagnostics)
			require.False(t, h.decode(read.NewState).IsNull())
			require.True(t, h.fields(created.NewState)["secret_key"].Equal(h.fields(read.NewState)["secret_key"]))
			require.Equal(t, 1, h.fake.creates)
			requireNoDiagErrors(t, h.destroy(read.NewState).Diagnostics, "inactive delete")
			require.Contains(t, h.fake.keys, "unrelated")
		})
	}
	t.Run("missing", func(t *testing.T) {
		h := newAccessKeyHarness(t)
		c := h.create(h.config(0, nil))
		delete(h.fake.keys, "managed-1")
		r := h.read(c.NewState)
		requireNoDiagErrors(t, r.Diagnostics, "missing read")
		require.True(t, h.decode(r.NewState).IsNull())
		requireNoDiagErrors(t, h.destroy(c.NewState).Diagnostics, "missing delete")
	})
	t.Run("absent expiry", func(t *testing.T) {
		h := newAccessKeyHarness(t)
		c := h.create(h.config(0, nil))
		h.fake.keys["managed-1"].Expiry = nil
		r := h.read(c.NewState)
		requireNoDiagErrors(t, r.Diagnostics, "read")
		require.True(t, h.fields(r.NewState)["expiry"].IsNull())
	})
}

func TestAccessKeyImport(t *testing.T) {
	h := newAccessKeyHarness(t)
	imported, err := h.server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: accessKeyTypeName, ID: " unrelated "})
	require.NoError(t, err)
	requireNoDiagErrors(t, imported.Diagnostics, "import")
	require.Len(t, imported.ImportedResources, 1)
	state := imported.ImportedResources[0].State
	m := h.fields(state)
	require.True(t, m["secret_key"].IsNull())
	require.True(t, m["duration_seconds"].IsNull())
	p := h.plan(h.config(nil, nil), h.decode(state))
	requireNoDiagErrors(t, p.Diagnostics, "import plan")
	require.Empty(t, p.RequiresReplace)
	require.True(t, h.decode(state).Equal(h.decode(p.PlannedState)))
	requireNoDiagErrors(t, h.read(state).Diagnostics, "import refresh")
	require.Zero(t, h.fake.creates)
	require.Empty(t, h.fake.revocations)
	p = h.plan(h.config(3600, nil), h.decode(state))
	requireNoDiagErrors(t, p.Diagnostics, "intentional rotation")
	require.NotEmpty(t, p.RequiresReplace)
	missing, err := h.server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: accessKeyTypeName, ID: "missing"})
	require.NoError(t, err)
	require.True(t, diagsHaveErrors(missing.Diagnostics))
}

func TestAccessKeyValidation(t *testing.T) {
	h := newAccessKeyHarness(t)
	for _, duration := range []int64{-1, 4294967296} {
		cfg := h.config(duration, nil)
		r, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: accessKeyTypeName, Config: h.dv(cfg)})
		require.NoError(t, err)
		require.True(t, diagsHaveErrors(r.Diagnostics))
	}
	require.True(t, diagsHaveErrors(h.plan(h.config(nil, nil), tftypes.NewValue(h.typ, nil)).Diagnostics))
	for _, key := range []string{"cw-tag", "role-extra", "groups-extra", "UPPER", "", "a.b", strings.Repeat("a", 64)} {
		cfg := h.config(0, map[string]tftypes.Value{key: tftypes.NewValue(tftypes.String, "ok")})
		r, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: accessKeyTypeName, Config: h.dv(cfg)})
		require.NoError(t, err)
		require.True(t, diagsHaveErrors(r.Diagnostics), key)
	}
	for _, value := range []any{nil, "with spaces", "-bad", strings.Repeat("x", 64)} {
		cfg := h.config(0, map[string]tftypes.Value{"app": tftypes.NewValue(tftypes.String, value)})
		r, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: accessKeyTypeName, Config: h.dv(cfg)})
		require.NoError(t, err)
		require.True(t, diagsHaveErrors(r.Diagnostics))
	}
	for _, value := range []any{"", "a_B.c-1", tftypes.UnknownValue} {
		cfg := h.config(4294967295, map[string]tftypes.Value{"app": tftypes.NewValue(tftypes.String, value)})
		r, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: accessKeyTypeName, Config: h.dv(cfg)})
		require.NoError(t, err)
		requireNoDiagErrors(t, r.Diagnostics, "valid attributes")
	}
	attrs := map[string]tftypes.Value{}
	for i := range 16 {
		attrs[fmt.Sprintf("key-%d", i)] = tftypes.NewValue(tftypes.String, "")
	}
	cfg := h.config(0, attrs)
	r, err := h.server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: accessKeyTypeName, Config: h.dv(cfg)})
	require.NoError(t, err)
	require.True(t, diagsHaveErrors(r.Diagnostics))
	require.Zero(t, h.fake.creates)
}

func TestAccessKeyFailureStateAndRetry(t *testing.T) {
	t.Run("missing secret remains null and key can be revoked", func(t *testing.T) {
		h := newAccessKeyHarness(t)
		h.fake.missingSecret = true
		c := h.create(h.config(0, nil))
		require.True(t, diagsHaveErrors(c.Diagnostics))
		require.False(t, h.decode(c.NewState).IsNull())
		fields := h.fields(c.NewState)
		require.True(t, fields["id"].Equal(tftypes.NewValue(tftypes.String, "managed-1")))
		require.True(t, fields["secret_key"].IsNull())
		refreshed := h.read(c.NewState)
		requireNoDiagErrors(t, refreshed.Diagnostics, "refresh")
		require.True(t, h.fields(refreshed.NewState)["secret_key"].IsNull())
		requireNoDiagErrors(t, h.destroy(refreshed.NewState).Diagnostics, "cleanup")
		require.Equal(t, []string{"managed-1"}, h.fake.revocations)
		require.Equal(t, "DELETED", h.fake.keys["managed-1"].Status)
		require.Equal(t, "ACTIVE", h.fake.keys["unrelated"].Status)
	})
	t.Run("create sent once", func(t *testing.T) {
		h := newAccessKeyHarness(t)
		h.fake.createError = connect.NewError(connect.CodeUnavailable, errors.New("lost response"))
		c := h.create(h.config(0, nil))
		require.True(t, diagsHaveErrors(c.Diagnostics))
		require.Equal(t, 1, h.fake.creates)
		require.Empty(t, h.fake.revocations)
	})
	t.Run("follow-up read retains secret", func(t *testing.T) {
		h := newAccessKeyHarness(t)
		h.fake.readError = connect.NewError(connect.CodePermissionDenied, errors.New("denied"))
		c := h.create(h.config(0, nil))
		require.True(t, diagsHaveErrors(c.Diagnostics))
		require.False(t, h.decode(c.NewState).IsNull())
		m := h.fields(c.NewState)
		require.False(t, m["id"].IsNull())
		require.False(t, m["secret_key"].IsNull())
		require.False(t, m["attributes"].IsNull())
		h.fake.readError = nil
		requireNoDiagErrors(t, h.destroy(c.NewState).Diagnostics, "cleanup")
	})
	t.Run("failed read and delete retain state", func(t *testing.T) {
		h := newAccessKeyHarness(t)
		c := h.create(h.config(0, nil))
		h.fake.readError = connect.NewError(connect.CodePermissionDenied, errors.New("denied"))
		r := h.read(c.NewState)
		require.True(t, diagsHaveErrors(r.Diagnostics))
		require.True(t, h.decode(c.NewState).Equal(h.decode(r.NewState)))
		h.fake.deleteError = h.fake.readError
		d := h.destroy(c.NewState)
		require.True(t, diagsHaveErrors(d.Diagnostics))
		require.False(t, h.decode(d.NewState).IsNull())
	})
	t.Run("invalid read preserves state", func(t *testing.T) {
		h := newAccessKeyHarness(t)
		c := h.create(h.config(0, nil))
		h.fake.missingInfo = true
		r := h.read(c.NewState)
		require.True(t, diagsHaveErrors(r.Diagnostics))
		require.True(t, h.decode(c.NewState).Equal(h.decode(r.NewState)))
	})
}

// Terraform CLI exercises replacement consistency as well as framework RPCs.
func TestAccessKeyTerraformLifecycle(t *testing.T) {
	fake := newAccessKeyFake(t)
	config := func(duration int) string {
		return fmt.Sprintf(`resource "coreweave_object_storage_access_key" "test" { duration_seconds = %d }`, duration)
	}
	// The fake endpoint uses process environment, so this test must run serially.
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories, CheckDestroy: func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if fake.keys["unrelated"] == nil || fake.keys["unrelated"].Status != "ACTIVE" {
			return fmt.Errorf("unrelated key was modified")
		}
		for id, key := range fake.keys {
			if id != "unrelated" && key.Status != "DELETED" {
				return fmt.Errorf("test key %s was not revoked", id)
			}
		}
		return nil
	}, Steps: []resource.TestStep{
		{Config: config(0), Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(accessKeyAddress, "status", "ACTIVE"), resource.TestCheckResourceAttrSet(accessKeyAddress, "secret_key"))},
		{Config: config(0), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		{Config: config(3600), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(accessKeyAddress, plancheck.ResourceActionDestroyBeforeCreate)}}},
		{Config: config(3600) + "\n", Check: resource.TestCheckResourceAttr(accessKeyAddress, "status", "ACTIVE")},
		{Config: `resource "coreweave_object_storage_access_key" "test" {
 duration_seconds = 3600
 attributes = { application = "trainer" }
 }`, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(accessKeyAddress, plancheck.ResourceActionDestroyBeforeCreate)}}},
		{Config: `resource "coreweave_object_storage_access_key" "test" {
 duration_seconds = 3600
 attributes = {}
 }`, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(accessKeyAddress, plancheck.ResourceActionDestroyBeforeCreate)}}},
		{ResourceName: accessKeyAddress, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"secret_key", "duration_seconds"}},
	}})
}

// Replacement must fail while planning, before revoking an imported key whose
// original duration is unavailable.
func TestAccessKeyTerraformImportReplacementNeedsDuration(t *testing.T) {
	fake := newAccessKeyFake(t)
	fake.keys["imported"] = &cwobjectv1.AccessKeyInfo{AccessKeyId: "imported", PrincipalName: "coreweave/caller", OrgId: "org-test", Status: "ACTIVE"}
	config := `resource "coreweave_object_storage_access_key" "test" {}`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.creates != 0 || len(fake.revocations) != 1 || fake.revocations[0] != "imported" || fake.keys["unrelated"] == nil {
				return fmt.Errorf("unexpected mutation during import or failed replacement")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, ResourceName: accessKeyAddress, ImportState: true, ImportStateId: "imported", ImportStatePersist: true},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{Config: `resource "coreweave_object_storage_access_key" "test" {
 attributes = { application = "changed" }
}`, ExpectError: regexp.MustCompile("Duration required for creation")},
			{Config: config, PreConfig: func() {
				fake.mu.Lock()
				defer fake.mu.Unlock()
				require.Contains(t, fake.keys, "imported", "failed replacement must leave the imported key intact")
				require.Empty(t, fake.revocations)
				require.Zero(t, fake.creates)
			}},
		},
	})
}

func TestAccessKeyDeletedRecord(t *testing.T) {
	h := newAccessKeyHarness(t)
	c := h.create(h.config(0, nil))
	requireNoDiagErrors(t, c.Diagnostics, "create")
	h.fake.keys["managed-1"].Status = "DELETED"
	r := h.read(c.NewState)
	requireNoDiagErrors(t, r.Diagnostics, "refresh revoked key")
	require.True(t, h.decode(r.NewState).IsNull())
	imported, err := h.server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: accessKeyTypeName, ID: "managed-1"})
	require.NoError(t, err)
	require.True(t, diagsHaveErrors(imported.Diagnostics))
	requireNoDiagErrors(t, h.destroy(c.NewState).Diagnostics, "delete revoked key")
	require.Equal(t, "ACTIVE", h.fake.keys["unrelated"].Status)
}
