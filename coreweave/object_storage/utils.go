package objectstorage

import (
	"context"
	"fmt"
	"time"
)

// pollUntil runs check(ctx) every interval until it returns (true, nil),
// or else returns the first non‐nil error, or a timeout error.
func pollUntil(operation string, parentCtx context.Context, interval, timeout time.Duration, check func(ctx context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out polling for %s after %v: %w", operation, timeout, ctx.Err())
		case <-ticker.C:
			ok, err := check(ctx)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}
	}
}
