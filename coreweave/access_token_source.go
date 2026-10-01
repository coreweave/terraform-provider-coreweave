package coreweave

import (
	"context"
)

// AccessTokenSource supplies a token before each HTTP attempt. Implementations
// must be safe for concurrent use and handle their own caching and refreshes.
// The context belongs to the current attempt.
//
// A source also owns its own retry policy and time budget. An error returned by
// Token, a deadline included, is final: the client does not retry the request
// on its behalf, because doing so would repeat the source's entire retry
// sequence once per attempt.
type AccessTokenSource interface {
	Token(ctx context.Context) (string, error)
}
