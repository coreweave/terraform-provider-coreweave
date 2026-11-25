// Package static supplies a configured CoreWeave API token.
package static

import (
	"context"
	"strings"
	"sync"

	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
)

const envAPIToken = "COREWEAVE_API_TOKEN" //nolint:gosec // Environment variable name, not a credential.

// Provider supplies a CoreWeave API token.
type Provider struct {
	mu     sync.Mutex
	token  string
	closed bool
}

// New creates a provider for an explicitly configured API token.
func New(token string) (*Provider, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, providers.ErrCredentialsNotConfigured
	}
	return &Provider{token: token}, nil
}

// Detect returns the configured API token provider, or nil if the token is unset.
func Detect(getenv func(string) string) *Provider {
	provider, _ := New(getenv(envAPIToken))
	return provider
}

// Retrieve returns the configured token if the provider and caller are active.
func (p *Provider) Retrieve(ctx context.Context) (providers.Credential, error) {
	if err := ctx.Err(); err != nil {
		return providers.Credential{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return providers.Credential{}, providers.ErrClosed
	}
	if p.token == "" {
		return providers.Credential{}, providers.ErrCredentialsNotConfigured
	}
	return providers.Credential{Token: p.token}, nil
}

// Close releases the provider's reference to the token and closes the provider.
func (p *Provider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.token = ""
	p.closed = true
}
