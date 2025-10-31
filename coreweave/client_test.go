package coreweave_test

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/alecthomas/assert/v2"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

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
