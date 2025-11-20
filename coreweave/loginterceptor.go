package coreweave

import (
	"context"
	"fmt"
	"reflect"

	"connectrpc.com/connect"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

const (
	logMessageKey = "message"
)

func tfLogBaseFields(req connect.AnyRequest) map[string]any {
	return map[string]any{
		"procedure":  req.Spec().Procedure,
		"streamType": req.Spec().StreamType.String(),
		"peer":       req.Peer().Protocol + "://" + req.Peer().Addr,
	}
}

func logFormatMessage(message proto.Message) string {
	return prototext.MarshalOptions{
		Multiline:    false,
		AllowPartial: true,
		EmitUnknown:  true,
	}.Format(message)
}

func tfLogRequest(ctx context.Context, req connect.AnyRequest) {
	reqFields := tfLogBaseFields(req)

	// This is tricky, because AnyRequest does not expose the underlying proto message directly, but always has it (for unary requests).
	if reqMsg, ok := reflect.ValueOf(req).Elem().FieldByName("Msg").Interface().(proto.Message); ok {
		reqFields[logMessageKey] = logFormatMessage(reqMsg)
	} else {
		tflog.Error(ctx, fmt.Sprintf("failed to get request message for logging; %T.Msg is not a proto.Message", req))
	}

	tflog.Debug(ctx, "sending API request", reqFields)
}

func tfLogResponse(ctx context.Context, req connect.AnyRequest, resp connect.AnyResponse, err error) {
	respFields := tfLogBaseFields(req)

	if err != nil {
		respFields["error"] = err.Error()
	}

	// Similarly to request, not knowing the type of AnyResponse means that we have to use reflection to get to the underlying proto message.
	respValue := reflect.ValueOf(resp)
	if !respValue.IsValid() || respValue.IsNil() {
		tflog.Debug(ctx, "got nil or invalid API response", respFields)
		// Special case, we can't get much more info out of it.
		return
	} else if respMsgAttr, ok := respValue.Elem().FieldByName("Msg").Interface().(proto.Message); ok {
		respFields[logMessageKey] = logFormatMessage(respMsgAttr)
	} else {
		tflog.Error(ctx, fmt.Sprintf("failed to get response message for logging; %T.Msg is not a proto.Message", resp))
	}

	tflog.Debug(ctx, "received API response", respFields)
}

func TFLogInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(uf connect.UnaryFunc) connect.UnaryFunc {
		return func(
			ctx context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			tfLogRequest(ctx, req)
			resp, err := uf(ctx, req)
			tfLogResponse(ctx, req, resp, err)

			return resp, err
		}
	})
}
