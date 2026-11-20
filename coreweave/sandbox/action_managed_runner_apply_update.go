package sandbox

import (
	"context"
	"fmt"
	"regexp"
	"time"

	sandboxv1 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ action.ActionWithConfigure      = &ManagedRunnerApplyUpdateAction{}
	_ action.ActionWithValidateConfig = &ManagedRunnerApplyUpdateAction{}
)

type ManagedRunnerApplyUpdateAction struct{ client *coreweave.Client }

type managedRunnerApplyUpdateModel struct {
	RunnerID types.String `tfsdk:"runner_id"`
	Timeout  types.String `tfsdk:"timeout"`
}

func NewManagedRunnerApplyUpdateAction() action.Action { return &ManagedRunnerApplyUpdateAction{} }

func (a *ManagedRunnerApplyUpdateAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_sandbox_managed_runner_apply_update"
}

func (a *ManagedRunnerApplyUpdateAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Applies the latest available version on a managed runner's release channel, bypassing maintenance windows and exclusions. Requires Terraform 1.14 or later and SANDBOX_ADMIN permission. The version is selected when the action runs; it is not pinned by the plan.\n\nInvoking the action when the runner is already current does not create another revision. The action waits for the returned target revision to become active, ready, and connected. A concurrent change to the target revision fails the wait. Timeout or cancellation stops waiting but does not cancel or roll back the server-side rollout.\n\nActions do not refresh Terraform resource state; refresh the managed runner resource afterward to observe its status. Declaring this action alone does not run it during an ordinary apply: invoke it explicitly with `terraform apply -invoke=action.coreweave_sandbox_managed_runner_apply_update.example`.",
		Attributes: map[string]schema.Attribute{
			"runner_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Identifier of an existing managed runner in the authenticated organization.",
				Validators:          []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must contain a non-whitespace character")},
			},
			"timeout": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Maximum duration for the update request and rollout wait, such as `30m` or `1h`. Must be a positive Go duration. Defaults to `30m`. Individual HTTP requests also respect the provider's HTTP timeout.",
			},
		},
	}
}

func (a *ManagedRunnerApplyUpdateAction) Configure(_ context.Context, req action.ConfigureRequest, resp *action.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*coreweave.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Action Configure Type", fmt.Sprintf("Expected *coreweave.Client, got %T.", req.ProviderData))
		return
	}
	a.client = client
}

func runnerUpdateTimeout(value types.String) (time.Duration, error) {
	if value.IsNull() {
		return 30 * time.Minute, nil
	}
	duration, err := time.ParseDuration(value.ValueString())
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("timeout must be a positive duration, such as 30m or 1h")
	}
	return duration, nil
}

func (a *ManagedRunnerApplyUpdateAction) ValidateConfig(ctx context.Context, req action.ValidateConfigRequest, resp *action.ValidateConfigResponse) {
	var data managedRunnerApplyUpdateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.Timeout.IsUnknown() {
		return
	}
	if _, err := runnerUpdateTimeout(data.Timeout); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("timeout"), "Invalid Update Timeout", err.Error())
	}
}

func (a *ManagedRunnerApplyUpdateAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data managedRunnerApplyUpdateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, err := runnerUpdateTimeout(data.Timeout)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Update Timeout", err.Error())
		return
	}
	if a.client == nil || a.client.SandboxRunnerManagement == nil {
		resp.Diagnostics.AddError("Unconfigured Action", "The CoreWeave API client is not configured.")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	progress := func(message string) { resp.SendProgress(action.InvokeProgressEvent{Message: message}) }
	if err := applyRunnerUpdate(ctx, a.client.SandboxRunnerManagement, data.RunnerID.ValueString(), 5*time.Second, progress); err != nil {
		resp.Diagnostics.AddError("Runner Update Failed", err.Error())
	}
}

type runnerUpdateClient interface {
	ApplyAvailableUpdate(context.Context, *sandboxv1.ApplyAvailableUpdateRequest) (*sandboxv1.ManagedRunner, error)
	GetManagedRunner(context.Context, *sandboxv1.GetManagedRunnerRequest) (*sandboxv1.ManagedRunner, error)
}

func applyRunnerUpdate(ctx context.Context, client runnerUpdateClient, runnerID string, interval time.Duration, progress func(string)) error {
	progress(fmt.Sprintf("Requesting the latest channel update for runner %s (bypassing maintenance policy).", runnerID))
	runner, err := client.ApplyAvailableUpdate(ctx, &sandboxv1.ApplyAvailableUpdateRequest{RunnerId: runnerID})
	if err != nil {
		return fmt.Errorf("requesting runner update: %w. The request may have reached the server; inspect runner status before retrying", err)
	}
	target := runner.GetTargetRevision()
	if target <= 0 {
		return fmt.Errorf("the update response has no target revision; inspect runner status before retrying")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if runner.GetIdentity().GetRunnerId() != runnerID {
			return fmt.Errorf("the server returned a different runner identity; inspect runner status")
		}
		if runner.GetTargetRevision() != target {
			return fmt.Errorf("runner %s target changed from revision %d to %d while waiting; inspect the concurrent update", runnerID, target, runner.GetTargetRevision())
		}
		if runner.GetInstallStatus() == sandboxv1.RunnerInstallStatus_RUNNER_INSTALL_STATUS_FAILED {
			return fmt.Errorf("runner %s installation failed for target revision %d: %s", runnerID, target, runner.GetInstallError().GetMessage())
		}
		if runner.GetActiveRevision() == target &&
			runner.GetInstallStatus() == sandboxv1.RunnerInstallStatus_RUNNER_INSTALL_STATUS_READY &&
			runner.GetConnectionStatus() == sandboxv1.RunnerConnectionStatus_RUNNER_CONNECTION_STATUS_CONNECTED {
			progress(fmt.Sprintf("Runner %s is ready and connected at revision %d.", runnerID, target))
			return nil
		}
		progress(fmt.Sprintf("Waiting for runner %s revision %d: active revision %d, %s, %s.", runnerID, target, runner.GetActiveRevision(), runner.GetInstallStatus(), runner.GetConnectionStatus()))
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for runner %s revision %d: %w; the server-side rollout was not canceled", runnerID, target, ctx.Err())
		case <-ticker.C:
		}
		runner, err = client.GetManagedRunner(ctx, &sandboxv1.GetManagedRunnerRequest{RunnerId: runnerID})
		if err != nil {
			return fmt.Errorf("reading runner %s while waiting for revision %d: %w; the server-side rollout was not canceled", runnerID, target, err)
		}
	}
}
