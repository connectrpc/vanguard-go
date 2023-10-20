// Copyright 2023 Buf Technologies, Inc.
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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	testDataString           = "abc def ghi"
	testCompressedDataString = "nop qrs tuv" // rot13 of above
)

func TestTranscoder_BufferTooLargeFails(t *testing.T) {
	t.Parallel()

	// Cases where we buffer:
	// 1. Using envelopingReader for request (same codec and compression, no body prep) where
	//    client protocol is not enveloped, request does not include content-length header,
	//    and server protocol is enveloped. In this case, we must buffer the request to measure
	//    its size, so we can create an envelope to send to the server.
	// 2. Similar to above, but reversed roles, using envelopingWriter for response.
	// 3. Using envelopingWriter for response, but with gRPC-Web or Connect streaming protocols,
	//    where we must buffer the final special message.
	// 4. Using transformingReader for request (different codec and/or compression or body prep).
	//    We must buffer request messages in all cases.
	// 5. Similar to above, but reversed roles, using transformingWriter for response. This
	//    includes buffering of final special message for gRPC-Web and Connect streaming protocols.
	// 6. Using errorWriter for response (failed unary RPC in Connect or REST protocol). The
	//    entire body must be buffered to construct the RPC error.

	var interceptor testInterceptor
	serveMux := http.NewServeMux()
	serveMux.Handle(testv1connect.NewLibraryServiceHandler(
		testv1connect.UnimplementedLibraryServiceHandler{},
		connect.WithInterceptors(&interceptor),
	))
	serveMux.Handle(testv1connect.NewContentServiceHandler(
		testv1connect.UnimplementedContentServiceHandler{},
		connect.WithInterceptors(&interceptor),
	))

	type testClients struct {
		contentClient testv1connect.ContentServiceClient
		libClient     testv1connect.LibraryServiceClient
	}
	type testRequest struct {
		name          string
		clientOptions []connect.ClientOption
		svcOpts       []ServiceOption // Does not need to include WithMaxMessageBufferBytes
		invoke        func(testClients, http.Header, []proto.Message) (http.Header, []proto.Message, http.Header, error)
		stream        testStream
	}
	ctx := context.Background()
	testCases := []struct {
		name        string
		expectation func(*testing.T, http.ResponseWriter, *http.Request)
		reqs        []testRequest
	}{
		{
			name: "enveloping_reader",
			expectation: func(t *testing.T, rw http.ResponseWriter, req *http.Request) {
				t.Helper()
				_, ok := req.Body.(*envelopingReader)
				assert.True(t, ok, "request body should be *envelopingReader")
			},
			reqs: []testRequest{
				{
					// Connect unary request; gRPC server
					name:    "must_buffer_request",
					svcOpts: []ServiceOption{WithTargetProtocols(ProtocolGRPC)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromUnary(ctx, clients.libClient.GetBook, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msgs: []testMsg{{in: &testMsgIn{
							msg: &testv1.GetBookRequest{Name: strings.Repeat("foo/", 1000)},
							err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
						}}},
					},
				},
			},
		},
		{
			name: "enveloping_writer",
			expectation: func(t *testing.T, rsp http.ResponseWriter, req *http.Request) {
				t.Helper()
				rw, ok := rsp.(*responseWriter)
				require.True(t, ok, "response writer should be *responseWriter")
				_, ok = rw.w.(*envelopingWriter)
				assert.True(t, ok, "response body should be *envelopingWriter")
			},
			reqs: []testRequest{
				{
					// gRPC unary request; Connect server
					name:          "must_buffer_response",
					clientOptions: []connect.ClientOption{connect.WithGRPC()},
					svcOpts:       []ServiceOption{WithTargetProtocols(ProtocolConnect)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromUnary(ctx, clients.libClient.GetBook, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.GetBookRequest{Name: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.Book{Name: strings.Repeat("foo/", 1000)},
							}},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
				{
					// gRPC-Web response with trailers too large
					name:    "buffer_grpcweb_endstream_trailers",
					svcOpts: []ServiceOption{WithTargetProtocols(ProtocolGRPCWeb)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromUnary(ctx, clients.libClient.GetBook, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.GetBookRequest{Name: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.Book{Name: "foo/bar"},
							}},
						},
						rspTrailer: map[string][]string{
							"Big-Trailer": {strings.Repeat("Blah-", 1000)},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
				{
					// gRPC-Web response with error too large
					name:    "buffer_grpcweb_endstream_error",
					svcOpts: []ServiceOption{WithTargetProtocols(ProtocolGRPCWeb)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromServerStream(ctx, clients.contentClient.Download, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.ContentServiceDownloadProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.DownloadRequest{Filename: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.DownloadResponse{File: &httpbody.HttpBody{ContentType: "foo/bar"}},
							}},
							{out: &testMsgOut{
								err: newConnectError(connect.CodeDataLoss, strings.Repeat("foo/", 1000)),
							}},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
				{
					// Connect streaming response with error too large
					name:          "buffer_connect_endstream_trailers",
					clientOptions: []connect.ClientOption{connect.WithGRPC()},
					svcOpts:       []ServiceOption{WithTargetProtocols(ProtocolConnect)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromServerStream(ctx, clients.contentClient.Download, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.ContentServiceDownloadProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.DownloadRequest{Filename: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.DownloadResponse{File: &httpbody.HttpBody{ContentType: "foo/bar"}},
							}},
						},
						rspTrailer: map[string][]string{
							"Big-Trailer": {strings.Repeat("Blah-", 1000)},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
				{
					// Connect streaming response with error too large
					name:          "buffer_connect_endstream_trailers",
					clientOptions: []connect.ClientOption{connect.WithGRPC()},
					svcOpts:       []ServiceOption{WithTargetProtocols(ProtocolConnect)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromServerStream(ctx, clients.contentClient.Download, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.ContentServiceDownloadProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.DownloadRequest{Filename: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.DownloadResponse{File: &httpbody.HttpBody{ContentType: "foo/bar"}},
							}},
							{out: &testMsgOut{
								err: newConnectError(connect.CodeDataLoss, strings.Repeat("foo/", 1000)),
							}},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
			},
		},
		{
			name: "transforming_reader",
			expectation: func(t *testing.T, rw http.ResponseWriter, req *http.Request) {
				t.Helper()
				_, ok := req.Body.(*transformingReader)
				assert.True(t, ok, "request body should be *transformingReader")
			},
			reqs: []testRequest{
				{
					// Proto request transformed to JSON
					name:    "must_buffer_request_unary",
					svcOpts: []ServiceOption{WithTargetCodecs(CodecJSON)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromUnary(ctx, clients.libClient.GetBook, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msgs: []testMsg{{in: &testMsgIn{
							msg: &testv1.GetBookRequest{Name: strings.Repeat("foo/", 1000)},
							err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
						}}},
					},
				},
				{
					name:    "must_buffer_request_stream",
					svcOpts: []ServiceOption{WithTargetCodecs(CodecJSON)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromClientStream(ctx, clients.contentClient.Upload, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.ContentServiceUploadProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.UploadRequest{Filename: "foo/bar"},
							}},
							{in: &testMsgIn{
								msg: &testv1.UploadRequest{File: &httpbody.HttpBody{Data: bytes.Repeat([]byte{0, 1, 2, 3}, 1000)}},
								err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
							}},
						},
					},
				},
			},
		},
		{
			name: "transforming_writer",
			expectation: func(t *testing.T, rsp http.ResponseWriter, req *http.Request) {
				t.Helper()
				rw, ok := rsp.(*responseWriter)
				require.True(t, ok, "response writer should be *responseWriter")
				_, ok = rw.w.(*transformingWriter)
				assert.True(t, ok, "response body should be *transformingWriter")
			},
			reqs: []testRequest{
				{
					// Proto response transformed to JSON
					name:    "must_buffer_response_unary",
					svcOpts: []ServiceOption{WithTargetCodecs(CodecJSON)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromUnary(ctx, clients.libClient.GetBook, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.GetBookRequest{Name: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.Book{Name: strings.Repeat("foo/", 1000)},
							}},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
				{
					name:    "must_buffer_response_stream",
					svcOpts: []ServiceOption{WithTargetCodecs(CodecJSON)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromServerStream(ctx, clients.contentClient.Download, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.ContentServiceDownloadProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.DownloadRequest{Filename: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.DownloadResponse{File: &httpbody.HttpBody{Data: []byte{0, 1, 2, 3}}},
							}},
							{out: &testMsgOut{
								msg: &testv1.DownloadResponse{File: &httpbody.HttpBody{Data: bytes.Repeat([]byte{0, 1, 2, 3}, 1000)}},
								err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
							}},
						},
					},
				},
				{
					// gRPC-Web response with trailers too large
					name:    "buffer_grpcweb_endstream_trailers",
					svcOpts: []ServiceOption{WithTargetCodecs(CodecJSON), WithTargetProtocols(ProtocolGRPCWeb)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromUnary(ctx, clients.libClient.GetBook, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.GetBookRequest{Name: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.Book{Name: "foo/bar"},
							}},
						},
						rspTrailer: map[string][]string{
							"Big-Trailer": {strings.Repeat("Blah-", 1000)},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
				{
					// gRPC-Web response with error too large
					name:    "buffer_grpcweb_endstream_error",
					svcOpts: []ServiceOption{WithTargetCodecs(CodecJSON), WithTargetProtocols(ProtocolGRPCWeb)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromServerStream(ctx, clients.contentClient.Download, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.ContentServiceDownloadProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.DownloadRequest{Filename: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.DownloadResponse{File: &httpbody.HttpBody{ContentType: "foo/bar"}},
							}},
							{out: &testMsgOut{
								err: newConnectError(connect.CodeDataLoss, strings.Repeat("foo/", 1000)),
							}},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
				{
					// Connect streaming response with error too large
					name:    "buffer_connect_endstream_trailers",
					svcOpts: []ServiceOption{WithTargetCodecs(CodecJSON), WithTargetProtocols(ProtocolConnect)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromServerStream(ctx, clients.contentClient.Download, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.ContentServiceDownloadProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.DownloadRequest{Filename: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.DownloadResponse{File: &httpbody.HttpBody{ContentType: "foo/bar"}},
							}},
						},
						rspTrailer: map[string][]string{
							"Big-Trailer": {strings.Repeat("Blah-", 1000)},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
				{
					// Connect streaming response with error too large
					name:    "buffer_connect_endstream_trailers",
					svcOpts: []ServiceOption{WithTargetCodecs(CodecJSON), WithTargetProtocols(ProtocolConnect)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromServerStream(ctx, clients.contentClient.Download, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.ContentServiceDownloadProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.DownloadRequest{Filename: "foo/bar"},
							}},
							{out: &testMsgOut{
								msg: &testv1.DownloadResponse{File: &httpbody.HttpBody{ContentType: "foo/bar"}},
							}},
							{out: &testMsgOut{
								err: newConnectError(connect.CodeDataLoss, strings.Repeat("foo/", 1000)),
							}},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
			},
		},
		{
			name: "error_writer",
			expectation: func(t *testing.T, rsp http.ResponseWriter, req *http.Request) {
				t.Helper()
				rw, ok := rsp.(*responseWriter)
				require.True(t, ok, "response writer should be *responseWriter")
				_, ok = rw.w.(*errorWriter)
				assert.True(t, ok, "response body should be *errorWriter")
			},
			reqs: []testRequest{
				{
					// gRPC request; Connect unary response with error
					name:          "must_buffer_error_response",
					clientOptions: []connect.ClientOption{connect.WithGRPC()},
					svcOpts:       []ServiceOption{WithTargetProtocols(ProtocolConnect)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromUnary(ctx, clients.libClient.GetBook, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msgs: []testMsg{
							{in: &testMsgIn{
								msg: &testv1.GetBookRequest{Name: "foo/bar"},
							}},
							{out: &testMsgOut{
								err: newConnectError(connect.CodeDataLoss, strings.Repeat("foo/", 1000)),
							}},
						},
						err: newConnectError(connect.CodeResourceExhausted, "buffer limit exceeded"),
					},
				},
			},
		},
	}
	muxTestModes := []struct {
		name          string
		optsOnService bool
		makeMux       func(*testRequest, []*Service) (http.Handler, error)
	}{
		{
			name:          "default_svc_opts",
			optsOnService: false,
			makeMux: func(req *testRequest, svcs []*Service) (http.Handler, error) {
				opts := append(make([]ServiceOption, 0, len(req.svcOpts)+1), req.svcOpts...)
				opts = append(opts, WithMaxMessageBufferBytes(1024))
				return NewTranscoder(svcs, WithDefaultServiceOptions(opts...))
			},
		},
		{
			name:          "per_svc_options",
			optsOnService: true,
			makeMux: func(req *testRequest, svcs []*Service) (http.Handler, error) {
				// svcs already have options defined on them
				return NewTranscoder(svcs, WithDefaultServiceOptions(WithMaxMessageBufferBytes(1024)))
			},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			for i := range testCase.reqs {
				testReq := &testCase.reqs[i]
				t.Run(testReq.name, func(t *testing.T) {
					t.Parallel()
					for _, mode := range muxTestModes {
						mode := mode
						t.Run(mode.name, func(t *testing.T) {
							t.Parallel()

							var expectationChecked atomic.Bool
							rpcHandler := http.HandlerFunc(func(respWriter http.ResponseWriter, req *http.Request) {
								serveMux.ServeHTTP(respWriter, req)
								defer expectationChecked.Store(true)
								testCase.expectation(t, respWriter, req)
							})

							var svcOpts []ServiceOption
							if mode.optsOnService {
								svcOpts = testReq.svcOpts
							}
							handler, err := mode.makeMux(testReq, []*Service{
								NewService(testv1connect.LibraryServiceName, rpcHandler, svcOpts...),
								NewService(testv1connect.ContentServiceName, rpcHandler, svcOpts...),
							})
							require.NoError(t, err)
							server := httptest.NewUnstartedServer(handler)
							server.EnableHTTP2 = true
							server.StartTLS()
							disableCompression(server)
							t.Cleanup(server.Close)

							var clients testClients
							// remove support for gzip, so we don't have to worry about compression
							// getting in the way of our too-large test payloads
							opts := make([]connect.ClientOption, 0, len(testReq.clientOptions)+1)
							opts = append(opts, testReq.clientOptions...)
							opts = append(opts, connect.WithAcceptCompression("gzip", nil, nil))
							clients.libClient = testv1connect.NewLibraryServiceClient(server.Client(), server.URL, opts...)
							clients.contentClient = testv1connect.NewContentServiceClient(server.Client(), server.URL, opts...)

							runRPCTestCase(t, &interceptor, clients, testReq.invoke, testReq.stream)
							assert.True(t, expectationChecked.Load())
						})
					}
				})
			}
		})
	}
}

func TestTranscoder_ConnectGetUsesPostIfRequestTooLarge(t *testing.T) {
	t.Parallel()

	var interceptor testInterceptor
	_, svcHandler := testv1connect.NewLibraryServiceHandler(
		testv1connect.UnimplementedLibraryServiceHandler{},
		connect.WithInterceptors(
			connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
				return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
					if req.HTTPMethod() != http.MethodPost {
						return nil, fmt.Errorf("server should only see POST; instead got %s", req.HTTPMethod())
					}
					return next(ctx, req)
				}
			}),
			&interceptor,
		),
	)

	handlerWithDefaultSvcOpt, err := NewTranscoder([]*Service{NewService(
		testv1connect.LibraryServiceName,
		svcHandler,
		WithMaxGetURLBytes(512),
		WithNoTargetCompression(),
	)})
	require.NoError(t, err)
	serverWithDefaultSvcOpt := httptest.NewServer(handlerWithDefaultSvcOpt)
	disableCompression(serverWithDefaultSvcOpt)
	t.Cleanup(serverWithDefaultSvcOpt.Close)

	handlerWithPerSvcOpt, err := NewTranscoder([]*Service{
		NewService(
			testv1connect.LibraryServiceName, svcHandler,
			WithMaxGetURLBytes(512),
			WithNoTargetCompression(),
		),
	})
	require.NoError(t, err)
	serverWithPerSvcOpt := httptest.NewServer(handlerWithPerSvcOpt)
	disableCompression(serverWithPerSvcOpt)
	t.Cleanup(serverWithPerSvcOpt.Close)

	testCases := []struct {
		name   string
		server *httptest.Server
	}{
		{
			name:   "with_default_svc_option",
			server: serverWithDefaultSvcOpt,
		},
		{
			name:   "with_per_svc_option",
			server: serverWithPerSvcOpt,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			largeRequest := &testv1.GetBookRequest{Name: strings.Repeat("foo/", 300) + "1"}
			interceptor.set(t, testStream{
				method: testv1connect.LibraryServiceGetBookProcedure,
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: largeRequest,
					}},
					{out: &testMsgOut{
						msg: &testv1.Book{Name: strings.Repeat("foo/", 300) + "1"},
					}},
				},
			})
			defer interceptor.del(t)

			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()

			client := testv1connect.NewLibraryServiceClient(
				testCase.server.Client(),
				testCase.server.URL,
				connect.WithHTTPGet(),
				connect.WithHTTPGetMaxURLSize(512, false),
				connect.WithSendGzip(),
			)
			req := connect.NewRequest(largeRequest)
			req.Header().Set("Test", t.Name()) // must set this for interceptor to work
			_, err := client.GetBook(ctx, req)
			// No error means it made through above interceptor unscathed
			// (so server handler got a POST).
			require.NoError(t, err)
			// But the client should have sent a GET, and the middleware should
			// have changed to POST because the request URL was too large.
			assert.Equal(t, http.MethodGet, req.HTTPMethod())

			// Sanity check that an RPC with a small request fails due to the above interceptor requiring POST
			// (Just to confirm that the above function is indeed intercepting the request).
			//
			// We don't need to reset the stream for the test interceptor to match the small request
			// because that interceptor won't see it. The other interceptor function should fail the
			// request before it gets that far.
			req = connect.NewRequest(&testv1.GetBookRequest{Name: "foo/bar"})
			req.Header().Set("Test", t.Name()) // must set this for interceptor to work
			_, err = client.GetBook(ctx, req)
			require.ErrorContains(t, err, "server should only see POST; instead got GET")
		})
	}
}

func TestTranscoder_Errors(t *testing.T) {
	t.Parallel()
	// These tests exercise error-handling in the way the operation is initialized.
	// These tests should not reach the underlying handler or any particular protocol
	// handler implementation (other than extracting request metadata).

	rpcHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	})
	services := []*Service{
		NewService(testv1connect.LibraryServiceName, rpcHandler),
		NewService(testv1connect.ContentServiceName, rpcHandler),
	}
	handler, err := NewTranscoder(services)
	require.NoError(t, err)
	unknownHandler, err := NewTranscoder(services, WithUnknownHandler(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusFailedDependency)
		}),
	))
	require.NoError(t, err)

	testCases := []struct {
		name                    string
		handler                 *Transcoder // use handler if nil
		useHTTP1                bool
		requestURL              string
		requestMethod           string
		requestHeaders          map[string][]string
		expectedCode            int
		expectedResponseHeaders map[string]string
	}{
		{
			name:          "multiple content types",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "GET",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/proto", "application/json"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "no content type, looks like connect from header",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "DELETE",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "no content type, looks like connect from query string",
			requestURL:    "/service.Foo/Bar?connect=v1",
			requestMethod: "DELETE",
			expectedCode:  http.StatusUnsupportedMediaType,
		},
		{
			name:          "rest, route not found",
			requestURL:    "/foo/bar/baz:buzz",
			requestMethod: "PUT",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/json"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "connect stream, method not found",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "connect post, method not found",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "connect get, method not found",
			requestURL:    "/service.Foo/Bar?connect=v1",
			requestMethod: "GET",
			expectedCode:  http.StatusNotFound,
		},
		{
			name:          "grpc, method not found",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+proto"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "grpc-web, method not found",
			requestURL:    "/service.Foo/Bar",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+proto"},
			},
			expectedCode: http.StatusNotFound,
		},
		{
			name:          "connect get, method not idempotent",
			requestURL:    "/vanguard.test.v1.LibraryService/CreateBook?connect=v1",
			requestMethod: "GET",
			expectedCode:  http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "connect unary, bad HTTP method",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "DELETE",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
			},
			expectedCode: http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "connect stream, bad HTTP method",
			requestURL:    "/vanguard.test.v1.ContentService/Download",
			requestMethod: "GET",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "grpc, bad HTTP method",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "PUT",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+proto"},
			},
			expectedCode: http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "grpc-web, bad HTTP method",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "PATCH",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+proto"},
			},
			expectedCode: http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "rest, unknown codec",
			requestURL:    "/v1/shelves/reference-123/books/isbn-0000111230012",
			requestMethod: "GET",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/foo"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect stream, unknown codec",
			requestURL:    "/vanguard.test.v1.ContentService/Download",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect post, unknown codec",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect get, unknown codec",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook?connect=v1&encoding=text",
			requestMethod: "GET",
			expectedCode:  http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc, unknown codec",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc-web, unknown codec",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "rest, unknown compression",
			requestURL:    "/v1/shelves/reference-123/books/isbn-0000111230012",
			requestMethod: "GET",
			requestHeaders: map[string][]string{
				"Content-Type":     {"application/json"},
				"Content-Encoding": {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect stream, unknown compression",
			requestURL:    "/vanguard.test.v1.ContentService/Download",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":             {"application/connect+proto"},
				"Connect-Content-Encoding": {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect post, unknown compression",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
				"Content-Encoding":         {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect get, unknown compression",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook?connect=v1&encoding=proto&compression=blah",
			requestMethod: "GET",
			expectedCode:  http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc, unknown compression",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":  {"application/grpc+proto"},
				"Grpc-Encoding": {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc-web, unknown compression",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":  {"application/grpc-web+proto"},
				"Grpc-Encoding": {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect stream, bidi and http 1.1",
			useHTTP1:      true,
			requestURL:    "/vanguard.test.v1.ContentService/Subscribe",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusHTTPVersionNotSupported,
		},
		{
			name:          "grpc, bidi and http 1.1",
			useHTTP1:      true,
			requestURL:    "/vanguard.test.v1.ContentService/Subscribe",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+proto"},
			},
			expectedCode: http.StatusHTTPVersionNotSupported,
		},
		{
			name:          "grpc-web, bidi and http 1.1",
			useHTTP1:      true,
			requestURL:    "/vanguard.test.v1.ContentService/Subscribe",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+proto"},
			},
			expectedCode: http.StatusHTTPVersionNotSupported,
		},
		{
			name:          "connect post, stream method",
			requestURL:    "/vanguard.test.v1.ContentService/Download",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect stream, unary method",
			requestURL:    "/vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "unknown handler",
			handler:       unknownHandler,
			requestURL:    "/vanguard.test.v1.LibraryService/UnknownMethod",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusFailedDependency,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			vanguardHandler := handler
			if testCase.handler != nil {
				vanguardHandler = testCase.handler
			}
			req := httptest.NewRequest(testCase.requestMethod, testCase.requestURL, http.NoBody)
			if testCase.useHTTP1 {
				req.Proto = "HTTP/1.1"
				req.ProtoMajor, req.ProtoMinor = 1, 1
			} else {
				req.Proto = "HTTP/2"
				req.ProtoMajor, req.ProtoMinor = 2, 0
			}
			for k, vals := range testCase.requestHeaders {
				for _, v := range vals {
					req.Header.Add(k, v)
				}
			}
			respWriter := httptest.NewRecorder()
			vanguardHandler.ServeHTTP(respWriter, req)
			resp := respWriter.Result()
			err := resp.Body.Close()
			require.NoError(t, err)
			require.Equal(t, testCase.expectedCode, resp.StatusCode)
			for k, v := range testCase.expectedResponseHeaders {
				require.Equal(t, v, resp.Header.Get(k))
			}
		})
	}
}

func TestTranscoder_PassThrough(t *testing.T) {
	t.Parallel()
	// These cases don't do any transformation and just pass through to the
	// underlying handler.

	var interceptor testInterceptor
	checkPassThrough := func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(respWriter http.ResponseWriter, request *http.Request) {
			// Get a *testing.T for this request so we can attribute error to correct case.
			testName := request.Header.Get("Test")
			if testName == "" {
				http.Error(respWriter, "request did not include test case ID", http.StatusBadRequest)
				return
			}
			t, ok := interceptor.get(testName)
			if !ok {
				http.Error(respWriter, fmt.Sprintf("test case name %q not found", testName), http.StatusBadRequest)
				return
			}

			_, isWrapped := respWriter.(*responseWriter)
			require.False(t, isWrapped)
			_, isWrapped = request.Body.(*envelopingReader)
			require.False(t, isWrapped)
			_, isWrapped = request.Body.(*transformingReader)
			require.False(t, isWrapped)

			// carry on...
			handler.ServeHTTP(respWriter, request)
		})
	}
	contentPath, contentHandler := testv1connect.NewContentServiceHandler(
		testv1connect.UnimplementedContentServiceHandler{},
		connect.WithInterceptors(&interceptor),
	)
	handler, err := NewTranscoder([]*Service{NewService(contentPath, checkPassThrough(contentHandler))})
	require.NoError(t, err)

	// Use HTTP/2 so we can test a bidi stream.
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	ctx := context.Background()

	type connectClientCase struct {
		name string
		opts []connect.ClientOption
	}
	compressionOptions := []connectClientCase{
		{
			name: "identity",
		},
		{
			name: "gzip",
			opts: []connect.ClientOption{connect.WithSendCompression(CompressionGzip)},
		},
	}
	encodingOptions := []connectClientCase{
		{
			name: "proto",
		},
		{
			name: "json",
			opts: []connect.ClientOption{connect.WithProtoJSON()},
		},
	}
	protocolOptions := []connectClientCase{
		{
			name: "connect",
		},
		{
			name: "grpc",
			opts: []connect.ClientOption{connect.WithGRPC()},
		},
		{
			name: "grpc-web",
			opts: []connect.ClientOption{connect.WithGRPCWeb()},
		},
	}
	testRequests := []struct {
		name   string
		invoke func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error)
		stream testStream
	}{
		{
			name: "unary success",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromUnary(ctx, client.Index, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.ContentServiceIndexProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.IndexRequest{Page: "abcdef"},
					}},
					{out: &testMsgOut{
						msg: &httpbody.HttpBody{
							ContentType: "text/html",
							Data:        ([]byte)(`<html><title>Foo</title><body><h1>Foo</h1></html>`),
						},
					}},
				},
				rspTrailer: http.Header{"Trailer-Val": []string{"end"}},
			},
		},
		{
			name: "unary fail",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromUnary(ctx, client.Index, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.ContentServiceIndexProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.IndexRequest{Page: "xyz"},
					}},
					{out: &testMsgOut{
						err: newConnectError(connect.CodeResourceExhausted, "foobar"),
					}},
				},
			},
		},
		{
			name: "client stream success",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromClientStream(ctx, client.Upload, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.ContentServiceUploadProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.UploadRequest{Filename: "xyz"},
					}},
					{in: &testMsgIn{
						msg: &testv1.UploadRequest{Filename: "xyz"},
					}},
					{out: &testMsgOut{
						msg: &emptypb.Empty{},
					}},
				},
				rspTrailer: http.Header{"Trailer-Val": []string{"end"}},
			},
		},
		{
			name: "client stream fail",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromClientStream(ctx, client.Upload, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.ContentServiceUploadProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.UploadRequest{Filename: "xyz"},
					}},
					{in: &testMsgIn{
						msg: &testv1.UploadRequest{Filename: "xyz"},
					}},
					{out: &testMsgOut{
						err: newConnectError(connect.CodeAborted, "foobar"),
					}},
				},
			},
		},
		{
			name: "server stream success",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromServerStream(ctx, client.Download, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.ContentServiceDownloadProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.DownloadRequest{Filename: "xyz"},
					}},
					{out: &testMsgOut{
						msg: &testv1.DownloadResponse{
							File: &httpbody.HttpBody{
								ContentType: "application/octet-stream",
								Data:        ([]byte)("abcdef"),
							},
						},
					}},
					{out: &testMsgOut{
						msg: &testv1.DownloadResponse{
							File: &httpbody.HttpBody{
								ContentType: "application/octet-stream",
								Data:        ([]byte)("abcdef"),
							},
						},
					}},
					{out: &testMsgOut{
						msg: &testv1.DownloadResponse{
							File: &httpbody.HttpBody{
								ContentType: "application/octet-stream",
								Data:        ([]byte)("abcdef"),
							},
						},
					}},
				},
				rspTrailer: http.Header{"Trailer-Val": []string{"end"}},
			},
		},
		{
			name: "server stream fail",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromServerStream(ctx, client.Download, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.ContentServiceDownloadProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.DownloadRequest{Filename: "xyz"},
					}},
					{out: &testMsgOut{
						msg: &testv1.DownloadResponse{
							File: &httpbody.HttpBody{
								ContentType: "application/octet-stream",
								Data:        ([]byte)("abcdef"),
							},
						},
					}},
					{out: &testMsgOut{
						err: newConnectError(connect.CodeDataLoss, "foobar"),
					}},
				},
			},
		},
		{
			name: "bidi stream success",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromBidiStream(ctx, client.Subscribe, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.ContentServiceSubscribeProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.SubscribeRequest{FilenamePatterns: []string{"xyz.*", "abc*.jpg"}},
					}},
					{out: &testMsgOut{
						msg: &testv1.SubscribeResponse{FilenameChanged: "xyz1.foo"},
					}},
					{out: &testMsgOut{
						msg: &testv1.SubscribeResponse{FilenameChanged: "xyz2.foo"},
					}},
					{in: &testMsgIn{
						msg: &testv1.SubscribeRequest{FilenamePatterns: []string{"test.test"}},
					}},
					{out: &testMsgOut{
						msg: &testv1.SubscribeResponse{FilenameChanged: "test.test"},
					}},
				},
				rspTrailer: http.Header{"Trailer-Val": []string{"end"}},
			},
		},
		{
			name: "bidi stream fail",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromBidiStream(ctx, client.Subscribe, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.ContentServiceSubscribeProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.SubscribeRequest{FilenamePatterns: []string{"xyz.*", "abc*.jpg"}},
					}},
					{out: &testMsgOut{
						msg: &testv1.SubscribeResponse{FilenameChanged: "xyz1.foo"},
					}},
					{out: &testMsgOut{
						err: newConnectError(connect.CodePermissionDenied, "foobar"),
					}},
				},
			},
		},
	}

	for _, protocolCase := range protocolOptions {
		protocolCase := protocolCase
		t.Run(protocolCase.name, func(t *testing.T) {
			t.Parallel()
			for _, encodingCase := range encodingOptions {
				encodingCase := encodingCase
				t.Run(encodingCase.name, func(t *testing.T) {
					t.Parallel()
					for _, compressionCase := range compressionOptions {
						compressionCase := compressionCase
						t.Run(compressionCase.name, func(t *testing.T) {
							t.Parallel()
							for _, testReq := range testRequests {
								testReq := testReq
								t.Run(testReq.name, func(t *testing.T) {
									t.Parallel()

									clientOptions := make([]connect.ClientOption, 0, 4)
									clientOptions = append(clientOptions, protocolCase.opts...)
									clientOptions = append(clientOptions, encodingCase.opts...)
									clientOptions = append(clientOptions, compressionCase.opts...)
									client := testv1connect.NewContentServiceClient(server.Client(), server.URL, clientOptions...)

									runRPCTestCase(t, &interceptor, client, testReq.invoke, testReq.stream)
								})
							}
						})
					}
				})
			}
		})
	}
}

func TestMessage_AdvanceStage(t *testing.T) {
	t.Parallel()
	// Tests the state machine for message.

	type testEnviron struct {
		abcCodec, xyzCodec                               *fakeCodec
		abcCompression, xyzCompression, otherCompression *fakeCompression
		op                                               *operation
	}
	newTestEnviron := func(isRequest bool) *testEnviron {
		abcCodec := &fakeCodec{name: "abc"}
		xyzCodec := &fakeCodec{name: "xyz"}
		abcCompression := &fakeCompression{name: "abc"}
		xyzCompression := &fakeCompression{name: "xyz"}
		otherCompression := &fakeCompression{name: "other"}
		var clientCodec, serverCodec Codec
		var clientReqComp, serverReqComp, respComp *compressionPool
		if isRequest {
			clientCodec = abcCodec
			serverCodec = xyzCodec
			clientReqComp = abcCompression.newPool()
			serverReqComp = xyzCompression.newPool()
			respComp = otherCompression.newPool()
		} else {
			clientCodec = xyzCodec
			serverCodec = abcCodec
			clientReqComp = xyzCompression.newPool()
			serverReqComp = xyzCompression.newPool()
			respComp = abcCompression.newPool()
		}
		op := &operation{
			bufferPool: &bufferPool{},
			client: clientProtocolDetails{
				codec:           clientCodec,
				reqCompression:  clientReqComp,
				respCompression: respComp,
			},
			server: serverProtocolDetails{
				codec:           serverCodec,
				reqCompression:  serverReqComp,
				respCompression: respComp,
			},
		}
		return &testEnviron{
			abcCodec:         abcCodec,
			xyzCodec:         xyzCodec,
			abcCompression:   abcCompression,
			xyzCompression:   xyzCompression,
			otherCompression: otherCompression,
			op:               op,
		}
	}
	resetEnv := func(env *testEnviron) {
		env.abcCodec.marshalCalls = 0
		env.abcCodec.unmarshalCalls = 0
		env.xyzCodec.marshalCalls = 0
		env.xyzCodec.unmarshalCalls = 0
		env.abcCompression.compressorCalls = 0
		env.abcCompression.decompressorCalls = 0
		env.xyzCompression.compressorCalls = 0
		env.xyzCompression.decompressorCalls = 0
		env.otherCompression.compressorCalls = 0
		env.otherCompression.decompressorCalls = 0
	}
	type expectedCounts struct {
		abcMarshalCalls    int
		abcUnmarshalCalls  int
		xyzMarshalCalls    int
		xyzUnmarshalCalls  int
		abcCompressCalls   int
		abcDecompressCalls int
		xyzCompressCalls   int
		xyzDecompressCalls int
	}
	checkCounts := func(t *testing.T, isRequest bool, env *testEnviron, counts expectedCounts) {
		t.Helper()
		if !isRequest {
			// for responses, compression for both client and server is the same (abc)
			counts.abcCompressCalls += counts.xyzCompressCalls
			counts.abcDecompressCalls += counts.xyzDecompressCalls
			counts.xyzCompressCalls = 0
			counts.xyzDecompressCalls = 0
		}
		assert.Equal(t, counts.abcMarshalCalls, env.abcCodec.marshalCalls)
		assert.Equal(t, counts.abcUnmarshalCalls, env.abcCodec.unmarshalCalls)
		assert.Equal(t, counts.xyzMarshalCalls, env.xyzCodec.marshalCalls)
		assert.Equal(t, counts.xyzUnmarshalCalls, env.xyzCodec.unmarshalCalls)
		assert.Equal(t, counts.abcCompressCalls, env.abcCompression.compressorCalls)
		assert.Equal(t, counts.abcDecompressCalls, env.abcCompression.decompressorCalls)
		assert.Equal(t, counts.xyzCompressCalls, env.xyzCompression.compressorCalls)
		assert.Equal(t, counts.xyzDecompressCalls, env.xyzCompression.decompressorCalls)
		assert.Zero(t, env.otherCompression.compressorCalls)
		assert.Zero(t, env.otherCompression.decompressorCalls)
	}

	testCases := []struct {
		name                      string
		createMessage             func() *message
		decodedToSend             expectedCounts
		decodedToSendIfCompressed *expectedCounts
		readToSend                expectedCounts
		readToSendIfCompressed    *expectedCounts
	}{
		{
			name:          "same codec, same compression",
			createMessage: func() *message { return &message{sameCodec: true, sameCompression: true} },
			// no calls necessary since client payload can be re-used
			decodedToSend: expectedCounts{},
			readToSend:    expectedCounts{},
		},
		{
			name:          "same codec, different compression",
			createMessage: func() *message { return &message{sameCodec: true} },
			// no calls necessary for uncompressed since payload can be re-used,
			// but we have to decompress/recompress for compressed payloads
			decodedToSend: expectedCounts{},
			decodedToSendIfCompressed: &expectedCounts{
				xyzCompressCalls: 1,
			},
			readToSend: expectedCounts{},
			readToSendIfCompressed: &expectedCounts{
				abcDecompressCalls: 1,
				xyzCompressCalls:   1,
			},
		},
		{
			name:          "different codec",
			createMessage: func() *message { return &message{} },
			// we must re-encode and re-compress
			decodedToSend: expectedCounts{
				xyzMarshalCalls: 1,
			},
			decodedToSendIfCompressed: &expectedCounts{
				xyzMarshalCalls:  1,
				xyzCompressCalls: 1,
			},
			readToSend: expectedCounts{
				abcUnmarshalCalls: 1,
				xyzMarshalCalls:   1,
			},
			readToSendIfCompressed: &expectedCounts{
				abcDecompressCalls: 1,
				abcUnmarshalCalls:  1,
				xyzMarshalCalls:    1,
				xyzCompressCalls:   1,
			},
		},
	}

	for _, compressed := range []bool{true, false} {
		compressed := compressed
		t.Run(fmt.Sprintf("compressed:%v", compressed), func(t *testing.T) {
			t.Parallel()
			for _, isRequest := range []bool{true, false} {
				isRequest := isRequest
				t.Run(fmt.Sprintf("request:%v", isRequest), func(t *testing.T) {
					t.Parallel()
					for _, testCase := range testCases {
						testCase := testCase
						t.Run(testCase.name, func(t *testing.T) {
							t.Parallel()

							originalData := testDataString
							if compressed {
								originalData = testCompressedDataString
							}

							env := newTestEnviron(isRequest)
							msg := testCase.createMessage()
							msg.msg = &wrapperspb.StringValue{}
							buffer := msg.reset(env.op.bufferPool, isRequest, compressed)
							checkStageEmpty(t, msg, compressed)

							buffer.WriteString(originalData)
							msg.stage = stageRead
							checkStageRead(t, msg, compressed)

							err := msg.advanceToStage(env.op, stageDecoded)
							require.NoError(t, err)
							// read -> decoded must always decode (and possibly first decompress)
							counts := expectedCounts{
								abcUnmarshalCalls: 1,
							}
							if compressed {
								counts.abcDecompressCalls = 1
							}
							checkCounts(t, isRequest, env, counts)
							checkStageDecoded(t, msg)

							resetEnv(env)
							err = msg.advanceToStage(env.op, stageSend)
							require.NoError(t, err)
							counts = testCase.decodedToSend
							if compressed && testCase.decodedToSendIfCompressed != nil {
								counts = *testCase.decodedToSendIfCompressed
							}
							checkCounts(t, isRequest, env, counts)
							checkStageSend(t, msg, compressed)

							// Re-create message and this time go directly from read to send
							msg = testCase.createMessage()
							msg.msg = &wrapperspb.StringValue{}
							buffer = msg.reset(env.op.bufferPool, isRequest, compressed)
							buffer.WriteString(originalData)
							msg.stage = stageRead

							resetEnv(env)
							err = msg.advanceToStage(env.op, stageSend)
							require.NoError(t, err)
							counts = testCase.readToSend
							if compressed && testCase.readToSendIfCompressed != nil {
								counts = *testCase.readToSendIfCompressed
							}
							checkCounts(t, isRequest, env, counts)
							checkStageSend(t, msg, compressed)
						})
					}
				})
			}
		})
	}
}

func checkStageEmpty(t *testing.T, msg *message, compressed bool) {
	t.Helper()
	require.Equal(t, stageEmpty, msg.stage)
	if compressed {
		require.NotNil(t, msg.compressed)
		require.Zero(t, msg.compressed.Len())
		require.Nil(t, msg.data)
	} else {
		require.Nil(t, msg.compressed)
		require.NotNil(t, msg.data)
		require.Zero(t, msg.data.Len())
	}
	// Should not be possible to advance from empty.
	require.Error(t, msg.advanceToStage(nil, stageRead))
	require.Error(t, msg.advanceToStage(nil, stageDecoded))
	require.Error(t, msg.advanceToStage(nil, stageSend))
}

func checkStageRead(t *testing.T, msg *message, compressed bool) {
	t.Helper()
	require.Equal(t, stageRead, msg.stage)
	if compressed {
		require.NotNil(t, msg.compressed)
		require.Equal(t, testCompressedDataString, msg.compressed.String())
		require.Nil(t, msg.data)
	} else {
		require.Nil(t, msg.compressed)
		require.NotNil(t, msg.data)
		require.Equal(t, testDataString, msg.data.String())
	}
	// Should not be possible to go backwards.
	require.Error(t, msg.advanceToStage(nil, stageEmpty))
}

func checkStageDecoded(t *testing.T, msg *message) {
	t.Helper()
	require.Equal(t, stageDecoded, msg.stage)
	require.Equal(t, testDataString, msg.msg.(*wrapperspb.StringValue).Value)
	// Should not be possible to go backwards.
	require.Error(t, msg.advanceToStage(nil, stageRead))
	require.Error(t, msg.advanceToStage(nil, stageEmpty))
}

func checkStageSend(t *testing.T, msg *message, compressed bool) {
	t.Helper()
	if compressed {
		require.NotNil(t, msg.compressed)
		require.Equal(t, testCompressedDataString, msg.compressed.String())
		// can't assert anything about m.data: if we didn't have to do
		// anything to get to send (same codec, same compression), we
		// won't have done anything to it; but if we had to re-encode
		// and re-compress, it would get released and set to nil
	} else {
		require.Nil(t, msg.compressed)
		require.NotNil(t, msg.data)
		require.Equal(t, testDataString, msg.data.String())
	}
	require.Equal(t, stageSend, msg.stage)
	// Should not be possible to go backwards.
	require.Error(t, msg.advanceToStage(nil, stageDecoded))
	require.Error(t, msg.advanceToStage(nil, stageRead))
	require.Error(t, msg.advanceToStage(nil, stageEmpty))
}

type fakeCodec struct {
	name                         string
	marshalCalls, unmarshalCalls int
}

func (f *fakeCodec) Name() string {
	return f.name
}

func (f *fakeCodec) MarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	f.marshalCalls++
	val := msg.(*wrapperspb.StringValue).Value
	return append(b, ([]byte)(val)...), nil
}

func (f *fakeCodec) Unmarshal(b []byte, msg proto.Message) error {
	f.unmarshalCalls++
	msg.(*wrapperspb.StringValue).Value = string(b)
	return nil
}

type fakeCompression struct {
	name                               string
	compressorCalls, decompressorCalls int
	reader                             io.Reader
	writer                             io.Writer
}

func (f *fakeCompression) newPool() *compressionPool {
	return newCompressionPool(
		f.name,
		func() connect.Compressor {
			return (*fakeCompressor)(f)
		},
		func() connect.Decompressor {
			return (*fakeDecompressor)(f)
		},
	)
}

type fakeCompressor fakeCompression

func (f *fakeCompressor) Write(p []byte) (n int, err error) {
	rot13(p)
	return f.writer.Write(p)
}

func (f *fakeCompressor) Close() error {
	return nil
}

func (f *fakeCompressor) Reset(writer io.Writer) {
	(*fakeCompression)(f).compressorCalls++
	f.writer = writer
}

type fakeDecompressor fakeCompression

func (f *fakeDecompressor) Read(p []byte) (n int, err error) {
	n, err = f.reader.Read(p)
	rot13(p[:n])
	return n, err
}

func (f *fakeDecompressor) Close() error {
	return nil
}

func (f *fakeDecompressor) Reset(reader io.Reader) error {
	(*fakeCompression)(f).decompressorCalls++
	f.reader = reader
	return nil
}

func rot13(data []byte) {
	for index, char := range data {
		if char >= 'A' && char <= 'Z' {
			char += 13
			if char > 'Z' {
				char -= 26
			}
		} else if char >= 'a' && char <= 'z' {
			char += 13
			if char > 'z' {
				char -= 26
			}
		}
		data[index] = char
	}
}
