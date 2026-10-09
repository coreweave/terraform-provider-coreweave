package observability

import (
	"context"
	"fmt"
	"time"

	clusterv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	typesv1beta1 "bsr.core-services.ingress.coreweave.com/gen/go/coreweave/o11y-mgmt/protocolbuffers/go/coreweave/telemetryrelay/types/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
)

const (
	endpointTimeout = 5 * time.Minute
)

func commonEndpointSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"slug": schema.StringAttribute{
			MarkdownDescription: "The slug of the forwarding endpoint. Used as a unique identifier. Must be 3-32 characters, start with an alphanumeric character, and contain only alphanumeric characters and hyphens.",
			Required:            true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
			},
			Validators: []validator.String{
				stringvalidator.LengthBetween(slugMinLength, endpointSlugMaxLength),
				stringvalidator.RegexMatches(slugPattern, slugPatternDetail),
			},
		},
		"display_name": schema.StringAttribute{
			MarkdownDescription: "The display name of the forwarding endpoint.",
			Required:            true,
			Validators: []validator.String{
				stringvalidator.LengthBetween(1, displayNameMaxLength),
			},
		},
		"created_at": schema.StringAttribute{
			MarkdownDescription: "The creation time of the forwarding endpoint.",
			Computed:            true,
			CustomType:          timetypes.RFC3339Type{},
			PlanModifiers: []planmodifier.String{
				// created_at is fixed for the life of the endpoint, so pinning it to
				// state keeps it out of every plan. The volatile status attributes below
				// deliberately have no such modifier.
				stringplanmodifier.UseStateForUnknown(),
			},
		},
		"updated_at": schema.StringAttribute{
			MarkdownDescription: "The last update time of the forwarding endpoint.",
			Computed:            true,
			CustomType:          timetypes.RFC3339Type{},
		},
		// state and state_message are observed, not desired: the server moves them
		// independently of config (a healthy endpoint can go CONNECTED -> ERROR with
		// no plan in sight). UseStateForUnknown() here would make the plan assert they
		// cannot change, so Terraform would report a value it has no basis for. Leave
		// them unpinned and let each plan show `(known after apply)`.
		"state": schema.StringAttribute{
			MarkdownDescription: fmt.Sprintf("The state of the forwarding endpoint. One of: %s.", coreweave.EnumMarkdownValues(typesv1beta1.ForwardingEndpointState_name, true)),
			Computed:            true,
		},
		"state_message": schema.StringAttribute{
			MarkdownDescription: "Additional context about the current state, most useful when `state` is `FORWARDING_ENDPOINT_STATE_ERROR`.",
			Computed:            true,
		},
		"credentials_version": schema.Int64Attribute{
			MarkdownDescription: "Rotation counter for `credentials`. Credential attributes are write-only, so they are null in both plan and state and cannot produce a diff on their own. " +
				"Increment this whenever you change a credential value; the next apply then sends the new credentials. " +
				"**Changing a credential without incrementing this is a silent no-op** — the stored credentials are left as they are.",
			Optional: true,
			Validators: []validator.Int64{
				int64validator.AtLeast(0),
			},
		},
		"credentials_configured": schema.BoolAttribute{
			MarkdownDescription: "Whether credentials are currently stored for this endpoint. Does not indicate that they are valid, only that they exist.",
			Computed:            true,
		},
		"credentials_updated_at": schema.StringAttribute{
			MarkdownDescription: "When credentials were last set or replaced. Null if credentials have never been configured.",
			Computed:            true,
			CustomType:          timetypes.RFC3339Type{},
		},
	}
}

// credentialAction decides what UpdateEndpointRequest.credential_action should
// carry. Write-only credential values are null in plan and state, so the
// decision is made from the surrounding signals: whether the config still has
// a credentials block, whether the server already holds credentials, and
// whether the user bumped the rotation counter.
//
//	credentials removed, server has some -> CLEAR    (rejected for S3)
//	credentials removed, server has none -> PRESERVE (nothing to do)
//	credentials newly added              -> REPLACE
//	credentials_version changed          -> REPLACE
//	otherwise                            -> PRESERVE
//
// Only REPLACE may carry a credentials payload; the proto says so and the
// server enforces it.
func credentialAction(hasCredentials, priorConfigured bool, priorVersion, configVersion types.Int64) clusterv1beta1.UpdateEndpointRequest_CredentialAction {
	switch {
	case !hasCredentials && priorConfigured:
		return clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_CLEAR
	case !hasCredentials:
		return clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_PRESERVE
	case !priorConfigured:
		return clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_REPLACE
	case !priorVersion.Equal(configVersion):
		return clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_REPLACE
	default:
		return clusterv1beta1.UpdateEndpointRequest_CREDENTIAL_ACTION_PRESERVE
	}
}

func pollForEndpointReady(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef) (*typesv1beta1.ForwardingEndpoint, error) {
	pollConf := retry.StateChangeConf{
		Pending: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_CONNECTED.String(),
		},
		Refresh: func() (result any, state string, err error) {
			getResp, err := client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: ref,
			}))
			if err != nil {
				return nil, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			endpoint := getResp.Msg.GetEndpoint()
			status := endpoint.GetStatus()

			// ERROR is terminal. Without this the poll treats it as an unrecognized
			// state and the user gets "unexpected state" after the full timeout
			// instead of the server's reason, now.
			if status.GetState() == typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_ERROR {
				return endpoint, status.GetState().String(), fmt.Errorf("endpoint %q entered state %s: %s",
					ref.GetSlug(), status.GetState().String(), endpointStateMessage(status))
			}

			return endpoint, status.GetState().String(), nil
		},
		Timeout: endpointTimeout,
	}

	rawEndpoint, err := pollConf.WaitForStateContext(ctx)
	if err != nil {
		return nil, err
	}

	endpoint, ok := rawEndpoint.(*typesv1beta1.ForwardingEndpoint)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T when waiting for forwarding endpoint", rawEndpoint)
	}

	return endpoint, nil
}

// endpointStateMessage returns the server's explanation for the current state,
// or a placeholder when it supplied none.
func endpointStateMessage(status *typesv1beta1.ForwardingEndpointStatus) string {
	if msg := status.GetStateMessage(); msg != "" {
		return msg
	}
	return "no state_message was returned by the API"
}

func readEndpoint(ctx context.Context, client *coreweave.Client, slug string, diags *diag.Diagnostics) *typesv1beta1.ForwardingEndpoint {
	ref := &typesv1beta1.ForwardingEndpointRef{Slug: slug}
	getResp, err := client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
		Ref: ref,
	}))
	if err != nil {
		if coreweave.IsNotFoundError(err) {
			return nil
		}
		coreweave.HandleAPIError(ctx, err, diags)
		return nil
	}

	return getResp.Msg.GetEndpoint()
}

// createEndpoint issues the Create RPC and returns the server's immediate view
// of the endpoint. It deliberately does not poll: the caller must write that
// view to state first, so a poll failure or timeout cannot orphan an endpoint
// the API has already accepted.
func createEndpoint(ctx context.Context, client *coreweave.Client, req *clusterv1beta1.CreateEndpointRequest) (endpoint *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	createResp, err := client.CreateEndpoint(ctx, connect.NewRequest(req))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &diagnostics)
		return nil, diagnostics
	}

	return createResp.Msg.GetEndpoint(), diagnostics
}

// updateEndpoint issues the Update RPC and returns the server's immediate view
// of the endpoint. As with createEndpoint, polling is the caller's job so that
// state can be written in between.
func updateEndpoint(ctx context.Context, client *coreweave.Client, req *clusterv1beta1.UpdateEndpointRequest) (endpoint *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	updateResp, err := client.UpdateEndpoint(ctx, connect.NewRequest(req))
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &diagnostics)
		return nil, diagnostics
	}

	return updateResp.Msg.GetEndpoint(), diagnostics
}

// awaitEndpointReady polls the endpoint to a terminal state and converts the
// outcome into diagnostics.
func awaitEndpointReady(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef) (endpoint *typesv1beta1.ForwardingEndpoint, diagnostics diag.Diagnostics) {
	endpoint, err := pollForEndpointReady(ctx, client, ref)
	if err != nil {
		coreweave.HandleAPIError(ctx, err, &diagnostics)
		return nil, diagnostics
	}

	return endpoint, diagnostics
}

// deleteEndpoint is the common Delete implementation for all forwarding endpoint resources.
// The data parameter should be a struct that has a ToMsg() method returning (*typesv1beta1.ForwardingEndpoint, diag.Diagnostics).
func deleteEndpoint(ctx context.Context, client *coreweave.Client, data interface {
	ToMsg() (*typesv1beta1.ForwardingEndpoint, diag.Diagnostics)
}) (diagnostics diag.Diagnostics) {
	endpointMsg, diags := data.ToMsg()
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return
	}

	if err := deleteEndpointAndWait(ctx, client, endpointMsg.GetRef()); err != nil {
		diagnostics.AddError("Error deleting Telemetry Relay endpoint", err.Error())
		return
	}

	return
}

func deleteEndpointAndWait(ctx context.Context, client *coreweave.Client, ref *typesv1beta1.ForwardingEndpointRef) error {
	if _, err := client.DeleteEndpoint(ctx, connect.NewRequest(&clusterv1beta1.DeleteEndpointRequest{
		Ref: ref,
	})); err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil
		}

		return fmt.Errorf("failed to delete endpoint %q: %w", ref.Slug, err)
	}

	// Poll until the endpoint is fully deleted
	pollConf := retry.StateChangeConf{
		Pending: []string{
			typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_PENDING.String(),
		},
		Target: []string{},
		Refresh: func() (any, string, error) {
			result, err := client.GetEndpoint(ctx, connect.NewRequest(&clusterv1beta1.GetEndpointRequest{
				Ref: ref,
			}))
			if err != nil {
				if coreweave.IsNotFoundError(err) {
					return nil, "", nil
				}
				return nil, typesv1beta1.ForwardingEndpointState_FORWARDING_ENDPOINT_STATE_UNSPECIFIED.String(), err
			}
			endpoint := result.Msg.GetEndpoint()
			return endpoint, endpoint.GetStatus().GetState().String(), nil
		},
		Timeout: endpointTimeout,
	}

	if _, err := pollConf.WaitForStateContext(ctx); err != nil {
		return fmt.Errorf("endpoint was not deleted: %w", err)
	}

	return nil
}
