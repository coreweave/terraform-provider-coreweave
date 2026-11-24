package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	descartesauth "github.com/coreweave/terraform-provider-coreweave/auth"
)

const TerraformCloudWorkloadIdentityTokenEnvVar = "TFC_WORKLOAD_IDENTITY_TOKEN"

// OIDCTokenSource supplies the external assertion exchanged for a CoreWeave
// service-account token.
type OIDCTokenSource = descartesauth.OIDCTokenSource

// WorkloadIdentityConfig contains the provider-owned inputs passed to the
// reusable authentication client.
type WorkloadIdentityConfig struct {
	Endpoint            string
	ServiceAccountID    string
	ServiceAccountOrgID string
	TokenFile           string
	TokenSource         OIDCTokenSource
	ExchangeTimeout     time.Duration
	UserAgent           string
}

// WorkloadIdentityTokenSource adapts the reusable Descartes authentication
// client to the token source consumed by the CoreWeave API client.
type WorkloadIdentityTokenSource struct {
	provider      *descartesauth.Provider
	cacheIdentity string
}

// NewWorkloadIdentityTokenSource constructs a lazy, refreshable token source.
// No network request is made until Token is first called.
func NewWorkloadIdentityTokenSource(config WorkloadIdentityConfig) (*WorkloadIdentityTokenSource, error) {
	provider, err := descartesauth.New(descartesauth.Config{
		Mode:             descartesauth.ModeWorkloadIdentity,
		Endpoint:         config.Endpoint,
		ServiceAccountID: config.ServiceAccountID,
		OrganizationID:   config.ServiceAccountOrgID,
		TokenFile:        config.TokenFile,
		TokenSource:      config.TokenSource,
		ExchangeTimeout:  config.ExchangeTimeout,
		UserAgent:        config.UserAgent,
	})
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
	return descartesauth.EnvTokenSource{Name: name}
}

// Token returns a cached CoreWeave credential or performs a shared exchange.
func (s *WorkloadIdentityTokenSource) Token(ctx context.Context) (string, error) {
	return s.provider.Token(ctx)
}

// CacheIdentity partitions derived S3 clients by the non-secret workload
// identity without retaining either the OIDC assertion or exchanged token.
func (s *WorkloadIdentityTokenSource) CacheIdentity() string {
	return s.cacheIdentity
}

// Close releases exchange-client resources. It is primarily exposed for
// callers and tests that own a bounded token-source lifecycle.
func (s *WorkloadIdentityTokenSource) Close() error {
	return s.provider.Close()
}
