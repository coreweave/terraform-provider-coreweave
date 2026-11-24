package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"time"

	serviceaccount "github.com/coreweave/terraform-provider-coreweave/auth/providers/service_account"
)

const TerraformCloudWorkloadIdentityTokenEnvVar = "TFC_WORKLOAD_IDENTITY_TOKEN" //nolint:gosec

// OIDCTokenSource supplies the external assertion exchanged for a CoreWeave
// service-account token.
type OIDCTokenSource func(context.Context) (string, error)

// WorkloadIdentityConfig contains the provider-owned inputs passed to the
// reusable authentication client.
type WorkloadIdentityConfig struct {
	Endpoint            string
	ServiceAccountID    string
	ServiceAccountOrgID string
	TokenFile           string
	TokenCommand        string
	TokenSource         OIDCTokenSource
	ExchangeTimeout     time.Duration
}

// WorkloadIdentityTokenSource adapts the reusable authentication client to the
// token source consumed by the CoreWeave API client.
type WorkloadIdentityTokenSource struct {
	provider      *serviceaccount.Provider
	cacheIdentity string
}

// NewWorkloadIdentityTokenSource constructs a lazy, refreshable token source.
// No network request is made until Token is first called.
func NewWorkloadIdentityTokenSource(config WorkloadIdentityConfig) (*WorkloadIdentityTokenSource, error) {
	options := serviceaccount.Options{
		Endpoint:          config.Endpoint,
		ServiceAccountID:  config.ServiceAccountID,
		OrganizationID:    config.ServiceAccountOrgID,
		TokenSource:       config.TokenSource,
		ExchangeTimeout:   config.ExchangeTimeout,
		RequestedLifetime: time.Hour,
	}
	switch {
	case config.TokenFile != "":
		options.Source = &serviceaccount.OIDCSource{File: config.TokenFile}
	case config.TokenCommand != "":
		options.Source = &serviceaccount.OIDCSource{
			Command: &serviceaccount.OIDCCommand{Args: strings.Fields(config.TokenCommand)},
		}
	}

	provider, err := serviceaccount.New(options)
	if err != nil {
		return nil, fmt.Errorf("configure service-account authentication: %w", err)
	}

	identity := sha256.Sum256([]byte(config.ServiceAccountOrgID + "\x00" + config.ServiceAccountID))
	return &WorkloadIdentityTokenSource{
		provider:      provider,
		cacheIdentity: fmt.Sprintf("workload-identity:%x", identity),
	}, nil
}

// NewEnvironmentOIDCTokenSource reads a rotating assertion from name whenever
// the reusable client needs to exchange or refresh a credential.
func NewEnvironmentOIDCTokenSource(name string) OIDCTokenSource {
	return func(context.Context) (string, error) {
		return strings.TrimSpace(os.Getenv(name)), nil
	}
}

// Token returns a cached CoreWeave credential or performs a shared exchange.
func (s *WorkloadIdentityTokenSource) Token(ctx context.Context) (string, error) {
	credential, err := s.provider.Retrieve(ctx)
	if err != nil {
		return "", err
	}
	return credential.Token, nil
}

// CacheIdentity partitions derived S3 clients by the non-secret workload
// identity without retaining either the OIDC assertion or exchanged token.
func (s *WorkloadIdentityTokenSource) CacheIdentity() string {
	return s.cacheIdentity
}

// Close releases exchange-client resources.
func (s *WorkloadIdentityTokenSource) Close() {
	s.provider.Close()
}
