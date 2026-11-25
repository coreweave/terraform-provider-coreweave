package serviceaccount

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/auth/cache"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
)

const (
	hardRefreshWindow    = 2 * time.Minute
	softRefreshWindow    = 5 * time.Minute
	maxRefreshJitter     = 2 * time.Minute
	minRefreshAge        = time.Minute
	tokenWarmup          = 10 * time.Second
	exchangeFailureDelay = 10 * time.Second
)

// Provider exchanges an OIDC token for a CoreWeave bearer token and manages refresh.
// Construct a provider with New or Detect.
type Provider struct {
	options  Options
	cacheKey string
	cache    cache.Cache

	mu       sync.Mutex
	exchange tokenExchange
	ctx      context.Context
	cancel   context.CancelFunc
	cached   cachedCredential // Published snapshot for readers during a refresh.
	flight   *refreshFlight
	failure  error
	retryAt  time.Time
}

type cachedCredential struct {
	providers.Credential
	SoftRefreshAt    time.Time `json:"RefreshAt"`
	RefreshNotBefore time.Time
}

type refreshFlight struct {
	done       chan struct{}
	force      bool
	credential providers.Credential
	warning    error
	err        error
}

func newProvider(opts Options) *Provider {
	ctx, cancel := context.WithCancel(context.Background())
	store := opts.Cache
	if store == nil {
		store = cache.NewMemory()
	}
	provider := &Provider{options: opts, cache: store, ctx: ctx, cancel: cancel}
	provider.exchange = provider.newTokenExchange()
	return provider
}

// Retrieve returns a cached credential or waits for a refresh to complete.
func (p *Provider) Retrieve(ctx context.Context) (providers.Credential, error) {
	if err := ctx.Err(); err != nil {
		return providers.Credential{}, err
	}

	credential, flight, err := p.credentialOrRefresh()
	if flight == nil {
		return credential, err
	}
	return p.waitForRefresh(ctx, flight, true)
}

// Refresh forces an exchange and requires the cache write to succeed.
func (p *Provider) Refresh(ctx context.Context) (providers.Credential, error) {
	for {
		if err := ctx.Err(); err != nil {
			return providers.Credential{}, err
		}
		p.mu.Lock()
		if p.ctx.Err() != nil {
			p.mu.Unlock()
			return providers.Credential{}, providers.ErrClosed
		}
		flight := p.startRefresh(true)
		p.mu.Unlock()
		if flight.force {
			return p.waitForRefresh(ctx, flight, false)
		}

		select {
		case <-ctx.Done():
			return providers.Credential{}, ctx.Err()
		case <-p.ctx.Done():
			return providers.Credential{}, providers.ErrClosed
		case <-flight.done:
		}
	}
}

func (p *Provider) waitForRefresh(ctx context.Context, flight *refreshFlight, allowFallback bool) (providers.Credential, error) {
	select {
	case <-ctx.Done():
		return providers.Credential{}, ctx.Err()
	case <-p.ctx.Done():
		return providers.Credential{}, providers.ErrClosed
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return providers.Credential{}, err
		}
		p.mu.Lock()
		if p.ctx.Err() != nil {
			p.mu.Unlock()
			return providers.Credential{}, providers.ErrClosed
		}
		credential, err, warning := flight.credential, flight.err, flight.warning
		if allowFallback {
			if fallback, ok := p.cached.fallback(err, time.Now()); ok {
				credential, err = fallback, nil
				warning = fmt.Errorf("could not refresh workload credential (%w); serving cached credential valid until %s",
					flight.err, credential.ExpiresAt.UTC().Format(time.RFC3339))
			}
		}
		p.mu.Unlock()
		if warning != nil && p.options.OnWarning != nil {
			p.options.OnWarning(warning)
		}
		return credential, err
	}
}

func (p *Provider) credentialOrRefresh() (providers.Credential, *refreshFlight, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		return providers.Credential{}, nil, providers.ErrClosed
	}

	now := time.Now()
	if !p.cached.valid(now) {
		p.cached = cachedCredential{}
	} else if p.cached.fresh(now, p.options.HardRefreshOnly) {
		return p.cached.Credential, nil, nil
	}
	if p.flight == nil && !now.Before(p.retryAt) {
		p.startRefresh(false)
	}
	if p.cached.ExpiresAt.Sub(now) >= hardRefreshWindow {
		return p.cached.Credential, nil, nil
	}
	if p.flight != nil {
		return providers.Credential{}, p.flight, nil
	}
	if credential, ok := p.cached.fallback(p.failure, now); ok {
		return credential, nil, nil
	}
	return providers.Credential{}, nil, withRemainingRetryDelay(p.failure, now, p.retryAt)
}

func (c cachedCredential) valid(now time.Time) bool {
	return c.Token != "" && c.ExpiresAt.After(now)
}

func (c cachedCredential) fresh(now time.Time, hardOnly bool) bool {
	if !c.valid(now) {
		return false
	}
	if now.Before(c.RefreshNotBefore) {
		return true
	}
	if hardOnly {
		return c.ExpiresAt.Sub(now) >= hardRefreshWindow
	}
	return !now.After(c.SoftRefreshAt)
}

// Exchange timeouts and transient failures allow continued use of a valid token.
func (c cachedCredential) fallback(err error, now time.Time) (providers.Credential, bool) {
	var exchangeErr *ExchangeError
	recoverable := retryableExchangeError(err) ||
		(errors.As(err, &exchangeErr) && errors.Is(err, context.DeadlineExceeded))
	if recoverable && c.valid(now) {
		return c.Credential, true
	}
	return providers.Credential{}, false
}

// startRefresh runs under mu and returns the current or new flight.
func (p *Provider) startRefresh(force bool) *refreshFlight {
	if p.flight == nil {
		p.flight = &refreshFlight{done: make(chan struct{}), force: force}
		go p.refresh(p.flight, p.cached)
	}
	return p.flight
}

func (p *Provider) refresh(flight *refreshFlight, previous cachedCredential) {
	cached, warning, softRefresh, err := p.refreshFromCache(flight.force, previous)
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	switch {
	case p.ctx.Err() != nil:
		err = providers.ErrClosed
		p.clearOwnedCache()
	case err == nil && !cached.valid(now):
		err = errors.New("workload credential expired before publication")
	}
	if err == nil {
		p.cached = cached
		p.failure = nil
		p.retryAt = time.Time{}
		flight.credential = cached.Credential
	} else if p.ctx.Err() == nil {
		if !flight.force && cached.valid(now) {
			p.cached = cached
		}
		p.failure = err
		p.retryAt = now.Add(exchangeFailureDelay)
		if serverRetryAt := exchangeRetryAt(err); serverRetryAt.After(p.retryAt) {
			p.retryAt = serverRetryAt
		}
		err = withRemainingRetryDelay(err, now, p.retryAt)
	}
	flight.err = err
	flight.warning = warning
	p.flight = nil
	close(flight.done)
	if err == nil && softRefresh {
		p.startRefresh(false)
	}
}

func (p *Provider) mintCredential() (cachedCredential, error) {
	credential, err := p.exchangeWithRetry()
	if err != nil {
		return cachedCredential{}, err
	}
	receivedAt := time.Now()
	readyAt := receivedAt.Add(tokenWarmup)
	if !credential.ExpiresAt.After(readyAt) {
		return cachedCredential{}, errors.New("workload credential expires during warm-up")
	}
	if err := waitForExchange(p.ctx, time.Until(readyAt)); err != nil {
		return cachedCredential{}, err
	}
	jitter := rand.N(maxRefreshJitter + time.Nanosecond) //nolint:gosec // Refresh jitter is not security-sensitive randomness.
	return cachedCredential{
		Credential:       credential,
		SoftRefreshAt:    credential.ExpiresAt.Add(-softRefreshWindow - jitter),
		RefreshNotBefore: receivedAt.Add(minRefreshAge),
	}, nil
}

// Close cancels the exchange and releases resources owned by the provider.
func (p *Provider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx == nil || p.ctx.Err() != nil {
		return
	}
	p.cancel()
	p.cached = cachedCredential{}
	p.failure = nil
	if p.flight == nil {
		p.clearOwnedCache()
	}
}

func waitForExchange(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
