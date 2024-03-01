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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestMux_RPCxRPC(t *testing.T) {
	t.Parallel()

	serviceNames := []string{
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
			WithTargetProtocols(protocol),
			WithTargetCodecs(codec),
		}
		if compression == CompressionIdentity {
			opts = append(opts, WithNoTargetCompression())
		} else {
			opts = append(opts, WithTargetCompression(compression))
		}
		svcHandler := protocolAssertMiddleware(protocol, codec, compression, serveMux)
		name := fmt.Sprintf("%s_%s_%s", protocol, codec, compression)

		services := make([]*Service, len(serviceNames))
		for i, svcName := range serviceNames {
			services[i] = NewService(svcName, svcHandler, opts...)
		}
		handler, err := NewTranscoder(services)
		require.NoError(t, err)
		server := httptest.NewUnstartedServer(handler)
		server.EnableHTTP2 = true
		server.StartTLS()
		disableCompression(server)
		t.Cleanup(server.Close)
		return testServer{name: name, server: server}
	}
	servers := []testServer{}
	for _, protocol := range protocols {
		for _, codec := range codecs {
			for _, compression := range compressions {
				servers = append(servers, makeServer(protocol, codec, compression))
			}
		}
	}
	for _, server := range servers {
		serverURL, err := url.Parse(server.server.URL)
		require.NoError(t, err)
		proxy := httputil.NewSingleHostReverseProxy(serverURL)
		proxy.Transport = server.server.Client().Transport
		proxyServer := httptest.NewUnstartedServer(proxy)
		proxyServer.EnableHTTP2 = true
		proxyServer.StartTLS()
		disableCompression(proxyServer)
		t.Cleanup(proxyServer.Close)
		servers = append(servers, testServer{name: server.name + "/proxy", server: proxyServer})
	}

	type testOpt struct {
		name   string
		server *httptest.Server
		opts   []connect.ClientOption
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
							name:   fmt.Sprintf("%s%s_%s_%s/%s", protocol, suffix, codec, compression, server.name),
							server: server.server,
							opts:   copyOpts,
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
						err: newConnectError(connect.CodeFailedPrecondition, "foo"),
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
						err: newConnectError(connect.CodeAborted, "foobar"),
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
						err: newConnectError(connect.CodeDataLoss, "foobar"),
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
						err: newConnectError(connect.CodePermissionDenied, "foobar"),
					}},
				},
			},
		},
	}
	for _, opts := range testOpts {
		opts := opts
		clients := testClients{
			libClient: testv1connect.NewLibraryServiceClient(
				opts.server.Client(), opts.server.URL, opts.opts...,
			),
			contentClient: testv1connect.NewContentServiceClient(
				opts.server.Client(), opts.server.URL, opts.opts...,
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
