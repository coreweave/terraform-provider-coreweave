package coreweave_test

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/coreweave/terraform-provider-coreweave/coreweave"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/stretchr/testify/assert"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
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
		},
		{
			name: "Permission denied error",
			err:  connect.NewError(connect.CodeInternal, errors.New("Something went wrong")),
			want: diag.Diagnostics{diag.NewErrorDiagnostic(
				"Internal Error",
				"API returned code 13 (internal): Something went wrong",
			)},
		},
		{
			name: "Not found error",
			err: func() error {
				e := connect.NewError(connect.CodeNotFound, connect.NewError(connect.CodeNotFound, errors.New("not found")))
				d, _ := connect.NewErrorDetail(&errdetails.ResourceInfo{
					ResourceType: "VM",
					ResourceName: "vm-1234",
					Description:  "The specified VM does not exist.",
				})
				e.AddDetail(d)
				return e
			}(),
			want: diag.Diagnostics{diag.NewErrorDiagnostic(
				"Not Found",
				"VM 'vm-1234' not found: The specified VM does not exist.",
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
