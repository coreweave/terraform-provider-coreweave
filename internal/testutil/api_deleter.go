package testutil

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
)

type getterFunc[T any, X any] func(context.Context, *connect.Request[T]) (*connect.Response[X], error)

// WaitForDelete waits for a resource to be deleted by periodically calling a provided get function
// until it returns a "not found" error or the context is canceled.
//
// Parameters:
//   - ctx: The context to control the timeout and cancellation of the wait operation.
//   - getFunc: A function that takes a context and a request object, and returns the resource or an error. This can generally be a func with a coreweave client receiver.
//   - req: The request object to be passed to the get function.
//
// Returns:
//   - error: An error if the get function returns an error other than "not found", or if the context is canceled.
func WaitForDelete[R any, X any](ctx context.Context, timeout, interval time.Duration, getFunc getterFunc[R, X], req *R) error {
	if interval <= 0 {
		return fmt.Errorf("interval must be greater than 0, got %v", interval)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, err := getFunc(ctx, connect.NewRequest(req))
		if err != nil && connect.CodeOf(err) == connect.CodeNotFound {
			return nil
		} else if err != nil {
			return fmt.Errorf("unexpected error: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			continue
		}
	}
}
