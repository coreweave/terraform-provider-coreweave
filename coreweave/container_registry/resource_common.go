package containerregistry

import (
	"context"
	"errors"
	"fmt"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/coreweave/registry/v1alpha1/registryv1alpha1connect"
	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/go-uuid"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// report keeps local operation failures distinct from shared API diagnostics.
func report(ctx context.Context, err error, d *diag.Diagnostics) {
	if err == nil {
		return
	}
	var conversion *diagnosticError
	if errors.As(err, &conversion) {
		d.Append(conversion.diagnostics...)
		return
	}
	var apiErr *connect.Error
	if !errors.As(err, &apiErr) {
		d.AddError("Container Registry operation failed", err.Error())
		return
	}
	coreweave.HandleAPIError(ctx, err, d)
}

// namespaceResourceName translates a Terraform namespace name at the API boundary.
func namespaceResourceName(name string) string {
	return kindNamespaces + "/" + name
}

// getNamespace verifies parent existence and optional ACTIVE mutation admission.
func getNamespace(ctx context.Context, c client.RegistryServiceClient, name string, active bool) (*api.RegistryNamespace, error) {
	p, e := c.GetRegistryNamespace(ctx, connect.NewRequest(&api.GetRegistryNamespaceRequest{Name: name}))
	if e != nil {
		return nil, e
	}
	if active && p.Msg.State != api.RegistryNamespace_STATE_ACTIVE {
		return p.Msg, fmt.Errorf("parent %s is %s; policy mutation requires ACTIVE", name, p.Msg.State)
	}
	return p.Msg, nil
}

// finishRecovery clears terminal failures but retains accepted work on polling errors.
func finishRecovery(ctx context.Context, p privateData, err error) error {
	var terminal *terminalOperationError
	if err != nil && errors.As(err, &terminal) {
		if saveErr := saveRecovery(ctx, p, nil); saveErr != nil {
			return saveErr
		}
	} else if err != nil && definitiveRejection(err) {
		rec, loadErr := loadRecovery(ctx, p)
		if loadErr != nil {
			return loadErr
		}
		if rec != nil && rec.Operation == "" && (rec.Action == actionCreate || rec.Action == actionUpdate || rec.Action == actionDelete || rec.Action == recoveryLifecycle) {
			if saveErr := saveRecovery(ctx, p, nil); saveErr != nil {
				return saveErr
			}
		}
	}
	return err
}

// definitiveRejection distinguishes rejected requests from uncertain transport outcomes.
func definitiveRejection(err error) bool {
	// Transient, ambiguous, and conflict outcomes deliberately retain recovery evidence.
	//nolint:exhaustive
	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument, connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeAlreadyExists, connect.CodeFailedPrecondition, connect.CodeUnimplemented:
		return true
	default:
		return false
	}
}

// newIdempotencyKey generates a canonical UUIDv4 for Registry API mutations.
func newIdempotencyKey() (string, error) {
	b, err := uuid.GenerateRandomBytes(16)
	if err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return uuid.FormatUUID(b)
}

// nullResourceTimeouts initializes imported timeout state with the Framework custom type.
func nullResourceTimeouts() timeouts.Value {
	attribute := timeouts.AttributesAll(context.Background())
	return timeouts.Value{Object: types.ObjectNull(attribute.GetType().(timeouts.Type).AttrTypes)}
}
