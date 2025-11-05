package coreweave_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"buf.build/gen/go/coreweave/cks/connectrpc/go/coreweave/cks/v1beta1/cksv1beta1connect"
	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-log/tflogtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTFLogInterceptor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		endpoint   string
		req        *connect.Request[cksv1beta1.CreateClusterRequest]
		returnResp *connect.Response[cksv1beta1.CreateClusterResponse]
		returnErr  error
		assertion  func(t *testing.T, logEntries []map[string]any)
	}{
		{
			name:       "Basic successful response",
			endpoint:   "https://api.coreweave.com",
			req:        connect.NewRequest(&cksv1beta1.CreateClusterRequest{}),
			returnResp: connect.NewResponse(&cksv1beta1.CreateClusterResponse{}),
			assertion: func(t *testing.T, logEntries []map[string]any) {
				t.Helper()
				assert.Len(t, logEntries, 2)
				respEntry := logEntries[1]
				t.Run("request matches", func(t *testing.T) {
					t.Parallel()
					reqEntry := logEntries[0]
					assert.Contains(t, reqEntry["@message"], "sending API request")
					assert.Equal(t, "connect://api.coreweave.com", reqEntry["peer"])
					assert.Equal(t, "unary", reqEntry["streamType"])
					assert.Equal(t, "/coreweave.cks.v1beta1.ClusterService/CreateCluster", reqEntry["procedure"])
					assert.NotEmpty(t, reqEntry["payload"])
					assert.NotContains(t, reqEntry, "error")
				})
				t.Run("response matches", func(t *testing.T) {
					t.Parallel()
					assert.Contains(t, respEntry["@message"], "received API response")
					assert.Equal(t, "connect://api.coreweave.com", respEntry["peer"])
					assert.Equal(t, "unary", respEntry["streamType"])
					assert.Equal(t, "/coreweave.cks.v1beta1.ClusterService/CreateCluster", respEntry["procedure"])
					assert.NotEmpty(t, respEntry["payload"])
					assert.NotContains(t, respEntry, "error")
				})
			},
		},
		{
			name:       "Basic error response",
			endpoint:   "https://api.coreweave.com",
			req:        connect.NewRequest(&cksv1beta1.CreateClusterRequest{}),
			returnResp: nil,
			returnErr:  connect.NewError(connect.CodeInternal, errors.New("Internal server error")),
			assertion: func(t *testing.T, logEntries []map[string]any) {
				t.Helper()
				t.Run("request matches", func(t *testing.T) {
					t.Parallel()
					reqEntry := logEntries[0]
					assert.Contains(t, reqEntry["@message"], "sending API request")
					assert.Equal(t, "connect://api.coreweave.com", reqEntry["peer"])
					assert.Equal(t, "unary", reqEntry["streamType"])
					assert.Equal(t, "/coreweave.cks.v1beta1.ClusterService/CreateCluster", reqEntry["procedure"])
					assert.NotEmpty(t, reqEntry["payload"])
					assert.NotContains(t, reqEntry, "error")
				})
				t.Run("resp shows error", func(t *testing.T) {
					t.Parallel()
					respEntry := logEntries[1]
					assert.Contains(t, respEntry["@message"], "got nil or invalid API response")
					assert.Equal(t, "connect://api.coreweave.com", respEntry["peer"])
					assert.Equal(t, "unary", respEntry["streamType"])
					assert.Equal(t, "/coreweave.cks.v1beta1.ClusterService/CreateCluster", respEntry["procedure"])
					assert.NotContains(t, respEntry, "payload")
					assert.Contains(t, respEntry, "error")
					assert.Contains(t, respEntry["error"], "Internal server error")
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logbuf bytes.Buffer
			ctx := tflogtest.RootLogger(t.Context(), &logbuf)

			interceptor := coreweave.TFLogInterceptor()
			c := cksv1beta1connect.NewClusterServiceClient(nil, tt.endpoint, connect.WithInterceptors(interceptor, connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
				return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
					// This short-circuits the client to return the desired response and error for testing without calling the next func.
					return tt.returnResp, tt.returnErr
				}
			})))
			resp, err := c.CreateCluster(ctx, tt.req)
			assert.Equal(t, tt.returnErr, err)
			assert.Equal(t, tt.returnResp, resp)

			logEntries, err := tflogtest.MultilineJSONDecode(&logbuf)
			require.NoError(t, err)

			if tt.assertion != nil {
				tt.assertion(t, logEntries)
			}
		})
	}
}

func TestHandleAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want diag.Diagnostics
	}{
		{
			name: "Generic error",
			err:  errors.New("weird error"),
			want: diag.Diagnostics{diag.NewErrorDiagnostic(
				"Unexpected Error",
				"An unexpected error occurred: \"weird error\". Please check the provider logs for more details.",
			)},
		}, {
			name: "Permission denied error",
			err:  connect.NewError(connect.CodeInternal, errors.New("Internal server error")),
			want: diag.Diagnostics{diag.NewErrorDiagnostic(
				"Internal Error",
				"An unexpected server error occurred: \"internal: Internal server error\". Please check the provider logs for more details.",
			)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var diagnostics diag.Diagnostics
			coreweave.HandleAPIError(t.Context(), tt.err, &diagnostics)
			assert.Equal(t, tt.want, diagnostics)
		})
	}
}
