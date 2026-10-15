package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
)

type accessTokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticTokenSource supplies the same non-expiring token for every request.
type StaticTokenSource struct {
	token string
}

// NewStaticTokenSource returns an access token source backed by token.
func NewStaticTokenSource(token string) *StaticTokenSource {
	return &StaticTokenSource{token: token}
}

// Token returns the static token, which never expires and never fails.
//
//nolint:unparam // The error return is required by AccessTokenSource.
func (s *StaticTokenSource) Token(context.Context) (string, error) {
	return s.token, nil
}

// CacheIdentity identifies clients using the same static credential without
// retaining that credential in the S3 client cache.
func (s *StaticTokenSource) CacheIdentity() string {
	return fmt.Sprintf("static:%x", sha256.Sum256([]byte(s.token)))
}

// SetAuthorizationHeader obtains the current access token and adds it to the
// supplied headers without exposing the token to callers or error messages.
func SetAuthorizationHeader(ctx context.Context, header http.Header, source accessTokenSource) error {
	if source == nil {
		return &TokenSourceError{err: errors.New("access token source is required")}
	}

	token, err := source.Token(ctx)
	if err != nil {
		return &TokenSourceError{err: err}
	}
	if token == "" {
		return &TokenSourceError{err: errors.New("access token source returned an empty token")}
	}

	header.Set("Authorization", "Bearer "+token)
	return nil
}
