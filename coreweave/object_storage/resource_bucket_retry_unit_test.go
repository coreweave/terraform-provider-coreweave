package objectstorage_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/gen/go/coreweave/cwobject/connectrpc/go/cwobject/v1/cwobjectv1connect"
	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/internal/provider"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	retryBucketName = "cldapi-1057-retry"
	retryBucketZone = "EU-SOUTH-03B"
)

// transientInvalidRegionService implements the provider's access-key RPC and
// the S3 operations used while creating a tagged bucket. The first tagging
// request models a newly recreated bucket being routed to a StoreWeave server
// that has not observed its home region yet. The identical retry succeeds.
type transientInvalidRegionService struct {
	cwobjectv1connect.UnimplementedCWObjectHandler

	mu sync.Mutex

	created            bool
	tagged             bool
	putTaggingAttempts int
}

func newTransientInvalidRegionService(t *testing.T) (string, *transientInvalidRegionService) {
	t.Helper()

	fake := &transientInvalidRegionService{}
	mux := http.NewServeMux()
	mux.Handle(cwobjectv1connect.NewCWObjectHandler(fake))
	mux.HandleFunc("/", fake.handleS3)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server.URL, fake
}

func (f *transientInvalidRegionService) CreateAccessKeyFromJWT(
	_ context.Context,
	_ *connect.Request[cwobjectv1.CreateAccessKeyFromJWTRequest],
) (*connect.Response[cwobjectv1.CreateAccessKeyFromJWTResponse], error) {
	return connect.NewResponse(&cwobjectv1.CreateAccessKeyFromJWTResponse{
		AccessKeyId: "test-access-key",
		SecretKey:   "test-secret-key",
		Expiry:      timestamppb.New(time.Now().Add(15 * time.Minute)),
	}), nil
}

func (f *transientInvalidRegionService) handleS3(resp http.ResponseWriter, req *http.Request) {
	bucket := requestBucket(req)

	switch {
	case req.Method == http.MethodGet && bucket == "":
		writeS3XML(resp, http.StatusOK, `<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Owner><ID>test</ID><DisplayName>test</DisplayName></Owner>
  <Buckets></Buckets>
</ListAllMyBucketsResult>`)

	case req.Method == http.MethodHead:
		f.mu.Lock()
		created := f.created
		f.mu.Unlock()

		if !created {
			resp.WriteHeader(http.StatusNotFound)

			return
		}

		resp.Header().Set("X-Amz-Bucket-Region", retryBucketZone)
		resp.WriteHeader(http.StatusOK)

	case req.Method == http.MethodPut && req.URL.Query().Has("tagging"):
		f.mu.Lock()
		f.putTaggingAttempts++
		attempt := f.putTaggingAttempts
		if attempt > 1 {
			f.tagged = true
		}
		f.mu.Unlock()

		if attempt == 1 {
			writeS3XML(resp, http.StatusBadRequest, `<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>InvalidRegion</Code>
  <Message>Region does not match.</Message>
  <RequestId>cldapi-1057</RequestId>
</Error>`)

			return
		}

		resp.WriteHeader(http.StatusOK)

	case req.Method == http.MethodPut:
		f.mu.Lock()
		f.created = true
		f.mu.Unlock()
		resp.WriteHeader(http.StatusOK)

	case req.Method == http.MethodGet && req.URL.Query().Has("tagging"):
		f.mu.Lock()
		tagged := f.tagged
		f.mu.Unlock()

		if !tagged {
			writeS3XML(resp, http.StatusNotFound, `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>NoSuchTagSet</Code><Message>The TagSet does not exist.</Message></Error>`)

			return
		}

		writeS3XML(resp, http.StatusOK, `<?xml version="1.0" encoding="UTF-8"?>
<Tagging xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <TagSet><Tag><Key>reproduce</Key><Value>CLDAPI-1057</Value></Tag></TagSet>
</Tagging>`)

	default:
		writeS3XML(resp, http.StatusNotImplemented, fmt.Sprintf(
			`<Error><Code>NotImplemented</Code><Message>%s %s</Message></Error>`,
			req.Method,
			req.URL.RequestURI(),
		))
	}
}

func (f *transientInvalidRegionService) taggingState() (attempts int, tagged bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.putTaggingAttempts, f.tagged
}

func requestBucket(req *http.Request) string {
	path := strings.Trim(req.URL.Path, "/")
	if path != "" {
		bucket, _, _ := strings.Cut(path, "/")

		return bucket
	}

	host := req.Host
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	if net.ParseIP(host) != nil || host == "localhost" {
		return ""
	}

	bucket, _, found := strings.Cut(host, ".")
	if !found {
		return ""
	}

	return bucket
}

func writeS3XML(resp http.ResponseWriter, status int, body string) {
	resp.Header().Set("Content-Type", "application/xml")
	resp.WriteHeader(status)
	_, _ = resp.Write([]byte(body))
}

func applyTaggedBucket(ctx context.Context, t *testing.T, endpoint string) []*tfprotov6.Diagnostic {
	t.Helper()

	t.Setenv("COREWEAVE_API_ENDPOINT", endpoint)
	t.Setenv("COREWEAVE_S3_ENDPOINT", endpoint)
	t.Setenv("COREWEAVE_API_TOKEN", "fake-token")

	server, err := provider.TestProtoV6ProviderFactories["coreweave"]()
	require.NoError(t, err)

	schemaResp, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	require.Empty(t, schemaResp.Diagnostics)

	resourceSchema, ok := schemaResp.ResourceSchemas["coreweave_object_storage_bucket"]
	require.True(t, ok)

	providerConfig, err := tfprotov6.NewDynamicValue(
		schemaResp.Provider.ValueType(), nullAttributesOf(t, schemaResp.Provider.ValueType()))
	require.NoError(t, err)

	configureResp, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &providerConfig})
	require.NoError(t, err)
	requireNoDiagErrors(t, configureResp.Diagnostics, "configure provider")

	objectType := resourceSchema.ValueType()
	configValue := tftypes.NewValue(objectType, map[string]tftypes.Value{
		"name": tftypes.NewValue(tftypes.String, retryBucketName),
		"zone": tftypes.NewValue(tftypes.String, retryBucketZone),
		"tags": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
			"reproduce": tftypes.NewValue(tftypes.String, "CLDAPI-1057"),
		}),
	})
	nullState := tftypes.NewValue(objectType, nil)

	dynamicValue := func(value tftypes.Value) *tfprotov6.DynamicValue {
		t.Helper()
		dv, dynamicErr := tfprotov6.NewDynamicValue(objectType, value)
		require.NoError(t, dynamicErr)

		return &dv
	}

	planResp, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "coreweave_object_storage_bucket",
		Config:           dynamicValue(configValue),
		PriorState:       dynamicValue(nullState),
		ProposedNewState: dynamicValue(configValue),
	})
	require.NoError(t, err)
	if diagsHaveErrors(planResp.Diagnostics) {
		return planResp.Diagnostics
	}

	applyResp, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     "coreweave_object_storage_bucket",
		Config:       dynamicValue(configValue),
		PriorState:   dynamicValue(nullState),
		PlannedState: planResp.PlannedState,
	})
	require.NoError(t, err)

	return applyResp.Diagnostics
}

// TestBucketResourceRetriesTransientInvalidRegionAfterCreate reproduces the
// customer-visible failure without depending on live DNS propagation timing.
// A replacement can briefly route the freshly created bucket to the wrong
// StoreWeave region, so PutBucketTagging must retry the transient InvalidRegion
// response instead of leaving the Terraform resource tainted.
func TestBucketResourceRetriesTransientInvalidRegionAfterCreate(t *testing.T) {
	ctx := t.Context()
	endpoint, fake := newTransientInvalidRegionService(t)

	diags := applyTaggedBucket(ctx, t, endpoint)
	attempts, tagged := fake.taggingState()

	require.False(t, diagsHaveErrors(diags),
		"tagged bucket creation must survive a transient InvalidRegion response; attempts=%d: %s",
		attempts,
		diagText(diags),
	)
	require.Equal(t, 2, attempts)
	require.True(t, tagged)
}
