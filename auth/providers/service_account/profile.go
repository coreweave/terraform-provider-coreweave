package serviceaccount

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/auth/cache"
)

// Profile describes a workload identity that can be saved and opened later.
// RequestedLifetime uses Go duration syntax. ResolveProfile fills defaults and
// makes the file path or command directory absolute before saving a profile.
type Profile struct {
	ServiceAccountID  string     `json:"service_account_id"`
	OrganizationID    string     `json:"organization_id"`
	Endpoint          string     `json:"endpoint,omitempty"`
	RequestedLifetime string     `json:"requested_lifetime,omitempty"`
	Source            OIDCSource `json:"source"`
}

// RuntimeOptions controls how an opened profile retrieves credentials.
type RuntimeOptions struct {
	// Getenv looks up COREWEAVE_AUTH_EXCHANGE_TIMEOUT when ExchangeTimeout is zero.
	Getenv          func(string) string
	ExchangeTimeout time.Duration
	Transport       http.RoundTripper
	Cache           cache.Cache
	HardRefreshOnly bool
	OnWarning       func(error)
}

// ResolveProfile fills omitted profile fields from getenv and normalizes them.
// It performs no token acquisition. A nil getenv skips environment lookup.
func ResolveProfile(input Profile, getenv func(string) string) (Profile, error) {
	opts, err := input.resolve(getenv, RuntimeOptions{})
	if err != nil {
		return Profile{}, err
	}
	return profileFromOptions(opts), nil
}

// DetectProfile resolves workload identity from getenv without opening it.
// It returns nil when no workload credentials are configured and ErrSourceOnly
// when exactly one token source is set and both IDs are unset.
func DetectProfile(getenv func(string) string) (*Profile, error) {
	if getenv == nil {
		return nil, nil
	}
	opts, err := resolveOptions(nil, getenv, nil, nil)
	if err != nil || opts == nil {
		return nil, err
	}
	profile := profileFromOptions(opts)
	return &profile, nil
}

// Open validates the profile and creates a provider. Environment lookup is
// limited to the exchange timeout. Token acquisition begins on retrieval.
func (p Profile) Open(runtime RuntimeOptions) (*Provider, error) {
	opts, err := p.resolve(nil, runtime)
	if err != nil {
		return nil, err
	}
	return openProvider(opts), nil
}

// CacheKey returns the credential cache key for the resolved profile.
func (p Profile) CacheKey() (string, error) {
	opts, err := p.resolve(nil, RuntimeOptions{})
	if err != nil {
		return "", err
	}
	return opts.cacheKey(), nil
}

// ClearCache deletes this profile's cached credential without opening the
// provider or reading the token source. A nil cache has no entry to delete.
func (p Profile) ClearCache(ctx context.Context, store cache.Cache) error {
	if store == nil {
		return nil
	}
	key, err := p.CacheKey()
	if err != nil {
		return err
	}
	if err := store.WithEntry(ctx, key, cache.Entry.Delete); err != nil {
		return fmt.Errorf("deleting cached service account credential: %w", err)
	}
	return nil
}

func (p Profile) resolve(getenv func(string) string, runtime RuntimeOptions) (*Options, error) {
	opts := Options{
		ServiceAccountID: p.ServiceAccountID,
		OrganizationID:   p.OrganizationID,
		Endpoint:         p.Endpoint,
		ExchangeTimeout:  runtime.ExchangeTimeout,
		Transport:        runtime.Transport,
		Cache:            runtime.Cache,
		HardRefreshOnly:  runtime.HardRefreshOnly,
		OnWarning:        runtime.OnWarning,
	}
	if p.Source.File != "" || p.Source.Command != nil {
		opts.Source = &p.Source
	}
	if raw := strings.TrimSpace(p.RequestedLifetime); raw != "" {
		lifetime, err := time.ParseDuration(raw)
		if err != nil || lifetime == 0 {
			return nil, errors.New(invalidWorkloadConfiguration + "RequestedLifetime must be a whole-second duration of at least 5m")
		}
		opts.RequestedLifetime = lifetime
	}
	return resolveOptions(&opts, getenv, nil, runtime.Getenv)
}

func profileFromOptions(opts *Options) Profile {
	return Profile{
		ServiceAccountID:  opts.ServiceAccountID,
		OrganizationID:    opts.OrganizationID,
		Endpoint:          opts.Endpoint,
		RequestedLifetime: opts.RequestedLifetime.String(),
		Source:            *opts.Source,
	}
}
