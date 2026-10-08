package containerregistry

import (
	"context"
	"errors"
	"fmt"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/coreweave/registry/v1alpha1/registryv1alpha1connect"
	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// observeRecovery checks accepted work once after an authoritative refresh; it never replays a write.
func observeRecovery(ctx context.Context, c client.RegistryServiceClient, revision int64, acknowledged bool, p privateData) error {
	rec, err := loadRecovery(ctx, p)
	if err != nil || rec == nil {
		return err
	}
	if rec.Operation != "" {
		op, err := getOperation(ctx, c, rec.Operation)
		if err != nil && !coreweave.IsNotFoundError(err) {
			return err
		}
		if err == nil && !op.Done {
			return nil
		}
	} else if rec.Action == recoveryAccessAccepted || rec.Action == recoveryLifecycleWait {
		if revision < rec.Revision || (revision == rec.Revision && !acknowledged) {
			return nil
		}
	}
	return saveRecovery(ctx, p, nil)
}

// waitSavedOperation waits for accepted work and preserves evidence on transport or polling failures.
func waitSavedOperation(ctx context.Context, c client.RegistryServiceClient, rec *recovery, result proto.Message, p privateData) (bool, error) {
	op, err := getOperation(ctx, c, rec.Operation)
	if coreweave.IsNotFoundError(err) {
		return false, saveRecovery(ctx, p, nil)
	}
	if err != nil {
		return false, err
	}
	if err = waitOperation(ctx, c, op, result); err != nil {
		var terminal *terminalOperationError
		if errors.As(err, &terminal) {
			_ = saveRecovery(ctx, p, nil)
		}
		return false, err
	}
	return true, nil
}

// resume waits only for a previously accepted namespace operation during apply.
func (r *NamespaceResource) resume(ctx context.Context, p privateData) (bool, error) {
	rec, err := loadRecovery(ctx, p)
	if err != nil || rec == nil {
		return false, err
	}
	if rec.Operation == "" {
		return false, saveRecovery(ctx, p, nil)
	}
	var result proto.Message = &api.RegistryNamespace{}
	if rec.Action == actionDelete {
		result = &emptypb.Empty{}
	}
	if _, err = waitSavedOperation(ctx, r.client, rec, result, p); err != nil {
		return false, err
	}
	return true, saveRecovery(ctx, p, nil)
}

// resume waits for an accepted access revision without replaying the update.
func (r *AccessConfigurationResource) resume(ctx context.Context, a *AccessConfigurationResourceModel, p privateData) error {
	rec, err := loadRecovery(ctx, p)
	if err != nil || rec == nil {
		return err
	}
	if rec.Action != recoveryAccessAccepted {
		return saveRecovery(ctx, p, nil)
	}
	result, err := waitAccess(ctx, r.client, namespaceResourceName(a.Namespace.ValueString()), rec.Revision)
	if result != nil {
		if setErr := conversionError(a.Set(ctx, result)); setErr != nil {
			return setErr
		}
	}
	if err != nil {
		return err
	}
	return saveRecovery(ctx, p, nil)
}

// resume waits for the saved lifecycle operation and its home-region acknowledgement.
func (r *LifecyclePolicyResource) resume(ctx context.Context, a *LifecyclePolicyResourceModel, p privateData) error {
	rec, err := loadRecovery(ctx, p)
	if err != nil || rec == nil {
		return err
	}
	if rec.Operation != "" {
		ack := new(api.UpdateRegistryLifecyclePolicyResponse)
		completed, err := waitSavedOperation(ctx, r.client, rec, ack, p)
		if err != nil || !completed {
			return err
		}
		if ack.Name != namespaceResourceName(a.Namespace.ValueString())+lifecycleSuffix {
			return fmt.Errorf("saved operation acknowledged an unexpected lifecycle policy")
		}
		rec.Revision = ack.AppliedRevision
		rec.Operation = ""
		rec.Action = recoveryLifecycleWait
		if err = saveRecovery(ctx, p, rec); err != nil {
			return err
		}
	}
	if rec.Action != recoveryLifecycleWait {
		return saveRecovery(ctx, p, nil)
	}
	result, err := waitLifecycle(ctx, r.client, namespaceResourceName(a.Namespace.ValueString()), rec.Revision)
	if result != nil {
		if setErr := conversionError(a.Set(ctx, result)); setErr != nil {
			return setErr
		}
	}
	if err != nil {
		return err
	}
	return saveRecovery(ctx, p, nil)
}

// protectPendingCreate protects only demonstrably pending work after an authoritative namespace NotFound.
// Central records a namespace before accepting its create operation, and purges only terminal operations.
// A completed or missing operation therefore cannot justify retaining an absent namespace indefinitely.
func protectPendingCreate(ctx context.Context, c client.RegistryServiceClient, p privateData) (bool, error) {
	rec, err := loadRecovery(ctx, p)
	if err != nil || rec == nil || rec.Action != actionCreate {
		return false, err
	}
	if rec.Operation == "" {
		return false, saveRecovery(ctx, p, nil)
	}
	op, err := getOperation(ctx, c, rec.Operation)
	if coreweave.IsNotFoundError(err) {
		return false, saveRecovery(ctx, p, nil)
	}
	if err != nil {
		return true, err
	}
	if op.Done {
		return false, saveRecovery(ctx, p, nil)
	}
	return true, nil
}

// getOperation fetches accepted work using its canonical name.
func getOperation(ctx context.Context, c client.RegistryServiceClient, name string) (*longrunningpb.Operation, error) {
	req, err := operationRequest(name)
	if err != nil {
		return nil, err
	}
	response, err := c.GetOperation(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}
