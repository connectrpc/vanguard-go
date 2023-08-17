// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
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
		if err := equalHeaders(stream.reqHeader, req.Header()); err != nil {
			return nil, err
		}
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
			return nil, out.err
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
	return connect.StreamingClientFunc(func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		return next(ctx, spec)
	})
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
		assert.Equal(stream.T, stream.reqHeader, conn.RequestHeader())

		for key, vals := range stream.rspHeader {
			conn.RequestHeader()[key] = vals
		}
		for _, msg := range stream.msgs {
			switch msg := msg.get().(type) {
			case *testMsgIn:
				got := proto.Clone(msg.msg)
				if err := conn.Receive(got); err != nil {
					return err
				}
				diff := cmp.Diff(msg, msg.msg, protocmp.Transform())
				if diff != "" {
					return fmt.Errorf("message didn't match: %s", diff)
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
	codec Codec, comp connect.Compressor, decomp connect.Decompressor,
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
		assert.Equal(stream.T, req.URL.String(), inn.method, "URL didn't match")

		assert.NoError(stream.T, equalHeaders(stream.reqHeader, req.Header), "headers didn't match")
		contentType := req.Header.Get("Content-Type")
		encoding := req.Header.Get("Content-Encoding")
		acceptEncoding := req.Header.Get("Accept-Encoding")

		var input io.Reader = req.Body
		if decomp != nil {
			assert.Equal(stream.T, encoding, "gzip", "expected encoding") // TODO: use decomp.Name()
			if err := decomp.Reset(input); err != nil {
				return err
			}
			defer decomp.Close()
		}
		body, err := io.ReadAll(input)
		if err != nil {
			return err
		}

		got := proto.Clone(inn.msg)
		if len(body) > 0 {
			codecName := codecNames[contentType]
			assert.Equal(stream.T, codec.Name(), codecName, "codec didn't match")
			if err := codec.Unmarshal(body, got); err != nil {
				return err
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
		rsp.Header().Set("Content-Encoding", acceptEncoding)
		var output io.Writer = rsp
		if comp != nil {
			assert.Equal(stream.T, acceptEncoding, "gzip", "expected gzip encoding") // TODO: use comp.Name()
			comp.Reset(output)
			defer comp.Close()
			output = comp
		}
		body, err = codec.MarshalAppend(nil, out.msg)
		if err != nil {
			return err
		}
		_, err = output.Write(body)
		assert.NoError(stream.T, err, "failed to write response")

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

func equalHeaders(a, b http.Header) error {
	for key, values := range a {
		if !equalSlices(values, b[key]) {
			return fmt.Errorf(
				"header %s: want %v got %v", key, a[key], b[key],
			)
		}
	}
	return nil
}
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index, values := range a {
		if values != b[index] {
			return false
		}
	}
	return true
}
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
type testOpt struct {
	name string
	svr  *httptest.Server
	opts []connect.ClientOption
}

func appendClientProtocolOptions(t *testing.T, opts []connect.ClientOption, protocol Protocol) []connect.ClientOption {
	t.Helper()
	switch protocol {
	case ProtocolGRPC:
		return append(opts, connect.WithGRPC())
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
