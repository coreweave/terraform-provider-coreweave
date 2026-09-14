package testutil

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
)

const defaultSweepParallelism = 4

type SweepRuntime struct {
	DryRun      bool
	Parallelism int
}

type SweepConfig[T any] struct {
	ResourceType string
	List         func(context.Context) ([]T, error)
	Name         func(T) string
	Match        func(T) bool
	Delete       func(context.Context, T) error
}

func SweepRuntimeFromEnv() (SweepRuntime, error) {
	runtime := SweepRuntime{Parallelism: defaultSweepParallelism}

	if value, ok := os.LookupEnv("SWEEP_DRY_RUN"); ok && value != "" {
		dryRun, err := strconv.ParseBool(value)
		if err != nil {
			return SweepRuntime{}, fmt.Errorf("parse SWEEP_DRY_RUN as bool: %w", err)
		}
		runtime.DryRun = dryRun
	}

	if value, ok := os.LookupEnv("TEST_ACC_SWEEP_PARALLEL"); ok && value != "" {
		parallelism, err := strconv.Atoi(value)
		if err != nil {
			return SweepRuntime{}, fmt.Errorf("parse TEST_ACC_SWEEP_PARALLEL as integer: %w", err)
		}
		if parallelism <= 0 {
			return SweepRuntime{}, fmt.Errorf("TEST_ACC_SWEEP_PARALLEL must be greater than zero, got %d", parallelism)
		}
		runtime.Parallelism = parallelism
	}

	return runtime, nil
}

func Sweep[T any](ctx context.Context, runtime SweepRuntime, config SweepConfig[T]) error {
	if err := validateSweep(runtime, config); err != nil {
		return err
	}

	resources, err := config.List(ctx)
	if err != nil {
		return fmt.Errorf("list %s resources: %w", config.ResourceType, err)
	}

	type namedResource struct {
		name     string
		resource T
	}
	selected := make([]namedResource, 0, len(resources))
	for _, resource := range resources {
		if config.Match(resource) {
			selected = append(selected, namedResource{name: config.Name(resource), resource: resource})
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].name < selected[j].name
	})

	if runtime.DryRun {
		for _, resource := range selected {
			slog.InfoContext(ctx, "dry-run: would sweep resource", "resource_type", config.ResourceType, "name", resource.name)
		}
		return nil
	}

	type job struct {
		index    int
		resource namedResource
	}
	jobs := make(chan job)
	deleteErrors := make([]error, len(selected))
	workerCount := min(runtime.Parallelism, len(selected))

	var workers sync.WaitGroup
	for range workerCount {
		workers.Go(func() {
			for job := range jobs {
				slog.InfoContext(ctx, "sweeping resource", "resource_type", config.ResourceType, "name", job.resource.name)
				if err := config.Delete(ctx, job.resource.resource); err != nil {
					if ctxErr := ctx.Err(); ctxErr != nil {
						err = withoutError(err, ctxErr)
						if err == nil {
							continue
						}
					}
					deleteErrors[job.index] = fmt.Errorf("delete %s %q: %w", config.ResourceType, job.resource.name, err)
				}
			}
		})
	}
	dispatching := true
	for index, resource := range selected {
		select {
		case <-ctx.Done():
			dispatching = false
		default:
		}
		if !dispatching {
			break
		}
		select {
		case <-ctx.Done():
			dispatching = false
		case jobs <- job{index: index, resource: resource}:
		}
		if !dispatching {
			break
		}
	}
	close(jobs)
	workers.Wait()

	joined := make([]error, 0, len(deleteErrors))
	for _, err := range deleteErrors {
		if err != nil {
			joined = append(joined, err)
		}
	}
	if err := ctx.Err(); err != nil {
		joined = append(joined, err)
	}
	return errors.Join(joined...)
}

func withoutError(err, target error) error {
	if err == nil || target == nil {
		return err
	}

	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		remaining := make([]error, 0, len(joined.Unwrap()))
		for _, child := range joined.Unwrap() {
			if filtered := withoutError(child, target); filtered != nil {
				remaining = append(remaining, filtered)
			}
		}
		return errors.Join(remaining...)
	}

	if errors.Is(err, target) {
		if wrapped, ok := err.(interface{ Unwrap() error }); ok {
			return withoutError(wrapped.Unwrap(), target)
		}
		return nil
	}

	return err
}

func validateSweep[T any](runtime SweepRuntime, config SweepConfig[T]) error {
	if config.ResourceType == "" {
		return errors.New("sweep ResourceType must not be empty")
	}
	if runtime.Parallelism <= 0 {
		return fmt.Errorf("sweep Parallelism must be greater than zero, got %d", runtime.Parallelism)
	}
	if config.List == nil {
		return fmt.Errorf("sweep %s List callback must not be nil", config.ResourceType)
	}
	if config.Name == nil {
		return fmt.Errorf("sweep %s Name callback must not be nil", config.ResourceType)
	}
	if config.Match == nil {
		return fmt.Errorf("sweep %s Match callback must not be nil", config.ResourceType)
	}
	if config.Delete == nil {
		return fmt.Errorf("sweep %s Delete callback must not be nil", config.ResourceType)
	}
	return nil
}
