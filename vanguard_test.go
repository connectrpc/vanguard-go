// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

type testStream struct {
	reqHeader  http.Header // expected
	rspHeader  http.Header // out
	rspTrailer http.Header // out
	msgs       []testMsg   // in, out
}

type testMsgIn struct {
	method string
	msg    proto.Message
}
type testMsgOut struct {
	msg proto.Message
	err *connect.Error
}
type testMsg struct {
	in  *testMsgIn
	out *testMsgOut
}

func (o *testMsg) getIn() (*testMsgIn, error) {
	if o == nil || o.in == nil {
		return nil, fmt.Errorf("missing input message")
	}
	return o.in, nil
}
func (o *testMsg) getOut() (*testMsgOut, error) {
	if o == nil || o.out == nil {
		return nil, fmt.Errorf("missing output message")
	}
	return o.out, nil
}
func (o *testMsg) get() any {
	if o.in != nil {
		return o.in
	}
	if o.out != nil {
		return o.out
	}
	return nil
}

type testInterceptor struct {
	sync.Map
}

type ttStream struct {
	*testing.T
	testStream
}

func (o *testInterceptor) get(testName string) (ttStream, bool) {
	val, ok := o.Load(testName)
	if !ok {
		return ttStream{}, false
	}
	stream, ok := val.(ttStream)
	return stream, ok
}
func (o *testInterceptor) set(t *testing.T, stream testStream) {
	t.Helper()
	o.Store(t.Name(), ttStream{t, stream})
}
func (o *testInterceptor) del(t *testing.T) {
	t.Helper()
	o.Delete(t.Name())
}

func (o *testInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		val := req.Header().Get("test")
		if val == "" {
			return next(ctx, req)
		}
		stream, ok := o.get(val)
		if !ok {
			return nil, fmt.Errorf("invalid testCase header: %s", val)
		}
		assert.Subset(stream.T, req.Header(), stream.reqHeader)
		if len(stream.msgs) != 2 {
			err := fmt.Errorf("expected 2 messages, got %d", len(stream.msgs))
			return nil, err
		}
		inn, err := stream.msgs[0].getIn()
		if err != nil {
			return nil, err
		}
		out, err := stream.msgs[1].getOut()
		if err != nil {
			return nil, err
		}
		if inn.method != "" && req.Spec().Procedure != inn.method {
			err := fmt.Errorf("expected %s, got %s", inn.method, req.Spec().Procedure)
			return nil, err
		}
		msg, ok := req.Any().(proto.Message)
		if !ok {
			return nil, fmt.Errorf("expected proto.Message, got %T", req.Any())
		}
		diff := cmp.Diff(msg, inn.msg, protocmp.Transform())
		if diff != "" {
			return nil, fmt.Errorf("message didn't match: %s", diff)
		}
		if out.err != nil {
			err := out.err
			if len(stream.rspHeader) > 0 {
				// make a copy of the error and add response headers to it
				err = connect.NewError(out.err.Code(), out.err.Unwrap())
				for _, detail := range out.err.Details() {
					err.AddDetail(detail)
				}
				for key, values := range stream.rspHeader {
					err.Meta()[key] = values
				}
			}
			return nil, err
		}

		// Build response with headers.
		rsp := &AnyResponse{msg: out.msg}
		for key, values := range stream.rspHeader {
			rsp.Header()[key] = values
		}
		for key, values := range stream.rspTrailer {
			rsp.Trailer()[key] = values
		}
		return rsp, nil
	})
}
func (o *testInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}
func (o *testInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return connect.StreamingHandlerFunc(func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		val := conn.RequestHeader().Get("test")
		if val == "" {
			return next(ctx, conn)
		}
		stream, ok := o.get(val)
		if !ok {
			return fmt.Errorf("invalid testCase header: %s", val)
		}
		stream.Log("WrapStreamingHandler", val)
		assert.Subset(stream.T, conn.RequestHeader(), stream.reqHeader)

		for key, vals := range stream.rspHeader {
			conn.ResponseHeader()[key] = vals
		}
		for _, msg := range stream.msgs {
			switch msg := msg.get().(type) {
			case *testMsgIn:
				got := proto.Clone(msg.msg)
				if err := conn.Receive(got); err != nil {
					return err
				}
				diff := cmp.Diff(got, msg.msg, protocmp.Transform())
				assert.Empty(stream.T, diff, "message didn't match")
				if diff != "" {
					return fmt.Errorf("message didn't match")
				}
			case *testMsgOut:
				if msg.err != nil {
					return msg.err
				}
				if err := conn.Send(msg.msg); err != nil {
					return err
				}
			default:
				return fmt.Errorf("expected message")
			}
		}
		for key, vals := range stream.rspTrailer {
			conn.ResponseTrailer()[key] = vals
		}
		return nil
	})
}

func (o *testInterceptor) restUnaryHandler(
	codec Codec, comp *compressionPool,
) http.HandlerFunc {
	codecNames := map[string]string{
		"application/json": "json",
	}
	handler := func(stream ttStream, rsp http.ResponseWriter, req *http.Request) error {
		if len(stream.msgs) != 2 {
			return fmt.Errorf("expected 2 messages, got %d", len(stream.msgs))
		}
		inn, err := stream.msgs[0].getIn()
		if err != nil {
			return err
		}
		out, err := stream.msgs[1].getOut()
		if err != nil {
			return err
		}
		assert.Equal(stream.T, inn.method, req.URL.String(), "url didn't match")
		assert.Subset(stream.T, req.Header, stream.reqHeader, "headers didn't match")
		contentType := req.Header.Get("Content-Type")
		encoding := req.Header.Get("Content-Encoding")
		acceptEncoding := req.Header.Get("Accept-Encoding")

		body, err := io.ReadAll(req.Body)
		if err != nil {
			return err
		}
		if comp != nil && len(body) > 0 && encoding != "" {
			assert.Equal(stream.T, comp.Name(), encoding, "expected encoding")
			var dst bytes.Buffer
			if err := comp.decompress(&dst, bytes.NewBuffer(body)); err != nil {
				return err
			}
			body = dst.Bytes()
		}

		got := proto.Clone(inn.msg)
		if len(body) > 0 { //nolint:nestif
			if isSpecialHTTPBody(got.ProtoReflect().Descriptor(), nil) {
				got, _ := got.(*httpbody.HttpBody)
				got.ContentType = contentType
				got.Data = body
			} else {
				codecName := codecNames[contentType]
				if !assert.Equal(stream.T, codec.Name(), codecName, "codec didn't match") {
					return fmt.Errorf("codec didn't match")
				}
				if err := codec.Unmarshal(body, got); err != nil {
					return err
				}
			}
		}
		diff := cmp.Diff(got, inn.msg, protocmp.Transform())
		assert.Empty(stream.T, diff, "message didn't match")

		// Write headers.
		for key, values := range stream.rspHeader {
			for _, value := range values {
				rsp.Header().Add(key, value)
			}
		}

		// Write error, if any.
		if out.err != nil {
			httpWriteError(rsp, out.err)
			//nolint:nilerr
			return nil
		}

		// Write body.
		rsp.Header().Set("Content-Type", contentType)
		rsp.Header().Set("Content-Encoding", "identity")
		if isSpecialHTTPBody(out.msg.ProtoReflect().Descriptor(), nil) { //nolint:nestif
			msg, _ := out.msg.(*httpbody.HttpBody)
			rsp.Header().Set("Content-Type", msg.ContentType)
			_, err = rsp.Write(msg.Data)
			assert.NoError(stream.T, err, "failed to write response")
		} else {
			body, err = codec.MarshalAppend(nil, out.msg)
			if err != nil {
				return err
			}
			if comp != nil && acceptEncoding != "" {
				assert.Equal(stream.T, comp.Name(), acceptEncoding, "expected encoding")
				rsp.Header().Set("Content-Encoding", comp.Name())
				var dst bytes.Buffer
				if err := comp.compress(&dst, bytes.NewBuffer(body)); err != nil {
					return err
				}
				body = dst.Bytes()
			}
			_, err = rsp.Write(body)
			assert.NoError(stream.T, err, "failed to write response")
		}

		// Write trailers.
		for key, values := range stream.rspTrailer {
			for _, value := range values {
				rsp.Header().Add(key, value)
			}
		}
		return nil
	}
	return func(rsp http.ResponseWriter, req *http.Request) {
		val := req.Header.Get("test")
		if val == "" {
			http.Error(rsp, "missing test header", http.StatusInternalServerError)
			return
		}
		stream, ok := o.get(val)
		if !ok {
			http.Error(rsp, "invalid test header", http.StatusInternalServerError)
			return
		}
		if err := handler(stream, rsp, req); err != nil {
			stream.T.Error(err)
			http.Error(rsp, err.Error(), http.StatusInternalServerError)
		}
	}
}

type unusedType struct{}

type AnyResponse struct {
	connect.Response[unusedType]
	msg proto.Message
}

func (a *AnyResponse) Any() any { return a.msg }

func getCompressor(t *testing.T, name string) connect.Compressor {
	t.Helper()
	switch name {
	case CompressionGzip:
		return DefaultGzipCompressor()
	case CompressionIdentity:
		return nil
	default:
		t.Fatalf("unknown compression: %s", name)
		return nil
	}
}
func getDecompressor(t *testing.T, name string) connect.Decompressor {
	t.Helper()
	switch name {
	case CompressionGzip:
		return DefaultGzipDecompressor()
	case CompressionIdentity:
		return nil
	default:
		t.Fatalf("unknown compression: %s", name)
		return nil
	}
}

type testServer struct {
	name string
	svr  *httptest.Server
}

func appendClientProtocolOptions(t *testing.T, opts []connect.ClientOption, protocol Protocol) []connect.ClientOption {
	t.Helper()
	switch protocol {
	case ProtocolGRPC:
		return append(opts, connect.WithGRPC())
	case ProtocolGRPCWeb:
		return append(opts, connect.WithGRPCWeb())
	case ProtocolConnect:
		return opts // no option needed
	default:
		t.Fatalf("unknown protocol: %s", protocol)
	}
	return opts
}

func appendClientCodecOptions(t *testing.T, opts []connect.ClientOption, codec string) []connect.ClientOption {
	t.Helper()
	switch codec {
	case CodecJSON:
		return append(opts, connect.WithProtoJSON())
	case CodecProto:
		// default...
	default:
		t.Fatalf("unknown codec: %s", codec)
	}
	return opts
}
func appendClientCompressionOptions(t *testing.T, opts []connect.ClientOption, compression string) []connect.ClientOption {
	t.Helper()
	switch compression {
	case CompressionIdentity:
		return append(opts,
			// NB: nil factory functions *remove* support for gzip, which is otherwise on by default.
			connect.WithAcceptCompression(
				CompressionGzip, nil, nil,
			),
		)
	case CompressionGzip:
		return append(opts,
			connect.WithAcceptCompression(
				CompressionGzip,
				func() connect.Decompressor {
					return getDecompressor(t, CompressionGzip)
				},
				func() connect.Compressor {
					return getCompressor(t, CompressionGzip)
				},
			),
			connect.WithSendCompression(compression),
		)
	default:
		t.Fatalf("unknown compression: %s", compression)
	}
	return opts
}

func makeRequest[T any](headers http.Header, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	for k, v := range headers {
		req.Header()[k] = v
	}
	return req
}

type unaryMethod[Req, Resp any] func(context.Context, *connect.Request[Req]) (*connect.Response[Resp], error)
type serverStreamMethod[Req, Resp any] func(context.Context, *connect.Request[Req]) (*connect.ServerStreamForClient[Resp], error)
type clientStreamMethod[Req, Resp any] func(context.Context) *connect.ClientStreamForClient[Req, Resp]
type bidiStreamMethod[Req, Resp any] func(context.Context) *connect.BidiStreamForClient[Req, Resp]

func outputFromUnary[Req, Resp any](
	ctx context.Context,
	method unaryMethod[Req, Resp],
	headers http.Header,
	reqs []proto.Message,
) (http.Header, []proto.Message, http.Header, error) {
	if len(reqs) != 1 {
		return nil, nil, nil, fmt.Errorf("unary method takes exactly 1 request but got %d", len(reqs))
	}
	req := any(reqs[0])
	resp, err := method(ctx, makeRequest(headers, req.(*Req)))
	if err != nil {
		var headers http.Header
		if connErr := new(connect.Error); errors.As(err, &connErr) {
			headers = connErr.Meta()
		}
		return headers, nil, nil, err
	}
	msg := any(resp.Msg)
	//nolint:forcetypeassert
	return resp.Header(), []proto.Message{msg.(proto.Message)}, resp.Trailer(), nil
}

func outputFromServerStream[Req, Resp any](
	ctx context.Context,
	method serverStreamMethod[Req, Resp],
	headers http.Header,
	reqs []proto.Message,
) (http.Header, []proto.Message, http.Header, error) {
	if len(reqs) != 1 {
		return nil, nil, nil, fmt.Errorf("unary method takes exactly 1 request but got %d", len(reqs))
	}
	req := any(reqs[0])
	str, err := method(ctx, makeRequest(headers, req.(*Req)))
	if err != nil {
		var headers http.Header
		if connErr := new(connect.Error); errors.As(err, &connErr) {
			headers = connErr.Meta()
		}
		return headers, nil, nil, err
	}
	var msgs []proto.Message
	for str.Receive() {
		msg := any(str.Msg())
		//nolint:forcetypeassert
		msgs = append(msgs, msg.(proto.Message))
	}
	return str.ResponseHeader(), msgs, str.ResponseTrailer(), str.Err()
}

func outputFromClientStream[Req, Resp any](
	ctx context.Context,
	method clientStreamMethod[Req, Resp],
	headers http.Header,
	reqs []proto.Message,
) (http.Header, []proto.Message, http.Header, error) {
	str := method(ctx)
	for k, v := range headers {
		str.RequestHeader()[k] = v
	}
	for _, msg := range reqs {
		//nolint:forcetypeassert
		if str.Send(any(msg).(*Req)) != nil {
			// we don't need this error; we'll get the error below
			// since str.CloseAndReceive returns the actual RPC errors
			break
		}
	}
	resp, err := str.CloseAndReceive()
	if err != nil {
		var headers http.Header
		if connErr := new(connect.Error); errors.As(err, &connErr) {
			headers = connErr.Meta()
		}
		return headers, nil, nil, err
	}
	msg := any(resp.Msg)
	//nolint:forcetypeassert
	return resp.Header(), []proto.Message{msg.(proto.Message)}, resp.Trailer(), nil
}

func outputFromBidiStream[Req, Resp any](
	ctx context.Context,
	method bidiStreamMethod[Req, Resp],
	headers http.Header,
	reqs []proto.Message,
) (http.Header, []proto.Message, http.Header, error) {
	str := method(ctx)
	defer func() {
		_ = str.CloseResponse()
	}()
	var msgs []proto.Message
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var resp *Resp
			resp, err = str.Receive()
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				return
			}
			msg := any(resp)
			//nolint:forcetypeassert
			msgs = append(msgs, msg.(proto.Message))
		}
	}()

	for k, v := range headers {
		str.RequestHeader()[k] = v
	}
	for _, msg := range reqs {
		//nolint:forcetypeassert
		if str.Send(any(msg).(*Req)) != nil {
			// we don't need this error; we'll get the error from above
			// goroutine since str.Receive returns the actual RPC errors
			break
		}
	}
	<-done
	return str.ResponseHeader(), msgs, str.ResponseTrailer(), err
}

func protocolAssertMiddleware(
	protocol Protocol, codec string, compression string,
	next http.Handler,
) http.HandlerFunc {
	var allowedCompression []string
	if compression != "" && compression != CompressionIdentity {
		// a server expecting gzip compression also allows identity/uncompressed
		allowedCompression = []string{compression, CompressionIdentity, ""}
	} else {
		allowedCompression = []string{CompressionIdentity, ""}
	}
	return func(rsp http.ResponseWriter, req *http.Request) {
		var wantHdr map[string][]string
		switch protocol {
		case ProtocolGRPC:
			wantHdr = map[string][]string{
				"Content-Type":  {fmt.Sprintf("application/grpc+%s", codec)},
				"Grpc-Encoding": allowedCompression,
			}
		case ProtocolGRPCWeb:
			wantHdr = map[string][]string{
				"Content-Type":  {fmt.Sprintf("application/grpc-web+%s", codec)},
				"Grpc-Encoding": allowedCompression,
			}
		case ProtocolConnect:
			if strings.HasPrefix(req.Header.Get("Content-Type"), "application/connect") {
				wantHdr = map[string][]string{
					"Content-Type":             {fmt.Sprintf("application/connect+%s", codec)},
					"Connect-Content-Encoding": allowedCompression,
				}
			} else {
				wantHdr = map[string][]string{
					"Content-Type":     {fmt.Sprintf("application/%s", codec)},
					"Content-Encoding": allowedCompression,
				}
			}
		default:
			http.Error(rsp, "unknown protocol", http.StatusInternalServerError)
			return
		}
		for key, vals := range wantHdr {
			var found bool
			gotHdr := req.Header.Get(key)
			for _, val := range vals {
				if gotHdr == val {
					found = true
					break
				}
			}
			if !found {
				http.Error(rsp, fmt.Sprintf("header %s is %q; should be one of [%v]", key, gotHdr, vals), http.StatusInternalServerError)
				return
			}
		}
		next.ServeHTTP(rsp, req)
	}
}

func runRPCTestCase[Client any](
	t *testing.T,
	interceptor *testInterceptor,
	client Client,
	invoke func(Client, http.Header, []proto.Message) (http.Header, []proto.Message, http.Header, error),
	stream testStream,
) {
	t.Helper()
	interceptor.set(t, stream)
	defer interceptor.del(t)
	reqHeaders := http.Header{}
	reqHeaders.Set("Test", t.Name()) // test header
	for k, v := range stream.reqHeader {
		reqHeaders[k] = v
	}
	var reqMsgs []proto.Message
	for _, streamMsg := range stream.msgs {
		if streamMsg.in != nil {
			reqMsgs = append(reqMsgs, streamMsg.in.msg)
		}
	}
	headers, responses, trailers, err := invoke(client, reqHeaders, reqMsgs)
	var expectedErr *connect.Error
	for _, streamMsg := range stream.msgs {
		if streamMsg.out != nil && streamMsg.out.err != nil {
			expectedErr = streamMsg.out.err
			break
		}
	}
	if expectedErr == nil {
		assert.NoError(t, err)
	} else {
		assert.Equal(t, expectedErr.Code(), connect.CodeOf(err))
	}
	assert.Subset(t, headers, stream.rspHeader)
	assert.Subset(t, trailers, stream.rspTrailer)
	var expectedResponses []proto.Message
	for _, streamMsg := range stream.msgs {
		if streamMsg.out != nil && streamMsg.out.msg != nil {
			expectedResponses = append(expectedResponses, streamMsg.out.msg)
		}
	}
	require.Len(t, responses, len(expectedResponses))
	for i, msg := range responses {
		want := expectedResponses[i]
		assert.Empty(t, cmp.Diff(want, msg, protocmp.Transform()))
	}
}
