package auth

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/coreweave/terraform-provider-coreweave/auth/internal/config"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
	serviceaccount "github.com/coreweave/terraform-provider-coreweave/auth/providers/service_account"
	"github.com/coreweave/terraform-provider-coreweave/auth/providers/static"
)

// New creates authenticated HTTP credentials and transport behavior.
func New(options ...Option) (*Credentials, error) {
	return newCredentials(os.Getenv, options...)
}

func newCredentials(getenv func(string) string, options ...Option) (*Credentials, error) {
	var settings newOptions
	for _, option := range options {
		if option != nil {
			if err := option(&settings); err != nil {
				return nil, err
			}
		}
	}

	var rawEndpoint string
	endpointSetting := "Endpoint"
	switch {
	case settings.endpoint != nil:
		rawEndpoint = *settings.endpoint
	case settings.serviceAccount != nil && strings.TrimSpace(settings.serviceAccount.Endpoint) != "":
		rawEndpoint = settings.serviceAccount.Endpoint
	default:
		rawEndpoint = getenv(config.EnvAPIEndpoint)
		endpointSetting = config.EnvAPIEndpoint
	}
	origin, valid := config.ParseEndpoint(rawEndpoint)
	if !valid {
		return nil, fmt.Errorf("%s must be an HTTP or HTTPS base URL", endpointSetting)
	}

	provider, err := settings.selectProvider(getenv, origin)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, providers.ErrCredentialsNotConfigured
	}

	transport := settings.transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	return &Credentials{
		provider:     provider,
		ownsProvider: settings.provider == nil,
		origins:      append([]*url.URL{origin.URL}, settings.allowedOrigins...),
		transport:    transport,
	}, nil
}

// Detect selects a provider from environment settings.
// Returns nil if no credential settings are present.
func Detect() (providers.Provider, error) {
	return newOptions{}.selectProvider(os.Getenv, nil)
}

// selectProvider uses explicit configuration first, then the environment.
func (settings newOptions) selectProvider(getenv func(string) string, endpoint *config.Endpoint) (providers.Provider, error) {
	switch {
	case settings.provider != nil:
		return settings.provider, nil
	case settings.apiToken != nil:
		return static.New(*settings.apiToken)
	case settings.serviceAccount == nil:
		if provider := static.Detect(getenv); provider != nil {
			return provider, nil
		}
	}
	provider, err := serviceaccount.FromResolvedEndpoint(settings.serviceAccount, getenv, endpoint)
	if err != nil || provider == nil {
		return nil, err
	}
	return provider, nil
}
