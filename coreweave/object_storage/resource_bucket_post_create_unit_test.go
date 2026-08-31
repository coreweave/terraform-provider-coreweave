package objectstorage_test

import (
	"context"
	"fmt"
	"io"
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
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	postCreateTestBucket = "tf-acc-objs-post-create-invalid-region"
	postCreateTestZone   = "US-EAST-04A"
	postCreateTestPolicy = `{"Version":"2012-10-17","Statement":[{"Sid":"allow-all","Effect":"Allow","Principal":{"CW":"*"},"Action":["s3:*"],"Resource":["arn:aws:s3:::tf-acc-objs-post-create-invalid-region"]}]}`
)

type postCreateInvalidRegionFake struct {
	cwobjectv1connect.UnimplementedCWObjectHandler

	mu sync.Mutex

	bucketExists bool
	policy       string
	versioning   string

	headCalls          int
	getPolicyCalls     int
	getVersioningCalls int
}

type postCreateRequestCounts struct {
	head          int
	getPolicy     int
	getVersioning int
}

func newPostCreateInvalidRegionFake(t *testing.T) (string, *postCreateInvalidRegionFake) {
	t.Helper()

	fake := &postCreateInvalidRegionFake{}
	mux := http.NewServeMux()
	path, handler := cwobjectv1connect.NewCWObjectHandler(fake)
	mux.Handle(path, handler)
	mux.HandleFunc("/", fake.handleS3)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		counts := fake.requestCounts()
		t.Logf(
			"post-create fake requests: HeadBucket=%d GetBucketPolicy=%d GetBucketVersioning=%d",
			counts.head,
			counts.getPolicy,
			counts.getVersioning,
		)
	})

	return server.URL, fake
}

func (f *postCreateInvalidRegionFake) CreateAccessKeyFromJWT(
	_ context.Context,
	_ *connect.Request[cwobjectv1.CreateAccessKeyFromJWTRequest],
) (*connect.Response[cwobjectv1.CreateAccessKeyFromJWTResponse], error) {
	return connect.NewResponse(&cwobjectv1.CreateAccessKeyFromJWTResponse{
		AccessKeyId: "test-access-key",
		SecretKey:   "test-secret-key",
		Expiry:      timestamppb.New(time.Now().Add(time.Hour)),
	}), nil
}

func (f *postCreateInvalidRegionFake) handleS3(w http.ResponseWriter, r *http.Request) {
	bucket := strings.Trim(r.URL.Path, "/")
	query := r.URL.Query()

	switch {
	case r.Method == http.MethodGet && bucket == "":
		f.writeListBuckets(w)

	case r.Method == http.MethodPut && bucket != "" && query.Has("versioning"):
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read PutBucketVersioning body: %v", err), http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		if strings.Contains(string(body), "<Status>Suspended</Status>") {
			f.versioning = "Suspended"
		} else {
			f.versioning = "Enabled"
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodGet && bucket != "" && query.Has("versioning"):
		f.mu.Lock()
		f.getVersioningCalls++
		attempt := f.getVersioningCalls
		versioning := f.versioning
		f.mu.Unlock()
		if attempt == 1 {
			writePostCreateS3Error(w, http.StatusBadRequest, "InvalidRegion", "Region does not match")
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(
			w,
			`<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>%s</Status></VersioningConfiguration>`,
			versioning,
		)

	case r.Method == http.MethodPut && bucket != "" && query.Has("policy"):
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read PutBucketPolicy body: %v", err), http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		f.policy = string(body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodGet && bucket != "" && query.Has("policy"):
		f.mu.Lock()
		f.getPolicyCalls++
		attempt := f.getPolicyCalls
		policy := f.policy
		f.mu.Unlock()
		if attempt == 1 {
			writePostCreateS3Error(w, http.StatusBadRequest, "InvalidRegion", "Region does not match")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, policy)

	case r.Method == http.MethodDelete && bucket != "" && query.Has("policy"):
		f.mu.Lock()
		f.policy = ""
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodGet && bucket != "" && query.Has("location"):
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(
			w,
			`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">%s</LocationConstraint>`,
			postCreateTestZone,
		)

	case r.Method == http.MethodGet && bucket != "" && query.Has("tagging"):
		writePostCreateS3Error(w, http.StatusNotFound, "NoSuchTagSet", "The TagSet does not exist")

	case r.Method == http.MethodPut && bucket != "":
		f.mu.Lock()
		f.bucketExists = true
		f.mu.Unlock()
		w.Header().Set("Location", "/"+bucket)
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodHead && bucket != "":
		f.mu.Lock()
		f.headCalls++
		exists := f.bucketExists
		f.mu.Unlock()
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("x-amz-bucket-region", postCreateTestZone)
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodDelete && bucket != "":
		f.mu.Lock()
		f.bucketExists = false
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, fmt.Sprintf("unhandled S3 request: %s %s", r.Method, r.URL.RequestURI()), http.StatusNotImplemented)
	}
}

func (f *postCreateInvalidRegionFake) writeListBuckets(w http.ResponseWriter) {
	f.mu.Lock()
	exists := f.bucketExists
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/xml")
	if exists {
		_, _ = fmt.Fprintf(
			w,
			`<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Buckets><Bucket><Name>%s</Name></Bucket></Buckets></ListAllMyBucketsResult>`,
			postCreateTestBucket,
		)
		return
	}
	_, _ = io.WriteString(w, `<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Buckets></Buckets></ListAllMyBucketsResult>`)
}

func (f *postCreateInvalidRegionFake) requestCounts() postCreateRequestCounts {
	f.mu.Lock()
	defer f.mu.Unlock()

	return postCreateRequestCounts{
		head:          f.headCalls,
		getPolicy:     f.getPolicyCalls,
		getVersioning: f.getVersioningCalls,
	}
}

func writePostCreateS3Error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", "post-create-test-request")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(
		w,
		`<Error><Code>%s</Code><Message>%s</Message><RequestId>post-create-test-request</RequestId></Error>`,
		code,
		message,
	)
}

func TestObjectStoragePostCreateInvalidRegion(t *testing.T) {
	endpoint, fake := newPostCreateInvalidRegionFake(t)
	t.Setenv("COREWEAVE_API_ENDPOINT", endpoint)
	t.Setenv("COREWEAVE_S3_ENDPOINT", endpoint)
	t.Setenv("COREWEAVE_API_TOKEN", "fake-token")

	config := fmt.Sprintf(`
resource "coreweave_object_storage_bucket" "test" {
  name = %q
  zone = %q
}

resource "coreweave_object_storage_bucket_versioning" "test" {
  bucket = coreweave_object_storage_bucket.test.name

  versioning_configuration {
    status = "Enabled"
  }
}

resource "coreweave_object_storage_bucket_policy" "test" {
  bucket = coreweave_object_storage_bucket.test.name
  policy = %q
}
`, postCreateTestBucket, postCreateTestZone, postCreateTestPolicy)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: provider.TestProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("coreweave_object_storage_bucket.test", "name", postCreateTestBucket),
				resource.TestCheckResourceAttr("coreweave_object_storage_bucket_versioning.test", "versioning_configuration.0.status", "Enabled"),
				resource.TestCheckResourceAttr("coreweave_object_storage_bucket_policy.test", "bucket", postCreateTestBucket),
				func(_ *terraform.State) error {
					counts := fake.requestCounts()
					if counts.head == 0 {
						return fmt.Errorf("HeadBucket requests = 0, want readiness barrier before child resources")
					}
					if counts.getPolicy != 2 {
						return fmt.Errorf("GetBucketPolicy requests = %d, want 2", counts.getPolicy)
					}
					if counts.getVersioning != 2 {
						return fmt.Errorf("GetBucketVersioning requests = %d, want 2", counts.getVersioning)
					}
					return nil
				},
			),
		}},
	})
}
