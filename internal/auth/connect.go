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
				return nil, classifyError(err)
			}
			return resp, nil
		}
	})
}

// classifyError gives the transport's own errors a Connect code. Errors that
// did not originate in the transport are returned untouched, so a code the
// server assigned is never overwritten.
func classifyError(err error) error {
	if err == nil || !IsTokenSourceError(err) {
		return err
	}

	var code connect.Code
	codedCode, hasCodedCode := tokenSourceConnectCodeValue(err)
	switch {
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	case hasCodedCode && safeTokenSourceCode(codedCode):
		code = codedCode
	case tokenSourceFailedOnNetwork(err):
		// Point operators at connectivity rather than credentials.
		code = connect.CodeUnavailable
	default:
		code = connect.CodeUnauthenticated
	}
	return connect.NewError(code, err)
}

// safeTokenSourceCode reports whether a code the authentication endpoint
// returned may be surfaced as the code of the API call that needed the token.
//
// Only codes that describe the authentication failure itself qualify. Codes
// that describe a resource must not escape: callers read them as facts about
// the resource they asked for. CodeNotFound is the dangerous one -- resource
// Read handlers treat [coreweave.IsNotFoundError] as proof that the resource
// was deleted and drop it from state, so a missing service account or trust
// configuration answering 404 would delete unrelated resources from state.
// Anything not listed here becomes CodeUnauthenticated; the original code and
// message are preserved in the error text either way.
//
//nolint:exhaustive // The default arm deliberately withholds every other code.
func safeTokenSourceCode(code connect.Code) bool {
	switch code {
	case connect.CodeUnauthenticated,
		connect.CodePermissionDenied,
		connect.CodeResourceExhausted,
		connect.CodeUnavailable,
		connect.CodeInternal,
		connect.CodeDeadlineExceeded,
		connect.CodeCanceled:
		return true
	default:
		return false
	}
}

type tokenSourceCodedError interface {
	tokenSourceCode() (connect.Code, bool)
}

func tokenSourceConnectCodeValue(err error) (connect.Code, bool) {
	var coded tokenSourceCodedError
	if !errors.As(err, &coded) {
		return connect.CodeUnknown, false
	}
	return coded.tokenSourceCode()
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
