package objectstorage

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aws/smithy-go"
)

func TestIsNoSuchTagSetError(t *testing.T) {
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
		"different API error": {
			err: &smithy.GenericAPIError{Code: ErrNoSuchBucket},
		},
		"non-API error": {
			err: errors.New("request failed"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := isNoSuchTagSetError(tt.err); got != tt.want {
				t.Errorf("isNoSuchTagSetError() = %t, want %t", got, tt.want)
			}
		})
	}
}
