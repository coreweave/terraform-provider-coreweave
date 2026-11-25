package auth

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/coreweave/terraform-provider-coreweave/auth/providers"
)

// Credentials adds CoreWeave bearer authentication to HTTP requests.
type Credentials struct {
	provider     providers.Provider
	ownsProvider bool
	origins      []*url.URL
	transport    http.RoundTripper
	closed       atomic.Bool
}

// RoundTrip authenticates requests to allowed API origins.
func (c *Credentials) RoundTrip(req *http.Request) (*http.Response, error) {
	if !c.authenticates(req) {
		return c.transport.RoundTrip(req)
	}

	var credential providers.Credential
	var err error
	if c.closed.Load() {
		err = providers.ErrClosed
	} else {
		credential, err = c.provider.Retrieve(req.Context())
	}
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}

	request := req.Clone(req.Context())
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("Authorization", "Bearer "+credential.Token)
	return c.transport.RoundTrip(request)
}

func (c *Credentials) authenticates(req *http.Request) bool {
	target := req.URL
	if target == nil || target.Opaque != "" {
		return false
	}
	if req.Host != "" && !sameOrigin(target, &url.URL{Scheme: target.Scheme, Host: req.Host}) {
		return false
	}
	return slices.ContainsFunc(c.origins, func(origin *url.URL) bool {
		return sameOrigin(target, origin)
	})
}

// Close ends authentication and releases resources owned by Credentials.
func (c *Credentials) Close() {
	c.closed.Store(true)
	if c.ownsProvider {
		c.provider.Close()
	}
}

func sameOrigin(first, second *url.URL) bool {
	return first.Scheme == second.Scheme &&
		strings.EqualFold(first.Hostname(), second.Hostname()) &&
		effectivePort(first) == effectivePort(second)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
