package coreweave

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const callerIdentityResponseLimit = 1 << 20

type CallerIdentity struct {
	OrganizationID string
	PrincipalID    string
}

type callerIdentityResponse struct {
	Principal *struct {
		OrganizationID string `json:"orgUid"`
		PrincipalID    string `json:"uid"`
	} `json:"principal"`
}

func (c *Client) GetCallerIdentity(ctx context.Context) (CallerIdentity, error) {
	endpoint, err := url.JoinPath(c.apiEndpoint, "v1beta1/auth/whoami")
	if err != nil {
		return CallerIdentity{}, fmt.Errorf("building caller identity URL: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return CallerIdentity{}, fmt.Errorf("building caller identity request: %w", err)
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return CallerIdentity{}, fmt.Errorf("reading caller identity: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return CallerIdentity{}, fmt.Errorf("reading caller identity: CoreWeave API returned %s", response.Status)
	}

	var payload callerIdentityResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, callerIdentityResponseLimit)).Decode(&payload); err != nil {
		return CallerIdentity{}, fmt.Errorf("decoding caller identity response: %w", err)
	}
	if payload.Principal == nil {
		return CallerIdentity{}, fmt.Errorf("decoding caller identity response: principal is missing")
	}
	if payload.Principal.OrganizationID == "" {
		return CallerIdentity{}, fmt.Errorf("decoding caller identity response: principal.orgUid is missing")
	}
	if payload.Principal.PrincipalID == "" {
		return CallerIdentity{}, fmt.Errorf("decoding caller identity response: principal.uid is missing")
	}

	return CallerIdentity{
		OrganizationID: payload.Principal.OrganizationID,
		PrincipalID:    payload.Principal.PrincipalID,
	}, nil
}
