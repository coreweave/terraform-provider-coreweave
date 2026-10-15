package coreweave_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"buf.build/gen/go/coreweave/cks/connectrpc/go/v2/coreweave/cks/v1beta1/cksv1beta1connect"
	cksv1beta1 "buf.build/gen/go/coreweave/cks/protocolbuffers/go/coreweave/cks/v1beta1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-log/tflogtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const (
	logMessageKey = "message"
)

func exampleClusterRequest() *cksv1beta1.CreateClusterRequest {
	return &cksv1beta1.CreateClusterRequest{
		Name:   "test-cluster",
		Zone:   "US-EAST-04A",
		VpcId:  "8e727c0d-5527-416b-9a80-c498f4802d15",
		Public: true,
	}
}

func exampleClusterResponse() *cksv1beta1.CreateClusterResponse {
	return &cksv1beta1.CreateClusterResponse{
		Cluster: &cksv1beta1.Cluster{
			Id:     "02762697-916a-4920-940b-5909780f358e",
			Name:   "test-cluster",
			Zone:   "US-EAST-04A",
			Status: cksv1beta1.Cluster_STATUS_CREATING,
			VpcId:  "8e727c0d-5527-416b-9a80-c498f4802d15",
			Public: true,
		},
	}
}

func TestTFLogInterceptor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		req        *cksv1beta1.CreateClusterRequest
		returnResp *cksv1beta1.CreateClusterResponse
		returnErr  error
		assertion  func(t *testing.T, logEntries []map[string]any, peer string)
	}{
		{
			name:       "Basic successful response",
			req:        exampleClusterRequest(),
			returnResp: exampleClusterResponse(),
			assertion: func(t *testing.T, logEntries []map[string]any, peer string) {
				t.Helper()
				assert.Len(t, logEntries, 2)
				respEntry := logEntries[1]
				t.Run("request matches", func(t *testing.T) {
					t.Parallel()
					reqEntry := logEntries[0]
					assert.Contains(t, reqEntry["@message"], "sending API request")
					assert.Equal(t, peer, reqEntry["peer"])
					assert.Equal(t, "unary", reqEntry["streamType"])
					assert.Equal(t, "/coreweave.cks.v1beta1.ClusterService/CreateCluster", reqEntry["procedure"])
					assert.NotEmpty(t, reqEntry[logMessageKey])

					referenceReq := exampleClusterRequest()
					assert.Contains(t, reqEntry[logMessageKey], referenceReq.Name)
					assert.Contains(t, reqEntry[logMessageKey], referenceReq.Zone)
					assert.Contains(t, reqEntry[logMessageKey], referenceReq.VpcId)

					assert.NotContains(t, reqEntry, "error")
				})
				t.Run("response matches", func(t *testing.T) {
					t.Parallel()
					assert.Contains(t, respEntry["@message"], "received API response")
					assert.Equal(t, peer, respEntry["peer"])
					assert.Equal(t, "unary", respEntry["streamType"])
					assert.Equal(t, "/coreweave.cks.v1beta1.ClusterService/CreateCluster", respEntry["procedure"])
					assert.NotEmpty(t, respEntry[logMessageKey])
					assert.NotContains(t, respEntry, "error")

					referenceResp := exampleClusterResponse()
					assert.Contains(t, respEntry[logMessageKey], referenceResp.Cluster.Id)
					assert.Contains(t, respEntry[logMessageKey], referenceResp.Cluster.Name)
					assert.Contains(t, respEntry[logMessageKey], referenceResp.Cluster.VpcId)
					assert.Contains(t, respEntry[logMessageKey], referenceResp.Cluster.Status.String())
				})
			},
		},
		{
			name:       "Basic error response",
			req:        exampleClusterRequest(),
			returnResp: nil,
			returnErr:  connect.NewError(connect.CodeInternal, "Internal server error"),
			assertion: func(t *testing.T, logEntries []map[string]any, peer string) {
				t.Helper()
				t.Run("request matches", func(t *testing.T) {
					t.Parallel()
					reqEntry := logEntries[0]
					assert.Contains(t, reqEntry["@message"], "sending API request")
					assert.Equal(t, peer, reqEntry["peer"])
					assert.Equal(t, "unary", reqEntry["streamType"])
					assert.Equal(t, "/coreweave.cks.v1beta1.ClusterService/CreateCluster", reqEntry["procedure"])
					assert.NotEmpty(t, reqEntry[logMessageKey])
					assert.NotContains(t, reqEntry, "error")
				})
				t.Run("resp shows error", func(t *testing.T) {
					t.Parallel()
					respEntry := logEntries[1]
					assert.Contains(t, respEntry["@message"], "got nil or invalid API response")
					assert.Equal(t, peer, respEntry["peer"])
					assert.Equal(t, "unary", respEntry["streamType"])
					assert.Equal(t, "/coreweave.cks.v1beta1.ClusterService/CreateCluster", respEntry["procedure"])
					assert.NotContains(t, respEntry, logMessageKey)
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

			rpcServer := connect.NewServer()
			cksv1beta1connect.RegisterClusterServiceHandler(rpcServer, &logTestClusterServer{response: tt.returnResp, err: tt.returnErr})
			mux := http.NewServeMux()
			connecthttp.Mount(mux, rpcServer)
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			c := cksv1beta1connect.NewClusterServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL), coreweave.TFLogInterceptor()))
			resp, err := c.CreateCluster(ctx, tt.req)
			logBytes := logbuf.Bytes() // Capture the log bytes so we can copy them as necessary.

			if tt.returnErr != nil {
				require.Error(t, err)
				assert.Equal(t, connect.CodeOf(tt.returnErr), connect.CodeOf(err))
				assert.Contains(t, err.Error(), tt.returnErr.Error())
			} else {
				require.NoError(t, err)
			}
			if tt.returnResp != nil {
				assert.True(t, proto.Equal(tt.returnResp, resp))
			} else {
				assert.Nil(t, resp)
			}

			logEntries, err := tflogtest.MultilineJSONDecode(bytes.NewBuffer(logBytes))
			require.NoError(t, err)
			require.Len(t, logEntries, 2)

			for i, entry := range logEntries {
				if msg, ok := entry[logMessageKey]; ok {
					assert.NotContains(t, msg, "\n", "field %q of log %d should be single-line in logs", logMessageKey, i)
				}
			}

			if tt.assertion != nil {
				tt.assertion(t, logEntries, "connect://"+strings.TrimPrefix(server.URL, "http://"))
			}
		})
	}
}

type logTestClusterServer struct {
	cksv1beta1connect.UnimplementedClusterServiceHandler
	response *cksv1beta1.CreateClusterResponse
	err      error
}

func (s *logTestClusterServer) CreateCluster(context.Context, *cksv1beta1.CreateClusterRequest) (*cksv1beta1.CreateClusterResponse, error) {
	return s.response, s.err
}
