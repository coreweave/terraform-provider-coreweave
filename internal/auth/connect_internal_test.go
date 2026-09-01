package auth

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wrapAsTransportError mimics what reaches an interceptor: http.Client wraps
// every RoundTrip error in a *url.Error, which is itself a net.Error.
func wrapAsTransportError(err error) error {
	return &url.Error{Op: "Post", URL: "https://api.example.test", Err: err}
}

func TestClassifyError(t *testing.T) {
	t.Parallel()

	unreachable := &net.OpError{Op: "dial", Err: errors.New("connection refused")}

	tests := map[string]struct {
		err           error
		wantCode      connect.Code
		wantUntouched bool
	}{
		"nil": {
			err:           nil,
			wantUntouched: true,
		},
		"not from the transport": {
			err:           wrapAsTransportError(errors.New("connection reset by peer")),
			wantUntouched: true,
		},
		"already classified by the server": {
			err:           connect.NewError(connect.CodeNotFound, errors.New("no such cluster")),
			wantUntouched: true,
		},
		"canceled": {
			err:      wrapAsTransportError(&TokenSourceError{err: context.Canceled}),
			wantCode: connect.CodeCanceled,
		},
		"deadline exceeded": {
			err:      wrapAsTransportError(&TokenSourceError{err: context.DeadlineExceeded}),
			wantCode: connect.CodeDeadlineExceeded,
		},
		"token endpoint unreachable": {
			err:      wrapAsTransportError(&TokenSourceError{err: unreachable}),
			wantCode: connect.CodeUnavailable,
		},
		"credentials rejected": {
			err:      wrapAsTransportError(&TokenSourceError{err: errors.New("invalid client secret")}),
			wantCode: connect.CodeUnauthenticated,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := classifyError(test.err)
			if test.wantUntouched {
				assert.Equal(t, test.err, got)
				return
			}

			var connectErr *connect.Error
			require.ErrorAs(t, got, &connectErr)
			assert.Equal(t, test.wantCode, connectErr.Code())
			require.ErrorIs(t, got, test.err, "the original error must stay in the chain")
		})
	}
}
