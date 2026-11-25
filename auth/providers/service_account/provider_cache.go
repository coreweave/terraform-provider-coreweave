package serviceaccount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/auth/cache"
)

// The default memory cache belongs to one provider, even for callbacks that
// lack a shareable cache key.
const memoryCredentialKey = "credential"

func (p *Provider) storageKey() string {
	if p.options.Cache == nil {
		return memoryCredentialKey
	}
	return p.cacheKey
}

// clearOwnedCache runs under mu when no refresh holds the entry.
func (p *Provider) clearOwnedCache() {
	if p.options.Cache == nil {
		_ = p.cache.WithEntry(context.Background(), memoryCredentialKey, cache.Entry.Delete)
	}
}

func (p *Provider) refreshFromCache(force bool, previous cachedCredential) (cached cachedCredential, warning error, softRefresh bool, err error) {
	cached = previous
	err = p.cache.WithEntry(p.ctx, p.storageKey(), func(entry cache.Entry) error {
		if !force {
			stored, err := loadCachedCredential(entry)
			if err != nil {
				return err
			}
			now := time.Now()
			if stored.valid(now) {
				cached = stored
				if stored.fresh(now, p.options.HardRefreshOnly) {
					return nil
				}
				if stored.ExpiresAt.Sub(now) >= hardRefreshWindow &&
					(stored.Token != previous.Token || !stored.ExpiresAt.Equal(previous.ExpiresAt)) {
					// Publish a credential loaded from another provider before
					// running its soft refresh in a separate flight.
					softRefresh = true
					return nil
				}
			}
		}

		fresh, err := p.mintCredential()
		if err != nil {
			return err
		}
		if err := p.ctx.Err(); err != nil {
			return err
		}
		if !fresh.valid(time.Now()) {
			return errors.New("workload credential expired before publication")
		}
		if err := fresh.save(entry); err != nil {
			if force {
				return err
			}
			warning = err
		}
		cached = fresh
		return nil
	})
	return cached, warning, softRefresh, err
}

func loadCachedCredential(entry cache.Entry) (cachedCredential, error) {
	data, err := entry.Load()
	if err != nil {
		return cachedCredential{}, fmt.Errorf("reading workload credential cache: %w", err)
	}
	var cached cachedCredential
	if len(data) != 0 && json.Unmarshal(data, &cached) != nil {
		// Treat malformed cache data as a miss so a fresh credential replaces it.
		return cachedCredential{}, nil //nolint:nilerr // A corrupt optional cache must not block authentication.
	}
	return cached, nil
}

func (c cachedCredential) save(entry cache.Entry) error {
	data, err := json.Marshal(c)
	if err == nil {
		err = entry.Save(data)
	}
	if err != nil {
		return fmt.Errorf("writing workload credential cache: %w", err)
	}
	return nil
}
