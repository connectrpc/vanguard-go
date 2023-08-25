// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1"
	"github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1/testv1connect"
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msg:    &testv1.GetBookRequest{Name: "shelves/1/books/1"},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msg:    &testv1.GetBookRequest{Name: strings.Repeat("foo/", 200) + "/1"},
					}},
					{out: &testMsgOut{
						msg: &testv1.Book{Name: strings.Repeat("foo/", 200) + "/1"},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.LibraryServiceGetBookProcedure,
						msg:    &testv1.GetBookRequest{Name: "shelves/1/books/1"},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.ContentServiceUploadProcedure,
						msg:    &testv1.UploadRequest{Filename: "xyz"},
					}},
					{in: &testMsgIn{
						method: testv1connect.ContentServiceUploadProcedure,
						msg:    &testv1.UploadRequest{Filename: "xyz"},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.ContentServiceUploadProcedure,
						msg:    &testv1.UploadRequest{Filename: "xyz"},
					}},
					{in: &testMsgIn{
						method: testv1connect.ContentServiceUploadProcedure,
						msg:    &testv1.UploadRequest{Filename: "xyz"},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.ContentServiceDownloadProcedure,
						msg:    &testv1.DownloadRequest{Filename: "xyz"},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.ContentServiceDownloadProcedure,
						msg:    &testv1.DownloadRequest{Filename: "xyz"},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.ContentServiceSubscribeProcedure,
						msg:    &testv1.SubscribeRequest{FilenamePatterns: []string{"xyz.*", "abc*.jpg"}},
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
						method: testv1connect.ContentServiceSubscribeProcedure,
						msg:    &testv1.SubscribeRequest{FilenamePatterns: []string{"test.test"}},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.ContentServiceSubscribeProcedure,
						msg:    &testv1.SubscribeRequest{FilenamePatterns: []string{"xyz.*", "abc*.jpg"}},
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
