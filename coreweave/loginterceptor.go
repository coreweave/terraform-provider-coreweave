package coreweave

import (
	"context"
	"errors"
	"io"

	clusterv1beta1 "buf.build/gen/go/coreweave/telemetry-relay/protocolbuffers/go/coreweave/telemetryrelay/svc/cluster/v1beta1"
	"connectrpc.com/connect/v2"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

const (
	logMessageKey     = "message"
	logCredentialsKey = "credentials"
)

// redactCredentials copies message with its credentials cleared, and reports
// whether any were set. prototext has no notion of sensitivity and Terraform's
// Sensitive and WriteOnly flags do not reach tflog fields, so without this the
// endpoint password, auth-header values and S3 keys land in TF_LOG output.
//
// It copies because the caller is logging a request that is about to go on the
// wire. These two requests are the only messages in the Telemetry Relay API
// carrying a credentials oneof; cwobject's CreateAccessKeyFrom*Response secret
// keys are a separate, pre-existing leak and are not covered here.
func redactCredentials(message proto.Message) (proto.Message, bool) {
	switch m := message.(type) {
	case *clusterv1beta1.CreateEndpointRequest:
		if m.WhichCredentials() == clusterv1beta1.CreateEndpointRequest_Credentials_not_set_case {
			return message, false
		}
		clone, _ := proto.Clone(m).(*clusterv1beta1.CreateEndpointRequest)
		clone.ClearCredentials()
		return clone, true
	case *clusterv1beta1.UpdateEndpointRequest:
		if m.WhichCredentials() == clusterv1beta1.UpdateEndpointRequest_Credentials_not_set_case {
			return message, false
		}
		clone, _ := proto.Clone(m).(*clusterv1beta1.UpdateEndpointRequest)
		clone.ClearCredentials()
		return clone, true
	default:
		return message, false
	}
}

// logMessageFields records the formatted message, redacted, on a log line.
func logMessageFields(fields map[string]any, msg any) {
	message, ok := msg.(proto.Message)
	if !ok {
		return
	}

	redacted, hadCredentials := redactCredentials(message)
	fields[logMessageKey] = logFormatMessage(redacted)
	if hadCredentials {
		fields[logCredentialsKey] = "redacted"
	}
}

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
	logMessageFields(fields, msg)
	tflog.Debug(ctx, "sending API request", fields)
}

func tfLogResponse(ctx context.Context, info *connect.CallInfo, msg any, err error) {
	fields := tfLogBaseFields(info)
	if err != nil {
		fields["error"] = err.Error()
		tflog.Debug(ctx, "got nil or invalid API response", fields)
		return
	}
	logMessageFields(fields, msg)
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
