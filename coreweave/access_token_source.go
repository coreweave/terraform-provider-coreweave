package coreweave

import (
	"context"
)

// AccessTokenSource supplies a token before each HTTP attempt. Implementations
// must be safe for concurrent use and handle their own caching and refreshes.
// The context belongs to the current attempt.
type AccessTokenSource interface {
	Token(ctx context.Context) (string, error)
}
