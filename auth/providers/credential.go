// Package providers defines the credential provider interface and shared types.
package providers

import (
	"errors"
	"time"
)

// ErrCredentialsNotConfigured indicates that a provider has no credentials.
var ErrCredentialsNotConfigured = errors.New("credentials are not configured")

// ErrClosed indicates that the provider has been closed.
var ErrClosed = errors.New("authentication provider is closed")

// Credential is a CoreWeave bearer credential. ExpiresAt is zero when the
// provider does not know its expiration time.
type Credential struct {
	Token     string
	ExpiresAt time.Time
}
