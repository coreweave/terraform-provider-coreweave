package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	standardhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type bucketHeadCheckerStub struct {
	err error
}

func (s bucketHeadCheckerStub) HeadBucket(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return &s3.HeadBucketOutput{}, s.err
}

func responseError(t *testing.T, statusCode int) error {
	t.Helper()
	return responseErrorWithCause(t, statusCode, errors.New("request failed"))
}

func responseErrorWithCause(t *testing.T, statusCode int, err error) error {
	t.Helper()

	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &standardhttp.Response{StatusCode: statusCode}},
		Err:      err,
	}
}

func TestIsMissingBucketTagSetError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want bool
	}{
		"matching API error": {
			err:  &smithy.GenericAPIError{Code: errNoSuchTagSet},
			want: true,
		},
		"wrapped matching API error": {
			err:  fmt.Errorf("get bucket tagging: %w", &smithy.GenericAPIError{Code: errNoSuchTagSet}),
			want: true,
		},
		"HTTP 404": {
			err: responseError(t, standardhttp.StatusNotFound),
		},
		"wrapped HTTP 404": {
			err: fmt.Errorf("get bucket tagging: %w", responseError(t, standardhttp.StatusNotFound)),
		},
		"different API error": {
			err: &smithy.GenericAPIError{Code: ErrNoSuchBucket},
		},
		"different API error with HTTP 404": {
			err: responseErrorWithCause(t, standardhttp.StatusNotFound, &smithy.GenericAPIError{Code: ErrNoSuchBucket}),
		},
		"HTTP 500": {
			err: responseError(t, standardhttp.StatusInternalServerError),
		},
		"non-API error": {
			err: errors.New("request failed"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := isMissingBucketTagSetError(tt.err); got != tt.want {
				t.Errorf("isMissingBucketTagSetError() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestWithBucketReadRetry(t *testing.T) {
	t.Parallel()

	const (
		globalMaxAttempts    = 3
		expectedReadAttempts = 8
	)
	options := s3.Options{Retryer: awsretry.NewStandard(func(options *awsretry.StandardOptions) {
		options.MaxAttempts = globalMaxAttempts
	})}
	withBucketReadRetry(&options)

	if got := options.Retryer.MaxAttempts(); got != expectedReadAttempts {
		t.Errorf("MaxAttempts() = %d, want bucket read cap %d", got, expectedReadAttempts)
	}
	if !options.Retryer.IsErrorRetryable(&smithy.GenericAPIError{Code: ErrNoSuchBucket}) {
		t.Error("NoSuchBucket should be retryable")
	}
	if !options.Retryer.IsErrorRetryable(&smithy.GenericAPIError{Code: errNotFound}) {
		t.Error("NotFound should be retryable")
	}
	if !options.Retryer.IsErrorRetryable(responseError(t, standardhttp.StatusNotFound)) {
		t.Error("HTTP 404 should be retryable")
	}
	if options.Retryer.IsErrorRetryable(&smithy.GenericAPIError{Code: errNoSuchTagSet}) {
		t.Error("NoSuchTagSet should not be retryable")
	}
}

type eventuallyVisibleBucketTransport struct {
	attempts          int
	transientFailures int
}

type immediateBackoff struct{}

func (immediateBackoff) BackoffDelay(int, error) (time.Duration, error) {
	return 0, nil
}

func (t *eventuallyVisibleBucketTransport) RoundTrip(request *standardhttp.Request) (*standardhttp.Response, error) {
	t.attempts++
	status := standardhttp.StatusOK
	if t.attempts <= t.transientFailures {
		status = standardhttp.StatusNotFound
	}

	return &standardhttp.Response{
		StatusCode: status,
		Status:     standardhttp.StatusText(status),
		Header:     make(standardhttp.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

func TestBucketExistsOutlivesGlobalS3RetryBudget(t *testing.T) {
	t.Parallel()

	const globalS3RetryAttempts = 3
	transport := &eventuallyVisibleBucketTransport{transientFailures: globalS3RetryAttempts}
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String("https://objects.example.test"),
		Credentials:  credentials.NewStaticCredentialsProvider("access-key", "secret-key", ""),
		HTTPClient:   &standardhttp.Client{Transport: transport},
		Region:       "US-EAST-04A",
		Retryer: awsretry.NewStandard(func(options *awsretry.StandardOptions) {
			options.Backoff = immediateBackoff{}
			options.MaxAttempts = globalS3RetryAttempts
		}),
	})

	exists, err := bucketExists(t.Context(), client, "eventually-visible")
	if err != nil {
		t.Fatalf("bucketExists() error = %v", err)
	}
	if !exists {
		t.Fatal("bucketExists() = false after transient 404 responses, want true")
	}
	if got, want := transport.attempts, globalS3RetryAttempts+1; got != want {
		t.Fatalf("HeadBucket attempts = %d, want %d", got, want)
	}
}

func TestBucketExistsStopsAtReadRetryBudget(t *testing.T) {
	t.Parallel()

	transport := &eventuallyVisibleBucketTransport{transientFailures: bucketReadMaxAttempts}
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String("https://objects.example.test"),
		Credentials:  credentials.NewStaticCredentialsProvider("access-key", "secret-key", ""),
		HTTPClient:   &standardhttp.Client{Transport: transport},
		Region:       "US-EAST-04A",
		Retryer: awsretry.NewStandard(func(options *awsretry.StandardOptions) {
			options.Backoff = immediateBackoff{}
			options.MaxAttempts = 3
		}),
	})

	exists, err := bucketExists(t.Context(), client, "absent-bucket")
	if err != nil {
		t.Fatalf("bucketExists() error = %v", err)
	}
	if exists {
		t.Fatal("bucketExists() = true after exhausting transient 404 responses, want false")
	}
	if got := transport.attempts; got != bucketReadMaxAttempts {
		t.Fatalf("HeadBucket attempts = %d, want capped at %d", got, bucketReadMaxAttempts)
	}
}

func TestBucketExists(t *testing.T) {
	t.Parallel()

	headNotFound := fmt.Errorf("head bucket: %w", responseError(t, standardhttp.StatusNotFound))
	headNoSuchBucket := fmt.Errorf("head bucket: %w", &smithy.GenericAPIError{Code: ErrNoSuchBucket})
	headFailure := responseError(t, standardhttp.StatusInternalServerError)

	tests := []struct {
		name       string
		err        error
		wantExists bool
		wantErr    error
	}{
		{name: "success confirms existence", wantExists: true},
		{name: "404 confirms absence", err: headNotFound},
		{name: "NoSuchBucket confirms absence", err: headNoSuchBucket},
		{name: "other error is propagated", err: headFailure, wantErr: headFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exists, err := bucketExists(t.Context(), bucketHeadCheckerStub{err: tt.err}, "test-bucket")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("bucketExists() error = %v, want %v", err, tt.wantErr)
			}
			if exists != tt.wantExists {
				t.Errorf("bucketExists() = %t, want %t", exists, tt.wantExists)
			}
		})
	}
}

func TestBucketTagsForState(t *testing.T) {
	t.Parallel()

	explicitEmpty := types.MapValueMust(types.StringType, map[string]attr.Value{})
	t.Run("preserves an explicitly empty configured map", func(t *testing.T) {
		t.Parallel()

		got, diagnostics := bucketTagsForState(explicitEmpty, nil)
		if diagnostics.HasError() {
			t.Fatalf("bucketTagsForState() diagnostics = %v", diagnostics)
		}
		if got.IsNull() || len(got.Elements()) != 0 {
			t.Fatalf("bucketTagsForState() = %#v, want known empty map", got)
		}
	})

	t.Run("keeps an unconfigured empty tag set null", func(t *testing.T) {
		t.Parallel()

		got, diagnostics := bucketTagsForState(types.MapNull(types.StringType), nil)
		if diagnostics.HasError() {
			t.Fatalf("bucketTagsForState() diagnostics = %v", diagnostics)
		}
		if !got.IsNull() {
			t.Fatalf("bucketTagsForState() = %#v, want null map", got)
		}
	})

	t.Run("remote tags replace the previous value", func(t *testing.T) {
		t.Parallel()

		got, diagnostics := bucketTagsForState(explicitEmpty, []s3types.Tag{{
			Key:   stringPointer("env"),
			Value: stringPointer("test"),
		}})
		if diagnostics.HasError() {
			t.Fatalf("bucketTagsForState() diagnostics = %v", diagnostics)
		}
		var values map[string]string
		if diagnostics := got.ElementsAs(t.Context(), &values, false); diagnostics.HasError() {
			t.Fatalf("read bucket tag state: %v", diagnostics)
		}
		if values["env"] != "test" || len(values) != 1 {
			t.Fatalf("bucketTagsForState() values = %#v, want env=test", values)
		}
	})
}

func TestMustRenderBucketResourceWithEmptyTags(t *testing.T) {
	t.Parallel()

	config := MustRenderBucketResource(t.Context(), "empty_tags", &BucketResourceModel{
		Name: types.StringValue("empty-tags-test"),
		Zone: types.StringValue("US-EAST-04A"),
		Tags: types.MapValueMust(types.StringType, map[string]attr.Value{}),
	})
	if !strings.Contains(config, "tags = {}") {
		t.Fatalf("rendered config does not preserve an explicit empty tag map:\n%s", config)
	}
}

func stringPointer(value string) *string {
	return &value
}
