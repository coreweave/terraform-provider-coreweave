package objectstorage

import (
	"errors"
	"fmt"
	standardhttp "net/http"
	"testing"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func TestIsMissingBucketTagSetError(t *testing.T) {
	t.Parallel()

	httpError := func(statusCode int) error {
		t.Helper()
		return &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &standardhttp.Response{StatusCode: statusCode}},
			Err:      errors.New("request failed"),
		}
	}

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
			err:  httpError(standardhttp.StatusNotFound),
			want: true,
		},
		"wrapped HTTP 404": {
			err:  fmt.Errorf("get bucket tagging: %w", httpError(standardhttp.StatusNotFound)),
			want: true,
		},
		"different API error": {
			err: &smithy.GenericAPIError{Code: ErrNoSuchBucket},
		},
		"HTTP 500": {
			err: httpError(standardhttp.StatusInternalServerError),
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
