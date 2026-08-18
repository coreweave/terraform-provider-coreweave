package objectstorage_test

import (
	"context"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testBucketName       = "test-bucket"
	bucketSettingsTypeNm = "coreweave_object_storage_bucket_settings"
)

// bucketSettingsHarness drives the bucket settings resource through the
// provider protocol server, against a fake CWObject service.
//
// Going through the protocol server rather than calling the resource's methods
// directly is what makes these tests worth having: PlanResourceChange applies
// schema defaults, plan modifiers and config validators exactly as they will be
// applied in production. A test that built plans by hand would have to
// reimplement that behavior, and would then be blind to precisely the class of
// mistake this file exists to catch -- a schema default that is unsafe to put
// on the wire.
//
// The provider is pointed at an httptest server by environment variable.
type bucketSettingsHarness struct {
	t          *testing.T
	server     tfprotov6.ProviderServer
	fake       *fakeCWObject
	objectType tftypes.Type

	// optionalComputed records which attributes are both Optional and Computed,
	// read from the provider schema for use by proposedNewState.
	optionalComputed map[string]bool
}

func newBucketSettingsHarness(ctx context.Context, t *testing.T, archiveEntitled bool) *bucketSettingsHarness {
	t.Helper()

	endpoint, fake := newFakeCWObject(t, testBucketName, archiveEntitled)

	// BuildClient reads these in preference to provider configuration, so they
	// are how the provider server is aimed at the fake. t.Setenv precludes
	// t.Parallel in this file.
	t.Setenv("COREWEAVE_API_ENDPOINT", endpoint)
	t.Setenv("COREWEAVE_S3_ENDPOINT", endpoint)
	t.Setenv("COREWEAVE_API_TOKEN", "fake-token")

	server, err := provider.TestProtoV6ProviderFactories["coreweave"]()
	require.NoError(t, err)

	schemaResp, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	require.Empty(t, schemaResp.Diagnostics)

	resourceSchema, ok := schemaResp.ResourceSchemas[bucketSettingsTypeNm]
	require.True(t, ok, "resource %q missing from provider schema", bucketSettingsTypeNm)

	providerConfig, err := tfprotov6.NewDynamicValue(
		schemaResp.Provider.ValueType(), nullAttributesOf(t, schemaResp.Provider.ValueType()))
	require.NoError(t, err)

	configureResp, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &providerConfig})
	require.NoError(t, err)
	requireNoDiagErrors(t, configureResp.Diagnostics, "configure provider")

	optionalComputed := make(map[string]bool)
	for _, attr := range resourceSchema.Block.Attributes {
		optionalComputed[attr.Name] = attr.Optional && attr.Computed
	}

	return &bucketSettingsHarness{
		t:                t,
		server:           server,
		fake:             fake,
		objectType:       resourceSchema.ValueType(),
		optionalComputed: optionalComputed,
	}
}

// nullAttributesOf builds an object with every attribute null, which is how an
// entirely unset block arrives over the protocol. A wholly null object is a
// different thing and is rejected during conversion.
func nullAttributesOf(t *testing.T, objectType tftypes.Type) tftypes.Value {
	t.Helper()

	obj, ok := objectType.(tftypes.Object)
	require.True(t, ok, "expected an object type, got %T", objectType)

	attrs := make(map[string]tftypes.Value, len(obj.AttributeTypes))
	for name, attrType := range obj.AttributeTypes {
		attrs[name] = tftypes.NewValue(attrType, nil)
	}

	return tftypes.NewValue(obj, attrs)
}

// bucketSettingsConfig is the Terraform configuration for one apply. A nil
// field is an attribute the practitioner omitted.
type bucketSettingsConfig struct {
	bucket                     string
	auditLoggingEnabled        *bool
	archiveEnabled             *bool
	archiveAfterLastAccessDays *int32
}

// value builds the configuration object.
//
// tftypes.NewValue requires the map to match the object type exactly, so adding
// an attribute to the resource schema fails this loudly rather than silently
// leaving the new attribute untested.
func (c bucketSettingsConfig) value(objectType tftypes.Type) tftypes.Value {
	boolValue := func(b *bool) tftypes.Value {
		if b == nil {
			return tftypes.NewValue(tftypes.Bool, nil)
		}

		return tftypes.NewValue(tftypes.Bool, *b)
	}

	daysValue := tftypes.NewValue(tftypes.Number, nil)
	if c.archiveAfterLastAccessDays != nil {
		daysValue = tftypes.NewValue(tftypes.Number, *c.archiveAfterLastAccessDays)
	}

	return tftypes.NewValue(objectType, map[string]tftypes.Value{
		"bucket":                         tftypes.NewValue(tftypes.String, c.bucket),
		"audit_logging_enabled":          boolValue(c.auditLoggingEnabled),
		"archive_enabled":                boolValue(c.archiveEnabled),
		"archive_after_last_access_days": daysValue,
	})
}

func (h *bucketSettingsHarness) dynamicValue(v tftypes.Value) *tfprotov6.DynamicValue {
	h.t.Helper()

	dv, err := tfprotov6.NewDynamicValue(h.objectType, v)
	require.NoError(h.t, err)

	return &dv
}

func (h *bucketSettingsHarness) nullValue() tftypes.Value {
	return tftypes.NewValue(h.objectType, nil)
}

// validate runs ValidateResourceConfig, which is where the resource's
// ValidateConfig implementation runs.
func (h *bucketSettingsHarness) validate(ctx context.Context, config bucketSettingsConfig) []*tfprotov6.Diagnostic {
	h.t.Helper()

	resp, err := h.server.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{
		TypeName: bucketSettingsTypeNm,
		Config:   h.dynamicValue(config.value(h.objectType)),
	})
	require.NoError(h.t, err)

	return resp.Diagnostics
}

// create plans and then applies a configuration against empty prior state,
// returning the resulting state and the diagnostics from whichever step
// produced them.
func (h *bucketSettingsHarness) create(
	ctx context.Context, config bucketSettingsConfig,
) (tftypes.Value, []*tfprotov6.Diagnostic) {
	h.t.Helper()

	return h.apply(ctx, config, h.nullValue())
}

// apply plans and applies a configuration against the given prior state.
func (h *bucketSettingsHarness) apply(
	ctx context.Context, config bucketSettingsConfig, priorState tftypes.Value,
) (tftypes.Value, []*tfprotov6.Diagnostic) {
	h.t.Helper()

	configValue := config.value(h.objectType)

	planned, diags := h.plan(ctx, config, priorState)
	if diagsHaveErrors(diags) {
		return tftypes.Value{}, diags
	}

	applyResp, err := h.server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     bucketSettingsTypeNm,
		Config:       h.dynamicValue(configValue),
		PriorState:   h.dynamicValue(priorState),
		PlannedState: h.dynamicValue(planned),
	})
	require.NoError(h.t, err)

	if diagsHaveErrors(applyResp.Diagnostics) {
		return tftypes.Value{}, applyResp.Diagnostics
	}

	return h.decode(applyResp.NewState), applyResp.Diagnostics
}

// proposedNewState merges the configuration with prior state. An omitted
// Optional+Computed attribute keeps its prior state value, while a plain
// Optional attribute reverts to null. Attribute behavior is read from the
// provider schema so schema changes are reflected automatically.
func (h *bucketSettingsHarness) proposedNewState(config, priorState tftypes.Value) tftypes.Value {
	h.t.Helper()

	if priorState.IsNull() {
		return config
	}

	var configAttrs, priorAttrs map[string]tftypes.Value
	require.NoError(h.t, config.As(&configAttrs))
	require.NoError(h.t, priorState.As(&priorAttrs))

	proposed := make(map[string]tftypes.Value, len(configAttrs))

	for name, configValue := range configAttrs {
		if configValue.IsNull() && h.optionalComputed[name] {
			proposed[name] = priorAttrs[name]

			continue
		}

		proposed[name] = configValue
	}

	return tftypes.NewValue(h.objectType, proposed)
}

// plan runs PlanResourceChange and returns the planned state.
func (h *bucketSettingsHarness) plan(
	ctx context.Context, config bucketSettingsConfig, priorState tftypes.Value,
) (tftypes.Value, []*tfprotov6.Diagnostic) {
	h.t.Helper()

	configValue := config.value(h.objectType)

	planResp, err := h.server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         bucketSettingsTypeNm,
		Config:           h.dynamicValue(configValue),
		PriorState:       h.dynamicValue(priorState),
		ProposedNewState: h.dynamicValue(h.proposedNewState(configValue, priorState)),
	})
	require.NoError(h.t, err)

	if diagsHaveErrors(planResp.Diagnostics) {
		return tftypes.Value{}, planResp.Diagnostics
	}

	return h.decode(planResp.PlannedState), planResp.Diagnostics
}

func (h *bucketSettingsHarness) decode(dv *tfprotov6.DynamicValue) tftypes.Value {
	h.t.Helper()

	value, err := dv.Unmarshal(h.objectType)
	require.NoError(h.t, err)

	return value
}

// refresh runs ReadResource, returning the state as the provider would refresh
// it from the API.
func (h *bucketSettingsHarness) refresh(ctx context.Context, state tftypes.Value) tftypes.Value {
	h.t.Helper()

	resp, err := h.server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     bucketSettingsTypeNm,
		CurrentState: h.dynamicValue(state),
	})
	require.NoError(h.t, err)
	requireNoDiagErrors(h.t, resp.Diagnostics, "refresh")

	return h.decode(resp.NewState)
}

// destroy applies a null configuration over the given prior state, which is how
// Terraform expresses a delete.
func (h *bucketSettingsHarness) destroy(ctx context.Context, priorState tftypes.Value) []*tfprotov6.Diagnostic {
	h.t.Helper()

	nullConfig := h.dynamicValue(h.nullValue())

	planResp, err := h.server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         bucketSettingsTypeNm,
		Config:           nullConfig,
		PriorState:       h.dynamicValue(priorState),
		ProposedNewState: nullConfig,
	})
	require.NoError(h.t, err)
	requireNoDiagErrors(h.t, planResp.Diagnostics, "plan destroy")

	applyResp, err := h.server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     bucketSettingsTypeNm,
		Config:       nullConfig,
		PriorState:   h.dynamicValue(priorState),
		PlannedState: planResp.PlannedState,
	})
	require.NoError(h.t, err)

	return applyResp.Diagnostics
}

func diagsHaveErrors(diags []*tfprotov6.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			return true
		}
	}

	return false
}

func diagText(diags []*tfprotov6.Diagnostic) string {
	var out string
	for _, d := range diags {
		out += d.Summary + ": " + d.Detail + "\n"
	}

	return out
}

func requireNoDiagErrors(t *testing.T, diags []*tfprotov6.Diagnostic, step string) {
	t.Helper()
	require.False(t, diagsHaveErrors(diags), "%s: %s", step, diagText(diags))
}

func boolPtr(b bool) *bool    { return &b }
func int32Ptr(i int32) *int32 { return &i }

// TestBucketSettingsUnentitledOrgIsUnaffectedByArchive is the regression test
// for the entitlement break. An organization that is not on the bucket archive
// allowlist -- the default state, since the allowlist is an explicit per-org map
// -- must be able to go on managing the settings it has always managed. That
// only holds if a configuration which never mentions archive puts no archive
// field on the wire, because the entitlement check keys on the presence of
// those fields rather than their value.
func TestBucketSettingsUnentitledOrgIsUnaffectedByArchive(t *testing.T) {
	ctx := t.Context()
	h := newBucketSettingsHarness(ctx, t, false)

	_, diags := h.create(ctx, bucketSettingsConfig{
		bucket:              testBucketName,
		auditLoggingEnabled: boolPtr(true),
		// archive_enabled and archive_after_last_access_days are omitted, as
		// they are for every practitioner who does not use the feature.
	})

	assert.False(t, diagsHaveErrors(diags),
		"an unentitled org managing only audit logging must not be rejected: %s", diagText(diags))

	sent := h.fake.setRequestSettings()
	require.Len(t, sent, 1)
	assert.Nil(t, sent[0].GetArchiveEnabled(),
		"a config that never mentions archive must not send archive_enabled")
	assert.Nil(t, sent[0].GetArchiveAfterLastAccessDays(),
		"a config that never mentions archive must not send archive_after_last_access_days")
}

// TestBucketSettingsUnentitledOrgCanDestroy covers the same gate on the delete
// path. Turning archive off is itself an archive-shaped request, so an
// unentitled org that unconditionally disables archive on destroy cannot remove
// the resource at all.
func TestBucketSettingsUnentitledOrgCanDestroy(t *testing.T) {
	ctx := t.Context()
	h := newBucketSettingsHarness(ctx, t, false)

	config := bucketSettingsConfig{
		bucket:              testBucketName,
		auditLoggingEnabled: boolPtr(true),
	}
	state, diags := h.create(ctx, config)
	requireNoDiagErrors(t, diags, "create")

	diags = h.destroy(ctx, state)

	assert.False(t, diagsHaveErrors(diags),
		"an unentitled org must be able to destroy the resource: %s", diagText(diags))
}

// TestBucketSettingsUnentitledOrgRejectedWhenEnablingArchive is the other side
// of the gate: asking for the feature without the entitlement should fail, and
// should surface as a comprehensible diagnostic rather than a bare error.
func TestBucketSettingsUnentitledOrgRejectedWhenEnablingArchive(t *testing.T) {
	ctx := t.Context()
	h := newBucketSettingsHarness(ctx, t, false)

	_, diags := h.create(ctx, bucketSettingsConfig{
		bucket:                     testBucketName,
		auditLoggingEnabled:        boolPtr(false),
		archiveEnabled:             boolPtr(true),
		archiveAfterLastAccessDays: int32Ptr(60),
	})

	require.True(t, diagsHaveErrors(diags), "enabling archive without entitlement must fail")
	assert.Contains(t, diagText(diags), "BucketArchive")
}

// TestBucketSettingsEntitledOrgRoundTrip checks the happy path for the
// population the acceptance suite already covers, so the fake is held to the
// same outcomes the real service produces there.
func TestBucketSettingsEntitledOrgRoundTrip(t *testing.T) {
	ctx := t.Context()
	h := newBucketSettingsHarness(ctx, t, true)

	_, diags := h.create(ctx, bucketSettingsConfig{
		bucket:                     testBucketName,
		auditLoggingEnabled:        boolPtr(true),
		archiveEnabled:             boolPtr(true),
		archiveAfterLastAccessDays: int32Ptr(90),
	})
	requireNoDiagErrors(t, diags, "create")

	stored := h.fake.storedSettings(testBucketName)
	require.NotNil(t, stored.archiveEnabled)
	assert.True(t, *stored.archiveEnabled)
	require.NotNil(t, stored.archiveAfterLastAccessDays)
	assert.Equal(t, int32(90), *stored.archiveAfterLastAccessDays)
}

// TestBucketSettingsDisablingArchiveDiscardsRetention pins the behavior that
// produced the drift this change set out to fix: the service nulls the
// retention when archive is turned off, so a configuration that keeps a day
// count while archive is disabled could never converge.
func TestBucketSettingsDisablingArchiveDiscardsRetention(t *testing.T) {
	ctx := t.Context()
	h := newBucketSettingsHarness(ctx, t, true)

	enabled := bucketSettingsConfig{
		bucket:                     testBucketName,
		auditLoggingEnabled:        boolPtr(true),
		archiveEnabled:             boolPtr(true),
		archiveAfterLastAccessDays: int32Ptr(90),
	}
	state, diags := h.create(ctx, enabled)
	requireNoDiagErrors(t, diags, "create with archive enabled")

	_, diags = h.apply(ctx, bucketSettingsConfig{
		bucket:              testBucketName,
		auditLoggingEnabled: boolPtr(true),
		archiveEnabled:      boolPtr(false),
	}, state)
	requireNoDiagErrors(t, diags, "update disabling archive")

	stored := h.fake.storedSettings(testBucketName)
	require.NotNil(t, stored.archiveEnabled)
	assert.False(t, *stored.archiveEnabled)
	assert.Nil(t, stored.archiveAfterLastAccessDays,
		"disabling archive must discard the retention server-side")
}

// TestBucketSettingsRetentionBelowMinimumIsRejected records that the service
// rejects an out-of-range day count rather than clamping it, which is what
// makes it safe for the provider to write the requested value into state.
func TestBucketSettingsRetentionBelowMinimumIsRejected(t *testing.T) {
	ctx := t.Context()
	h := newBucketSettingsHarness(ctx, t, true)

	_, diags := h.create(ctx, bucketSettingsConfig{
		bucket:                     testBucketName,
		auditLoggingEnabled:        boolPtr(false),
		archiveEnabled:             boolPtr(true),
		archiveAfterLastAccessDays: int32Ptr(30),
	})

	require.True(t, diagsHaveErrors(diags), "a day count below the minimum must be rejected")
	assert.Contains(t, diagText(diags), "must be >= 60")
}

// TestBucketSettingsConvergesAfterApply is the offline counterpart to the
// acceptance suite's empty-plan assertion, and it covers the failure this whole
// change began with: state and configuration disagreeing after a successful
// apply, so that every subsequent plan proposes the same update forever.
//
// It runs for both entitled and unentitled organizations, because the archive
// attributes are disclosed to one and withheld from the other, and a
// configuration must converge either way.
func TestBucketSettingsConvergesAfterApply(t *testing.T) {
	for _, tc := range []struct {
		name            string
		archiveEntitled bool
		config          bucketSettingsConfig
	}{
		{
			name:            "unentitled org managing only audit logging",
			archiveEntitled: false,
			config: bucketSettingsConfig{
				bucket:              testBucketName,
				auditLoggingEnabled: boolPtr(true),
			},
		},
		{
			name:            "entitled org managing only audit logging",
			archiveEntitled: true,
			config: bucketSettingsConfig{
				bucket:              testBucketName,
				auditLoggingEnabled: boolPtr(true),
			},
		},
		{
			name:            "entitled org with archive enabled",
			archiveEntitled: true,
			config: bucketSettingsConfig{
				bucket:                     testBucketName,
				auditLoggingEnabled:        boolPtr(true),
				archiveEnabled:             boolPtr(true),
				archiveAfterLastAccessDays: int32Ptr(90),
			},
		},
		{
			name:            "entitled org with archive explicitly disabled",
			archiveEntitled: true,
			config: bucketSettingsConfig{
				bucket:              testBucketName,
				auditLoggingEnabled: boolPtr(true),
				archiveEnabled:      boolPtr(false),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			h := newBucketSettingsHarness(ctx, t, tc.archiveEntitled)

			applied, diags := h.create(ctx, tc.config)
			requireNoDiagErrors(t, diags, "create")

			// Refresh first, as Terraform does before planning. This is where a
			// value the API declines to persist reverts and shows up as drift.
			refreshed := h.refresh(ctx, applied)

			replanned, diags := h.plan(ctx, tc.config, refreshed)
			requireNoDiagErrors(t, diags, "re-plan")

			assert.True(t, replanned.Equal(refreshed),
				"plan after apply must be empty\n refreshed state: %s\n planned state:   %s", refreshed, replanned)
		})
	}
}

// TestBucketSettingsConvergesAfterRemovingArchiveFromConfig covers the case
// that justifies archive_enabled being Computed rather than merely Optional.
//
// Once archive has been configured the column is no longer null, so the API
// reports a concrete false even after archive is switched off. A practitioner
// who then deletes the attribute from configuration has a null config facing a
// false in state. Computed absorbs that; Optional alone would propose setting it
// back to null on every plan, forever.
func TestBucketSettingsConvergesAfterRemovingArchiveFromConfig(t *testing.T) {
	ctx := t.Context()
	h := newBucketSettingsHarness(ctx, t, true)

	enabled := bucketSettingsConfig{
		bucket:                     testBucketName,
		auditLoggingEnabled:        boolPtr(true),
		archiveEnabled:             boolPtr(true),
		archiveAfterLastAccessDays: int32Ptr(90),
	}
	state, diags := h.create(ctx, enabled)
	requireNoDiagErrors(t, diags, "create with archive enabled")

	// Switch archive off, leaving the API holding a concrete false.
	state, diags = h.apply(ctx, bucketSettingsConfig{
		bucket:              testBucketName,
		auditLoggingEnabled: boolPtr(true),
		archiveEnabled:      boolPtr(false),
	}, state)
	requireNoDiagErrors(t, diags, "disable archive")

	// Now stop managing archive at all.
	unmanaged := bucketSettingsConfig{
		bucket:              testBucketName,
		auditLoggingEnabled: boolPtr(true),
	}
	state, diags = h.apply(ctx, unmanaged, state)
	requireNoDiagErrors(t, diags, "remove archive from config")

	refreshed := h.refresh(ctx, state)

	replanned, diags := h.plan(ctx, unmanaged, refreshed)
	requireNoDiagErrors(t, diags, "re-plan")

	assert.True(t, replanned.Equal(refreshed),
		"plan must be empty after archive is dropped from config\n refreshed state: %s\n planned state:   %s",
		refreshed, replanned)
}

// TestBucketSettingsRetentionWithoutArchiveRejectedAtValidate covers the
// config-level pairing rule, which runs before any request is made.
func TestBucketSettingsRetentionWithoutArchiveRejectedAtValidate(t *testing.T) {
	ctx := t.Context()
	h := newBucketSettingsHarness(ctx, t, true)

	diags := h.validate(ctx, bucketSettingsConfig{
		bucket:                     testBucketName,
		archiveEnabled:             boolPtr(false),
		archiveAfterLastAccessDays: int32Ptr(90),
	})

	require.True(t, diagsHaveErrors(diags), "a retention with archive off must be rejected")
	assert.Contains(t, diagText(diags), "Unexpected archive_after_last_access_days")
}

// TestBucketSettingsArchiveWithoutRetentionRejectedAtValidate covers the
// forward direction of the same rule.
func TestBucketSettingsArchiveWithoutRetentionRejectedAtValidate(t *testing.T) {
	ctx := t.Context()
	h := newBucketSettingsHarness(ctx, t, true)

	diags := h.validate(ctx, bucketSettingsConfig{
		bucket:         testBucketName,
		archiveEnabled: boolPtr(true),
	})

	require.True(t, diagsHaveErrors(diags), "archive on without a retention must be rejected")
	assert.Contains(t, diagText(diags), "Missing archive_after_last_access_days")
}
