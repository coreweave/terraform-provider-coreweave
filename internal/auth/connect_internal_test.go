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

// A code the authentication endpoint returned must never be mistaken for a
// statement about the resource the caller asked for. CodeNotFound is the one
// that causes real damage: resource Read handlers treat it as proof the
// resource was deleted and remove it from Terraform state.
func TestClassifyErrorWithholdsResourceCodesFromTokenSource(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		exchanged connect.Code
		want      connect.Code
	}{
		"not found is withheld":           {connect.CodeNotFound, connect.CodeUnauthenticated},
		"already exists is withheld":      {connect.CodeAlreadyExists, connect.CodeUnauthenticated},
		"invalid argument is withheld":    {connect.CodeInvalidArgument, connect.CodeUnauthenticated},
		"failed precondition is withheld": {connect.CodeFailedPrecondition, connect.CodeUnauthenticated},
		"unimplemented is withheld":       {connect.CodeUnimplemented, connect.CodeUnauthenticated},
		"unauthenticated passes":          {connect.CodeUnauthenticated, connect.CodeUnauthenticated},
		"permission denied passes":        {connect.CodePermissionDenied, connect.CodePermissionDenied},
		"unavailable passes":              {connect.CodeUnavailable, connect.CodeUnavailable},
		"internal passes":                 {connect.CodeInternal, connect.CodeInternal},
		"resource exhausted passes":       {connect.CodeResourceExhausted, connect.CodeResourceExhausted},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := &TokenSourceError{err: &codedTokenError{code: test.exchanged}}
			got := classifyError(wrapAsTransportError(source))

			var connectErr *connect.Error
			require.ErrorAs(t, got, &connectErr)
			assert.Equal(t, test.want, connectErr.Code())
			assert.NotEqual(t, connect.CodeNotFound, connectErr.Code(),
				"a token source failure must never look like a missing resource")
			// The exchange's own code stays visible to the operator.
			assert.ErrorIs(t, got, source)
		})
	}
}

type codedTokenError struct {
	code connect.Code
}

func (e *codedTokenError) Error() string { return "exchange failed: " + e.code.String() }

func (e *codedTokenError) tokenSourceCode() (connect.Code, bool) { return e.code, true }

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
		"token endpoint unavailable response": {
			err: wrapAsTransportError(&TokenSourceError{err: &exchangeHTTPError{
				statusCode: 503,
				code:       connect.CodeUnavailable,
				hasCode:    true,
			}}),
			wantCode: connect.CodeUnavailable,
		},
		"token exchange permission denied": {
			err: wrapAsTransportError(&TokenSourceError{err: &exchangeHTTPError{
				statusCode: 403,
				code:       connect.CodePermissionDenied,
				hasCode:    true,
			}}),
			wantCode: connect.CodePermissionDenied,
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
