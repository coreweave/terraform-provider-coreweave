package coreweave

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type kubernetesTokenSourceFunc func(context.Context) (string, error)

func (f kubernetesTokenSourceFunc) Token(ctx context.Context) (string, error) {
	return f(ctx)
}

type kubernetesRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f kubernetesRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDoKubernetesRequest(t *testing.T) {
	t.Parallel()

	client := &Client{
		kubernetesHTTPClient: &http.Client{Transport: kubernetesRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPatch {
				t.Fatalf("method = %q, want PATCH", request.Method)
			}
			if request.URL.String() != "https://cluster.example/apis/compute.coreweave.com/v1alpha1/nodepools/pool-a?fieldValidation=Strict" {
				t.Fatalf("URL = %q", request.URL.String())
			}
			if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Fatalf("Authorization = %q", got)
			}
			if got := request.Header.Get("Content-Type"); got != "application/merge-patch+json" {
				t.Fatalf("Content-Type = %q", got)
			}
			if got := request.Header.Get("User-Agent"); got != "terraform-provider-coreweave/test" {
				t.Fatalf("User-Agent = %q", got)
			}
			payload, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(payload) != `{"spec":{"targetNodes":2}}` {
				t.Fatalf("body = %q", payload)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"kind":"NodePool"}`)),
				Header:     make(http.Header),
			}, nil
		})},
		tokenSource: kubernetesTokenSourceFunc(func(context.Context) (string, error) { return "test-token", nil }),
		userAgent:   "terraform-provider-coreweave/test",
	}

	payload, err := client.DoKubernetesRequest(
		context.Background(),
		http.MethodPatch,
		"https://cluster.example/",
		"/apis/compute.coreweave.com/v1alpha1/nodepools/pool-a",
		"application/merge-patch+json",
		[]byte(`{"spec":{"targetNodes":2}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"kind":"NodePool"}` {
		t.Fatalf("response = %q", payload)
	}
}

func TestDoKubernetesRequestReturnsAPIError(t *testing.T) {
	t.Parallel()

	client := &Client{
		kubernetesHTTPClient: &http.Client{Transport: kubernetesRoundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Status:     "404 Not Found",
				Body:       io.NopCloser(strings.NewReader(`{"message":"not found"}`)),
				Header:     make(http.Header),
			}, nil
		})},
		tokenSource: kubernetesTokenSourceFunc(func(context.Context) (string, error) { return "test-token", nil }),
	}

	_, err := client.DoKubernetesRequest(context.Background(), http.MethodGet, "https://cluster.example", "/nodepool", "", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *KubernetesAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T", err)
	}
	if !IsKubernetesNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
	if apiErr.Body != `{"message":"not found"}` {
		t.Fatalf("body = %q", apiErr.Body)
	}
}

func TestDoKubernetesRequestRejectsNonHTTPS(t *testing.T) {
	t.Parallel()

	client := &Client{}
	_, err := client.DoKubernetesRequest(context.Background(), http.MethodGet, "http://cluster.example", "/nodepool", "", nil)
	if err == nil || !strings.Contains(err.Error(), "absolute HTTPS URL") {
		t.Fatalf("error = %v", err)
	}
}
