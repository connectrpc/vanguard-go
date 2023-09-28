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
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/stretchr/testify/require"
)

func TestHandler_Errors(t *testing.T) {
	t.Parallel()
	// These tests exercise error-handling in the way the operation is initialized.
	// These tests should not reach the underlying handler or any particular protocol
	// handler implementation (other than extracting request metadata).

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	})
	grpcMux := &Mux{Protocols: []Protocol{ProtocolGRPC, ProtocolGRPCWeb}}
	connectMux := &Mux{Protocols: []Protocol{ProtocolConnect}}
	allMux := &Mux{} // supports all three
	for _, mux := range []*Mux{grpcMux, connectMux, allMux} {
		require.NoError(t, mux.RegisterServiceByName(handler, testv1connect.LibraryServiceName))
		require.NoError(t, mux.RegisterServiceByName(handler, testv1connect.ContentServiceName))
	}

	testCases := []struct {
		name                    string
		mux                     *Mux // use allMux if nil
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
			name: "unknown handler",
			mux: &Mux{
				UnknownHandler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.WriteHeader(http.StatusTeapot)
				}),
			},
			requestURL:    "/vanguard.test.v1.LibraryService/UnknownMethod",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusTeapot,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			targetMux := allMux
			if testCase.mux != nil {
				targetMux = testCase.mux
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
			targetMux.ServeHTTP(respWriter, req)
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

/*func TestHandler_PassThrough(t *testing.T) {
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
	_, contentHandler := testv1connect.NewContentServiceHandler(
		testv1connect.UnimplementedContentServiceHandler{},
		connect.WithInterceptors(&interceptor),
	)
	var mux Mux
	require.NoError(t, mux.RegisterServiceByName(checkPassThrough(contentHandler), testv1connect.ContentServiceName))

	// Use HTTP/2 so we can test a bidi stream.
	server := httptest.NewUnstartedServer(&mux)
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
}*/
