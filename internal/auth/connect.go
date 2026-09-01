package auth

import (
	"context"
	"errors"
	"net"

	"connectrpc.com/connect"
)

// NewConnectErrorInterceptor classifies authentication transport errors for
// unary Connect calls.
func NewConnectErrorInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err != nil {
				return nil, ClassifyTokenSourceError(err)
			}
			return resp, nil
		}
	})
}

// ClassifyTokenSourceError gives authentication transport failures a Connect
// code while leaving errors from other sources untouched.
func ClassifyTokenSourceError(err error) error {
	if err == nil || !IsTokenSourceError(err) {
		return err
	}

	var code connect.Code
	switch {
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	case tokenSourceFailedOnNetwork(err):
		// Point operators at connectivity rather than credentials.
		code = connect.CodeUnavailable
	default:
		code = connect.CodeUnauthenticated
	}
	return connect.NewError(code, err)
}

// Inspect the source error rather than http.Client's outer *url.Error.
func tokenSourceFailedOnNetwork(err error) bool {
	var tokenErr *TokenSourceError
	if !errors.As(err, &tokenErr) {
		return false
	}

	var netErr net.Error
	return errors.As(tokenErr.Unwrap(), &netErr)
}
