package coreweave

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
)

const kubernetesResponseLimit = 4 << 20

// KubernetesAPIError is returned when a cluster Kubernetes API request
// completes with a non-successful HTTP status.
type KubernetesAPIError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *KubernetesAPIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("Kubernetes API returned %s", e.Status)
	}
	return fmt.Sprintf("Kubernetes API returned %s: %s", e.Status, e.Body)
}

func IsKubernetesNotFound(err error) bool {
	var apiErr *KubernetesAPIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// DoKubernetesRequest sends an authenticated request to a CKS cluster API.
// CoreWeave API access tokens are also Kubernetes bearer tokens.
func (c *Client) DoKubernetesRequest(ctx context.Context, method, endpoint, requestPath, contentType string, body []byte) ([]byte, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parsing Kubernetes API endpoint: %w", err)
	}
	if base.Scheme != "https" || base.Host == "" {
		return nil, fmt.Errorf("Kubernetes API endpoint %q must be an absolute HTTPS URL", endpoint)
	}

	base.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(requestPath, "/")
	if method == http.MethodPost || method == http.MethodPatch {
		query := base.Query()
		query.Set("fieldValidation", "Strict")
		base.RawQuery = query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building Kubernetes API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if err := auth.SetAuthorizationHeader(ctx, request.Header, c.tokenSource); err != nil {
		return nil, fmt.Errorf("authorizing Kubernetes API request: %w", err)
	}

	response, err := c.kubernetesHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("calling Kubernetes API: %w", err)
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(response.Body, kubernetesResponseLimit))
	if err != nil {
		return nil, fmt.Errorf("reading Kubernetes API response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &KubernetesAPIError{
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Body:       strings.TrimSpace(string(payload)),
		}
	}
	return payload, nil
}
