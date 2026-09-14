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

package vanguard

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Forward returns a [connect.Method] that relays calls for desc to client.
// Metadata and errors pass through in both directions. Messages are
// instantiated from desc, so a proxy needs no generated code.
func Forward(client *connect.Client, desc protoreflect.MethodDescriptor) connect.Method {
	resolver := connectproto.TypeResolver(protoregistry.GlobalTypes)
	if service, ok := desc.Parent().(protoreflect.ServiceDescriptor); ok {
		resolver = resolverForService(service)
	}
	spec := specFromDescriptor(desc)
	requestType := messageType(resolver, desc.Input())
	responseType := messageType(resolver, desc.Output())
	return connect.Method{
		Spec: spec,
		Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
			return forward(ctx, client, spec, requestType, responseType, stream)
		},
	}
}

// ForwardService returns a [Forward] method for every RPC of service.
func ForwardService(client *connect.Client, service protoreflect.ServiceDescriptor) []connect.Method {
	methods := service.Methods()
	forwarded := make([]connect.Method, methods.Len())
	for i := range methods.Len() {
		forwarded[i] = Forward(client, methods.Get(i))
	}
	return forwarded
}

func specFromDescriptor(desc protoreflect.MethodDescriptor) connect.Spec {
	spec := connect.Spec{
		Schema:    desc,
		Procedure: "/" + string(desc.Parent().FullName()) + "/" + string(desc.Name()),
	}
	if desc.IsStreamingClient() {
		spec.StreamType |= connect.StreamTypeClient
	}
	if desc.IsStreamingServer() {
		spec.StreamType |= connect.StreamTypeServer
	}
	if opts, ok := desc.Options().(*descriptorpb.MethodOptions); ok {
		switch opts.GetIdempotencyLevel() {
		case descriptorpb.MethodOptions_NO_SIDE_EFFECTS:
			spec.IdempotencyLevel = connect.IdempotencyNoSideEffects
		case descriptorpb.MethodOptions_IDEMPOTENT:
			spec.IdempotencyLevel = connect.IdempotencyIdempotent
		case descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN:
		}
	}
	return spec
}

// forward pumps the downstream server stream into an upstream client
// stream and the upstream responses back, concurrently, so every stream
// type is served.
func forward(
	ctx context.Context,
	client *connect.Client,
	spec connect.Spec,
	requestType, responseType protoreflect.MessageType,
	downstream connect.ServerStream,
) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	upstreamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	upstreamCtx, upstreamInfo := connect.NewClientContext(upstreamCtx)
	if info != nil {
		copyHeader(upstreamInfo.RequestHeader(), info.RequestHeader())
	}
	upstream, err := client.CallClientStream(upstreamCtx, spec)
	if err != nil {
		return forwardError(err)
	}
	defer upstream.Close()

	sent := make(chan error, 1)
	go func() {
		sent <- forwardRequests(downstream, upstream, requestType, cancel)
	}()
	err = forwardResponses(upstream, downstream, responseType, info, upstreamInfo)
	select {
	case sendErr := <-sent:
		if sendErr != nil {
			return sendErr
		}
	default:
	}
	return err
}

func forwardRequests(
	downstream connect.ServerStream,
	upstream connect.ClientStream,
	requestType protoreflect.MessageType,
	cancel context.CancelFunc,
) error {
	for {
		request := requestType.New().Interface()
		err := downstream.Receive(request)
		if errors.Is(err, io.EOF) {
			return forwardError(upstream.CloseSend())
		}
		if err != nil {
			cancel() // the upstream call cannot complete without its requests
			return err
		}
		if err := upstream.Send(request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil // upstream closed early; its verdict arrives via Receive
			}
			return forwardError(err)
		}
	}
}

func forwardResponses(
	upstream connect.ClientStream,
	downstream connect.ServerStream,
	responseType protoreflect.MessageType,
	info, upstreamInfo *connect.CallInfo,
) error {
	headersCopied := false
	for {
		response := responseType.New().Interface()
		err := upstream.Receive(response)
		if info != nil {
			// Unary trailers ride in the headers, so they must be known
			// before the first Send commits them.
			copyHeader(info.ResponseTrailer(), upstreamInfo.ResponseTrailer())
			if !headersCopied {
				copyHeader(info.ResponseHeader(), upstreamInfo.ResponseHeader())
				headersCopied = true
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return forwardError(err)
		}
		if err := downstream.Send(response); err != nil {
			return err
		}
	}
}

// forwardError re-raises an upstream error as this handler's own, keeping
// its code, message, and details.
func forwardError(err error) error {
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		return err
	}
	forwarded := connect.NewError(connectErr.Code(), connectErr.Message())
	for _, detail := range connectErr.Details() {
		forwarded = forwarded.WithDetail(detail)
	}
	return forwarded
}

// hopByHopHeaders describe one HTTP connection and never cross a proxy.
//
//nolint:gochecknoglobals
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Content-Length":      {},
	"Host":                {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func copyHeader(dst, src *connect.Header) {
	for key, vals := range src.All() {
		if _, skip := hopByHopHeaders[key]; skip {
			continue
		}
		dst.SetValues(key, vals)
	}
}
