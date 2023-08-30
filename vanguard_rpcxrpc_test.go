// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/bufbuild/vanguard-go/internal/gen/buf/vanguard/test/v1"
	"github.com/bufbuild/vanguard-go/internal/gen/buf/vanguard/test/v1/testv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"
)

//nolint:dupl // some of these testStream literals are the same as in handler_test cases, but we don't need to share
func TestMux_RPCxRPC(t *testing.T) {
	t.Parallel()

	services := []protoreflect.FullName{
		testv1connect.LibraryServiceName,
		testv1connect.ContentServiceName,
	}
	codecs := []string{
		CodecJSON,
		CodecProto,
	}
	compressions := []string{
		CompressionGzip,
		CompressionIdentity,
	}
	protocols := []Protocol{
		ProtocolGRPC,
		ProtocolGRPCWeb,
		ProtocolConnect,
	}

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

	makeServer := func(protocol Protocol, codec, compression string) testServer {
		opts := []ServiceOption{
			WithProtocols(protocol),
			WithCodecs(codec),
		}
		if compression == CompressionIdentity {
			opts = append(opts, WithNoCompression())
		} else {
			opts = append(opts, WithCompression(compression))
		}
		hdlr := protocolAssertMiddleware(protocol, codec, compression, serveMux)
		name := fmt.Sprintf("%s_%s_%s", protocol, codec, compression)

		mux := &Mux{}
		for _, service := range services {
			err := mux.RegisterServiceByName(hdlr, service, opts...)
			require.NoError(t, err)
		}
		server := httptest.NewUnstartedServer(mux.AsHandler())
		server.EnableHTTP2 = true
		server.StartTLS()
		disableCompression(server)
		t.Cleanup(server.Close)
		return testServer{name: name, svr: server}
	}
	var servers []testServer
	for _, protocol := range protocols {
		for _, codec := range codecs {
			for _, compression := range compressions {
				servers = append(servers, makeServer(protocol, codec, compression))
			}
		}
	}

	type testOpt struct {
		name string
		svr  *httptest.Server
		opts []connect.ClientOption
	}
	var testOpts []testOpt
	for _, server := range servers {
		var opts []connect.ClientOption
		for _, protocol := range protocols {
			opts := appendClientProtocolOptions(t, opts, protocol)
			addlOpts := map[string][]connect.ClientOption{"": nil}
			if protocol == ProtocolConnect {
				addlOpts["(GET)"] = []connect.ClientOption{
					connect.WithHTTPGet(),
					connect.WithHTTPGetMaxURLSize(200, true),
				}
			}
			for suffix, addlOpt := range addlOpts {
				if addlOpt != nil {
					opts = append(opts, addlOpt...)
				}
				for _, codec := range codecs {
					opts := appendClientCodecOptions(t, opts, codec)
					for _, compression := range compressions {
						opts := appendClientCompressionOptions(t, opts, compression)
						copyOpts := make([]connect.ClientOption, len(opts))
						copy(copyOpts, opts)
						testOpts = append(testOpts, testOpt{
							name: fmt.Sprintf("%s%s_%s_%s/%s", protocol, suffix, codec, compression, server.name),
							svr:  server.svr,
							opts: copyOpts,
						})
					}
				}
			}
		}
	}

	ctx := context.Background()
	type testClients struct {
		contentClient testv1connect.ContentServiceClient
		libClient     testv1connect.LibraryServiceClient
	}
	testRequests := []struct {
		name   string
		invoke func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error)
		stream testStream
	}{
		{
			name: "GetBook_success",
			invoke: func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromUnary(ctx, clients.libClient.GetBook, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.LibraryServiceGetBookProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.GetBookRequest{Name: "shelves/1/books/1"},
					}},
					{out: &testMsgOut{
						msg: &testv1.Book{Name: "shelves/1/books/1"},
					}},
				},
				rspTrailer: http.Header{"Trailer-Val": []string{"end"}},
			},
		},
		{
			// Should force compression in Connect GET requests, for clients that support both.
			// For clients that use GET but not compression, this will be too big for GET and will use POST instead.
			name: "GetBook_large_request",
			invoke: func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromUnary(ctx, clients.libClient.GetBook, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.LibraryServiceGetBookProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.GetBookRequest{Name: strings.Repeat("foo/", 300) + "/1"},
					}},
					{out: &testMsgOut{
						msg: &testv1.Book{Name: strings.Repeat("foo/", 300) + "/1"},
					}},
				},
				rspTrailer: http.Header{"Trailer-Val": []string{"end"}},
			},
		},
		{
			name: "GetBook_error",
			invoke: func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromUnary(ctx, clients.libClient.GetBook, headers, msgs)
			},
			stream: testStream{
				method:    testv1connect.LibraryServiceGetBookProcedure,
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						msg: &testv1.GetBookRequest{Name: "shelves/1/books/1"},
					}},
					{out: &testMsgOut{
						err: connect.NewError(connect.CodeFailedPrecondition, errors.New("foo")),
					}},
				},
			},
		},
		{
			name: "Upload_success",
			invoke: func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromClientStream(ctx, clients.contentClient.Upload, headers, msgs)
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
			name: "Upload_error",
			invoke: func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromClientStream(ctx, clients.contentClient.Upload, headers, msgs)
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
			name: "Download_success",
			invoke: func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromServerStream(ctx, clients.contentClient.Download, headers, msgs)
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
			name: "Download_error",
			invoke: func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromServerStream(ctx, clients.contentClient.Download, headers, msgs)
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
			name: "Subscribe_success",
			invoke: func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromBidiStream(ctx, clients.contentClient.Subscribe, headers, msgs)
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
						msg: &testv1.SubscribeResponse{
							FilenameChanged: "xyz1.foo",
						},
					}},
					{out: &testMsgOut{
						msg: &testv1.SubscribeResponse{
							FilenameChanged: "xyz2.foo",
						},
					}},
					{in: &testMsgIn{
						msg: &testv1.SubscribeRequest{FilenamePatterns: []string{"test.test"}},
					}},
					{out: &testMsgOut{
						msg: &testv1.SubscribeResponse{
							FilenameChanged: "test.test",
						},
					}},
				},
				rspTrailer: http.Header{"Trailer-Val": []string{"end"}},
			},
		},
		{
			name: "Subscribe_error",
			invoke: func(clients testClients, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromBidiStream(ctx, clients.contentClient.Subscribe, headers, msgs)
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
						msg: &testv1.SubscribeResponse{
							FilenameChanged: "xyz1.foo",
						},
					}},
					{out: &testMsgOut{
						err: connect.NewError(connect.CodePermissionDenied, errors.New("foobar")),
					}},
				},
			},
		},
		// TODO: Add more tests -- more permutations to catch things like trailers-only responses in gRPC,
		//       empty client streams, empty server streams
		// TODO: Exercise Connect GET for unary operations with Connect client
		// TODO: Verify timeouts are propagated correctly
	}
	for _, opts := range testOpts {
		opts := opts
		clients := testClients{
			libClient: testv1connect.NewLibraryServiceClient(
				opts.svr.Client(), opts.svr.URL, opts.opts...,
			),
			contentClient: testv1connect.NewContentServiceClient(
				opts.svr.Client(), opts.svr.URL, opts.opts...,
			),
		}
		t.Run(opts.name, func(t *testing.T) {
			t.Parallel()
			for _, testReq := range testRequests {
				testCase := testReq
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()
					runRPCTestCase(t, &interceptor, clients, testCase.invoke, testCase.stream)
				})
			}
		})
	}
}

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
		muxSvcOpts      []ServiceOption // Does not need to include WithMaxMessageBufferSize
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
				mux.MaxMessageBufferSize = 1024
				return mux, nil
			},
		},
		{
			name: "mux_svc_options",
			makeMux: func(req *testRequest) (*Mux, []ServiceOption) {
				return &Mux{}, append(req.muxSvcOpts, WithMaxMessageBufferSize(1024))
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

	muxWithSetting := &Mux{MaxGetURLSize: 512, Compressors: []string{}}
	err := muxWithSetting.RegisterServiceByName(hdlr, testv1connect.LibraryServiceName)
	require.NoError(t, err)
	serverWithSetting := httptest.NewServer(muxWithSetting.AsHandler())
	disableCompression(serverWithSetting)
	t.Cleanup(serverWithSetting.Close)

	muxWithSvcOption := &Mux{}
	err = muxWithSvcOption.RegisterServiceByName(
		hdlr,
		testv1connect.LibraryServiceName,
		WithMaxGetURLSize(512),
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
