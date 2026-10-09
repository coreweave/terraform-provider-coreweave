package containerregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
)

// TestReportIncludesContextAndEveryDetail verifies ErrorInfo does not hide actionable field diagnostics.
func TestReportIncludesContextAndEveryDetail(t *testing.T) {
	for _, tc := range []struct {
		code     connect.Code
		detail   proto.Message
		expected string
	}{
		{connect.CodeInvalidArgument, &errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "policy", Description: "bad expression"}}}, "bad expression"},
		{connect.CodeFailedPrecondition, &errdetails.PreconditionFailure{Violations: []*errdetails.PreconditionFailure_Violation{{Type: "etag", Description: "stale etag"}}}, "stale etag"},
		{connect.CodeResourceExhausted, &errdetails.QuotaFailure{Violations: []*errdetails.QuotaFailure_Violation{{Subject: "namespace", Description: "quota exhausted"}}}, "quota exhausted"},
	} {
		err := connect.NewError(tc.code, "request rejected")
		for _, m := range []proto.Message{&errdetails.ErrorInfo{Reason: "REJECTED"}, tc.detail} {
			detail, e := connectproto.NewErrorDetail(m)
			require.NoError(t, e)
			err = err.WithDetail(detail)
		}
		var diagnostics diag.Diagnostics
		report(t.Context(), fmt.Errorf("updating registry policy: %w", err), &diagnostics)
		require.NotEmpty(t, diagnostics)
		message := ""
		for _, diagnostic := range diagnostics {
			require.Contains(t, diagnostic.Detail(), "updating registry policy")
			require.Contains(t, diagnostic.Detail(), "REJECTED")
			require.NotEqual(t, "Container Registry API error", diagnostic.Summary())
			message += diagnostic.Detail()
		}
		require.Contains(t, message, tc.expected)
	}
}

// TestSelectorDeprecationUsesDescriptor preserves legacy imports while warning for new configuration.
func TestSelectorDeprecationUsesDescriptor(t *testing.T) {
	var response validator.StringResponse
	selectorValidator{}.ValidateString(t.Context(), validator.StringRequest{Path: path.Root("identity_selector"), ConfigValue: types.StringValue("COREWEAVE_KUBERNETES")}, &response)
	require.False(t, response.Diagnostics.HasError())
	require.NotEmpty(t, response.Diagnostics.Warnings())
	require.True(t, validSelector("WORKLOAD_FEDERATION"))
	require.False(t, validSelector("UNSPECIFIED"))
}

type testPrivate map[string][]byte

// GetKey retrieves a test's private recovery record.
func (p testPrivate) GetKey(_ context.Context, key string) ([]byte, diag.Diagnostics) {
	return p[key], nil
}

// SetKey persists a test's private recovery record.
func (p testPrivate) SetKey(_ context.Context, key string, value []byte) diag.Diagnostics {
	p[key] = value
	return nil
}

// TestUncertainRecoveryClears verifies refresh drops uncertain intent without state removal or replay.
func TestUncertainRecoveryClears(t *testing.T) {
	p := testPrivate{}
	rec := &recovery{Action: actionDelete}
	require.NoError(t, saveRecovery(t.Context(), p, rec))
	require.NoError(t, observeRecovery(t.Context(), nil, 0, true, p))
	record, err := loadRecovery(t.Context(), p)
	require.NoError(t, err)
	require.Nil(t, record)
}

// TestReportLocalError avoids classifying local recovery failures as API errors.
func TestReportLocalError(t *testing.T) {
	var diagnostics diag.Diagnostics
	report(t.Context(), errors.New("saved operation expired"), &diagnostics)
	require.Len(t, diagnostics, 1)
	require.Equal(t, "Container Registry operation failed", diagnostics[0].Summary())
	require.Equal(t, "saved operation expired", diagnostics[0].Detail())
}

// TestLegacyRecoveryMetadata preserves accepted-operation evidence while dropping obsolete request fields.
func TestLegacyRecoveryMetadata(t *testing.T) {
	p := testPrivate{"container_registry_recovery": []byte(`{"action":"create","operation":"namespaces/example-images/operations/create-1","key":"legacy-key","request":"bGVnYWN5","fingerprint":"legacy-fingerprint","started":"2026-10-01T00:00:00Z"}`)}
	rec, err := loadRecovery(t.Context(), p)
	require.NoError(t, err)
	require.Equal(t, &recovery{Action: actionCreate, Operation: "namespaces/example-images/operations/create-1"}, rec)
	require.NoError(t, saveRecovery(t.Context(), p, rec))
	for _, field := range []string{"key", "request", "fingerprint", "started"} {
		require.NotContains(t, string(p["container_registry_recovery"]), `"`+field+`"`)
	}
}

// TestConversionDiagnosticsKeepWarningsAndPaths preserves structured diagnostics across operation helpers.
func TestConversionDiagnosticsKeepWarningsAndPaths(t *testing.T) {
	var original diag.Diagnostics
	original.AddAttributeWarning(path.Root("policy_sets"), "Compatibility warning", "Use the preferred selector.")
	original.AddAttributeError(path.Root("policy_sets").AtMapKey("readers"), "Invalid policy", "Conversion failed.")
	var reported diag.Diagnostics
	report(t.Context(), fmt.Errorf("converting access policy: %w", conversionError(original)), &reported)
	require.Equal(t, original, reported)
}

// TestProtoValidationAccumulatesAnnotatedViolations uses public descriptors as the validation contract.
func TestProtoValidationAccumulatesAnnotatedViolations(t *testing.T) {
	var d diag.Diagnostics
	validateProto(&api.RegistryLifecyclePolicy{Rules: []*api.RegistryLifecycleRule{{RuleId: "INVALID", Regex: strings.Repeat("x", 1025), KeepNewest: proto.Uint32(101)}}}, &d)
	require.Len(t, d.Errors(), 3)
	text := ""
	for _, diagnostic := range d {
		text += diagnostic.Detail()
	}
	for _, field := range []string{"rule_id", "regex", "keep_newest"} {
		require.Contains(t, text, field)
	}
}

// TestSelectorDocumentationTracksProto ensures practitioners can discover every supported selector.
func TestSelectorDocumentationTracksProto(t *testing.T) {
	values := api.RegistryIdentitySelector(0).Descriptor().Values()
	description := selectorDescription()
	for i := 0; i < values.Len(); i++ {
		if value := values.Get(i); value.Number() != 0 {
			require.Contains(t, description, strings.TrimPrefix(string(value.Name()), "REGISTRY_IDENTITY_SELECTOR_"))
		}
	}
}
