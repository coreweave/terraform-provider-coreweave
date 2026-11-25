package auth

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/coreweave/terraform-provider-coreweave/auth/internal/config"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
	serviceaccount "github.com/coreweave/terraform-provider-coreweave/auth/providers/service_account"
)

type newOptions struct {
	apiToken       *string
	serviceAccount *serviceaccount.Options
	provider       providers.Provider

	endpoint       *string
	allowedOrigins []*url.URL
	transport      http.RoundTripper
}

// Option configures credentials created by New.
type Option func(*newOptions) error

// WithAPIToken selects the supplied CoreWeave API token.
func WithAPIToken(token string) Option {
	return func(settings *newOptions) error {
		if err := settings.checkProviderSelection(); err != nil {
			return err
		}
		settings.apiToken = &token
		return nil
	}
}

// WithServiceAccount selects workload identity. Explicit fields take precedence
// over the corresponding workload environment settings.
func WithServiceAccount(opts serviceaccount.Options) Option {
	return func(settings *newOptions) error {
		if err := settings.checkProviderSelection(); err != nil {
			return err
		}
		settings.serviceAccount = &opts
		return nil
	}
}

// WithProvider selects an application-owned credential provider.
// The caller manages the provider's lifetime.
func WithProvider(provider providers.Provider) Option {
	return func(settings *newOptions) error {
		if providerIsNil(provider) {
			return errors.New("credential provider is required")
		}
		if err := settings.checkProviderSelection(); err != nil {
			return err
		}
		settings.provider = provider
		return nil
	}
}

// WithEndpoint sets the workload exchange endpoint and authorizes its origin.
// An empty value selects the default API endpoint.
func WithEndpoint(raw string) Option {
	return func(settings *newOptions) error {
		settings.endpoint = &raw
		return nil
	}
}

// WithTransport sets the underlying HTTP transport used by Credentials.
func WithTransport(transport http.RoundTripper) Option {
	return func(settings *newOptions) error {
		settings.transport = transport
		return nil
	}
}

// WithAllowedOrigins adds exact HTTP or HTTPS origins that can receive bearer
// credentials. Each value contains a scheme, a host, and an optional port.
func WithAllowedOrigins(origins ...string) Option {
	origins = slices.Clone(origins)
	return func(settings *newOptions) error {
		for _, raw := range origins {
			origin, valid := config.ParseEndpoint(raw)
			if !valid || strings.TrimSpace(raw) == "" ||
				(origin.Path != "" && origin.Path != "/") || strings.Contains(origin.Hostname(), "*") {
				return errors.New("allowed origins must be HTTP or HTTPS origins")
			}
			settings.allowedOrigins = append(settings.allowedOrigins, origin.URL)
		}
		return nil
	}
}

func (settings *newOptions) checkProviderSelection() error {
	if settings.apiToken != nil || settings.serviceAccount != nil || settings.provider != nil {
		return errors.New("only one explicit credential method may be selected")
	}
	return nil
}

func providerIsNil(provider providers.Provider) bool {
	if provider == nil {
		return true
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() { //nolint:exhaustive // Only nil-capable kinds require special handling.
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
