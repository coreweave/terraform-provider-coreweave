package objectstorage

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	standardhttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type scriptedBucketClient struct {
	listOutputs []*s3.ListBucketsOutput
	listErrors  []error
	listInputs  []*s3.ListBucketsInput

	createErrors      []error
	createCalls       int
	createMaxAttempts int

	locationOutputs []*s3.GetBucketLocationOutput
	locationErrors  []error
	locationCalls   int

	headErrors []error
	headCalls  int

	putTagErrors []error
	putTagCalls  int

	getTagOutputs []*s3.GetBucketTaggingOutput
	getTagErrors  []error
	getTagCalls   int

	deleteErrors []error
	deleteCalls  int
}

func (c *scriptedBucketClient) ListBuckets(
	_ context.Context,
	input *s3.ListBucketsInput,
	_ ...func(*s3.Options),
) (*s3.ListBucketsOutput, error) {
	c.listInputs = append(c.listInputs, input)
	index := len(c.listInputs) - 1
	if index < len(c.listErrors) && c.listErrors[index] != nil {
		return nil, c.listErrors[index]
	}
	if index >= len(c.listOutputs) {
		return &s3.ListBucketsOutput{}, nil
	}
	return c.listOutputs[index], nil
}

func (c *scriptedBucketClient) CreateBucket(
	_ context.Context,
	_ *s3.CreateBucketInput,
	options ...func(*s3.Options),
) (*s3.CreateBucketOutput, error) {
	callOptions := s3.Options{}
	for _, option := range options {
		option(&callOptions)
	}
	if callOptions.Retryer != nil {
		c.createMaxAttempts = callOptions.Retryer.MaxAttempts()
	}
	index := c.createCalls
	c.createCalls++
	if index < len(c.createErrors) {
		return &s3.CreateBucketOutput{}, c.createErrors[index]
	}
	return &s3.CreateBucketOutput{}, nil
}

func (c *scriptedBucketClient) GetBucketLocation(
	_ context.Context,
	_ *s3.GetBucketLocationInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketLocationOutput, error) {
	index := c.locationCalls
	c.locationCalls++
	if index < len(c.locationErrors) && c.locationErrors[index] != nil {
		return nil, c.locationErrors[index]
	}
	if index >= len(c.locationOutputs) {
		return &s3.GetBucketLocationOutput{}, nil
	}
	return c.locationOutputs[index], nil
}

func (c *scriptedBucketClient) HeadBucket(
	_ context.Context,
	_ *s3.HeadBucketInput,
	_ ...func(*s3.Options),
) (*s3.HeadBucketOutput, error) {
	index := c.headCalls
	c.headCalls++
	if index < len(c.headErrors) {
		return &s3.HeadBucketOutput{}, c.headErrors[index]
	}
	return &s3.HeadBucketOutput{}, nil
}

func (c *scriptedBucketClient) PutBucketTagging(
	_ context.Context,
	_ *s3.PutBucketTaggingInput,
	_ ...func(*s3.Options),
) (*s3.PutBucketTaggingOutput, error) {
	index := c.putTagCalls
	c.putTagCalls++
	if index < len(c.putTagErrors) {
		return &s3.PutBucketTaggingOutput{}, c.putTagErrors[index]
	}
	return &s3.PutBucketTaggingOutput{}, nil
}

func (c *scriptedBucketClient) GetBucketTagging(
	_ context.Context,
	_ *s3.GetBucketTaggingInput,
	_ ...func(*s3.Options),
) (*s3.GetBucketTaggingOutput, error) {
	index := c.getTagCalls
	c.getTagCalls++
	if index < len(c.getTagErrors) && c.getTagErrors[index] != nil {
		return nil, c.getTagErrors[index]
	}
	if index >= len(c.getTagOutputs) {
		return &s3.GetBucketTaggingOutput{}, nil
	}
	return c.getTagOutputs[index], nil
}

func (c *scriptedBucketClient) DeleteBucket(
	_ context.Context,
	_ *s3.DeleteBucketInput,
	_ ...func(*s3.Options),
) (*s3.DeleteBucketOutput, error) {
	index := c.deleteCalls
	c.deleteCalls++
	if index < len(c.deleteErrors) {
		return &s3.DeleteBucketOutput{}, c.deleteErrors[index]
	}
	return &s3.DeleteBucketOutput{}, nil
}

func bucket(name string) s3types.Bucket {
	return s3types.Bucket{Name: aws.String(name)}
}

func TestBucketNameOwnedPreflight(t *testing.T) {
	t.Parallel()

	t.Run("exact name on later page", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{listOutputs: []*s3.ListBucketsOutput{
			{Buckets: []s3types.Bucket{bucket("target-prefix"), bucket("suffix-target")}, ContinuationToken: aws.String("page-2")},
			{Buckets: []s3types.Bucket{bucket("target")}},
		}}

		owned, err := bucketNameOwned(t.Context(), client, "target")
		if err != nil {
			t.Fatalf("bucketNameOwned() error = %v", err)
		}
		if !owned {
			t.Fatal("bucketNameOwned() = false, want exact later-page match")
		}
		if got := aws.ToString(client.listInputs[1].ContinuationToken); got != "page-2" {
			t.Fatalf("second continuation token = %q, want page-2", got)
		}
	})

	t.Run("opaque continuation token is sent verbatim", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{listOutputs: []*s3.ListBucketsOutput{
			{ContinuationToken: aws.String(" token with spaces ")},
			{},
		}}
		if _, err := bucketNameOwned(t.Context(), client, "target"); err != nil {
			t.Fatalf("bucketNameOwned() error = %v", err)
		}
		if got := aws.ToString(client.listInputs[1].ContinuationToken); got != " token with spaces " {
			t.Fatalf("second continuation token = %q, want verbatim opaque token", got)
		}
	})

	t.Run("lookalikes do not match", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{listOutputs: []*s3.ListBucketsOutput{{
			Buckets: []s3types.Bucket{bucket("target-prefix"), bucket("suffix-target")},
		}}}
		owned, err := bucketNameOwned(t.Context(), client, "target")
		if err != nil {
			t.Fatalf("bucketNameOwned() error = %v", err)
		}
		if owned {
			t.Fatal("bucketNameOwned() = true for prefix/suffix lookalikes")
		}
	})

	t.Run("missing continuation token fails closed", func(t *testing.T) {
		t.Parallel()

		buckets := make([]s3types.Bucket, bucketPreflightPageSize)
		for i := range buckets {
			buckets[i] = bucket(fmt.Sprintf("other-%d", i))
		}
		client := &scriptedBucketClient{listOutputs: []*s3.ListBucketsOutput{{Buckets: buckets}}}

		owned, err := bucketNameOwned(t.Context(), client, "target")
		if err == nil {
			t.Fatal("bucketNameOwned() error = nil, want incomplete-enumeration error")
		}
		if owned {
			t.Fatal("bucketNameOwned() = true on incomplete enumeration")
		}
	})

	t.Run("repeated continuation token fails closed", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{listOutputs: []*s3.ListBucketsOutput{
			{ContinuationToken: aws.String("page-2")},
			{ContinuationToken: aws.String("page-2")},
		}}

		owned, err := bucketNameOwned(t.Context(), client, "target")
		if err == nil {
			t.Fatal("bucketNameOwned() error = nil, want non-advancing-token error")
		}
		if owned {
			t.Fatal("bucketNameOwned() = true on incomplete enumeration")
		}
	})

	t.Run("empty continuation token fails closed", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{listOutputs: []*s3.ListBucketsOutput{{
			ContinuationToken: aws.String("   "),
		}}}
		if owned, err := bucketNameOwned(t.Context(), client, "target"); err == nil || owned {
			t.Fatalf("bucketNameOwned() = (%t, %v), want false and an unusable-token error", owned, err)
		}
	})
}

func TestCreateBucketSafely(t *testing.T) {
	t.Parallel()

	t.Run("normal create is sent once", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{listOutputs: []*s3.ListBucketsOutput{{}}}
		if err := createBucketSafely(t.Context(), client, "target", "US-EAST-04A"); err != nil {
			t.Fatalf("createBucketSafely() error = %v", err)
		}
		if client.createCalls != 1 {
			t.Fatalf("CreateBucket calls = %d, want 1", client.createCalls)
		}
		if client.createMaxAttempts != 1 {
			t.Fatalf("CreateBucket max attempts = %d, want 1", client.createMaxAttempts)
		}
	})

	t.Run("owned exact name is not created", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{listOutputs: []*s3.ListBucketsOutput{
			{Buckets: []s3types.Bucket{bucket("target-prefix")}, ContinuationToken: aws.String("page-2")},
			{Buckets: []s3types.Bucket{bucket("target")}},
		}}
		err := createBucketSafely(t.Context(), client, "target", "US-EAST-04A")
		var alreadyExistsErr *bucketAlreadyExistsError
		if !errors.As(err, &alreadyExistsErr) {
			t.Fatalf("createBucketSafely() error = %v, want already-exists error", err)
		}
		if client.createCalls != 0 {
			t.Fatalf("CreateBucket calls = %d, want 0", client.createCalls)
		}
	})

	t.Run("incomplete inventory is not created", func(t *testing.T) {
		t.Parallel()

		buckets := make([]s3types.Bucket, bucketPreflightPageSize)
		client := &scriptedBucketClient{listOutputs: []*s3.ListBucketsOutput{{Buckets: buckets}}}
		if err := createBucketSafely(t.Context(), client, "target", "US-EAST-04A"); err == nil {
			t.Fatal("createBucketSafely() error = nil, want incomplete-enumeration error")
		}
		if client.createCalls != 0 {
			t.Fatalf("CreateBucket calls = %d, want 0", client.createCalls)
		}
	})

	t.Run("ambiguous create converges only with owned name and zone", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{
			listOutputs: []*s3.ListBucketsOutput{
				{},
				{Buckets: []s3types.Bucket{bucket("target")}},
			},
			createErrors: []error{responseError(t, 503)},
			locationOutputs: []*s3.GetBucketLocationOutput{{
				LocationConstraint: s3types.BucketLocationConstraint("US-EAST-04A"),
			}},
		}

		if err := createBucketSafely(t.Context(), client, "target", "US-EAST-04A"); err != nil {
			t.Fatalf("createBucketSafely() error = %v", err)
		}
		if client.createCalls != 1 {
			t.Fatalf("CreateBucket calls = %d, want 1", client.createCalls)
		}
		if len(client.listInputs) != 2 || client.locationCalls != 1 {
			t.Fatalf("reconciliation calls: ListBuckets=%d GetBucketLocation=%d, want 2 and 1", len(client.listInputs), client.locationCalls)
		}
	})

	t.Run("ambiguous create retries transient location propagation", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{
			listOutputs: []*s3.ListBucketsOutput{
				{},
				{Buckets: []s3types.Bucket{bucket("target")}},
				{Buckets: []s3types.Bucket{bucket("target")}},
			},
			createErrors:   []error{responseError(t, 503)},
			locationErrors: []error{&smithy.GenericAPIError{Code: errInvalidRegion}, nil},
			locationOutputs: []*s3.GetBucketLocationOutput{
				nil,
				{LocationConstraint: s3types.BucketLocationConstraint("US-EAST-04A")},
			},
		}
		waits := 0

		if err := createBucketSafelyWithOptions(
			t.Context(),
			client,
			"target",
			"US-EAST-04A",
			immediatePhaseOptions(&waits),
		); err != nil {
			t.Fatalf("createBucketSafelyWithOptions() error = %v", err)
		}
		if client.createCalls != 1 || len(client.listInputs) != 3 || client.locationCalls != 2 || waits != 1 {
			t.Fatalf("calls: Create=%d List=%d Location=%d waits=%d, want 1, 3, 2, 1", client.createCalls, len(client.listInputs), client.locationCalls, waits)
		}
	})

	t.Run("ambiguous create rejects conflicting zone", func(t *testing.T) {
		t.Parallel()

		client := &scriptedBucketClient{
			listOutputs: []*s3.ListBucketsOutput{
				{},
				{Buckets: []s3types.Bucket{bucket("target")}},
			},
			createErrors: []error{responseError(t, 503)},
			locationOutputs: []*s3.GetBucketLocationOutput{{
				LocationConstraint: s3types.BucketLocationConstraint("US-EAST-02A"),
			}},
		}

		if err := createBucketSafely(t.Context(), client, "target", "US-EAST-04A"); err == nil {
			t.Fatal("createBucketSafely() error = nil, want conflicting-zone rejection")
		}
		if client.createCalls != 1 {
			t.Fatalf("CreateBucket calls = %d, want 1", client.createCalls)
		}
	})
}

func immediatePhaseOptions(waits *int) s3PhaseOptions {
	return s3PhaseOptions{
		now:   time.Now,
		delay: func(int) time.Duration { return 0 },
		wait: func(ctx context.Context, _ time.Duration) error {
			*waits++
			return ctx.Err()
		},
	}
}

func TestReconcileBucketAfterCreate(t *testing.T) {
	t.Parallel()

	expectedTags := []s3types.Tag{{Key: aws.String("env"), Value: aws.String("test")}}
	client := &scriptedBucketClient{
		headErrors: []error{
			responseErrorWithCause(t, 400, &smithy.GenericAPIError{Code: "BadRequest"}),
			&smithy.GenericAPIError{Code: errInvalidRegion},
			responseError(t, 404),
			nil,
		},
		putTagErrors: []error{
			&smithy.GenericAPIError{Code: errInvalidRegion},
			nil,
		},
		getTagOutputs: []*s3.GetBucketTaggingOutput{
			{TagSet: []s3types.Tag{{Key: aws.String("env"), Value: aws.String("stale")}}},
			{TagSet: expectedTags},
		},
	}
	waits := 0

	err := reconcileBucketAfterCreate(
		t.Context(),
		client,
		"target",
		"US-EAST-04A",
		expectedTags,
		true,
		immediatePhaseOptions(&waits),
	)
	if err != nil {
		t.Fatalf("reconcileBucketAfterCreate() error = %v", err)
	}
	if client.headCalls != 4 || client.putTagCalls != 2 || client.getTagCalls != 2 {
		t.Fatalf("calls: Head=%d PutTags=%d GetTags=%d, want 4, 2, 2", client.headCalls, client.putTagCalls, client.getTagCalls)
	}
	if waits != 5 {
		t.Fatalf("backoff waits = %d, want 5", waits)
	}
}

func TestReconcileBucketAfterCreateDoesNotRetryUnrelated400(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketClient{headErrors: []error{
		&smithy.GenericAPIError{Code: "InvalidBucketName"},
		nil,
	}}
	waits := 0
	err := reconcileBucketAfterCreate(
		t.Context(),
		client,
		"target",
		"US-EAST-04A",
		nil,
		false,
		immediatePhaseOptions(&waits),
	)
	if err == nil {
		t.Fatal("reconcileBucketAfterCreate() error = nil, want permanent failure")
	}
	if client.headCalls != 1 || waits != 0 {
		t.Fatalf("calls: Head=%d waits=%d, want 1 and 0", client.headCalls, waits)
	}
}

func TestDeleteBucketWithRetryHandlesLocationPropagation(t *testing.T) {
	t.Parallel()

	client := &scriptedBucketClient{deleteErrors: []error{
		&smithy.GenericAPIError{Code: errInvalidRegion},
		nil,
	}}
	waits := 0
	if err := deleteBucketWithRetry(
		t.Context(),
		client,
		"target",
		"US-EAST-04A",
		immediatePhaseOptions(&waits),
	); err != nil {
		t.Fatalf("deleteBucketWithRetry() error = %v", err)
	}
	if client.deleteCalls != 2 || waits != 1 {
		t.Fatalf("calls: Delete=%d waits=%d, want 2 and 1", client.deleteCalls, waits)
	}
}

func TestIsPostCreateRetryableS3Error(t *testing.T) {
	t.Parallel()

	tlsErr := &url.Error{
		Op:  "Head",
		URL: "https://bucket.example.test",
		Err: &tls.CertificateVerificationError{Err: errors.New("unknown authority")},
	}
	tests := map[string]struct {
		err  error
		want bool
	}{
		"InvalidRegion":       {err: &smithy.GenericAPIError{Code: errInvalidRegion}, want: true},
		"404":                 {err: responseError(t, 404), want: true},
		"408":                 {err: responseError(t, 408), want: true},
		"429":                 {err: responseError(t, 429), want: true},
		"429 with API code":   {err: responseErrorWithCause(t, 429, &smithy.GenericAPIError{Code: "TooManyRequests"}), want: true},
		"500 with API code":   {err: responseErrorWithCause(t, 500, &smithy.GenericAPIError{Code: "InternalServerError"}), want: true},
		"502 with API code":   {err: responseErrorWithCause(t, 502, &smithy.GenericAPIError{Code: "BadGateway"}), want: true},
		"503":                 {err: responseError(t, 503), want: true},
		"504 with API code":   {err: responseErrorWithCause(t, 504, &smithy.GenericAPIError{Code: "GatewayTimeout"}), want: true},
		"wrapped network":     {err: fmt.Errorf("head bucket: %w", &url.Error{Op: "Head", URL: "https://example.test", Err: errors.New("connection reset")}), want: true},
		"wrapped certificate": {err: fmt.Errorf("head bucket: %w", tlsErr)},
		"unrelated 400":       {err: &smithy.GenericAPIError{Code: "InvalidBucketName"}},
		"access denied":       {err: &smithy.GenericAPIError{Code: "AccessDenied"}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := isPostCreateRetryableS3Error(tt.err); got != tt.want {
				t.Fatalf("isPostCreateRetryableS3Error() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestIsAmbiguousCreateError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		ctx  context.Context
		err  error
		want bool
	}{
		"HTTP 503":          {ctx: t.Context(), err: responseError(t, 503), want: true},
		"429 with API code": {ctx: t.Context(), err: responseErrorWithCause(t, 429, &smithy.GenericAPIError{Code: "TooManyRequests"}), want: true},
		"500 with API code": {ctx: t.Context(), err: responseErrorWithCause(t, 500, &smithy.GenericAPIError{Code: "InternalServerError"}), want: true},
		"502 with API code": {ctx: t.Context(), err: responseErrorWithCause(t, 502, &smithy.GenericAPIError{Code: "BadGateway"}), want: true},
		"504 with API code": {ctx: t.Context(), err: responseErrorWithCause(t, 504, &smithy.GenericAPIError{Code: "GatewayTimeout"}), want: true},
		"InternalError":     {ctx: t.Context(), err: &smithy.GenericAPIError{Code: "InternalError"}, want: true},
		"wrapped network":   {ctx: t.Context(), err: fmt.Errorf("create bucket: %w", &url.Error{Op: "Put", URL: "https://example.test", Err: errors.New("connection reset")}), want: true},
		"attempt deadline":  {ctx: t.Context(), err: fmt.Errorf("create bucket: %w", &url.Error{Op: "Put", URL: "https://example.test", Err: context.DeadlineExceeded}), want: true},
		"certificate":       {ctx: t.Context(), err: &url.Error{Op: "Put", URL: "https://example.test", Err: &tls.CertificateVerificationError{Err: errors.New("unknown authority")}}},
		"unrelated API 400": {ctx: t.Context(), err: &smithy.GenericAPIError{Code: "InvalidBucketName"}},
	}
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	tests["operation canceled"] = struct {
		ctx  context.Context
		err  error
		want bool
	}{ctx: canceledCtx, err: context.Canceled}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := isAmbiguousCreateError(tt.ctx, tt.err); got != tt.want {
				t.Fatalf("isAmbiguousCreateError() = %t, want %t", got, tt.want)
			}
		})
	}
}

type ambiguousTimeoutRoundTripper struct {
	bucket      string
	zone        string
	createCalls int
	listCalls   int
	zoneCalls   int
}

func (r *ambiguousTimeoutRoundTripper) RoundTrip(request *standardhttp.Request) (*standardhttp.Response, error) {
	if request.Method == standardhttp.MethodPut {
		r.createCalls++
		<-request.Context().Done()
		return nil, request.Context().Err()
	}

	body := ""
	switch {
	case request.Method == standardhttp.MethodGet && request.URL.Query().Has("location"):
		r.zoneCalls++
		body = fmt.Sprintf(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">%s</LocationConstraint>`, r.zone)
	case request.Method == standardhttp.MethodGet:
		r.listCalls++
		bucketXML := ""
		if r.listCalls > 1 {
			bucketXML = fmt.Sprintf("<Bucket><Name>%s</Name></Bucket>", r.bucket)
		}
		body = fmt.Sprintf(`<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Buckets>%s</Buckets></ListAllMyBucketsResult>`, bucketXML)
	default:
		return nil, fmt.Errorf("unexpected S3 request %s %s", request.Method, request.URL.String())
	}

	return &standardhttp.Response{
		StatusCode: standardhttp.StatusOK,
		Status:     "200 OK",
		Header:     make(standardhttp.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func TestCreateBucketSafelyReconcilesHTTPAttemptTimeout(t *testing.T) {
	const (
		bucketName = "accepted-after-timeout"
		zone       = "US-EAST-04A"
	)
	transport := &ambiguousTimeoutRoundTripper{bucket: bucketName, zone: zone}
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String("https://objects.example.test"),
		Credentials:  credentials.NewStaticCredentialsProvider("access-key", "secret-key", ""),
		HTTPClient: &standardhttp.Client{
			Timeout:   10 * time.Millisecond,
			Transport: transport,
		},
		Region:  zone,
		Retryer: aws.NopRetryer{},
	})
	waits := 0

	if err := createBucketSafelyWithOptions(
		t.Context(),
		client,
		bucketName,
		zone,
		immediatePhaseOptions(&waits),
	); err != nil {
		t.Fatalf("createBucketSafelyWithOptions() error = %v", err)
	}
	if transport.createCalls != 1 {
		t.Fatalf("CreateBucket sends = %d, want 1", transport.createCalls)
	}
	if transport.listCalls != 2 || transport.zoneCalls != 1 {
		t.Fatalf("reconciliation calls: List=%d Location=%d, want 2 and 1", transport.listCalls, transport.zoneCalls)
	}
}

type stateRetentionRoundTripper struct {
	createCalls int
	headCalls   int
}

func (r *stateRetentionRoundTripper) RoundTrip(request *standardhttp.Request) (*standardhttp.Response, error) {
	status := standardhttp.StatusOK
	body := ""
	switch request.Method {
	case standardhttp.MethodGet:
		body = `<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Buckets></Buckets></ListAllMyBucketsResult>`
	case standardhttp.MethodPut:
		r.createCalls++
	case standardhttp.MethodHead:
		r.headCalls++
		status = standardhttp.StatusNotFound
	default:
		return nil, fmt.Errorf("unexpected S3 method %s", request.Method)
	}

	return &standardhttp.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, standardhttp.StatusText(status)),
		Header:     make(standardhttp.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func TestBucketCreateRetainsIdentityWhenReadinessTimesOut(t *testing.T) {
	transport := &stateRetentionRoundTripper{}
	s3Client := s3.New(s3.Options{
		BaseEndpoint: aws.String("https://objects.example.test"),
		Credentials:  credentials.NewStaticCredentialsProvider("access-key", "secret-key", ""),
		HTTPClient:   &standardhttp.Client{Transport: transport},
		Region:       "US-EAST-04A",
		Retryer:      aws.NopRetryer{},
	})

	resourceUnderTest := &BucketResource{
		s3ClientForZone: func(context.Context, string) (*s3.Client, error) {
			return s3Client, nil
		},
		postCreateOptions: s3PhaseOptions{
			now:   time.Now,
			delay: func(int) time.Duration { return 0 },
			wait: func(context.Context, time.Duration) error {
				return context.DeadlineExceeded
			},
		},
	}
	var schemaResponse fwresource.SchemaResponse
	resourceUnderTest.Schema(t.Context(), fwresource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("bucket schema diagnostics: %v", schemaResponse.Diagnostics)
	}

	model := BucketResourceModel{
		Name: types.StringValue("state-retention-test"),
		Zone: types.StringValue("US-EAST-04A"),
		Tags: types.MapNull(types.StringType),
	}
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diagnostics := plan.Set(t.Context(), &model); diagnostics.HasError() {
		t.Fatalf("set test plan: %v", diagnostics)
	}
	response := &fwresource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}

	resourceUnderTest.Create(t.Context(), fwresource.CreateRequest{Plan: plan}, response)

	if !response.Diagnostics.HasError() {
		t.Fatal("Create() diagnostics contain no error, want readiness timeout")
	}
	if transport.createCalls != 1 {
		t.Fatalf("CreateBucket sends = %d, want 1", transport.createCalls)
	}
	if transport.headCalls == 0 {
		t.Fatal("HeadBucket sends = 0, want immediate readiness check")
	}

	var retained BucketResourceModel
	if diagnostics := response.State.Get(t.Context(), &retained); diagnostics.HasError() {
		t.Fatalf("read retained state: %v", diagnostics)
	}
	if retained.Name.ValueString() != model.Name.ValueString() || retained.Zone.ValueString() != model.Zone.ValueString() {
		t.Fatalf("retained identity = (%q, %q), want (%q, %q)", retained.Name.ValueString(), retained.Zone.ValueString(), model.Name.ValueString(), model.Zone.ValueString())
	}
}
