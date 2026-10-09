package auth

import (
	"context"
	"errors"
	"net"

	"connectrpc.com/connect/v2"
)

// NewConnectErrorInterceptor classifies authentication transport errors when
// opening a Connect stream and when sending or receiving its messages.
func NewConnectErrorInterceptor() connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			stream, err := next(ctx, spec)
			if err != nil {
				return nil, classifyError(err)
			}
			return &errorClassifyingStream{ClientStream: stream}, nil
		}
	}
}

type errorClassifyingStream struct {
	connect.ClientStream
}

func (s *errorClassifyingStream) SendHeaders() error {
	return classifyError(s.ClientStream.SendHeaders())
}

func (s *errorClassifyingStream) Send(msg any) error {
	return classifyError(s.ClientStream.Send(msg))
}

func (s *errorClassifyingStream) CloseSend() error {
	return classifyError(s.ClientStream.CloseSend())
}

func (s *errorClassifyingStream) Receive(msg any) error {
	return classifyError(s.ClientStream.Receive(msg))
}

func (s *errorClassifyingStream) Close() error {
	return classifyError(s.ClientStream.Close())
}

// classifyError gives the transport's own errors a Connect code. Errors that
// did not originate in the transport are returned untouched, so a code the
// server assigned is never overwritten.
func classifyError(err error) error {
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
	return connect.NewError(code, err.Error()).WithCause(err)
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
