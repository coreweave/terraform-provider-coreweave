package containerregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	client "buf.build/gen/go/coreweave/container-registry-api/connectrpc/go/coreweave/registry/v1alpha1/registryv1alpha1connect"
	api "buf.build/gen/go/coreweave/container-registry-api/protocolbuffers/go/coreweave/registry/v1alpha1"
	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"google.golang.org/protobuf/proto"
)

// terminalOperationError distinguishes a completed failure from a failed poll.
type terminalOperationError struct{ error }

// Unwrap preserves Connect codes and structured error details.
func (e *terminalOperationError) Unwrap() error { return e.error }

type privateData interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
	SetKey(context.Context, string, []byte) diag.Diagnostics
}
type recovery struct {
	Action    string `json:"action"`
	Operation string `json:"operation,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
}

// saveRecovery persists accepted-operation evidence alongside returned Terraform state.
func saveRecovery(ctx context.Context, p privateData, r *recovery) error {
	if p == nil {
		return nil
	}
	var b []byte
	var e error
	if r != nil {
		b, e = json.Marshal(r)
		if e != nil {
			return e
		}
	}
	d := p.SetKey(ctx, "container_registry_recovery", b)
	if d.HasError() {
		return fmt.Errorf("saving recovery state: %s", d.Errors()[0].Detail())
	}
	return nil
}

// loadRecovery retrieves interrupted work without treating private state as a durable WAL.
func loadRecovery(ctx context.Context, p privateData) (*recovery, error) {
	if p == nil {
		return nil, nil
	}
	b, d := p.GetKey(ctx, "container_registry_recovery")
	if d.HasError() {
		return nil, fmt.Errorf("reading recovery state: %s", d.Errors()[0].Detail())
	}
	if len(b) == 0 {
		return nil, nil
	}
	r := new(recovery)
	if e := json.Unmarshal(b, r); e != nil {
		return nil, e
	}
	return r, nil
}

// poll checks immediately, then uses the shared polling helper within the action deadline.
func poll(ctx context.Context, operation string, check func(context.Context) (bool, error)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	done, err := check(ctx)
	if err != nil || done {
		return err
	}
	timeout := remainingTimeout(ctx)
	return coreweave.PollUntil(operation, ctx, 3*time.Second, timeout, check)
}

// operationRequest maps the exact canonical operation name to the public wrapper request.
func operationRequest(name string) (*api.RegistryServiceGetOperationRequest, error) {
	p := strings.Split(name, "/")
	if len(p) != 4 || p[0] != kindNamespaces || !validNamespace(p[1]) || p[2] != "operations" || p[3] == "" {
		return nil, fmt.Errorf("invalid operation name %q", name)
	}
	return &api.RegistryServiceGetOperationRequest{Namespace: p[1], Operation: p[3]}, nil
}

// operationResult decodes terminal completion without treating transport errors as operation failures.
func operationResult(op *longrunningpb.Operation, result proto.Message) (bool, error) {
	if !op.Done {
		return false, nil
	}
	if status := op.GetError(); status != nil {
		if status.Code < 1 || status.Code > 16 {
			return true, fmt.Errorf("operation %s returned invalid status code %d", op.Name, status.Code)
		}
		e := connect.NewError(connect.Code(status.Code), fmt.Errorf("operation %s: %s", op.Name, status.Message))
		for _, d := range status.Details {
			m, err := d.UnmarshalNew()
			if err != nil {
				continue
			}
			if detail, err := connect.NewErrorDetail(m); err == nil {
				e.AddDetail(detail)
			}
		}
		return true, &terminalOperationError{e}
	}
	if op.GetResponse() == nil {
		return true, fmt.Errorf("operation %s completed without a response", op.Name)
	}
	if err := op.GetResponse().UnmarshalTo(result); err != nil {
		return true, fmt.Errorf("operation %s response: %w", op.Name, err)
	}
	return true, nil
}

// waitOperation inspects initial completion before reading operation history.
func waitOperation(ctx context.Context, c client.RegistryServiceClient, op *longrunningpb.Operation, result proto.Message) error {
	if op == nil {
		return fmt.Errorf("API returned no operation")
	}
	if done, err := operationResult(op, result); done {
		return err
	}
	name := op.Name
	err := coreweave.PollUntil("operation "+name, ctx, 3*time.Second, remainingTimeout(ctx), func(ctx context.Context) (bool, error) {
		next, err := getOperation(ctx, c, name)
		if err != nil {
			return false, fmt.Errorf("operation %s: %w", name, err)
		}
		op = next
		if op.Name != name {
			return false, fmt.Errorf("operation identity changed from %s to %s", name, op.Name)
		}
		return operationResult(op, result)
	})
	if ctx.Err() != nil {
		return fmt.Errorf("operation %s is unfinished (last phase: %s); server work continues: %w", name, operationPhase(op), err)
	}
	return err
}

// remainingTimeout preserves the caller's Framework action deadline for shared polling.
func remainingTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return 20 * time.Minute
}

// waitAccess requires acceptance of precisely the owned desired revision.
func waitAccess(ctx context.Context, c client.RegistryServiceClient, parent string, revision int64) (*api.RegistryAccessConfiguration, error) {
	var last *api.RegistryAccessConfiguration
	err := poll(ctx, "access policy "+parent, func(ctx context.Context) (bool, error) {
		response, err := c.GetRegistryAccessConfiguration(ctx, connect.NewRequest(&api.GetRegistryAccessConfigurationRequest{Parent: parent}))
		if err != nil {
			return false, err
		}
		last = response.Msg
		if last.Revision > revision {
			return false, fmt.Errorf("competing writer at %s: expected revision %d, got %d; refresh and replan", parent, revision, last.Revision)
		}
		return last.Revision == revision && last.AccessConfigState == api.RegistryAccessConfiguration_ACCESS_CONFIG_STATE_ACCEPTED, nil
	})
	if ctx.Err() != nil && last != nil {
		return last, fmt.Errorf("access revision %d remains %s; server work continues: %w", revision, last.AccessConfigState, err)
	}
	return last, err
}

// waitLifecycle also covers ownership handoff without a new operation.
func waitLifecycle(ctx context.Context, c client.RegistryServiceClient, parent string, revision int64) (*api.RegistryLifecyclePolicy, error) {
	var last *api.RegistryLifecyclePolicy
	err := poll(ctx, "lifecycle policy "+parent, func(ctx context.Context) (bool, error) {
		response, err := c.GetRegistryLifecyclePolicy(ctx, connect.NewRequest(&api.GetRegistryLifecyclePolicyRequest{Parent: parent}))
		if err != nil {
			return false, err
		}
		last = response.Msg
		if last.Revision > revision {
			return false, fmt.Errorf("competing writer at %s: expected revision %d, got %d; refresh and replan", parent, revision, last.Revision)
		}
		return last.Revision == revision && last.AppliedRevision != nil && *last.AppliedRevision == revision, nil
	})
	if ctx.Err() != nil {
		return last, fmt.Errorf("lifecycle revision %d is not acknowledged; server work continues: %w", revision, err)
	}
	return last, err
}

// operationPhase reports public operation metadata without logging the original request.
func operationPhase(op *longrunningpb.Operation) string {
	const unknownPhase = "unknown"
	if op.Metadata == nil {
		return unknownPhase
	}
	m, err := op.Metadata.UnmarshalNew()
	if err != nil {
		return unknownPhase
	}
	fields := m.ProtoReflect().Descriptor().Fields()
	field := fields.ByName("phase")
	if field == nil {
		return unknownPhase
	}
	if enum := field.Enum(); enum != nil {
		if value := enum.Values().ByNumber(m.ProtoReflect().Get(field).Enum()); value != nil {
			return string(value.Name())
		}
	}
	return fmt.Sprint(m.ProtoReflect().Get(field))
}
