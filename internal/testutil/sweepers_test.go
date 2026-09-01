package testutil_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sweepResource struct {
	name     string
	selected bool
}

const (
	testResourceType        = "widgets"
	testIgnoredResourceName = "ignored"
)

func TestSweepRuntimeFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		dryRun      *string
		parallelism *string
		want        testutil.SweepRuntime
		wantError   string
	}{
		{name: "defaults", want: testutil.SweepRuntime{Parallelism: 4}},
		{name: "empty values use defaults", dryRun: pointer(""), parallelism: pointer(""), want: testutil.SweepRuntime{Parallelism: 4}},
		{name: "valid overrides", dryRun: pointer("true"), parallelism: pointer("7"), want: testutil.SweepRuntime{DryRun: true, Parallelism: 7}},
		{name: "malformed bool", dryRun: pointer("sometimes"), wantError: "parse SWEEP_DRY_RUN as bool"},
		{name: "malformed integer", parallelism: pointer("many"), wantError: "parse TEST_ACC_SWEEP_PARALLEL as integer"},
		{name: "zero parallelism", parallelism: pointer("0"), wantError: "must be greater than zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setOptionalEnv(t, "SWEEP_DRY_RUN", tt.dryRun)
			setOptionalEnv(t, "TEST_ACC_SWEEP_PARALLEL", tt.parallelism)

			got, err := testutil.SweepRuntimeFromEnv()
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSweepDryRun(t *testing.T) {
	for _, tt := range []struct {
		name      string
		value     string
		wantPanic bool
	}{
		{name: "empty is false"},
		{name: "malformed panics", value: "sometimes", wantPanic: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SWEEP_DRY_RUN", tt.value)
			if tt.wantPanic {
				assert.Panics(t, func() { testutil.SweepDryRun() })
				return
			}
			assert.False(t, testutil.SweepDryRun())
		})
	}
}

func TestSweep(t *testing.T) {
	errFirst := errors.New("first failure")
	errSecond := errors.New("second failure")
	tests := []struct {
		name           string
		runtime        testutil.SweepRuntime
		resources      []sweepResource
		listError      error
		deleteErrors   map[string]error
		mutateConfig   func(*testutil.SweepConfig[sweepResource])
		wantAttempted  []string
		wantError      []string
		wantExactError string
	}{
		{name: "no matches", runtime: testutil.SweepRuntime{Parallelism: 2}, resources: []sweepResource{{name: testIgnoredResourceName}}},
		{name: "filter and sort", runtime: testutil.SweepRuntime{Parallelism: 1}, resources: []sweepResource{{name: "c", selected: true}, {name: testIgnoredResourceName}, {name: "a", selected: true}}, wantAttempted: []string{"a", "c"}},
		{name: "dry run", runtime: testutil.SweepRuntime{DryRun: true, Parallelism: 2}, resources: []sweepResource{{name: "a", selected: true}}},
		{name: "list failure", runtime: testutil.SweepRuntime{Parallelism: 2}, listError: errors.New("list unavailable"), wantError: []string{"list widgets resources: list unavailable"}},
		{name: "aggregate errors and attempt all", runtime: testutil.SweepRuntime{Parallelism: 2}, resources: []sweepResource{{name: "z", selected: true}, {name: "a", selected: true}, {name: "m", selected: true}}, deleteErrors: map[string]error{"z": errSecond, "a": errFirst}, wantAttempted: []string{"a", "m", "z"}, wantError: []string{"delete widgets \"a\": first failure", "delete widgets \"z\": second failure"}, wantExactError: "delete widgets \"a\": first failure\ndelete widgets \"z\": second failure"},
		{name: "missing resource type", runtime: testutil.SweepRuntime{Parallelism: 1}, mutateConfig: func(config *testutil.SweepConfig[sweepResource]) { config.ResourceType = "" }, wantError: []string{"ResourceType must not be empty"}},
		{name: "invalid runtime", runtime: testutil.SweepRuntime{}, wantError: []string{"Parallelism must be greater than zero"}},
		{name: "missing list", runtime: testutil.SweepRuntime{Parallelism: 1}, mutateConfig: func(config *testutil.SweepConfig[sweepResource]) { config.List = nil }, wantError: []string{"List callback must not be nil"}},
		{name: "missing name", runtime: testutil.SweepRuntime{Parallelism: 1}, mutateConfig: func(config *testutil.SweepConfig[sweepResource]) { config.Name = nil }, wantError: []string{"Name callback must not be nil"}},
		{name: "missing match", runtime: testutil.SweepRuntime{Parallelism: 1}, mutateConfig: func(config *testutil.SweepConfig[sweepResource]) { config.Match = nil }, wantError: []string{"Match callback must not be nil"}},
		{name: "missing delete", runtime: testutil.SweepRuntime{Parallelism: 1}, mutateConfig: func(config *testutil.SweepConfig[sweepResource]) { config.Delete = nil }, wantError: []string{"Delete callback must not be nil"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attemptedLock sync.Mutex
			var attempted []string
			listCalls := 0
			config := testutil.SweepConfig[sweepResource]{
				ResourceType: testResourceType,
				List: func(context.Context) ([]sweepResource, error) {
					listCalls++
					return tt.resources, tt.listError
				},
				Name:  func(resource sweepResource) string { return resource.name },
				Match: func(resource sweepResource) bool { return resource.selected },
				Delete: func(ctx context.Context, resource sweepResource) error {
					attemptedLock.Lock()
					attempted = append(attempted, resource.name)
					attemptedLock.Unlock()
					if err := ctx.Err(); err != nil {
						return err
					}
					return tt.deleteErrors[resource.name]
				},
			}
			if tt.mutateConfig != nil {
				tt.mutateConfig(&config)
			}

			err := testutil.Sweep(t.Context(), tt.runtime, config)
			if len(tt.wantError) == 0 {
				require.NoError(t, err)
			} else {
				for _, fragment := range tt.wantError {
					assert.ErrorContains(t, err, fragment)
				}
			}
			if tt.wantExactError != "" {
				assert.EqualError(t, err, tt.wantExactError)
			}
			if tt.runtime.Parallelism > 1 {
				sort.Strings(attempted)
			}
			assert.Equal(t, tt.wantAttempted, attempted)
			if tt.mutateConfig != nil || tt.runtime.Parallelism <= 0 {
				assert.Zero(t, listCalls)
			} else {
				assert.Equal(t, 1, listCalls)
			}
		})
	}
}

func TestSweepLogsSelectedResources(t *testing.T) {
	tests := []struct {
		name        string
		runtime     testutil.SweepRuntime
		wantMessage string
		wantDeleted bool
	}{
		{name: "dry run", runtime: testutil.SweepRuntime{DryRun: true, Parallelism: 1}, wantMessage: "dry-run: would sweep resource"},
		{name: "actual delete", runtime: testutil.SweepRuntime{Parallelism: 1}, wantMessage: "sweeping resource", wantDeleted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureDefaultLogs(t)
			deleted := false
			err := testutil.Sweep(t.Context(), tt.runtime, testutil.SweepConfig[sweepResource]{
				ResourceType: testResourceType,
				List: func(context.Context) ([]sweepResource, error) {
					return []sweepResource{{name: "selected", selected: true}, {name: testIgnoredResourceName}}, nil
				},
				Name:  func(resource sweepResource) string { return resource.name },
				Match: func(resource sweepResource) bool { return resource.selected },
				Delete: func(context.Context, sweepResource) error {
					deleted = true
					return nil
				},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.wantDeleted, deleted)
			assert.Equal(t, []sweepLogRecord{{
				Message:      tt.wantMessage,
				ResourceType: testResourceType,
				Name:         "selected",
			}}, decodeSweepLogs(t, logs))
		})
	}
}

func TestSweepCancellationStopsDispatch(t *testing.T) {
	deleteFailure := errors.New("delete failed")
	compositeFailure := errors.New("composite failure")
	resources := []sweepResource{
		{name: "d", selected: true},
		{name: "c", selected: true},
		{name: "a", selected: true},
		{name: "b", selected: true},
	}
	started := make(chan string, 3)
	release := make(chan struct{})
	config := testutil.SweepConfig[sweepResource]{
		ResourceType: testResourceType,
		List:         func(context.Context) ([]sweepResource, error) { return resources, nil },
		Name:         func(resource sweepResource) string { return resource.name },
		Match:        func(sweepResource) bool { return true },
		Delete: func(ctx context.Context, resource sweepResource) error {
			started <- resource.name
			<-ctx.Done()
			<-release
			if resource.name == "a" {
				return deleteFailure
			}
			if resource.name == "b" {
				return errors.Join(compositeFailure, ctx.Err())
			}
			return ctx.Err()
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var releaseOnce sync.Once
	releaseWorkers := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseWorkers()
	done := make(chan error, 1)
	go func() {
		done <- testutil.Sweep(ctx, testutil.SweepRuntime{Parallelism: 3}, config)
	}()

	attempted := []string{waitForStarted(t, started), waitForStarted(t, started), waitForStarted(t, started)}
	cancel()
	releaseWorkers()
	var err error
	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("sweep did not stop after cancellation")
	}

	sort.Strings(attempted)
	assert.Equal(t, []string{"a", "b", "c"}, attempted)
	assert.EqualError(t, err, "delete widgets \"a\": delete failed\ndelete widgets \"b\": composite failure\ncontext canceled")
}

func TestSweepPreCanceledContextDoesNotDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	deleteCalls := 0

	err := testutil.Sweep(ctx, testutil.SweepRuntime{Parallelism: 1}, testutil.SweepConfig[sweepResource]{
		ResourceType: testResourceType,
		List: func(context.Context) ([]sweepResource, error) {
			return []sweepResource{{name: "not-dispatched", selected: true}}, nil
		},
		Name:  func(resource sweepResource) string { return resource.name },
		Match: func(resource sweepResource) bool { return resource.selected },
		Delete: func(context.Context, sweepResource) error {
			deleteCalls++
			return nil
		},
	})

	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, deleteCalls)
}

type cancelOnErrContext struct {
	context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func (ctx *cancelOnErrContext) Err() error {
	ctx.once.Do(ctx.cancel)
	return ctx.Context.Err()
}

func TestSweepDeletesHandedOffJobWhenCancellationBecomesObservable(t *testing.T) {
	baseCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &cancelOnErrContext{Context: baseCtx, cancel: cancel}
	attempted := make(chan string, 1)

	err := testutil.Sweep(ctx, testutil.SweepRuntime{Parallelism: 1}, testutil.SweepConfig[sweepResource]{
		ResourceType: testResourceType,
		List: func(context.Context) ([]sweepResource, error) {
			return []sweepResource{{name: "handed-off", selected: true}}, nil
		},
		Name:  func(resource sweepResource) string { return resource.name },
		Match: func(resource sweepResource) bool { return resource.selected },
		Delete: func(ctx context.Context, resource sweepResource) error {
			attempted <- resource.name
			return ctx.Err()
		},
	})

	assert.ErrorIs(t, err, context.Canceled)
	require.Len(t, attempted, 1)
	assert.Equal(t, "handed-off", <-attempted)
}

func TestSweepBoundsConcurrency(t *testing.T) {
	const parallelism = 3
	resources := make([]sweepResource, 12)
	for index := range resources {
		resources[index] = sweepResource{name: fmt.Sprintf("resource-%02d", index), selected: true}
	}

	var active atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, len(resources))
	config := testutil.SweepConfig[sweepResource]{
		ResourceType: testResourceType,
		List:         func(context.Context) ([]sweepResource, error) { return resources, nil },
		Name:         func(resource sweepResource) string { return resource.name },
		Match:        func(sweepResource) bool { return true },
		Delete: func(context.Context, sweepResource) error {
			current := active.Add(1)
			old := maximum.Load()
			for current > old && !maximum.CompareAndSwap(old, current) {
				old = maximum.Load()
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			return nil
		},
	}

	done := make(chan error, 1)
	ctx := t.Context()
	go func() {
		done <- testutil.Sweep(ctx, testutil.SweepRuntime{Parallelism: parallelism}, config)
	}()
	for range parallelism {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("workers did not reach configured concurrency")
		}
	}
	exceeded := false
	select {
	case <-started:
		exceeded = true
	case <-time.After(100 * time.Millisecond):
	}
	assert.Equal(t, int32(parallelism), maximum.Load())
	close(release)
	require.NoError(t, <-done)
	assert.False(t, exceeded, "a fourth callback started while three were blocked")
	assert.LessOrEqual(t, maximum.Load(), int32(parallelism))
}

func ExampleSweep() {
	resources := []string{"second", "first"}
	err := testutil.Sweep(context.Background(), testutil.SweepRuntime{Parallelism: 1}, testutil.SweepConfig[string]{
		ResourceType: "examples",
		List:         func(context.Context) ([]string, error) { return resources, nil },
		Name:         func(name string) string { return name },
		Match:        func(string) bool { return true },
		Delete: func(_ context.Context, name string) error {
			fmt.Println(name)
			return nil
		},
	})
	if err != nil {
		fmt.Println(err)
	}
	// Output:
	// first
	// second
}

func pointer(value string) *string { return &value }

func waitForStarted(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case name := <-started:
		return name
	case <-time.After(time.Second):
		t.Fatal("delete did not start")
		return ""
	}
}

func setOptionalEnv(t *testing.T, name string, value *string) {
	t.Helper()
	if value != nil {
		t.Setenv(name, *value)
		return
	}
	previous, found := os.LookupEnv(name)
	require.NoError(t, os.Unsetenv(name))
	t.Cleanup(func() {
		if found {
			require.NoError(t, os.Setenv(name, previous))
		} else {
			require.NoError(t, os.Unsetenv(name))
		}
	})
}

type sweepLogRecord struct {
	Message      string `json:"msg"`
	ResourceType string `json:"resource_type"`
	Name         string `json:"name"`
}

func captureDefaultLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	return &logs
}

func decodeSweepLogs(t *testing.T, logs *bytes.Buffer) []sweepLogRecord {
	t.Helper()
	var records []sweepLogRecord
	decoder := json.NewDecoder(logs)
	for {
		var record sweepLogRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		records = append(records, record)
	}
	return records
}
