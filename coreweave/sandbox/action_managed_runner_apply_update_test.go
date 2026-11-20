package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	sandboxv1 "buf.build/gen/go/coreweave/sandbox/protocolbuffers/go/coreweave/sandbox/v1"
	"connectrpc.com/connect/v2"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const updateTestRunnerID = "test"

type updateClientStub struct {
	runner   *sandboxv1.ManagedRunner
	applyErr error
	readErr  error
	onRead   func(*sandboxv1.ManagedRunner)
	applies  int
	reads    int
}

func (s *updateClientStub) ApplyAvailableUpdate(_ context.Context, req *sandboxv1.ApplyAvailableUpdateRequest) (*sandboxv1.ManagedRunner, error) {
	s.applies++
	if req.RunnerId != updateTestRunnerID {
		return nil, errors.New("incorrect runner ID")
	}
	return s.runner, s.applyErr
}
func (s *updateClientStub) GetManagedRunner(_ context.Context, req *sandboxv1.GetManagedRunnerRequest) (*sandboxv1.ManagedRunner, error) {
	s.reads++
	if req.RunnerId != updateTestRunnerID {
		return nil, errors.New("incorrect runner ID")
	}
	if s.onRead != nil {
		s.onRead(s.runner)
	}
	return s.runner, s.readErr
}
func readyUpdateRunner() *sandboxv1.ManagedRunner {
	return &sandboxv1.ManagedRunner{
		Identity:       &sandboxv1.RunnerIdentity{RunnerId: updateTestRunnerID},
		ActiveRevision: 2, TargetRevision: 2,
		InstallStatus:    sandboxv1.RunnerInstallStatus_RUNNER_INSTALL_STATUS_READY,
		ConnectionStatus: sandboxv1.RunnerConnectionStatus_RUNNER_CONNECTION_STATUS_CONNECTED,
	}
}

func TestApplyRunnerUpdate(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*updateClientStub)
		wantErr   string
		wantReads int
	}{
		{name: "already current"},
		{name: "wait for active revision", setup: func(s *updateClientStub) {
			s.runner.ActiveRevision = 1
			s.onRead = func(r *sandboxv1.ManagedRunner) { r.ActiveRevision = 2 }
		}, wantReads: 1},
		{name: "wait for connectivity", setup: func(s *updateClientStub) {
			s.runner.ConnectionStatus = sandboxv1.RunnerConnectionStatus_RUNNER_CONNECTION_STATUS_DISCONNECTED
			s.onRead = func(r *sandboxv1.ManagedRunner) {
				r.ConnectionStatus = sandboxv1.RunnerConnectionStatus_RUNNER_CONNECTION_STATUS_CONNECTED
			}
		}, wantReads: 1},
		{name: "wait for readiness", setup: func(s *updateClientStub) {
			s.runner.InstallStatus = sandboxv1.RunnerInstallStatus_RUNNER_INSTALL_STATUS_PROVISIONING
			s.onRead = func(r *sandboxv1.ManagedRunner) {
				r.InstallStatus = sandboxv1.RunnerInstallStatus_RUNNER_INSTALL_STATUS_READY
			}
		}, wantReads: 1},
		{name: "installation failure", setup: func(s *updateClientStub) {
			s.runner.ActiveRevision = 1
			s.onRead = func(r *sandboxv1.ManagedRunner) {
				r.InstallStatus = sandboxv1.RunnerInstallStatus_RUNNER_INSTALL_STATUS_FAILED
				r.InstallError = &sandboxv1.RunnerInstallError{Message: "insufficient capacity"}
			}
		}, wantReads: 1, wantErr: "insufficient capacity"},
		{name: "superseded revision", setup: func(s *updateClientStub) {
			s.runner.ActiveRevision = 1
			s.onRead = func(r *sandboxv1.ManagedRunner) { r.TargetRevision = 3; r.ActiveRevision = 3 }
		}, wantReads: 1, wantErr: "target changed from revision 2 to 3"},
		{name: "apply forbidden", setup: func(s *updateClientStub) {
			s.applyErr = connect.NewError(connect.CodePermissionDenied, "denied")
		}, wantErr: "permission_denied"},
		{name: "read failure", setup: func(s *updateClientStub) {
			s.runner.ActiveRevision = 1
			s.readErr = connect.NewError(connect.CodeNotFound, "deleted")
		}, wantReads: 1, wantErr: "not_found"},
		{name: "no revision", setup: func(s *updateClientStub) { s.runner.TargetRevision = 0 }, wantErr: "no target revision"},
		{name: "empty response", setup: func(s *updateClientStub) { s.runner = nil }, wantErr: "no target revision"},
		{name: "wrong identity", setup: func(s *updateClientStub) { s.runner.Identity.RunnerId = "other" }, wantErr: "different runner identity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &updateClientStub{runner: proto.Clone(readyUpdateRunner()).(*sandboxv1.ManagedRunner)}
			if tt.setup != nil {
				tt.setup(s)
			}
			var progress []string
			err := applyRunnerUpdate(t.Context(), s, updateTestRunnerID, time.Millisecond, func(message string) { progress = append(progress, message) })
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Contains(t, progress[len(progress)-1], "ready and connected at revision 2")
			}
			require.Equal(t, 1, s.applies)
			require.Equal(t, tt.wantReads, s.reads)
		})
	}
}

func TestApplyRunnerUpdateStopsWaiting(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancellation"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			s := &updateClientStub{runner: readyUpdateRunner()}
			s.runner.ActiveRevision = 1
			err := applyRunnerUpdate(ctx, s, updateTestRunnerID, time.Hour, func(message string) {
				if canceled && len(message) > 0 {
					cancel()
				}
			})
			if canceled {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			require.ErrorContains(t, err, "server-side rollout was not canceled")
			require.Equal(t, 1, s.applies)
			require.Zero(t, s.reads)
		})
	}
}

func TestRunnerUpdateTimeout(t *testing.T) {
	duration, err := runnerUpdateTimeout(types.StringNull())
	require.NoError(t, err)
	require.Equal(t, 30*time.Minute, duration)
	duration, err = runnerUpdateTimeout(types.StringValue("1h"))
	require.NoError(t, err)
	require.Equal(t, time.Hour, duration)
	for _, value := range []string{"", "0s", "-1m", "invalid", "9999999999999999999999h"} {
		_, err = runnerUpdateTimeout(types.StringValue(value))
		require.Error(t, err, value)
	}
}
