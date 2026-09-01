package auth

import "errors"

// TokenSourceError reports that a request could not obtain an access token.
// It deliberately contains only the source error, never the token value.
type TokenSourceError struct {
	err error
}

func (e *TokenSourceError) Error() string {
	return "getting access token: " + e.err.Error()
}

func (e *TokenSourceError) Unwrap() error {
	return e.err
}

func IsTokenSourceError(err error) bool {
	var tokenErr *TokenSourceError
	return errors.As(err, &tokenErr)
}
