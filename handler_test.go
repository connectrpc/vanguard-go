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
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
			targetMux.AsHandler().ServeHTTP(respWriter, req)
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

//nolint:dupl // some of these testStream literals are the same as in vanguard_rpcxrpc_test cases, but we don't need to share
func TestHandler_PassThrough(t *testing.T) {
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
	server := httptest.NewUnstartedServer(mux.AsHandler())
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
			bufferPool: newBufferPool(),
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

func TestIntersection(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		a, b, result []string
		resultCap    int
	}{
		{
			name:      "b is superset",
			a:         []string{"a", "b", "c"},
			b:         []string{"a", "b", "c", "d", "e", "f"},
			result:    []string{"a", "b", "c"},
			resultCap: 3,
		},
		{
			name:      "a is superset",
			a:         []string{"a", "b", "c", "d", "e", "f"},
			b:         []string{"a", "b", "c"},
			result:    []string{"a", "b", "c"},
			resultCap: 3,
		},
		{
			name:   "a is empty",
			a:      nil,
			b:      []string{"a", "b", "c", "d", "e", "f"},
			result: []string{},
		},
		{
			name:   "b is empty",
			a:      []string{"a", "b", "c"},
			b:      nil,
			result: []string{},
		},
		{
			name:      "result is empty",
			a:         []string{"a", "b", "c"},
			b:         []string{"d", "e", "f"},
			result:    []string{}, // only nil when one of the inputs is empty
			resultCap: 3,
		},
		{
			name:      "result is subset of both",
			a:         []string{"x", "y", "z", "a", "b", "c"},
			b:         []string{"a", "b", "c", "d", "e", "f"},
			result:    []string{"a", "b", "c"},
			resultCap: 6,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result := intersect(testCase.a, testCase.b)
			require.Equal(t, testCase.result, result)
			require.Equal(t, testCase.resultCap, cap(result))
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
	require.Equal(t, testDataString, msg.msg.(*wrapperspb.StringValue).Value) //nolint:forcetypeassert
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
	val := msg.(*wrapperspb.StringValue).Value //nolint:forcetypeassert
	return append(b, ([]byte)(val)...), nil
}

func (f *fakeCodec) Unmarshal(b []byte, msg proto.Message) error {
	f.unmarshalCalls++
	msg.(*wrapperspb.StringValue).Value = string(b) //nolint:forcetypeassert
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
