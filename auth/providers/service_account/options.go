package serviceaccount

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coreweave/terraform-provider-coreweave/auth/cache"
	"github.com/coreweave/terraform-provider-coreweave/auth/internal/config"
)

const (
	envServiceAccountID        = "COREWEAVE_SERVICE_ACCOUNT_ID"
	envServiceAccountTokenFile = "COREWEAVE_SERVICE_ACCOUNT_TOKEN_FILE" //nolint:gosec // Environment variable name, not a credential.
	envTokenCommand            = "COREWEAVE_SERVICE_ACCOUNT_TOKEN_CMD"  //nolint:gosec // Environment variable name, not a credential.
	envExchangeTimeout         = "COREWEAVE_AUTH_EXCHANGE_TIMEOUT"
	envTokenDuration           = "COREWEAVE_AUTH_TOKEN_DURATION" //nolint:gosec // Environment variable name, not a credential.

	invalidWorkloadConfiguration = "invalid workload identity configuration: "

	defaultRequestedLifetime = 15 * time.Minute
	minRequestedLifetime     = 5 * time.Minute
	defaultExchangeTimeout   = 2 * time.Minute
)

// ErrSourceOnly indicates that detection found an OIDC token source but neither
// workload identity ID. Callers can match it with errors.Is.
var ErrSourceOnly = errors.New(invalidWorkloadConfiguration + envServiceAccountID +
	" is required; " + config.EnvOrganizationID + " is required")

// Options supplies the two IDs and OIDC token source for workload identity.
// Endpoint, RequestedLifetime, and ExchangeTimeout default to the CoreWeave
// API, 15 minutes, and two minutes for the complete exchange when omitted.
// RequestedLifetime must be a whole-second duration of at least five minutes.
type Options struct {
	ServiceAccountID string
	OrganizationID   string

	// Source configures a file or command instead of a TokenSource callback.
	Source *OIDCSource

	// TokenSourceID gives a custom callback a stable identity for persistent caching.
	TokenSourceID string

	// TokenSource obtains an external OIDC token and must honor its context.
	TokenSource func(context.Context) (string, error)

	Endpoint          string
	RequestedLifetime time.Duration
	ExchangeTimeout   time.Duration

	// Transport sends token exchange requests. Nil uses http.DefaultTransport.
	// The caller manages any supplied transport.
	Transport http.RoundTripper

	// Cache coordinates credential storage and refresh with other providers.
	// Nil uses a private in-memory cache.
	Cache cache.Cache

	// HardRefreshOnly skips background soft refresh. Retrieval refreshes in
	// the hard refresh window or when no valid credential is cached.
	HardRefreshOnly bool

	// OnWarning reports a cache or refresh error when a usable credential is returned.
	OnWarning func(error)
}

// New validates options and creates a service-account provider.
// The first Retrieve reads the OIDC token and starts the exchange.
func New(opts Options) (*Provider, error) {
	return FromResolvedEndpoint(&opts, nil, nil)
}

// Detect builds a provider when workload credentials are present in getenv.
// It returns ErrSourceOnly when exactly one token source is set and both IDs
// are unset.
func Detect(getenv func(string) string) (*Provider, error) {
	return FromResolvedEndpoint(nil, getenv, nil)
}

// FromEnvironment fills omitted options from the environment and requires a
// workload identity configuration.
func FromEnvironment(opts Options, getenv func(string) string) (*Provider, error) {
	return FromResolvedEndpoint(&opts, getenv, nil)
}

// FromResolvedEndpoint uses the supplied endpoint in preference to configured
// values. With environment lookup, nil options allow absent credentials.
func FromResolvedEndpoint(explicit *Options, getenv func(string) string, endpoint *config.Endpoint) (*Provider, error) {
	opts, err := resolveOptions(explicit, getenv, endpoint, getenv)
	if err != nil || opts == nil {
		return nil, err
	}
	return openProvider(opts), nil
}

func openProvider(opts *Options) *Provider {
	provider := newProvider(*opts)
	provider.cacheKey = opts.cacheKey()
	return provider
}

func resolveOptions(explicit *Options, getenv func(string) string, endpoint *config.Endpoint, runtimeGetenv func(string) string) (*Options, error) {
	var opts Options
	if explicit != nil {
		opts = *explicit
	}
	opts.ServiceAccountID = strings.TrimSpace(opts.ServiceAccountID)
	opts.OrganizationID = strings.TrimSpace(opts.OrganizationID)
	serviceAccountField, organizationField := "ServiceAccountID", "OrganizationID"
	sourceProblem := "TokenSource is required"
	if getenv != nil {
		present := opts.loadCredentialEnvironment(getenv)
		if explicit == nil {
			if !present {
				return nil, nil
			}
			if opts.Source != nil && opts.ServiceAccountID == "" && opts.OrganizationID == "" {
				return nil, ErrSourceOnly
			}
		}
		serviceAccountField, organizationField = envServiceAccountID, config.EnvOrganizationID
		sourceProblem = "configure exactly one of " + envServiceAccountTokenFile + " and " + envTokenCommand
	}

	var problems []string
	if opts.ServiceAccountID == "" {
		problems = append(problems, serviceAccountField+" is required")
	}
	if opts.OrganizationID == "" {
		problems = append(problems, organizationField+" is required")
	}
	if opts.TokenSource == nil && opts.Source == nil {
		problems = append(problems, sourceProblem)
	}
	if opts.Cache != nil && opts.Source == nil && opts.TokenSource != nil && opts.TokenSourceID == "" {
		problems = append(problems, "TokenSourceID is required to cache a TokenSource callback")
	}

	if endpoint == nil {
		rawEndpoint, field := opts.Endpoint, "Endpoint"
		if getenv != nil && strings.TrimSpace(rawEndpoint) == "" {
			rawEndpoint, field = getenv(config.EnvAPIEndpoint), config.EnvAPIEndpoint
		}
		var valid bool
		endpoint, valid = config.ParseEndpoint(rawEndpoint)
		if !valid {
			problems = append(problems, field+" must be an HTTP or HTTPS base URL")
		}
	}

	var lifetimeField, timeoutField string
	opts.RequestedLifetime, lifetimeField = resolveDuration(opts.RequestedLifetime, defaultRequestedLifetime, "RequestedLifetime", envTokenDuration, getenv)
	opts.ExchangeTimeout, timeoutField = resolveDuration(opts.ExchangeTimeout, defaultExchangeTimeout, "ExchangeTimeout", envExchangeTimeout, runtimeGetenv)
	if opts.RequestedLifetime < minRequestedLifetime || opts.RequestedLifetime%time.Second != 0 {
		problems = append(problems, lifetimeField+" must be a whole-second duration of at least 5m")
	}
	if opts.ExchangeTimeout <= 0 {
		problems = append(problems, timeoutField+" must be a positive duration")
	}
	if len(problems) != 0 {
		return nil, errors.New(invalidWorkloadConfiguration + strings.Join(problems, "; "))
	}

	opts.Endpoint = endpoint.String()
	if err := opts.prepareSource(); err != nil {
		return nil, errors.New(invalidWorkloadConfiguration + err.Error())
	}
	return &opts, nil
}

func (opts *Options) loadCredentialEnvironment(getenv func(string) string) bool {
	if opts.ServiceAccountID == "" {
		opts.ServiceAccountID = strings.TrimSpace(getenv(envServiceAccountID))
	}
	if opts.OrganizationID == "" {
		opts.OrganizationID = strings.TrimSpace(getenv(config.EnvOrganizationID))
	}
	if opts.TokenSource != nil || opts.Source != nil {
		return true
	}

	file := strings.TrimSpace(getenv(envServiceAccountTokenFile))
	command := strings.TrimSpace(getenv(envTokenCommand))
	switch {
	case file != "" && command == "":
		opts.Source = &OIDCSource{File: file}
	case command != "" && file == "":
		opts.Source = &OIDCSource{Command: &OIDCCommand{Args: strings.Fields(command)}}
	}
	// Detect workload identity from the service account or token source.
	return opts.ServiceAccountID != "" || file != "" || command != ""
}

// resolveDuration keeps a supplied zero or malformed environment value
// distinct from an omitted duration so the validator can reject it.
func resolveDuration(value, fallback time.Duration, field, envField string, getenv func(string) string) (time.Duration, string) {
	if value != 0 {
		return value, field
	}
	if getenv != nil {
		if raw := strings.TrimSpace(getenv(envField)); raw != "" {
			duration, err := time.ParseDuration(raw)
			if err != nil {
				duration = 0
			}
			return duration, envField
		}
	}
	return fallback, field
}
