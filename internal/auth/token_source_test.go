package auth_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
	"github.com/hashicorp/terraform-plugin-log/tflogtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tokenSourceFunc func(context.Context) (string, error)

func (f tokenSourceFunc) Token(ctx context.Context) (string, error) {
	return f(ctx)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestStaticTokenSource(t *testing.T) {
	t.Parallel()

	source := auth.NewStaticTokenSource("static-token")
	token, err := source.Token(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "static-token", token)
}

func TestStaticTokenSourceCacheIdentity(t *testing.T) {
	t.Parallel()

	first := auth.NewStaticTokenSource("first-token")
	same := auth.NewStaticTokenSource("first-token")
	different := auth.NewStaticTokenSource("different-token")

	assert.Equal(t, first.CacheIdentity(), same.CacheIdentity())
	assert.NotEqual(t, first.CacheIdentity(), different.CacheIdentity())
	assert.NotContains(t, first.CacheIdentity(), "first-token")
}

func TestTransportGetsTokenForEachAttempt(t *testing.T) {
	t.Parallel()

	tokens := []string{"first-token", "refreshed-token"}
	call := 0
	source := tokenSourceFunc(func(context.Context) (string, error) {
		token := tokens[call]
		call++
		return token, nil
	})

	var authorizationHeaders []string
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		authorizationHeaders = append(authorizationHeaders, req.Header.Get("Authorization"))
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	transport, err := auth.NewTransport(base, source, "https://api.example.test")
	require.NoError(t, err)

	for range tokens {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.example.test", nil)
		require.NoError(t, reqErr)
		_, roundTripErr := transport.RoundTrip(req)
		require.NoError(t, roundTripErr)
	}

	assert.Equal(t, []string{"Bearer first-token", "Bearer refreshed-token"}, authorizationHeaders)
}

func TestTransportReturnsTypedTokenSourceError(t *testing.T) {
	t.Parallel()

	source := tokenSourceFunc(func(context.Context) (string, error) {
		return "", errors.New("exchange unavailable")
	})
	baseCalled := false
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		baseCalled = true
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	transport, err := auth.NewTransport(base, source, "https://api.example.test")
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.example.test", nil)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)

	require.ErrorContains(t, err, "getting access token: exchange unavailable")
	assert.True(t, auth.IsTokenSourceError(err))
	assert.False(t, baseCalled)
}

func TestTransportAuthenticatesOnlyTheConfiguredOrigin(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint        string
		requestURL      string
		wantAuthemitted bool
	}{
		"same origin": {
			endpoint:        "https://api.example.test",
			requestURL:      "https://api.example.test",
			wantAuthemitted: true,
		},
		"host differing only in case": {
			endpoint:        "https://api.example.test",
			requestURL:      "https://API.EXAMPLE.TEST",
			wantAuthemitted: true,
		},
		"redirect to another host": {
			endpoint:        "https://api.example.test",
			requestURL:      "https://redirected.example.test",
			wantAuthemitted: false,
		},
		"same host upgraded to https": {
			endpoint:        "http://api.example.test",
			requestURL:      "https://api.example.test",
			wantAuthemitted: true,
		},
		"same host downgraded to http": {
			endpoint:        "https://api.example.test",
			requestURL:      "http://api.example.test",
			wantAuthemitted: false,
		},
		"explicit default https port": {
			endpoint:        "https://api.example.test",
			requestURL:      "https://api.example.test:443",
			wantAuthemitted: true,
		},
		"explicit default http port": {
			endpoint:        "http://api.example.test:80",
			requestURL:      "http://api.example.test",
			wantAuthemitted: true,
		},
		"non-default port": {
			endpoint:        "https://api.example.test",
			requestURL:      "https://api.example.test:8443",
			wantAuthemitted: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sourceCalled := false
			source := tokenSourceFunc(func(context.Context) (string, error) {
				sourceCalled = true
				return "secret-token", nil
			})
			var authorization string
			base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				authorization = req.Header.Get("Authorization")
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			})
			transport, err := auth.NewTransport(base, source, test.endpoint)
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, test.requestURL, nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer caller-token")
			_, err = transport.RoundTrip(req)
			require.NoError(t, err)

			assert.Equal(t, test.wantAuthemitted, sourceCalled)
			if test.wantAuthemitted {
				assert.Equal(t, "Bearer secret-token", authorization)
			} else {
				assert.Empty(t, authorization)
			}
			assert.Equal(t, "Bearer caller-token", req.Header.Get("Authorization"), "transport must not mutate the caller's request")
		})
	}
}

func TestNewTransportRejectsUnusableEndpoints(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint string
		wantErr  string
	}{
		"unparseable": {
			endpoint: "https://api.example.test:port",
			wantErr:  "parsing endpoint",
		},
		"empty": {
			endpoint: "",
			wantErr:  "must be an absolute http or https URL",
		},
		"no scheme or host": {
			endpoint: "api.example.test/v1",
			wantErr:  "must be an absolute http or https URL",
		},
		"scheme-relative": {
			endpoint: "//api.example.test",
			wantErr:  "must be an absolute http or https URL",
		},
		"non-http scheme": {
			endpoint: "ftp://api.example.test",
			wantErr:  "must be an absolute http or https URL",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			transport, err := auth.NewTransport(nil, auth.NewStaticTokenSource("token"), test.endpoint)
			require.ErrorContains(t, err, test.wantErr)
			assert.Nil(t, transport)
		})
	}
}

func TestNewTransportRequiresATokenSource(t *testing.T) {
	t.Parallel()

	var typedNil *auth.StaticTokenSource
	tests := map[string]coreweaveAccessTokenSource{
		"nil interface": nil,
		"typed nil":     typedNil,
	}

	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			transport, err := auth.NewTransport(nil, source, "https://api.example.test")

			require.ErrorContains(t, err, "access token source is required")
			assert.Nil(t, transport)
		})
	}
}

type coreweaveAccessTokenSource interface {
	Token(context.Context) (string, error)
}

func TestTransportWarnsWhenAuthenticationIsSkipped(t *testing.T) {
	t.Parallel()

	var logbuf bytes.Buffer
	ctx := tflogtest.RootLogger(t.Context(), &logbuf)
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: http.NoBody}, nil
	})
	transport, err := auth.NewTransport(base, auth.NewStaticTokenSource("token"), "https://api.example.test:8443")
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.test/path?secret=do-not-log", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	entries, err := tflogtest.MultilineJSONDecode(bytes.NewReader(logbuf.Bytes()))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "warn", entries[0]["@level"])
	assert.Equal(t, "https://api.example.test", entries[0]["request_origin"])
	assert.Equal(t, "https://api.example.test:8443", entries[0]["configured_origin"])
	assert.NotContains(t, logbuf.String(), "do-not-log")
}

// http.RoundTripper requires RoundTrip to close the request body even when it
// returns an error; net/http leaves that to the RoundTripper.
func TestTransportClosesRequestBodyOnError(t *testing.T) {
	t.Parallel()

	newRequest := func(t *testing.T, body *trackedBody) *http.Request {
		t.Helper()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.example.test", body)
		require.NoError(t, err)
		return req
	}

	t.Run("token source failure", func(t *testing.T) {
		t.Parallel()

		source := tokenSourceFunc(func(context.Context) (string, error) {
			return "", errors.New("exchange unavailable")
		})
		transport, err := auth.NewTransport(nil, source, "https://api.example.test")
		require.NoError(t, err)

		body := &trackedBody{}
		_, err = transport.RoundTrip(newRequest(t, body))

		require.Error(t, err)
		assert.True(t, body.closed, "the request body must be closed when RoundTrip fails")
	})
}

type trackedBody struct {
	closed bool
}

func (b *trackedBody) Read([]byte) (int, error) {
	return 0, io.EOF
}

//nolint:unparam // The error return is required by io.ReadCloser.
func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

func TestTransportForwardsCloseIdleConnections(t *testing.T) {
	t.Parallel()

	base := &closeIdleRoundTripper{}
	transport, err := auth.NewTransport(base, auth.NewStaticTokenSource("token"), "https://api.example.test")
	require.NoError(t, err)

	closer, ok := transport.(interface{ CloseIdleConnections() })
	require.True(t, ok, "transport must expose CloseIdleConnections so http.Client can forward to it")
	closer.CloseIdleConnections()

	assert.Equal(t, 1, base.closed)
}

type closeIdleRoundTripper struct {
	closed int
}

func (r *closeIdleRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func (r *closeIdleRoundTripper) CloseIdleConnections() {
	r.closed++
}
