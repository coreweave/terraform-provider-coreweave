package serviceaccount

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

// OIDCSource describes a file or command that supplies an OIDC token.
// Configure exactly one of File and Command.
type OIDCSource struct {
	File    string       `json:"file,omitempty"`
	Command *OIDCCommand `json:"command,omitempty"`
}

// OIDCCommand supplies an OIDC token using an executable and its arguments.
// An empty Dir uses the working directory at provider construction.
type OIDCCommand struct {
	Args []string `json:"args"`
	Dir  string   `json:"dir,omitempty"`
}

// CacheKey identifies the credential configuration.
// It returns false for callbacks without TokenSourceID.
// Command identity includes its arguments and directory, but not PATH resolution.
func (p *Provider) CacheKey() (string, bool) {
	return p.cacheKey, p.cacheKey != ""
}

// prepareSource normalizes source paths and installs the token reader.
func (opts *Options) prepareSource() error {
	if opts.Source == nil {
		return nil
	}
	if opts.TokenSource != nil || opts.TokenSourceID != "" {
		return errors.New("source cannot be combined with TokenSource or TokenSourceID")
	}
	source := opts.Source.clone()
	if (source.File != "") == (source.Command != nil) {
		return errors.New("source requires exactly one of File and Command")
	}
	if source.File != "" {
		path, err := filepath.Abs(source.File)
		if err != nil {
			return errors.New("cannot resolve OIDC token file path")
		}
		source.File = path
		opts.Source = &source
		opts.TokenSource = func(ctx context.Context) (string, error) {
			return readOIDCTokenFile(ctx, path)
		}
		return nil
	}
	command := source.Command
	if len(command.Args) == 0 || strings.TrimSpace(command.Args[0]) == "" {
		return errors.New("OIDC token command requires an executable")
	}
	dir, err := filepath.Abs(command.Dir)
	if err != nil {
		return errors.New("cannot resolve OIDC token command directory")
	}
	command.Dir = dir
	opts.Source = &source
	opts.TokenSource = func(ctx context.Context) (string, error) {
		return readOIDCTokenCommand(ctx, command.Args, dir)
	}
	return nil
}

type cacheIdentity struct {
	OrganizationID    string
	ServiceAccountID  string
	Endpoint          string
	RequestedLifetime string
	Source            *OIDCSource
	TokenSourceID     string
}

func (opts *Options) cacheKey() string {
	if opts.Source == nil && opts.TokenSourceID == "" {
		return ""
	}
	key := cacheIdentity{
		OrganizationID:    opts.OrganizationID,
		ServiceAccountID:  opts.ServiceAccountID,
		Endpoint:          opts.Endpoint,
		RequestedLifetime: opts.RequestedLifetime.String(),
		Source:            opts.Source,
		TokenSourceID:     opts.TokenSourceID,
	}
	// This concrete structure contains only JSON-encodable fields.
	encoded, _ := json.Marshal(key)
	hash := sha256.Sum256(encoded)
	return "service-account:v1:" + hex.EncodeToString(hash[:])
}

func (source OIDCSource) clone() OIDCSource {
	if source.Command != nil {
		command := *source.Command
		command.Args = slices.Clone(command.Args)
		source.Command = &command
	}
	return source
}
