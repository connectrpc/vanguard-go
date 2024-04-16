// Copyright 2023-2024 Buf Technologies, Inc.
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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultTestTimeout = 30 * time.Second
)

func TestServiceWithSchema(t *testing.T) {
	t.Parallel()
	file, err := makeFile("")
	require.NoError(t, err)

	svcDesc := file.Services().ByName("BlahService")
	require.NotNil(t, svcDesc)
	methodPath := "/" + string(svcDesc.FullName()) + "/Do"

	t.Run("default_bespoke_resolver", func(t *testing.T) {
		t.Parallel()
		svc := NewServiceWithSchema(svcDesc, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

		// Make sure we can validate the service and that the default resolver is able to
		// correctly resolve request and response types.
		transcoder, err := NewTranscoder([]*Service{svc})
		require.NoError(t, err)

		res := transcoder.methods[methodPath].resolver
		// Resolver can also resolve other messages in the file.
		_, err = res.FindMessageByName("foo.bar.baz.v1.Blue")
		require.NoError(t, err)
		// And it can resolve anything in the service file's transitive dependencies.
		timestampType, err := res.FindMessageByName("google.protobuf.Timestamp")
		require.NoError(t, err)
		// All types are dynamic.
		_, ok := timestampType.New().Interface().(*dynamicpb.Message)
		assert.True(t, ok)
	})
	t.Run("default_bespoke_resolver_service_has_no_parent_file", func(t *testing.T) {
		t.Parallel()
		svcDescNoParent := &serviceWithNoParentFile{ServiceDescriptor: svcDesc}
		svc := NewServiceWithSchema(svcDescNoParent, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

		// Still works. But now we just use dynamic message types using the
		// method descriptors.
		transcoder, err := NewTranscoder([]*Service{svc})
		require.NoError(t, err)

		res := transcoder.methods[methodPath].resolver
		// It cannot resolve other types that were in the same file.
		_, err = res.FindMessageByName("foo.bar.baz.v1.Blue")
		require.ErrorIs(t, err, protoregistry.NotFound)
		// This type can be resolved, because the default resolver will fallback to global types.
		timestampType, err := res.FindMessageByName("google.protobuf.Timestamp")
		require.NoError(t, err)
		// But since it is from global types, it is NOT a dynamic message type.
		_, ok := timestampType.New().Interface().(*timestamppb.Timestamp)
		assert.True(t, ok)
	})
	t.Run("uses_dynamic_message_type_with_bad_resolver", func(t *testing.T) {
		t.Parallel()
		svc := NewServiceWithSchema(
			svcDesc,
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
			// Global registry doesn't know about these types.
			WithTypeResolver(protoregistry.GlobalTypes),
		)

		// Still works. But now we just use dynamic message types using the
		// method descriptors.
		transcoder, err := NewTranscoder([]*Service{svc})
		require.NoError(t, err)

		res := transcoder.methods[methodPath].resolver
		// It cannot resolve other types that were in the same file.
		_, err = res.FindMessageByName("foo.bar.baz.v1.Blue")
		require.ErrorIs(t, err, protoregistry.NotFound)
		// This type can be resolved, because the default resolver will fallback to global types.
		timestampType, err := res.FindMessageByName("google.protobuf.Timestamp")
		require.NoError(t, err)
		// But since it is from global types, it is NOT a dynamic message type.
		_, ok := timestampType.New().Interface().(*timestamppb.Timestamp)
		assert.True(t, ok)
	})
}

func TestRuleSelector(t *testing.T) {
	t.Parallel()

	var interceptor testInterceptor
	svc := NewService(testv1connect.NewLibraryServiceHandler(
		testv1connect.UnimplementedLibraryServiceHandler{},
		connect.WithInterceptors(&interceptor),
	))

	_, err := NewTranscoder([]*Service{svc}, WithRules(&annotations.HttpRule{
		Selector: "grpc.health.v1.Health.Check",
		Pattern: &annotations.HttpRule_Get{
			Get: "/healthz",
		},
	}))
	assert.ErrorContains(t, err, "rule \"grpc.health.v1.Health.Check\" does not match any methods") //nolint:testifylint

	_, err = NewTranscoder([]*Service{svc}, WithRules(&annotations.HttpRule{
		Selector: "invalid.*.Get",
		Pattern: &annotations.HttpRule_Get{
			Get: "/v1/*",
		},
	}))
	assert.ErrorContains(t, err, "wildcard selector \"invalid.*.Get\" must be at the end") //nolint:testifylint

	_, err = NewTranscoder([]*Service{svc}, WithRules(&annotations.HttpRule{
		Selector: "grpc.health.v1.Health.*",
		Pattern: &annotations.HttpRule_Get{
			Get: "/healthz",
		},
	}))
	assert.ErrorContains(t, err, "rule \"grpc.health.v1.Health.*\" does not match any methods") //nolint:testifylint

	_, err = NewTranscoder([]*Service{svc}, WithRules(&annotations.HttpRule{
		Pattern: &annotations.HttpRule_Get{
			Get: "/v1/*",
		},
	}))
	assert.ErrorContains(t, err, "rule missing selector") //nolint:testifylint

	handler, err := NewTranscoder(
		[]*Service{svc},
		WithRules(&annotations.HttpRule{
			Selector: "vanguard.test.v1.LibraryService.GetBook",
			Pattern: &annotations.HttpRule_Get{
				Get: "/v1/selector/{name=shelves/*/books/*}",
			},
		}),
	)
	require.NoError(t, err)

	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, defaultTestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/selector/shelves/123/books/456", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Message", "hello")
	req.Header.Set("Test", t.Name()) // for interceptor
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()

	interceptor.set(t, testStream{
		method: testv1connect.LibraryServiceGetBookProcedure,
		reqHeader: http.Header{
			"Message": []string{"hello"},
		},
		rspHeader: http.Header{
			"Message": []string{"world"},
		},
		msgs: []testMsg{
			{in: &testMsgIn{
				msg: &testv1.GetBookRequest{Name: "shelves/123/books/456"},
			}},
			{out: &testMsgOut{
				msg: &testv1.Book{Name: "shelves/123/books/456"},
			}},
		},
	})
	defer interceptor.del(t)

	handler.ServeHTTP(rsp, req)
	result := rsp.Result()
	defer result.Body.Close()

	dump, err := httputil.DumpResponse(result, true)
	require.NoError(t, err)
	t.Log(string(dump))

	assert.Equal(t, http.StatusOK, result.StatusCode)
	assert.Equal(t, "application/json", result.Header.Get("Content-Type"))
	assert.Equal(t, "world", result.Header.Get("Message"))
}

type testStream struct {
	method     string
	reqHeader  http.Header // expected
	rspHeader  http.Header // out
	rspTrailer http.Header // out
	msgs       []testMsg   // in, out

	// If set, the error that a client expects, overriding any other error
	// (or lack thereof) in msgs.
	err *connect.Error
}

type testMsg struct {
	in  *testMsgIn
	out *testMsgOut
}

func (o *testMsg) getIn() (*testMsgIn, error) {
	if o == nil || o.in == nil {
		return nil, errors.New("missing input message")
	}
	return o.in, nil
}

func (o *testMsg) getOut() (*testMsgOut, error) {
	if o == nil || o.out == nil {
		return nil, errors.New("missing output message")
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

type testMsgIn struct {
	msg proto.Message
	// An error on input means that the middleware should generate an error here.
	// The msg is present so that runRPCTestCase knows what message to send, but
	// if err != nil then the interceptor instead accepts the operation to be
	// cancelled (and the middleware will send this error back to the clent).
	err *connect.Error
}

type testMsgOut struct {
	msg proto.Message
	// If msg is nil, the interceptor will return this error instead of sending
	// a message. But if both are non-nil, then the interceptor will send the
	// message but expect an error doing so. So in that case, this error is
	// expected by both the server handler and the client.
	err *connect.Error
}

type ttStream struct {
	*testing.T
	testStream

	started atomic.Bool
	result  error
	done    chan struct{}
}

func (s *ttStream) start() {
	// Called from the interceptor when it starts handling the stream
	s.started.Store(true)
}

func (s *ttStream) finish(result error) {
	// Called from the interceptor when it finishes handling the stream
	s.result = result
	close(s.done)
}

func (s *ttStream) await(t *testing.T, expectServerDone bool) (serverInvoked bool, serverErr error) {
	t.Helper()
	// Called from test code to make sure server handler has completed.
	// Returns any error that the interceptor finished with.
	// Should only be called after the RPC appears to have completed in
	// the test client.
	if !s.started.Load() {
		// Interceptor never started, so nothing to wait for.
		return false, nil
	}
	if expectServerDone {
		select {
		case <-s.done:
			return true, s.result
		default:
			t.Fatal("expecting server to already be done but it's not")
		}
	}
	select {
	case <-s.done:
		return true, s.result
	case <-time.After(3 * time.Second):
		return true, errors.New("timeout: interceptor still did not finish after 3 seconds")
	}
}

type testInterceptor struct {
	sync.Map
}

func (i *testInterceptor) get(testName string) (*ttStream, bool) {
	val, ok := i.Load(testName)
	if !ok {
		return nil, false
	}
	stream, ok := val.(*ttStream)
	return stream, ok
}

func (i *testInterceptor) set(t *testing.T, stream testStream) func(*testing.T, bool) (bool, error) {
	t.Helper()
	str := &ttStream{
		T:          t,
		testStream: stream,
		done:       make(chan struct{}),
	}
	i.Store(t.Name(), str)
	// The returned function can be used by test code to await server completion.
	// (Useful in the event that middleware cancels the operation early, so client
	// could see a completed response while server still running concurrently.)
	return str.await
}

func (i *testInterceptor) del(t *testing.T) {
	t.Helper()
	i.Delete(t.Name())
}

func (i *testInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (_ connect.AnyResponse, resultError error) {
		if err := assertTestTimeoutEncoded(ctx); err != nil {
			return nil, err
		}
		val := req.Header().Get("test")
		if val == "" {
			return next(ctx, req)
		}
		stream, ok := i.get(val)
		if !ok {
			return nil, fmt.Errorf("invalid testCase header: %s", val)
		}
		stream.start()
		defer func() {
			stream.finish(resultError)
		}()
		if !assert.Equal(stream.T, stream.method, req.Spec().Procedure) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("expected %s, got %s", stream.method, req.Spec().Procedure))
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
		if inn.err != nil {
			return nil, errors.New("testMsgIn should not have err field set for unary request")
		}

		out, err := stream.msgs[1].getOut()
		if err != nil {
			return nil, err
		}
		if out.msg != nil && out.err != nil {
			return nil, errors.New("testMsgOut should not have both msg and err fields set for unary request")
		}

		msg, ok := req.Any().(proto.Message)
		if !ok {
			return nil, fmt.Errorf("expected proto.Message, got %T", req.Any())
		}
		diff := cmp.Diff(msg, inn.msg, protocmp.Transform())
		if diff != "" {
			stream.Error(diff)
			return nil, errors.New("unexpected message diff")
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
	}
}

func (i *testInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *testInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) (resultError error) {
		if err := assertTestTimeoutEncoded(ctx); err != nil {
			return err
		}
		val := conn.RequestHeader().Get("test")
		if val == "" {
			return next(ctx, conn)
		}
		stream, ok := i.get(val)
		if !ok {
			return fmt.Errorf("invalid testCase header: %s", val)
		}
		stream.start()
		defer func() {
			stream.finish(resultError)
		}()
		if !assert.Equal(stream.T, stream.method, conn.Spec().Procedure) {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("expected %s, got %s", stream.method, conn.Spec().Procedure))
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
				err := conn.Receive(got)
				switch {
				case msg.err != nil:
					// We're expecting an error at this point, which prevented this
					// message from being delivered.
					if err == nil {
						err = errors.New("expecting an error receiving message but got none")
					}
					return err
				case err != nil:
					return err // not expecting an error
				default:
					diff := cmp.Diff(got, msg.msg, protocmp.Transform())
					assert.Empty(stream.T, diff, "message didn't match")
					if diff != "" {
						return fmt.Errorf("message didn't match: %s", diff)
					}
				}
			case *testMsgOut:
				switch {
				case msg.msg != nil && msg.err != nil:
					err := conn.Send(msg.msg)
					if err == nil {
						err = errors.New("expecting an error sending message but got none")
					}
					return err
				case msg.err != nil:
					return msg.err
				default:
					if err := conn.Send(msg.msg); err != nil {
						return err
					}
				}
			default:
				return errors.New("expected message")
			}
		}
		for key, vals := range stream.rspTrailer {
			conn.ResponseTrailer()[key] = vals
		}
		return nil
	}
}

func (i *testInterceptor) restUnaryHandler(
	codec Codec, comp *compressionPool,
) http.HandlerFunc {
	codecNames := map[string]string{
		"application/json": "json",
	}
	handler := func(stream *ttStream, rsp http.ResponseWriter, req *http.Request) error {
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
		assert.Equal(stream.T, stream.method, req.URL.String(), "url didn't match")
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
			if restIsHTTPBody(got.ProtoReflect().Descriptor(), nil) {
				got, _ := got.(*httpbody.HttpBody)
				got.ContentType = contentType
				got.Data = body
			} else {
				codecName := codecNames[contentType]
				if !assert.Equal(stream.T, codec.Name(), codecName, "codec didn't match") {
					return errors.New("codec didn't match")
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
			return nil // ignore
		}

		// Write body.
		rsp.Header().Set("Content-Type", contentType)
		rsp.Header().Set("Content-Encoding", "identity")
		if restIsHTTPBody(out.msg.ProtoReflect().Descriptor(), nil) { //nolint:nestif
			msg, _ := out.msg.(*httpbody.HttpBody)
			rsp.Header().Set("Content-Type", msg.GetContentType())
			_, err = rsp.Write(msg.GetData())
			require.NoError(stream.T, err, "failed to write response")
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
			require.NoError(stream.T, err, "failed to write response")
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
		stream, ok := i.get(val)
		if !ok {
			http.Error(rsp, "invalid test header", http.StatusInternalServerError)
			return
		}
		timeoutStr := req.Header.Get("X-Server-Timeout")
		timeout, err := restDecodeTimeout(timeoutStr)
		if err != nil {
			http.Error(rsp, "invalid timeout header", http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), timeout)
		defer cancel()
		if err := assertTestTimeoutEncoded(ctx); err != nil {
			http.Error(rsp, err.Error(), http.StatusInternalServerError)
			return
		}
		req = req.WithContext(ctx)
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
		return defaultGzipCompressor()
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
		return defaultGzipDecompressor()
	case CompressionIdentity:
		return nil
	default:
		t.Fatalf("unknown compression: %s", name)
		return nil
	}
}

type testServer struct {
	name   string
	server *httptest.Server
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
			// nil factory functions *remove* support for gzip, which is otherwise on by default.
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
	ctx, cancel := context.WithTimeout(ctx, defaultTestTimeout)
	defer cancel()
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
	return resp.Header(), []proto.Message{msg.(proto.Message)}, resp.Trailer(), nil
}

func outputFromServerStream[Req, Resp any](
	ctx context.Context,
	method serverStreamMethod[Req, Resp],
	headers http.Header,
	reqs []proto.Message,
) (http.Header, []proto.Message, http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTestTimeout)
	defer cancel()
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
	ctx, cancel := context.WithTimeout(ctx, defaultTestTimeout)
	defer cancel()
	str := method(ctx)
	for k, v := range headers {
		str.RequestHeader()[k] = v
	}
	for _, msg := range reqs {
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
	return resp.Header(), []proto.Message{msg.(proto.Message)}, resp.Trailer(), nil
}

func outputFromBidiStream[Req, Resp any](
	ctx context.Context,
	method bidiStreamMethod[Req, Resp],
	headers http.Header,
	reqs []proto.Message,
) (http.Header, []proto.Message, http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTestTimeout)
	defer cancel()
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
			msgs = append(msgs, msg.(proto.Message))
		}
	}()

	for k, v := range headers {
		str.RequestHeader()[k] = v
	}
	for _, msg := range reqs {
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
				"Content-Type":  {"application/grpc+" + codec},
				"Grpc-Encoding": allowedCompression,
			}
		case ProtocolGRPCWeb:
			wantHdr = map[string][]string{
				"Content-Type":  {"application/grpc-web+" + codec},
				"Grpc-Encoding": allowedCompression,
			}
		case ProtocolConnect:
			if strings.HasPrefix(req.Header.Get("Content-Type"), "application/connect") {
				wantHdr = map[string][]string{
					"Content-Type":             {"application/connect+" + codec},
					"Connect-Content-Encoding": allowedCompression,
				}
			} else if req.Method == http.MethodPost {
				wantHdr = map[string][]string{
					"Content-Type":     {"application/" + codec},
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
	awaitServer := interceptor.set(t, stream)
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
	if err != nil {
		t.Logf("RPC error: %v", err)
	}
	var expectedErr *connect.Error
	expectServerDone := true
	var expectServerCancel bool
	for _, streamMsg := range stream.msgs {
		if streamMsg.in != nil && streamMsg.in.err != nil {
			expectedErr = streamMsg.in.err
			expectServerDone = false
			expectServerCancel = true
			break
		}
		if streamMsg.out != nil && streamMsg.out.err != nil {
			expectedErr = streamMsg.out.err
			expectServerDone = streamMsg.out.msg == nil
			expectServerCancel = true
			break
		}
	}
	serverInvoked, serverErr := awaitServer(t, expectServerDone)
	// Verify the error received by the client.
	receivedErr := expectedErr
	if stream.err != nil {
		receivedErr = stream.err
	}
	if receivedErr == nil {
		require.NoError(t, err)
	} else {
		require.Equal(t, receivedErr.Code(), connect.CodeOf(err))
	}
	// Also check the error observed by the server.
	if expectedErr == nil {
		require.NoError(t, serverErr)
	} else if serverInvoked {
		require.Error(t, serverErr)
		if expectServerCancel && connect.CodeOf(serverErr) != connect.CodeOf(expectedErr) {
			// We expect the server to either have seen the same error or it later
			// observed a cancel error (since the middleware cancels the request
			// after it aborts the operation).
			assert.Equal(t, connect.CodeCanceled, connect.CodeOf(serverErr))
		} else {
			assert.Equal(t, expectedErr.Code(), connect.CodeOf(serverErr))
		}
	}
	assert.Subset(t, headers, stream.rspHeader)
	if stream.err == nil {
		// if middleware created the error, trailers may not come across
		assert.Subset(t, trailers, stream.rspTrailer)
	}
	var expectedResponses []proto.Message
	for _, streamMsg := range stream.msgs {
		if streamMsg.out != nil && streamMsg.out.msg != nil && streamMsg.out.err == nil {
			expectedResponses = append(expectedResponses, streamMsg.out.msg)
		}
	}
	// If we expect an error from the middleware, the last response
	// may not have been delivered.
	if stream.err != nil && len(responses) < len(expectedResponses) {
		expectedResponses = expectedResponses[:len(expectedResponses)-1]
	}
	require.Len(t, responses, len(expectedResponses))
	for i, msg := range responses {
		want := expectedResponses[i]
		assert.Empty(t, cmp.Diff(want, msg, protocmp.Transform()))
	}
}

func disableCompression(server *httptest.Server) {
	transport, _ := server.Client().Transport.(*http.Transport)
	transport.DisableCompression = true
}

func newConnectError(code connect.Code, msg string) *connect.Error {
	err := connect.NewError(code, errors.New(msg))
	// Meta is initialized lazily. We need to trigger it, via the Meta() accessor,
	// so it doesn't happen in thread-unsafe way later.
	err.Meta()
	return err
}

// assert a 30 second timeout has been set.
func assertTestTimeoutEncoded(ctx context.Context) error {
	now := time.Now()
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("context should have deadline")
	}
	if deadline.After(now.Add(defaultTestTimeout)) {
		return errors.New("context deadline should be 30 seconds")
	}
	// Allow a little bit of slop.
	if deadline.Before(now.Add(defaultTestTimeout - 5*time.Second)) {
		return errors.New("context deadline should be at least 20 seconds")
	}
	return nil
}

type serviceWithNoParentFile struct {
	protoreflect.ServiceDescriptor
}

func (s *serviceWithNoParentFile) ParentFile() protoreflect.FileDescriptor {
	return nil
}

func (s *serviceWithNoParentFile) Parent() protoreflect.Descriptor {
	return nil
}
