package coreweave

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect/v2"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

const logMessageKey = "message"

func tfLogBaseFields(info *connect.CallInfo) map[string]any {
	return map[string]any{
		"procedure":  info.Spec.Procedure,
		"streamType": info.Spec.StreamType.String(),
		"peer":       info.Protocol + "://" + info.PeerAddr,
	}
}

func logFormatMessage(message proto.Message) string {
	return prototext.MarshalOptions{
		Multiline:    false,
		AllowPartial: true,
		EmitUnknown:  true,
	}.Format(message)
}

func tfLogRequest(ctx context.Context, info *connect.CallInfo, msg any) {
	fields := tfLogBaseFields(info)
	if message, ok := msg.(proto.Message); ok {
		fields[logMessageKey] = logFormatMessage(message)
	}
	tflog.Debug(ctx, "sending API request", fields)
}

func tfLogResponse(ctx context.Context, info *connect.CallInfo, msg any, err error) {
	fields := tfLogBaseFields(info)
	if err != nil {
		fields["error"] = err.Error()
		tflog.Debug(ctx, "got nil or invalid API response", fields)
		return
	}
	if message, ok := msg.(proto.Message); ok {
		fields[logMessageKey] = logFormatMessage(message)
	}
	tflog.Debug(ctx, "received API response", fields)
}

func TFLogInterceptor() connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			stream, err := next(ctx, spec)
			info, _ := connect.CallInfoForClientContext(ctx)
			if err != nil {
				tfLogResponse(ctx, info, nil, err)
				return nil, err
			}
			return &loggingClientStream{ClientStream: stream, ctx: ctx, info: info}, nil
		}
	}
}

// Connect opens HTTP requests lazily, so RPC logging follows the stream's
// message operations rather than just its initialization.
type loggingClientStream struct {
	connect.ClientStream
	ctx  context.Context
	info *connect.CallInfo
}

func (s *loggingClientStream) SendHeaders() error {
	err := s.ClientStream.SendHeaders()
	if err != nil {
		tfLogResponse(s.ctx, s.info, nil, err)
	}
	return err
}

func (s *loggingClientStream) Send(msg any) error {
	tfLogRequest(s.ctx, s.info, msg)
	err := s.ClientStream.Send(msg)
	if err != nil {
		tfLogResponse(s.ctx, s.info, nil, err)
	}
	return err
}

func (s *loggingClientStream) CloseSend() error {
	err := s.ClientStream.CloseSend()
	if err != nil {
		tfLogResponse(s.ctx, s.info, nil, err)
	}
	return err
}

func (s *loggingClientStream) Receive(msg any) error {
	err := s.ClientStream.Receive(msg)
	if !errors.Is(err, io.EOF) {
		tfLogResponse(s.ctx, s.info, msg, err)
	}
	return err
}

func (s *loggingClientStream) Close() error {
	err := s.ClientStream.Close()
	if err != nil {
		tfLogResponse(s.ctx, s.info, nil, err)
	}
	return err
}
