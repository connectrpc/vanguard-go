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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/bufbuild/vanguard-go/internal/gen/buf/vanguard/test/v1"
	"github.com/bufbuild/vanguard-go/internal/gen/buf/vanguard/test/v1/testv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"
)

//nolint:dupl // some of these testStream literals are the same as in other cases, but we don't need to share
func TestMux_BufferTooLargeFails(t *testing.T) {
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
		name            string
		clientOptions   []connect.ClientOption
		muxWithSettings *Mux            // Does not need to configure MaxMessageBufferSize
		muxSvcOpts      []ServiceOption // Does not need to include WithMaxMessageBufferBytes
		invoke          func(testClients, http.Header, []proto.Message) (http.Header, []proto.Message, http.Header, error)
		stream          testStream
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
					name:            "must_buffer_request",
					muxWithSettings: &Mux{Protocols: []Protocol{ProtocolGRPC}},
					muxSvcOpts:      []ServiceOption{WithProtocols(ProtocolGRPC)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromUnary(ctx, clients.libClient.GetBook, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msgs: []testMsg{{in: &testMsgIn{
							msg: &testv1.GetBookRequest{Name: strings.Repeat("foo/", 1000)},
							err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
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
					name:            "must_buffer_response",
					clientOptions:   []connect.ClientOption{connect.WithGRPC()},
					muxWithSettings: &Mux{Protocols: []Protocol{ProtocolConnect}},
					muxSvcOpts:      []ServiceOption{WithProtocols(ProtocolConnect)},
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
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
					},
				},
				{
					// gRPC-Web response with trailers too large
					name:            "buffer_grpcweb_endstream_trailers",
					muxWithSettings: &Mux{Protocols: []Protocol{ProtocolGRPCWeb}},
					muxSvcOpts:      []ServiceOption{WithProtocols(ProtocolGRPCWeb)},
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
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
					},
				},
				{
					// gRPC-Web response with error too large
					name:            "buffer_grpcweb_endstream_error",
					muxWithSettings: &Mux{Protocols: []Protocol{ProtocolGRPCWeb}},
					muxSvcOpts:      []ServiceOption{WithProtocols(ProtocolGRPCWeb)},
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
								err: connect.NewError(connect.CodeDataLoss, errors.New(strings.Repeat("foo/", 1000))),
							}},
						},
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
					},
				},
				{
					// Connect streaming response with error too large
					name:            "buffer_connect_endstream_trailers",
					clientOptions:   []connect.ClientOption{connect.WithGRPC()},
					muxWithSettings: &Mux{Protocols: []Protocol{ProtocolConnect}},
					muxSvcOpts:      []ServiceOption{WithProtocols(ProtocolConnect)},
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
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
					},
				},
				{
					// Connect streaming response with error too large
					name:            "buffer_connect_endstream_trailers",
					clientOptions:   []connect.ClientOption{connect.WithGRPC()},
					muxWithSettings: &Mux{Protocols: []Protocol{ProtocolConnect}},
					muxSvcOpts:      []ServiceOption{WithProtocols(ProtocolConnect)},
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
								err: connect.NewError(connect.CodeDataLoss, errors.New(strings.Repeat("foo/", 1000))),
							}},
						},
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
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
					name:            "must_buffer_request_unary",
					muxWithSettings: &Mux{Codecs: []string{CodecJSON}},
					muxSvcOpts:      []ServiceOption{WithCodecs(CodecJSON)},
					invoke: func(clients testClients, hdrs http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
						return outputFromUnary(ctx, clients.libClient.GetBook, hdrs, msgs)
					},
					stream: testStream{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msgs: []testMsg{{in: &testMsgIn{
							msg: &testv1.GetBookRequest{Name: strings.Repeat("foo/", 1000)},
							err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
						}}},
					},
				},
				{
					name:            "must_buffer_request_stream",
					muxWithSettings: &Mux{Codecs: []string{CodecJSON}},
					muxSvcOpts:      []ServiceOption{WithCodecs(CodecJSON)},
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
								err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
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
					name:            "must_buffer_response_unary",
					muxWithSettings: &Mux{Codecs: []string{CodecJSON}},
					muxSvcOpts:      []ServiceOption{WithCodecs(CodecJSON)},
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
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
					},
				},
				{
					name:            "must_buffer_response_stream",
					muxWithSettings: &Mux{Codecs: []string{CodecJSON}},
					muxSvcOpts:      []ServiceOption{WithCodecs(CodecJSON)},
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
								err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
							}},
						},
					},
				},
				{
					// gRPC-Web response with trailers too large
					name:            "buffer_grpcweb_endstream_trailers",
					muxWithSettings: &Mux{Codecs: []string{CodecJSON}, Protocols: []Protocol{ProtocolGRPCWeb}},
					muxSvcOpts:      []ServiceOption{WithCodecs(CodecJSON), WithProtocols(ProtocolGRPCWeb)},
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
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
					},
				},
				{
					// gRPC-Web response with error too large
					name:            "buffer_grpcweb_endstream_error",
					muxWithSettings: &Mux{Codecs: []string{CodecJSON}, Protocols: []Protocol{ProtocolGRPCWeb}},
					muxSvcOpts:      []ServiceOption{WithCodecs(CodecJSON), WithProtocols(ProtocolGRPCWeb)},
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
								err: connect.NewError(connect.CodeDataLoss, errors.New(strings.Repeat("foo/", 1000))),
							}},
						},
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
					},
				},
				{
					// Connect streaming response with error too large
					name:            "buffer_connect_endstream_trailers",
					muxWithSettings: &Mux{Codecs: []string{CodecJSON}, Protocols: []Protocol{ProtocolConnect}},
					muxSvcOpts:      []ServiceOption{WithCodecs(CodecJSON), WithProtocols(ProtocolConnect)},
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
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
					},
				},
				{
					// Connect streaming response with error too large
					name:            "buffer_connect_endstream_trailers",
					muxWithSettings: &Mux{Codecs: []string{CodecJSON}, Protocols: []Protocol{ProtocolConnect}},
					muxSvcOpts:      []ServiceOption{WithCodecs(CodecJSON), WithProtocols(ProtocolConnect)},
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
								err: connect.NewError(connect.CodeDataLoss, errors.New(strings.Repeat("foo/", 1000))),
							}},
						},
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
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
					name:            "must_buffer_error_response",
					clientOptions:   []connect.ClientOption{connect.WithGRPC()},
					muxWithSettings: &Mux{Protocols: []Protocol{ProtocolConnect}},
					muxSvcOpts:      []ServiceOption{WithProtocols(ProtocolConnect)},
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
								err: connect.NewError(connect.CodeDataLoss, errors.New(strings.Repeat("foo/", 1000))),
							}},
						},
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("buffer limit exceeded")),
					},
				},
			},
		},
	}
	muxTestModes := []struct {
		name    string
		makeMux func(*testRequest) (*Mux, []ServiceOption)
	}{
		{
			name: "mux_settings",
			makeMux: func(req *testRequest) (*Mux, []ServiceOption) {
				mux := req.muxWithSettings
				mux.MaxMessageBufferBytes = 1024
				return mux, nil
			},
		},
		{
			name: "mux_svc_options",
			makeMux: func(req *testRequest) (*Mux, []ServiceOption) {
				return &Mux{}, append(req.muxSvcOpts, WithMaxMessageBufferBytes(1024))
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
							hdlr := http.HandlerFunc(func(respWriter http.ResponseWriter, req *http.Request) {
								serveMux.ServeHTTP(respWriter, req)
								defer expectationChecked.Store(true)
								testCase.expectation(t, respWriter, req)
							})

							mux, svcOpts := mode.makeMux(testReq)
							err := mux.RegisterServiceByName(hdlr, testv1connect.LibraryServiceName, svcOpts...)
							require.NoError(t, err)
							err = mux.RegisterServiceByName(hdlr, testv1connect.ContentServiceName, svcOpts...)
							require.NoError(t, err)
							server := httptest.NewUnstartedServer(mux.AsHandler())
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

func TestMux_ConnectGetUsesPostIfRequestTooLarge(t *testing.T) {
	t.Parallel()

	var interceptor testInterceptor
	_, hdlr := testv1connect.NewLibraryServiceHandler(
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

	muxWithSetting := &Mux{MaxGetURLBytes: 512, Compressors: []string{}}
	err := muxWithSetting.RegisterServiceByName(hdlr, testv1connect.LibraryServiceName)
	require.NoError(t, err)
	serverWithSetting := httptest.NewServer(muxWithSetting.AsHandler())
	disableCompression(serverWithSetting)
	t.Cleanup(serverWithSetting.Close)

	muxWithSvcOption := &Mux{}
	err = muxWithSvcOption.RegisterServiceByName(
		hdlr,
		testv1connect.LibraryServiceName,
		WithMaxGetURLBytes(512),
		WithNoCompression(),
	)
	require.NoError(t, err)
	serverWithSvcOption := httptest.NewServer(muxWithSvcOption.AsHandler())
	disableCompression(serverWithSvcOption)
	t.Cleanup(serverWithSvcOption.Close)

	testCases := []struct {
		name string
		svr  *httptest.Server
	}{
		{
			name: "with_mux_setting",
			svr:  serverWithSetting,
		},
		{
			name: "with_svc_option",
			svr:  serverWithSvcOption,
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

			client := testv1connect.NewLibraryServiceClient(
				testCase.svr.Client(),
				testCase.svr.URL,
				connect.WithHTTPGet(),
				connect.WithHTTPGetMaxURLSize(512, false),
				connect.WithSendGzip(),
			)
			req := connect.NewRequest(largeRequest)
			req.Header().Set("Test", t.Name()) // must set this for interceptor to work
			_, err := client.GetBook(context.Background(), req)
			// No error means it made through above interceptor unscathed
			// (so server handler got a POST).
			require.NoError(t, err)
			// But the client should have sent a GET, and the middleware should
			// have changed to POST because the request URL was too large.
			assert.Equal(t, http.MethodGet, req.HTTPMethod())

			// Sanity check that an RPC with a small request fails due to the above interceptor requiring POST
			// (Just to confirm that the above function is indeed intercepting the request).
			//
			// NB: We don't need to reset the stream for the test interceptor to match the small request
			//     because that interceptor won't see it. The other interceptor function should fail the
			//     request before it gets that far.
			req = connect.NewRequest(&testv1.GetBookRequest{Name: "foo/bar"})
			req.Header().Set("Test", t.Name()) // must set this for interceptor to work
			_, err = client.GetBook(context.Background(), req)
			require.ErrorContains(t, err, "server should only see POST; instead got GET")
		})
	}
}

//nolint:dupl // some of these testStream literals are the same as in other cases, but we don't need to share
func TestMux_Hooks(t *testing.T) {
	t.Parallel()
	// NB: These cases are identical to the pass-through cases, but should
	// not just pass through when a request or response hook is configured.

	var interceptor testInterceptor
	_, contentHandler := testv1connect.NewContentServiceHandler(
		testv1connect.UnimplementedContentServiceHandler{},
		connect.WithInterceptors(&interceptor),
	)

	var reqMsgs sync.Map
	var respMsgs sync.Map
	type testCaseName struct{}
	hookFactory := func(req bool) func(context.Context, Operation, proto.Message, bool, int) error {
		var msgsRecorded *sync.Map
		if req {
			msgsRecorded = &reqMsgs
		} else {
			msgsRecorded = &respMsgs
		}
		return func(ctx context.Context, op Operation, msg proto.Message, compressed bool, size int) error {
			name, ok := ctx.Value(testCaseName{}).(string)
			if !ok {
				return errors.New("no testCaseName in context")
			}
			if size <= 0 {
				return fmt.Errorf("invalid wire size: %d", size)
			}
			var expectCompressed bool
			if req {
				// For gzip test cases, we expect the request to be compressed.
				expectCompressed = strings.Contains(name, "gzip")
			} else {
				// But all responses can be compressed
				expectCompressed = true
			}
			if compressed != expectCompressed {
				return fmt.Errorf("invalid compressed: expecting %v, got %v", expectCompressed, compressed)
			}
			var slice []proto.Message
			val, exists := msgsRecorded.Load(name)
			if exists {
				var isSlice bool
				slice, isSlice = val.([]proto.Message)
				if !isSlice {
					return fmt.Errorf("val in map is wrong type: %T", val)
				}
			}
			// must make defensive copy since middleware re-uses same message
			// for each value in the stream
			msg = proto.Clone(msg)
			slice = append(slice, msg)
			msgsRecorded.Store(name, slice)
			return nil
		}
	}
	makeHooks := func(req, resp bool) func(context.Context, Operation) (Hooks, error) {
		return func(_ context.Context, _ Operation) (Hooks, error) {
			var hooks Hooks
			if req {
				hooks.OnClientRequestMessage = hookFactory(true)
			}
			if resp {
				hooks.OnServerResponseMessage = hookFactory(false)
			}
			return hooks, nil
		}
	}

	svrCases := []struct {
		name     string
		reqHook  bool
		respHook bool
		svr      *httptest.Server
	}{
		{
			name:    "request_hook",
			reqHook: true,
		},
		{
			name:     "response_hook",
			respHook: true,
		},
		{
			name:     "both_hooks",
			reqHook:  true,
			respHook: true,
		},
	}
	for i := range svrCases {
		svrCase := &svrCases[i]
		mux := &Mux{
			HooksCallback: makeHooks(svrCase.reqHook, svrCase.respHook),
		}
		require.NoError(t, mux.RegisterServiceByName(contentHandler, testv1connect.ContentServiceName))
		handler := mux.AsHandler()
		// propagate test name into context so that request and response hooks can access it
		setContextHandler := http.HandlerFunc(func(respWriter http.ResponseWriter, request *http.Request) {
			testName := request.Header.Get("Test")
			ctx := context.WithValue(request.Context(), testCaseName{}, testName)
			handler.ServeHTTP(respWriter, request.WithContext(ctx))
		})
		// Use HTTP/2 so we can test a bidi stream.
		server := httptest.NewUnstartedServer(setContextHandler)
		server.EnableHTTP2 = true
		server.StartTLS()
		t.Cleanup(server.Close)

		svrCase.svr = server
	}

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
						err: connect.NewError(connect.CodeResourceExhausted, errors.New("foobar")),
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
						err: connect.NewError(connect.CodeAborted, errors.New("foobar")),
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
						err: connect.NewError(connect.CodeDataLoss, errors.New("foobar")),
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
						err: connect.NewError(connect.CodePermissionDenied, errors.New("foobar")),
					}},
				},
			},
		},
	}

	checkHookResults := func(t *testing.T, expected []proto.Message, actualsMap *sync.Map) {
		t.Helper()
		// Make sure the hook recorded exactly the expected messages.
		var slice []proto.Message
		vals, exists := actualsMap.LoadAndDelete(t.Name())
		if exists {
			var ok bool
			slice, ok = vals.([]proto.Message)
			require.True(t, ok)
		}
		require.Len(t, slice, len(expected))
		for i, msg := range slice {
			want := expected[i]
			assert.Empty(t, cmp.Diff(want, msg, protocmp.Transform()))
		}
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
									for _, svrCase := range svrCases {
										svrCase := svrCase
										t.Run(svrCase.name, func(t *testing.T) {
											clientOptions := make([]connect.ClientOption, 0, 4)
											clientOptions = append(clientOptions, protocolCase.opts...)
											clientOptions = append(clientOptions, encodingCase.opts...)
											clientOptions = append(clientOptions, compressionCase.opts...)
											client := testv1connect.NewContentServiceClient(svrCase.svr.Client(), svrCase.svr.URL, clientOptions...)

											runRPCTestCase(t, &interceptor, client, testReq.invoke, testReq.stream)

											if svrCase.reqHook {
												var reqs []proto.Message
												for _, msg := range testReq.stream.msgs {
													if msg.in != nil {
														reqs = append(reqs, msg.in.msg)
													}
												}
												checkHookResults(t, reqs, &reqMsgs)
											}
											if svrCase.respHook {
												var resps []proto.Message
												for _, msg := range testReq.stream.msgs {
													if msg.out != nil && msg.out.msg != nil {
														resps = append(resps, msg.out.msg)
													}
												}
												checkHookResults(t, resps, &respMsgs)
											}
										})
									}
								})
							}
						})
					}
				})
			}
		})
	}
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

func (str *ttStream) start() {
	// Called from the interceptor when it starts handling the stream
	str.started.Store(true)
}

func (str *ttStream) finish(result error) {
	// Called from the interceptor when it finishes handling the stream
	str.result = result
	close(str.done)
}

func (str *ttStream) await(t *testing.T, expectServerDone bool) (svrInvoked bool, svrErr error) {
	t.Helper()
	// Called from test code to make sure server handler has completed.
	// Returns any error that the interceptor finished with.
	// Should only be called after the RPC appears to have completed in
	// the test client.
	if !str.started.Load() {
		// Interceptor never started, so nothing to wait for.
		return false, nil
	}
	if expectServerDone {
		select {
		case <-str.done:
			return true, str.result
		default:
			t.Fatal("expecting server to already be done but it's not")
		}
	}
	select {
	case <-str.done:
		return true, str.result
	case <-time.After(3 * time.Second):
		return true, fmt.Errorf("timeout: interceptor still did not finish after 3 seconds")
	}
}

type testInterceptor struct {
	sync.Map
}

func (ti *testInterceptor) get(testName string) (*ttStream, bool) {
	val, ok := ti.Load(testName)
	if !ok {
		return nil, false
	}
	stream, ok := val.(*ttStream)
	return stream, ok
}

func (ti *testInterceptor) set(t *testing.T, stream testStream) func(*testing.T, bool) (bool, error) {
	t.Helper()
	str := &ttStream{
		T:          t,
		testStream: stream,
		done:       make(chan struct{}),
	}
	ti.Store(t.Name(), str)
	// The returned function can be used by test code to await server completion.
	// (Useful in the event that middleware cancels the operation early, so client
	// could see a completed response while server still running concurrently.)
	return str.await
}

func (ti *testInterceptor) del(t *testing.T) {
	t.Helper()
	ti.Delete(t.Name())
}

func (ti *testInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (_ connect.AnyResponse, resultError error) {
		val := req.Header().Get("test")
		if val == "" {
			return next(ctx, req)
		}
		stream, ok := ti.get(val)
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
	}
}

func (ti *testInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (ti *testInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) (resultError error) {
		val := conn.RequestHeader().Get("test")
		if val == "" {
			return next(ctx, conn)
		}
		stream, ok := ti.get(val)
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
				return fmt.Errorf("expected message")
			}
		}
		for key, vals := range stream.rspTrailer {
			conn.ResponseTrailer()[key] = vals
		}
		return nil
	}
}

func (ti *testInterceptor) restUnaryHandler(
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
		if restIsHTTPBody(out.msg.ProtoReflect().Descriptor(), nil) { //nolint:nestif
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
		stream, ok := ti.get(val)
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
			break
		}
	}
	svrInvoked, svrErr := awaitServer(t, expectServerDone)
	// Verify the error received by the client.
	receivedErr := expectedErr
	if stream.err != nil {
		receivedErr = stream.err
	}
	if receivedErr == nil {
		assert.NoError(t, err)
	} else {
		assert.Equal(t, receivedErr.Code(), connect.CodeOf(err))
	}
	// Also check the error observed by the server.
	switch {
	case expectedErr == nil:
		assert.NoError(t, svrErr)
	case expectServerCancel:
		if svrInvoked && svrErr != nil {
			// We expect the server to either have seen the same error or it later
			// observed a cancel error (since the middleware cancels the request
			// after it aborts the operation).
			if connect.CodeOf(svrErr) != connect.CodeOf(expectedErr) && !errors.Is(svrErr, context.Canceled) {
				assert.Equal(t, connect.CodeCanceled, connect.CodeOf(svrErr))
			}
		}
	default:
		assert.Error(t, svrErr)
		assert.Equal(t, expectedErr.Code(), connect.CodeOf(svrErr))
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

func disableCompression(svr *httptest.Server) {
	transport := svr.Client().Transport.(*http.Transport) //nolint:errcheck,forcetypeassert
	transport.DisableCompression = true
}
