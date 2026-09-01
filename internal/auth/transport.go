package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type transport struct {
	base          http.RoundTripper
	source        accessTokenSource
	allowedScheme string
	allowedHost   string
}

// NewTransport attaches a fresh bearer token to each request attempt addressed
// to the configured API origin.
func NewTransport(base http.RoundTripper, source accessTokenSource, endpoint string) (http.RoundTripper, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	if isNilTokenSource(source) {
		return nil, errors.New("access token source is required")
	}

	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parsing endpoint: %w", err)
	}
	if !isHTTPScheme(parsedEndpoint.Scheme) || parsedEndpoint.Host == "" {
		return nil, fmt.Errorf("endpoint %q must be an absolute http or https URL", endpoint)
	}

	return &transport{
		base:          base,
		source:        source,
		allowedScheme: parsedEndpoint.Scheme,
		allowedHost:   canonicalHost(parsedEndpoint.Scheme, parsedEndpoint.Host),
	}, nil
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.shouldAuthenticate(req.URL) {
		tflog.Warn(req.Context(), "sending request without authentication: it is not addressed to the configured API endpoint", map[string]any{
			"request_origin":    requestOrigin(req.URL),
			"configured_origin": t.allowedScheme + "://" + t.allowedHost,
		})
		request := req.Clone(req.Context())
		request.Header.Del("Authorization")
		return t.base.RoundTrip(request)
	}

	request := req.Clone(req.Context())
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	if err := SetAuthorizationHeader(request.Context(), request.Header, t.source); err != nil {
		closeRequestBody(req)
		return nil, err
	}
	return t.base.RoundTrip(request)
}

func isNilTokenSource(source accessTokenSource) bool {
	if source == nil {
		return true
	}

	value := reflect.ValueOf(source)
	kind := value.Kind()
	nilable := kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice
	return nilable && value.IsNil()
}

func requestOrigin(target *url.URL) string {
	return target.Scheme + "://" + canonicalHost(target.Scheme, target.Host)
}

// RoundTripper must close the request body even when it returns an error.
func closeRequestBody(req *http.Request) {
	if req != nil && req.Body != nil {
		_ = req.Body.Close()
	}
}

// CloseIdleConnections forwards cleanup to the wrapped transport.
func (t *transport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// Authenticate only the configured origin or a same-host HTTPS upgrade.
func (t *transport) shouldAuthenticate(target *url.URL) bool {
	if !strings.EqualFold(canonicalHost(target.Scheme, target.Host), t.allowedHost) {
		return false
	}
	return strings.EqualFold(target.Scheme, t.allowedScheme) || strings.EqualFold(target.Scheme, schemeHTTPS)
}

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// isHTTPScheme reports whether scheme is one net/http can actually request.
func isHTTPScheme(scheme string) bool {
	return strings.EqualFold(scheme, schemeHTTP) || strings.EqualFold(scheme, schemeHTTPS)
}

// canonicalHost drops the port when it is the default for scheme, so that
// https://api.example.test and https://api.example.test:443 are recognized as
// the same origin.
func canonicalHost(scheme, host string) string {
	switch {
	case strings.EqualFold(scheme, schemeHTTP):
		return strings.TrimSuffix(host, ":80")
	case strings.EqualFold(scheme, schemeHTTPS):
		return strings.TrimSuffix(host, ":443")
	default:
		return host
	}
}
