package coreweave

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/auth"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	serviceAccountResponseLimit = 1 << 20

	getServiceAccountProcedure        = "/coreweave.directory.v1alpha.InternalDirectoryService/GetServiceAccount"
	createServiceAccountProcedure     = "/coreweave.directory.v1alpha.InternalDirectoryService/CreateServiceAccount"
	updateServiceAccountProcedure     = "/coreweave.directory.v1alpha.InternalDirectoryService/UpdateServiceAccount"
	deleteServiceAccountProcedure     = "/coreweave.directory.v1alpha.InternalDirectoryService/DeleteServiceAccount"
	activateServiceAccountProcedure   = "/coreweave.directory.v1alpha.InternalDirectoryService/ActivateServiceAccount"
	deactivateServiceAccountProcedure = "/coreweave.directory.v1alpha.InternalDirectoryService/DeactivateServiceAccount"
)

// ServiceAccount is the directory API representation used by the Terraform resource.
// This small wire model can be replaced with generated public API types when available.
type ServiceAccount struct {
	Name           string     `json:"name"`
	UID            *string    `json:"uid,omitempty"`
	OrganizationID *string    `json:"orgId,omitempty"`
	Creator        *string    `json:"creator,omitempty"`
	DisplayName    *string    `json:"displayName,omitempty"`
	SystemManaged  *bool      `json:"systemManaged,omitempty"`
	Active         *bool      `json:"active,omitempty"`
	CreateTime     *time.Time `json:"createTime,omitempty"`
	UpdateTime     *time.Time `json:"updateTime,omitempty"`
}

type CreateServiceAccountRequest struct {
	ServiceAccount *ServiceAccount `json:"serviceAccount"`
}

type UpdateServiceAccountRequest struct {
	ServiceAccount *ServiceAccount `json:"serviceAccount"`
	UpdateMask     string          `json:"updateMask"`
}

type serviceAccountNameRequest struct {
	Name string `json:"name"`
}

type connectErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (c *Client) callServiceAccountAPI(ctx context.Context, procedure string, request, response any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encoding service account request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(c.apiEndpoint, "/")+procedure,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("building service account request: %w", err)
	}
	tflog.Debug(ctx, "sending service account API request", map[string]any{
		"procedure": procedure,
		"message":   string(body),
	})
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Connect-Protocol-Version", "1")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", c.userAgent)

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		classified := classifyServiceAccountClientError(fmt.Errorf("calling service account API: %w", err))
		tflog.Debug(ctx, "service account API request failed", map[string]any{
			"procedure": procedure,
			"error":     classified.Error(),
		})
		return classified
	}
	defer httpResponse.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, serviceAccountResponseLimit+1))
	if err != nil {
		return classifyServiceAccountClientError(fmt.Errorf("reading service account response: %w", err))
	}
	if len(responseBody) > serviceAccountResponseLimit {
		return fmt.Errorf("reading service account response: body exceeds %d bytes", serviceAccountResponseLimit)
	}
	tflog.Debug(ctx, "received service account API response", map[string]any{
		"procedure": procedure,
		"status":    httpResponse.Status,
		"message":   string(responseBody),
	})
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		var apiError connectErrorResponse
		if decodeErr := json.Unmarshal(responseBody, &apiError); decodeErr != nil {
			return connect.NewError(safeConnectCodeForHTTPError(httpResponse.StatusCode, "", false), fmt.Errorf("service account API returned %s", httpResponse.Status))
		}
		code := safeConnectCodeForHTTPError(httpResponse.StatusCode, apiError.Code, true)
		message := strings.TrimSpace(apiError.Message)
		if message == "" {
			message = httpResponse.Status
		}
		return connect.NewError(code, errors.New(message))
	}

	if response == nil || httpResponse.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(responseBody, response); err != nil {
		return fmt.Errorf("decoding service account response: %w", err)
	}
	return nil
}

func classifyServiceAccountClientError(err error) error {
	classified := auth.ClassifyTokenSourceError(err)
	var connectErr *connect.Error
	if errors.As(classified, &connectErr) {
		return classified
	}

	switch {
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}

// A 404 is safe to interpret as resource absence only when the API returned a
// valid Connect error that says not_found. An ingress or stale procedure path
// may also return 404, often with an HTML body, and must not remove state.
func safeConnectCodeForHTTPError(status int, apiCode string, parsedConnectError bool) connect.Code {
	if parsedConnectError && apiCode != "" {
		var code connect.Code
		if err := code.UnmarshalText([]byte(apiCode)); err == nil {
			return code
		}
	}
	if status == http.StatusNotFound {
		return connect.CodeUnknown
	}
	return connectCodeForHTTPStatus(status)
}

func connectCodeForHTTPStatus(status int) connect.Code {
	switch status {
	case http.StatusBadRequest:
		return connect.CodeInvalidArgument
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied
	case http.StatusNotFound:
		return connect.CodeNotFound
	case http.StatusRequestTimeout:
		return connect.CodeDeadlineExceeded
	case http.StatusConflict:
		return connect.CodeAlreadyExists
	case http.StatusTooManyRequests:
		return connect.CodeResourceExhausted
	case http.StatusNotImplemented:
		return connect.CodeUnimplemented
	case http.StatusInternalServerError:
		return connect.CodeInternal
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return connect.CodeUnavailable
	default:
		return connect.CodeUnknown
	}
}

func (c *Client) GetServiceAccount(ctx context.Context, name string) (*ServiceAccount, error) {
	var response ServiceAccount
	if err := c.callServiceAccountAPI(ctx, getServiceAccountProcedure, &serviceAccountNameRequest{Name: name}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) CreateServiceAccount(ctx context.Context, request *CreateServiceAccountRequest) (*ServiceAccount, error) {
	var response ServiceAccount
	if err := c.callServiceAccountAPI(withoutRetries(ctx), createServiceAccountProcedure, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) UpdateServiceAccount(ctx context.Context, request *UpdateServiceAccountRequest) (*ServiceAccount, error) {
	var response ServiceAccount
	if err := c.callServiceAccountAPI(ctx, updateServiceAccountProcedure, request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) DeleteServiceAccount(ctx context.Context, name string) error {
	return c.callServiceAccountAPI(ctx, deleteServiceAccountProcedure, &serviceAccountNameRequest{Name: name}, nil)
}

func (c *Client) ActivateServiceAccount(ctx context.Context, name string) (*ServiceAccount, error) {
	var response ServiceAccount
	if err := c.callServiceAccountAPI(ctx, activateServiceAccountProcedure, &serviceAccountNameRequest{Name: name}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) DeactivateServiceAccount(ctx context.Context, name string) (*ServiceAccount, error) {
	var response ServiceAccount
	if err := c.callServiceAccountAPI(ctx, deactivateServiceAccountProcedure, &serviceAccountNameRequest{Name: name}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}
