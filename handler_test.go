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
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1"
	"github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1/testv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
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
			requestURL:    "/foo/bar/baz",
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
			requestURL:    "/buf.vanguard.test.v1.LibraryService/CreateBook?connect=v1",
			requestMethod: "GET",
			expectedCode:  http.StatusMethodNotAllowed,
			expectedResponseHeaders: map[string]string{
				"Allow": "POST",
			},
		},
		{
			name:          "connect unary, bad HTTP method",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
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
			requestURL:    "/buf.vanguard.test.v1.ContentService/Download",
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
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
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
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
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
			requestURL:    "/buf.vanguard.test.v1.ContentService/Download",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect post, unknown codec",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect get, unknown codec",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook?connect=v1&encoding=text",
			requestMethod: "GET",
			expectedCode:  http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc, unknown codec",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc-web, unknown codec",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+text"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect stream, unknown compression, pass-through",
			requestURL:    "/buf.vanguard.test.v1.ContentService/Download",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":             {"application/connect+proto"},
				"Connect-Content-Encoding": {"blah"},
			},
			// When a supported protocol and codec, middleware will pass through
			// with unsupported compression and let underlying handler complain.
			expectedCode: http.StatusTeapot,
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
			mux:           grpcMux, // must target different protocol for the error
			requestURL:    "/buf.vanguard.test.v1.ContentService/Download",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":             {"application/connect+proto"},
				"Connect-Content-Encoding": {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect post, unknown compression, pass-through",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
				"Content-Encoding":         {"blah"},
			},
			expectedCode: http.StatusTeapot,
		},
		{
			name:          "connect post, unknown compression",
			mux:           grpcMux,
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
				"Content-Encoding":         {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect get, unknown compression, pass-through",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook?connect=v1&encoding=proto&compression=blah",
			requestMethod: "GET",
			expectedCode:  http.StatusTeapot,
		},
		{
			name:          "connect get, unknown compression",
			mux:           grpcMux,
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook?connect=v1&encoding=proto&compression=blah",
			requestMethod: "GET",
			expectedCode:  http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc, unknown compression, pass-through",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":  {"application/grpc+proto"},
				"Grpc-Encoding": {"blah"},
			},
			expectedCode: http.StatusTeapot,
		},
		{
			name:          "grpc, unknown compression",
			mux:           connectMux,
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":  {"application/grpc+proto"},
				"Grpc-Encoding": {"blah"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "grpc-web, unknown compression, pass-through",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type":  {"application/grpc-web+proto"},
				"Grpc-Encoding": {"blah"},
			},
			expectedCode: http.StatusTeapot,
		},
		{
			name:          "grpc-web, unknown compression",
			mux:           connectMux,
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
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
			requestURL:    "/buf.vanguard.test.v1.ContentService/Subscribe",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusHTTPVersionNotSupported,
		},
		{
			name:          "grpc, bidi and http 1.1",
			useHTTP1:      true,
			requestURL:    "/buf.vanguard.test.v1.ContentService/Subscribe",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc+proto"},
			},
			expectedCode: http.StatusHTTPVersionNotSupported,
		},
		{
			name:          "grpc-web, bidi and http 1.1",
			useHTTP1:      true,
			requestURL:    "/buf.vanguard.test.v1.ContentService/Subscribe",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/grpc-web+proto"},
			},
			expectedCode: http.StatusHTTPVersionNotSupported,
		},
		{
			name:          "connect post, stream method",
			requestURL:    "/buf.vanguard.test.v1.ContentService/Download",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Connect-Protocol-Version": {"1"},
				"Content-Type":             {"application/proto"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
		},
		{
			name:          "connect stream, unary method",
			requestURL:    "/buf.vanguard.test.v1.LibraryService/GetBook",
			requestMethod: "POST",
			requestHeaders: map[string][]string{
				"Content-Type": {"application/connect+proto"},
			},
			expectedCode: http.StatusUnsupportedMediaType,
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
			// NB: connect has a JSON codec implementation, but it is not exposed or
			//     selectable from the client; it is only available for handlers when
			//     handling requests that use this format ¯\_(ツ)_/¯
			opts: []connect.ClientOption{connect.WithCodec(jsonConnectCodec{})},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.ContentServiceIndexProcedure,
						msg:    &testv1.IndexRequest{Page: "abcdef"},
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
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: testv1connect.ContentServiceIndexProcedure,
						msg:    &testv1.IndexRequest{Page: "xyz"},
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
			name: "client stream fail",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromClientStream(ctx, client.Upload, headers, msgs)
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
			name: "server stream success",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromServerStream(ctx, client.Download, headers, msgs)
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
			name: "server stream fail",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromServerStream(ctx, client.Download, headers, msgs)
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
			name: "bidi stream success",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromBidiStream(ctx, client.Subscribe, headers, msgs)
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
			name: "bidi stream fail",
			invoke: func(client testv1connect.ContentServiceClient, headers http.Header, msgs []proto.Message) (http.Header, []proto.Message, http.Header, error) {
				return outputFromBidiStream(ctx, client.Subscribe, headers, msgs)
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

									interceptor.set(t, testReq.stream)
									reqHeaders := http.Header{}
									reqHeaders.Set("Test", t.Name()) // test header
									for k, v := range testReq.stream.reqHeader {
										reqHeaders[k] = v
									}
									var reqMsgs []proto.Message
									for _, streamMsg := range testReq.stream.msgs {
										if streamMsg.in != nil {
											reqMsgs = append(reqMsgs, streamMsg.in.msg)
										}
									}
									headers, responses, trailers, err := testReq.invoke(client, reqHeaders, reqMsgs)
									var expectedErr *connect.Error
									for _, streamMsg := range testReq.stream.msgs {
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
									assert.Subset(t, headers, testReq.stream.rspHeader)
									assert.Subset(t, trailers, testReq.stream.rspTrailer)
									var expectedResponses []proto.Message
									for _, streamMsg := range testReq.stream.msgs {
										if streamMsg.out != nil && streamMsg.out.msg != nil {
											expectedResponses = append(expectedResponses, streamMsg.out.msg)
										}
									}
									require.Len(t, responses, len(expectedResponses))
									for i, msg := range responses {
										want := expectedResponses[i]
										assert.Empty(t, cmp.Diff(want, msg, protocmp.Transform()))
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
			result: nil,
		},
		{
			name:   "b is empty",
			a:      []string{"a", "b", "c"},
			b:      nil,
			result: nil,
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

func TestMessageConvert(t *testing.T) {
	t.Parallel()
	buffers := newBufferPool()
	codecJSON := DefaultJSONCodec(protoregistry.GlobalTypes)
	codecProto := DefaultProtoCodec(protoregistry.GlobalTypes)
	compGzip := newCompressionPool(CompressionGzip, DefaultGzipCompressor, DefaultGzipDecompressor)

	encode := func(t *testing.T, codec Codec, comp *compressionPool, msg proto.Message) string {
		t.Helper()
		data, err := codec.MarshalAppend(nil, msg)
		require.NoError(t, err)
		if comp == nil {
			return string(data)
		}
		var buf bytes.Buffer
		require.NoError(t, comp.compress(&buf, bytes.NewBuffer(data)))
		return buf.String()
	}

	testCases := []struct {
		name                string
		src, dst            string
		srcCodec, dstCodec  Codec
		srcComp, dstComp    *compressionPool
		msg                 proto.Message
		mustDecode          bool
		wantMsg             proto.Message
		wantMarshalCalls    int
		wantUnmarshalCalls  int
		wantCompressCalls   int
		wantDecompressCalls int
		wantErr             string
	}{{
		name: "SameCodec",
		src:  `"hello"`, dst: `"hello"`,
		srcCodec: codecJSON, dstCodec: codecJSON,
		msg:     &wrapperspb.StringValue{},
		wantMsg: &wrapperspb.StringValue{}, // Not decoded
	}, {
		name: "SameCodecMustDecode",
		src:  `"hello"`, dst: `"hello"`,
		srcCodec: codecJSON, dstCodec: codecJSON,
		mustDecode:         true,
		msg:                &wrapperspb.StringValue{},
		wantMsg:            &wrapperspb.StringValue{Value: "hello"}, // decoded
		wantUnmarshalCalls: 1,
	}, {
		name: "DiffCodec",
		src:  `"hello"`, dst: encode(t, codecProto, nil, &wrapperspb.StringValue{Value: "hello"}),
		srcCodec: codecJSON, dstCodec: codecProto,
		msg:                &wrapperspb.StringValue{},
		wantMsg:            &wrapperspb.StringValue{Value: "hello"},
		wantUnmarshalCalls: 1,
		wantMarshalCalls:   1,
	}, {
		name: "Compress",
		src:  `"hello"`, dst: encode(t, codecProto, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		srcCodec: codecJSON, dstCodec: codecProto,
		srcComp: nil, dstComp: compGzip,
		msg:                &wrapperspb.StringValue{},
		wantMsg:            &wrapperspb.StringValue{Value: "hello"},
		wantUnmarshalCalls: 1,
		wantMarshalCalls:   1,
		wantCompressCalls:  1,
	}, {
		name: "SameCodecCompress",
		src:  `"hello"`, dst: encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		srcCodec: codecJSON, dstCodec: codecJSON,
		srcComp: nil, dstComp: compGzip,
		wantCompressCalls: 1,
	}, {
		name: "Decompress",
		src:  encode(t, codecProto, compGzip, &wrapperspb.StringValue{Value: "hello"}), dst: `"hello"`,
		srcCodec: codecProto, dstCodec: codecJSON,
		srcComp: compGzip, dstComp: nil,
		msg:                 &wrapperspb.StringValue{},
		wantMsg:             &wrapperspb.StringValue{Value: "hello"},
		wantUnmarshalCalls:  1,
		wantMarshalCalls:    1,
		wantDecompressCalls: 1,
	}, {
		name:     "SameCodecDecompress",
		src:      encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		dst:      `"hello"`,
		srcCodec: codecJSON, dstCodec: codecJSON,
		srcComp: compGzip, dstComp: nil,
		wantDecompressCalls: 1,
	}, {
		name:     "SameCodecCompMustDecode",
		src:      encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		dst:      encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		srcCodec: codecJSON, dstCodec: codecJSON,
		srcComp: compGzip, dstComp: compGzip,
		mustDecode:          true,
		msg:                 &wrapperspb.StringValue{},
		wantMsg:             &wrapperspb.StringValue{Value: "hello"},
		wantUnmarshalCalls:  1,
		wantDecompressCalls: 1,
	}, {
		name: "ForceRecode",
		src:  `""`, dst: `"from msg"`,
		srcCodec: nil, dstCodec: codecJSON,
		srcComp: nil, dstComp: nil,
		msg:              &wrapperspb.StringValue{Value: "from msg"},
		wantMarshalCalls: 1,
	}, {
		name: "ForceRecodeRecompress",
		src:  `""`, dst: encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "from msg"}),
		srcCodec: nil, dstCodec: codecJSON,
		srcComp: nil, dstComp: compGzip,
		msg:               &wrapperspb.StringValue{Value: "from msg"},
		wantMarshalCalls:  1,
		wantCompressCalls: 1,
	}}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var (
				marshalCalls, unmarshalCalls   int
				compressCalls, decompressCalls int
			)
			var srcComp compressor
			if testCase.srcComp != nil {
				srcComp = countCompressor{
					compressionPool:   testCase.srcComp,
					compressorCalls:   &compressCalls,
					decompressorCalls: &decompressCalls,
				}
			}
			var srcCodec Codec
			if testCase.srcCodec != nil {
				srcCodec = countCodec{
					Codec:          testCase.srcCodec,
					marshalCalls:   &marshalCalls,
					unmarshalCalls: &unmarshalCalls,
				}
			}
			var dstComp compressor
			if testCase.dstComp != nil {
				dstComp = countCompressor{
					compressionPool:   testCase.dstComp,
					compressorCalls:   &compressCalls,
					decompressorCalls: &decompressCalls,
				}
			}
			var dstCodec Codec
			if testCase.dstCodec != nil {
				dstCodec = countCodec{
					Codec:          testCase.dstCodec,
					marshalCalls:   &marshalCalls,
					unmarshalCalls: &unmarshalCalls,
				}
			}
			msgbuffer := &message{
				buf:   bytes.NewBufferString(testCase.src),
				codec: srcCodec,
				comp:  srcComp,
			}
			got := testCase.msg
			if err := msgbuffer.convert(buffers, dstComp, dstCodec, got, testCase.mustDecode); err != nil {
				assert.EqualError(t, err, testCase.wantErr)
				return
			}
			if testCase.wantMsg != nil {
				assert.Empty(t, cmp.Diff(testCase.wantMsg, got, protocmp.Transform()))
			}
			assert.Equal(t, testCase.dst, msgbuffer.buf.String())
			assert.Equal(t, testCase.wantMarshalCalls, marshalCalls, "marshalCalls")
			assert.Equal(t, testCase.wantUnmarshalCalls, unmarshalCalls, "unmarshalCalls")
			assert.Equal(t, testCase.wantCompressCalls, compressCalls, "compressCalls")
			assert.Equal(t, testCase.wantDecompressCalls, decompressCalls, "decompressCalls")
		})
	}
}

type countCodec struct {
	Codec
	marshalCalls, unmarshalCalls *int
}

func (c countCodec) MarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	*c.marshalCalls++
	return c.Codec.MarshalAppend(b, msg)
}
func (c countCodec) Unmarshal(b []byte, msg proto.Message) error {
	*c.unmarshalCalls++
	return c.Codec.Unmarshal(b, msg)
}

type countCompressor struct {
	*compressionPool
	compressorCalls, decompressorCalls *int
}

func (c countCompressor) compress(dst io.Writer, src *bytes.Buffer) error {
	*c.compressorCalls++
	return c.compressionPool.compress(dst, src)
}
func (c countCompressor) decompress(dst *bytes.Buffer, src io.Reader) error {
	*c.decompressorCalls++
	return c.compressionPool.decompress(dst, src)
}

type jsonConnectCodec struct{}

func (j jsonConnectCodec) Name() string {
	return CodecJSON
}

func (j jsonConnectCodec) Marshal(a any) ([]byte, error) {
	msg, ok := a.(proto.Message)
	if !ok {
		return nil, errors.New("not a message")
	}
	return protojson.Marshal(msg)
}

func (j jsonConnectCodec) Unmarshal(bytes []byte, a any) error {
	msg, ok := a.(proto.Message)
	if !ok {
		return errors.New("not a message")
	}
	return protojson.Unmarshal(bytes, msg)
}
