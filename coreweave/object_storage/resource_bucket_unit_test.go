package objectstorage

import (
	"context"
	"errors"
	"fmt"
	standardhttp "net/http"
	"strings"
	"testing"

	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
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

	const maxAttempts = 3
	options := s3.Options{Retryer: awsretry.NewStandard(func(options *awsretry.StandardOptions) {
		options.MaxAttempts = maxAttempts
	})}
	withBucketReadRetry(&options)

	if got := options.Retryer.MaxAttempts(); got != maxAttempts {
		t.Errorf("MaxAttempts() = %d, want preserved cap %d", got, maxAttempts)
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
