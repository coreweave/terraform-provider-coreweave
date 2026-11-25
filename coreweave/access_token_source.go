package coreweave

import (
	"context"
)

// AccessTokenSource supplies a token before each HTTP attempt. Implementations
// must be safe for concurrent use and handle their own caching and refreshes.
// The context belongs to the current attempt.
//
// A source owns its retry policy and time budget. A returned error is final for
// the application request; the outer client must not repeat the source's
// exchange sequence.
type AccessTokenSource interface {
	Token(ctx context.Context) (string, error)
}
