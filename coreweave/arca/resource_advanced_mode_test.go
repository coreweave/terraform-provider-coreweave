package arca

import (
	"errors"
	"testing"

	arcav1beta1 "buf.build/gen/go/coreweave/arca/protocolbuffers/go/coreweave/arca/v1beta1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdvancedModeResourceSchema(t *testing.T) {
	t.Parallel()

	var resp resource.SchemaResponse
	NewAdvancedModeResource().Schema(t.Context(), resource.SchemaRequest{}, &resp)
	require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics.Errors())
	require.False(t, resp.Schema.ValidateImplementation(t.Context()).HasError())
	assert.Contains(t, resp.Schema.Attributes, "github_installation_id")
	assert.Contains(t, resp.Schema.Attributes, "allow_destroy_apps")
}

func TestSetAdvancedModeState(t *testing.T) {
	t.Parallel()

	data := AdvancedModeResourceModel{AllowDestroyApps: types.BoolValue(true)}
	var diagnostics diag.Diagnostics
	ok := setAdvancedModeState(t.Context(), &data, &arcav1beta1.AdvancedMode{
		Name:                 "cw1234-github",
		GithubInstallationId: "12345",
		Repository:           "https://github.com/coreweave/platform-config",
		Branch:               "main",
		ManifestPath:         ".arca/apps.yaml",
		Connected:            true,
		Ready:                true,
		AppCount:             2,
		AvailableRepositories: []*arcav1beta1.RepoInfo{
			{Url: "https://github.com/coreweave/platform-config", Permissions: []string{"contents:read"}},
		},
		Conditions: []*arcav1beta1.AdvancedModeCondition{
			{Type: "Ready", Status: "True", Reason: "Reconciled", ObservedGeneration: 3},
		},
		LastReconciledAt: "2026-08-06T12:00:00Z",
	}, &diagnostics)

	require.True(t, ok)
	require.False(t, diagnostics.HasError(), diagnostics.Errors())
	assert.Equal(t, "cw1234-github", data.ID.ValueString())
	assert.Equal(t, "12345", data.GitHubInstallationID.ValueString())
	assert.True(t, data.Ready.ValueBool())
	assert.Equal(t, int64(2), data.AppCount.ValueInt64())
	assert.Equal(t, 1, len(data.AvailableRepositories.Elements()))
	assert.Equal(t, 1, len(data.Conditions.Elements()))
	assert.True(t, data.AllowDestroyApps.ValueBool(), "provider-only deletion setting must be preserved")
}

func TestAdvancedModeUpdatePaths(t *testing.T) {
	t.Parallel()

	state := AdvancedModeResourceModel{
		Repository:   types.StringValue("https://github.com/coreweave/old"),
		Branch:       types.StringValue("main"),
		ManifestPath: types.StringValue(defaultManifestPath),
	}
	plan := state
	plan.Repository = types.StringValue("https://github.com/coreweave/new")
	plan.Branch = types.StringValue("release")

	assert.Equal(t, []string{"repository", "branch"}, advancedModeUpdatePaths(plan, state))
	assert.Empty(t, advancedModeUpdatePaths(state, state))
}

func TestAdvancedModeReadiness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mode      *arcav1beta1.AdvancedMode
		wantState string
		wantErr   bool
	}{
		{name: "pending", mode: &arcav1beta1.AdvancedMode{}, wantState: pendingState},
		{name: "ready", mode: &arcav1beta1.AdvancedMode{Ready: true}, wantState: readyState},
		{
			name: "terminal failure",
			mode: &arcav1beta1.AdvancedMode{
				LastError: "manifest is invalid",
				Conditions: []*arcav1beta1.AdvancedModeCondition{
					{Type: "Ready", Status: "False", ObservedGeneration: 2},
				},
			},
			wantState: pendingState,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := advancedModeReadiness(tt.mode)
			assert.Equal(t, tt.wantState, state)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, errReconciliation))
			} else {
				require.NoError(t, err)
			}
		})
	}
}
