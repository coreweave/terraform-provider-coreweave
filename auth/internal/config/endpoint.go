package config

import (
	"net/url"
	"strings"
)

const defaultAPIEndpoint = "https://api.coreweave.com"

// Endpoint holds a validated API base URL.
type Endpoint struct{ *url.URL }

// ParseEndpoint validates an API base URL. A blank value selects the default.
func ParseEndpoint(raw string) (*Endpoint, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultAPIEndpoint
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, false
	}
	return &Endpoint{URL: parsed}, true
}
