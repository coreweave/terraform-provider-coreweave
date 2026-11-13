package coretf

import (
	"context"
	"time"
)

// PollTimeout returns how long a poll may run: the remaining time on ctx when
// the caller has already applied the operator's `timeouts` value, otherwise
// fallback.
func PollTimeout(ctx context.Context, fallback time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return fallback
}
