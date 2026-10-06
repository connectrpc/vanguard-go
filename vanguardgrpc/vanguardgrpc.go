// Copyright 2023-2026 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package vanguardgrpc bridges google.golang.org/grpc generated stubs
// into a connectrpc.com/connect/v2 Server. [NewServiceRegistrar]
// returns a [grpc.ServiceRegistrar], so generated Register<Service>Server
// functions from protoc-gen-go-grpc register straight into the
// connect Server. The Server can then be exposed over Connect/gRPC
// (connecthttp), REST (connectrpc.com/vanguard), or any other connect
// transport without changing the service implementation.
//
//	server := connect.NewServer()
//	registrar := vanguardgrpc.NewServiceRegistrar(server)
//
//	// Existing protoc-gen-go-grpc-generated code:
//	pingv1grpc.RegisterPingServiceServer(registrar, &pingServer{})
//
//	mux := http.NewServeMux()
//	connecthttp.Mount(mux, server) // Connect, gRPC, gRPC-Web
//	vanguard.Mount(mux, server)    // google.api.http
package vanguardgrpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

var _ grpc.ServerStream = (*serverStreamAdapter)(nil)

// NewCodec returns a gRPC [encoding.Codec] that uses the given
// Connect Codec as its backing implementation. In particular, this
// can be combined with [connectrpc.com/vanguard.JSONCodec] to easily
// create a gRPC Codec to support the "json" message format.
func NewCodec(codec connect.Codec) encoding.Codec {
	return &grpcCodec{codec: codec}
}

type grpcCodec struct {
	codec connect.Codec
}

func (g *grpcCodec) Marshal(v any) ([]byte, error) {
	msg, ok := v.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("value is not a proto.Message: %T", v)
	}
	var buf bytes.Buffer
	if err := g.codec.MarshalWrite(context.Background(), &buf, msg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (g *grpcCodec) Unmarshal(data []byte, v any) error {
	msg, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("value is not a proto.Message: %T", v)
	}
	return g.codec.UnmarshalRead(context.Background(), bytes.NewReader(data), msg)
}

func (g *grpcCodec) Name() string {
	return g.codec.Name()
}

// NewServiceRegistrar returns a grpc.ServiceRegistrar that registers services
// onto the provided connect.Server.
//
// It bridges gRPC generated code and Connect, allowing you to use it anywhere
// protoc-gen-go-grpc expects a *grpc.Server. Each call to RegisterService
// translates the grpc.ServiceDesc methods and streams into connect.Method
// entries and registers them. Once registered, the connect.Server can be
// served via connecthttp, vanguard, or any other connect-aware transport.
//
// As with grpc-go, registering an implementation that does not satisfy the
// service's expected HandlerType will panic.
//
// The registrar takes no options. In the Connect ecosystem, concerns like
// interceptors, message-size limits, keepalives, and credentials are handled
// separately: interceptors register directly on the [connect.Server], while
// transport-level configuration lives in connecthttp or vanguard.
func NewServiceRegistrar(server *connect.Server) grpc.ServiceRegistrar {
	return &serviceRegistrar{server: server}
}

// serviceRegistrar adapts a [connect.Server] to [grpc.ServiceRegistrar].
type serviceRegistrar struct {
	server *connect.Server
}

// RegisterService implements [grpc.ServiceRegistrar]. It builds a
// [connect.Method] per entry in desc.Methods and desc.Streams and
// forwards them to the underlying [connect.Server].
//
// Mirrors grpc-go's reflective check that impl satisfies
// desc.HandlerType; a mismatch panics with the offending types
// (matching grpc-go's logger.Fatalf behaviour).
func (s *serviceRegistrar) RegisterService(desc *grpc.ServiceDesc, impl any) {
	methods := buildMethods(desc, impl)
	s.server.Register(methods...)
}

// buildMethods is RegisterService minus the server.Register call,
// factored out so tests can inspect the conversion.
func buildMethods(desc *grpc.ServiceDesc, impl any) []connect.Method {
	if impl != nil && desc.HandlerType != nil {
		handlerType := reflect.TypeOf(desc.HandlerType)
		if handlerType.Kind() == reflect.Pointer {
			handlerType = handlerType.Elem()
		}
		if implType := reflect.TypeOf(impl); !implType.Implements(handlerType) {
			//nolint:forbidigo // registration-time misuse: panic surfaces the bug at startup, like grpc-go
			panic(fmt.Sprintf("vanguardgrpc: handler %v does not implement %v", implType, handlerType))
		}
	}

	svcDesc := findService(desc.ServiceName)

	methods := make([]connect.Method, 0, len(desc.Methods)+len(desc.Streams))
	for _, m := range desc.Methods {
		spec := makeSpec(desc.ServiceName, m.MethodName, connect.StreamTypeUnary, svcDesc)
		methods = append(methods, connect.Method{
			Spec:    spec,
			Handler: unaryServerFunc(impl, m.Handler),
		})
	}
	for _, streamDesc := range desc.Streams {
		var streamType connect.StreamType
		switch {
		case streamDesc.ClientStreams && streamDesc.ServerStreams:
			streamType = connect.StreamTypeBidi
		case streamDesc.ClientStreams:
			streamType = connect.StreamTypeClient
		case streamDesc.ServerStreams:
			streamType = connect.StreamTypeServer
		default:
			streamType = connect.StreamTypeUnary
		}
		spec := makeSpec(desc.ServiceName, streamDesc.StreamName, streamType, svcDesc)
		methods = append(methods, connect.Method{
			Spec:    spec,
			Handler: streamServerFunc(impl, streamDesc.Handler),
		})
	}
	return methods
}

// findService looks up a ServiceDescriptor by its fully-qualified name
// via the global proto registry. Returns nil when the name does not
// resolve (e.g. the schema was not linked into the binary), in which
// case Spec.Schema stays nil and downstream transports that need the
// descriptor (notably vanguard for HTTP rule extraction) will skip
// the procedure.
func findService(name string) protoreflect.ServiceDescriptor {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		return nil
	}
	svc, _ := desc.(protoreflect.ServiceDescriptor)
	return svc
}

func makeSpec(
	svcName, methodName string,
	streamType connect.StreamType,
	svcDesc protoreflect.ServiceDescriptor,
) connect.Spec {
	spec := connect.Spec{
		StreamType: streamType,
		Procedure:  "/" + svcName + "/" + methodName,
	}
	if svcDesc == nil {
		return spec
	}
	md := svcDesc.Methods().ByName(protoreflect.Name(methodName))
	if md == nil {
		return spec
	}
	spec.Schema = md
	if opts, ok := md.Options().(*descriptorpb.MethodOptions); ok {
		switch opts.GetIdempotencyLevel() {
		case descriptorpb.MethodOptions_NO_SIDE_EFFECTS:
			spec.IdempotencyLevel = connect.IdempotencyNoSideEffects
		case descriptorpb.MethodOptions_IDEMPOTENT:
			spec.IdempotencyLevel = connect.IdempotencyIdempotent
		case descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN:
			spec.IdempotencyLevel = connect.IdempotencyUnknown
		}
	}
	return spec
}

// unaryServerFunc adapts a [grpc.MethodHandler] into a
// [connect.ServerFunc]. The dec callback the gRPC handler expects is
// satisfied by delegating to stream.Receive; the returned response
// message goes back via stream.Send.
//
// The gRPC interceptor argument is nil: interceptors registered on the
// connect.Server run a layer above, and gRPC-style per-method
// interceptors are not supported here.
func unaryServerFunc(impl any, handler grpc.MethodHandler) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		ctx, _, err := newGRPCContext(ctx, spec, stream)
		if err != nil {
			return err
		}
		dec := func(req any) error {
			return stream.Receive(req)
		}
		resp, err := handler(impl, ctx, dec, nil)
		if err != nil {
			return convertError(err)
		}
		return stream.Send(resp)
	}
}

// streamServerFunc adapts a [grpc.StreamHandler] into a
// [connect.ServerFunc]. The gRPC stub receives a serverStreamAdapter
// that forwards SendMsg / RecvMsg / Context onto the connect stream.
func streamServerFunc(impl any, handler grpc.StreamHandler) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		ctx, transport, err := newGRPCContext(ctx, spec, stream)
		if err != nil {
			return err
		}
		adapter := &serverStreamAdapter{ctx: ctx, stream: stream, transport: transport}
		if err := handler(impl, adapter); err != nil {
			return convertError(err)
		}
		return nil
	}
}

// convertError translates gRPC-flavoured errors into *connect.Error
// so transports downstream encode the right wire status.
// *connect.Error passes through unchanged;
// google.golang.org/grpc/status errors are translated by code; any
// other error is returned as-is so the transport encodes it as
// CodeUnknown.
func convertError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*connect.Error](err); ok {
		return err
	}
	if st, ok := status.FromError(err); ok && st.Code() != codes.OK {
		out := connect.Errorf(connect.Code(st.Code()), "%s", st.Message())
		for _, detail := range st.Proto().GetDetails() {
			out = out.WithDetail(&connect.ErrorDetail{
				Type:  typeNameFromURL(detail.GetTypeUrl()),
				Value: detail.GetValue(),
			})
		}
		return out.WithCause(err)
	}
	return err
}

func typeNameFromURL(url string) string {
	return url[strings.LastIndexByte(url, '/')+1:]
}

// newGRPCContext gives a gRPC handler the context grpc-go would: the
// request headers as incoming [metadata], the caller as a [peer], and a
// [grpc.ServerTransportStream] so [grpc.Method], [grpc.SetHeader],
// [grpc.SendHeader] and [grpc.SetTrailer] work.
func newGRPCContext(
	ctx context.Context,
	spec connect.Spec,
	stream connect.ServerStream,
) (context.Context, *transportStream, error) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		info = &connect.CallInfo{} // no transport to carry metadata
	}
	incoming, err := incomingMetadata(info.RequestHeader())
	if err != nil {
		return nil, nil, err
	}
	ctx = metadata.NewIncomingContext(ctx, incoming)
	if info.PeerAddr != "" {
		ctx = peer.NewContext(ctx, &peer.Peer{Addr: peerAddr(info.PeerAddr)})
	}
	transport := &transportStream{method: spec.Procedure, info: info, stream: stream}
	return grpc.NewContextWithServerTransportStream(ctx, transport), transport, nil
}

// incomingMetadata converts request headers to gRPC metadata the way
// grpc-go does: protocol headers are dropped and -bin values decoded.
func incomingMetadata(header *connect.Header) (metadata.MD, error) {
	incoming := metadata.MD{}
	for key, values := range header.All() {
		key = strings.ToLower(key)
		if _, reserved := reservedHeaders[key]; reserved {
			continue
		}
		for _, value := range values {
			if strings.HasSuffix(key, binaryHeaderSuffix) {
				decoded, err := connect.DecodeBinaryHeader(value)
				if err != nil {
					return nil, connect.Errorf(connect.CodeInternal,
						"malformed binary metadata %q in header %q: %s", value, key, err).WithCause(err)
				}
				value = string(decoded)
			}
			incoming.Append(key, value)
		}
	}
	return incoming, nil
}

// binaryHeaderSuffix marks metadata whose values are raw bytes, sent
// base64-encoded on the wire.
const binaryHeaderSuffix = "-bin"

// reservedHeaders are the protocol headers grpc-go keeps out of incoming
// metadata.
//
//nolint:gochecknoglobals
var reservedHeaders = map[string]struct{}{
	"grpc-message-type": {},
	"grpc-encoding":     {},
	"grpc-message":      {},
	"grpc-status":       {},
	"grpc-timeout":      {},
	"te":                {},
}

// peerAddr parses a [connect.CallInfo] PeerAddr. grpc-go reports a
// *net.TCPAddr, so that is used when the address parses as one.
func peerAddr(addr string) net.Addr {
	if addrPort, err := netip.ParseAddrPort(addr); err == nil {
		return net.TCPAddrFromAddrPort(addrPort)
	}
	return stringAddr(addr)
}

type stringAddr string

func (a stringAddr) Network() string { return "unknown" }
func (a stringAddr) String() string  { return string(a) }

// transportStream implements [grpc.ServerTransportStream] on the call's
// [connect.CallInfo].
type transportStream struct {
	method string
	info   *connect.CallInfo
	stream connect.ServerStream
}

func (t *transportStream) Method() string { return t.method }

func (t *transportStream) SetHeader(meta metadata.MD) error {
	mergeMetadata(t.info.ResponseHeader(), meta)
	return nil
}

func (t *transportStream) SendHeader(meta metadata.MD) error {
	mergeMetadata(t.info.ResponseHeader(), meta)
	return t.stream.SendHeaders()
}

func (t *transportStream) SetTrailer(meta metadata.MD) error {
	mergeMetadata(t.info.ResponseTrailer(), meta)
	return nil
}

// serverStreamAdapter satisfies [grpc.ServerStream] by forwarding onto
// a [connect.ServerStream]. Generated gRPC stubs receive this in
// place of grpc-go's *serverStream and drive it the same way. Metadata
// goes through the call's [transportStream].
type serverStreamAdapter struct {
	ctx       context.Context //nolint:containedctx // matches grpc-go's ServerStream contract
	stream    connect.ServerStream
	transport *transportStream
}

func (s *serverStreamAdapter) Context() context.Context { return s.ctx }

func (s *serverStreamAdapter) SendMsg(m any) error {
	return s.stream.Send(m)
}

func (s *serverStreamAdapter) RecvMsg(m any) error {
	return s.stream.Receive(m)
}

func (s *serverStreamAdapter) SetHeader(meta metadata.MD) error {
	return s.transport.SetHeader(meta)
}

func (s *serverStreamAdapter) SendHeader(meta metadata.MD) error {
	return s.transport.SendHeader(meta)
}

func (s *serverStreamAdapter) SetTrailer(meta metadata.MD) {
	_ = s.transport.SetTrailer(meta) // never fails
}

// mergeMetadata appends meta onto header, matching grpc-go's SetHeader
// and SetTrailer, which merge with metadata set earlier in the call.
// Values of -bin keys are raw bytes, so they are base64-encoded.
func mergeMetadata(header *connect.Header, meta metadata.MD) {
	for key, values := range meta {
		binary := strings.HasSuffix(strings.ToLower(key), binaryHeaderSuffix)
		for _, value := range values {
			if binary {
				value = connect.EncodeBinaryHeader([]byte(value))
			}
			header.Add(key, value)
		}
	}
}
