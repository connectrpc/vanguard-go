// Copyright 2023-2026 Buf Technologies, Inc.
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
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestMount_RESTRequests(t *testing.T) {
	t.Parallel()

	type input struct {
		method string
		path   string
		values url.Values
		body   proto.Message
		meta   http.Header
	}
	type output struct {
		code    int
		body    any
		rawBody string
		meta    http.Header
	}
	type testRequest struct {
		name   string
		input  input
		stream testStream
		output output
	}
	testRequests := []testRequest{{
		name: "ListShelves-GetNilRequest",
		input: input{
			method: http.MethodGet,
			path:   "/v1/shelves",
			body:   nil,
		},
		stream: testStream{
			method: testv1connect.LibraryServiceListShelvesProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.ListShelvesRequest{},
				}},
				{out: &testMsgOut{
					msg: &testv1.ListShelvesResponse{},
				}},
			},
		},
		output: output{
			code: http.StatusOK,
			body: &testv1.ListShelvesResponse{},
		},
	}, {
		name: "GetBook",
		input: input{
			method: http.MethodGet,
			path:   "/v1/shelves/1/books/1",
			body:   nil,
			meta: http.Header{
				"Message": []string{"hello"},
			},
		},
		stream: testStream{
			method: testv1connect.LibraryServiceGetBookProcedure,
			reqHeader: http.Header{
				"Message": []string{"hello"},
			},
			rspHeader: http.Header{
				"Message": []string{"world"},
			},
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.GetBookRequest{Name: "shelves/1/books/1"},
				}},
				{out: &testMsgOut{
					msg: &testv1.Book{Name: "shelves/1/books/1"},
				}},
			},
		},
		output: output{
			code: http.StatusOK,
			body: &testv1.Book{Name: "shelves/1/books/1"},
			meta: http.Header{
				"Message": []string{"world"},
			},
		},
	}, {
		name: "GetBook-NotAllowed",
		input: input{
			method: http.MethodPut,
			path:   "/v1/shelves/1/books/1",
		},
		output: output{
			code: http.StatusMethodNotAllowed,
			body: &status.Status{
				Code:    int32(connect.CodeUnimplemented),
				Message: "HTTP method PUT not allowed",
			},
			meta: http.Header{
				"Allow": []string{"DELETE, GET, PATCH"},
			},
		},
	}, {
		name: "GetBook-ErrorAfterSend",
		input: input{
			method: http.MethodGet,
			path:   "/v1/shelves/1/books/1",
		},
		stream: testStream{
			method: testv1connect.LibraryServiceGetBookProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.GetBookRequest{Name: "shelves/1/books/1"},
				}},
				{out: &testMsgOut{
					msg: &testv1.Book{Name: "shelves/1/books/1"},
				}},
				{out: &testMsgOut{
					err: connect.NewError(connect.CodeInternal, "late failure"),
				}},
			},
		},
		output: output{
			code: http.StatusOK,
			body: &testv1.Book{Name: "shelves/1/books/1"},
		},
	}, {
		name: "GetBook-Error",
		input: input{
			method: http.MethodGet,
			path:   "/v1/shelves/1/books/1",
			body:   nil,
			meta: http.Header{
				"Message": []string{"hello"},
			},
		},
		stream: testStream{
			method: testv1connect.LibraryServiceGetBookProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.GetBookRequest{Name: "shelves/1/books/1"},
				}},
				{out: &testMsgOut{
					err: connect.NewError(connect.CodePermissionDenied, "permission denied"),
				}},
			},
		},
		output: output{
			code: http.StatusForbidden,
			body: &status.Status{
				Code:    int32(connect.CodePermissionDenied),
				Message: "permission denied",
			},
		},
	}, {
		name: "GetBook-NoResponse",
		input: input{
			method: http.MethodGet,
			path:   "/v1/shelves/1/books/1",
		},
		stream: testStream{
			method: testv1connect.LibraryServiceGetBookProcedure,
			rspHeader: http.Header{
				"Message": []string{"world"},
			},
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.GetBookRequest{Name: "shelves/1/books/1"},
				}},
			},
		},
		output: output{
			code: http.StatusInternalServerError,
			body: &status.Status{
				Code:    int32(connect.CodeInternal),
				Message: "handler sent no response message",
			},
			meta: http.Header{
				"Message": []string{"world"},
			},
		},
	}, {
		name: "CreateBook",
		input: input{
			method: http.MethodPost,
			path:   "/v1/shelves/1/books",
			values: url.Values{
				"bookId":     []string{"1"},
				"request_id": []string{"2"},
			},
			body: &testv1.Book{
				Title:  "The Art of Computer Programming",
				Author: "Donald E. Knuth",
			},
		},
		stream: testStream{
			method: testv1connect.LibraryServiceCreateBookProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.CreateBookRequest{
						Parent:    "shelves/1",
						BookId:    "1",
						RequestId: "2",
						Book: &testv1.Book{
							Title:  "The Art of Computer Programming",
							Author: "Donald E. Knuth",
						},
					},
				}},
				{out: &testMsgOut{
					msg: &testv1.Book{
						Title:  "The Art of Computer Programming",
						Author: "Donald E. Knuth",
					},
				}},
			},
		},
		output: output{
			code: http.StatusOK,
			body: &testv1.Book{
				Title:  "The Art of Computer Programming",
				Author: "Donald E. Knuth",
			},
		},
	}, {
		name: "CreateBook-NoBody",
		input: input{
			method: http.MethodPost,
			path:   "/v1/shelves/1/books",
			values: url.Values{"bookId": []string{"1"}},
		},
		stream: testStream{
			method: testv1connect.LibraryServiceCreateBookProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.CreateBookRequest{},
					err: connect.NewError(connect.CodeInvalidArgument, ""),
				}},
			},
		},
		output: output{
			code: http.StatusBadRequest,
			body: &status.Status{
				Code:    int32(connect.CodeInvalidArgument),
				Message: "decode request: zero-length payload is not a valid JSON object",
			},
		},
	}, {
		name: "MoveBooks",
		input: input{
			method: http.MethodPost,
			path:   "/v2/shelves/1/books:move",
			body: &httpbody.HttpBody{
				ContentType: "application/json",
				Data:        ([]byte)(`["book1", "book2", "book3", "book4"]`),
			},
		},
		stream: testStream{
			method: testv1connect.LibraryServiceMoveBooksProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.MoveBooksRequest{
						NewParent: "shelves/1",
						Books:     []string{"book1", "book2", "book3", "book4"},
					},
				}},
				{out: &testMsgOut{
					msg: &testv1.MoveBooksResponse{},
				}},
			},
		},
		output: output{
			code: http.StatusOK,
			body: &testv1.MoveBooksResponse{},
		},
	}, {
		name: "ListCheckouts",
		input: input{
			method: http.MethodGet,
			path:   "/v2/shelves/1/books/abc:checkouts",
		},
		stream: testStream{
			method: testv1connect.LibraryServiceListCheckoutsProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.ListCheckoutsRequest{
						Name: "shelves/1/books/abc",
					},
				}},
				{out: &testMsgOut{
					msg: &testv1.ListCheckoutsResponse{
						Checkouts: []*testv1.Checkout{
							{
								Id: 123,
								Books: []*testv1.Book{
									{
										Name:   "shelves/1/books/abc",
										Parent: "shelves/1",
									},
									{
										Name:   "shelves/1/books/def",
										Parent: "shelves/1",
									},
								},
							},
						},
					},
				}},
			},
		},
		output: output{
			code: http.StatusOK,
			body: `[
				{
					"id": "123",
					"books": [
						{
							"name": "shelves/1/books/abc", "parent": "shelves/1",
							"createTime": null, "updateTime": null,
							"title": "", "author": "", "description": "",
							"labels": {}
						},
						{
							"name": "shelves/1/books/def", "parent": "shelves/1",
							"createTime": null, "updateTime": null,
							"title": "", "author": "", "description": "",
							"labels": {}
						}
					]
				}
			]`,
		},
	}, {
		name: "GetCheckout-Error",
		input: input{
			method: http.MethodGet,
			path:   "/v2/checkouts/nan",
			body:   nil,
			meta: http.Header{
				"Message": []string{"hello"},
			},
		},
		stream: testStream{
			method: testv1connect.LibraryServiceGetCheckoutProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.GetCheckoutRequest{},
					err: connect.NewError(connect.CodeInvalidArgument, ""),
				}},
			},
		},
		output: output{
			code: http.StatusBadRequest,
			body: &status.Status{
				Code:    int32(connect.CodeInvalidArgument),
				Message: "invalid parameter \"id\": invalid character 'a' in literal null (expecting 'u')",
			},
		},
	}, {
		name: "Index",
		input: input{
			method: http.MethodGet,
			path:   "/page.html",
		},
		stream: testStream{
			method: testv1connect.ContentServiceIndexProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.IndexRequest{
						Page: "page.html",
					},
				}},
				{out: &testMsgOut{
					msg: &httpbody.HttpBody{
						ContentType: "text/html",
						Data:        []byte("<html>hello</html>"),
					},
				}},
			},
		},
		output: output{
			code:    http.StatusOK,
			rawBody: `<html>hello</html>`,
			meta: http.Header{
				"Content-Type": []string{"text/html"},
			},
		},
	}, {
		name: "Upload",
		input: input{
			method: http.MethodPost,
			path:   "/message.txt:upload",
			body: &httpbody.HttpBody{
				ContentType: "text/plain",
				Data:        []byte("hello"),
			},
		},
		stream: testStream{
			method: testv1connect.ContentServiceUploadProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.UploadRequest{
						Filename: "message.txt",
						File: &httpbody.HttpBody{
							ContentType: "text/plain",
							Data:        []byte("hello"),
						},
					},
				}},
				{out: &testMsgOut{
					msg: &emptypb.Empty{},
				}},
			},
		},
		output: output{
			code: http.StatusOK,
			body: &emptypb.Empty{},
		},
	}, {
		name: "Download",
		input: input{
			method: http.MethodGet,
			path:   "/message.txt:download",
		},
		stream: testStream{
			method: testv1connect.ContentServiceDownloadProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.DownloadRequest{
						Filename: "message.txt",
					},
				}},
				{out: &testMsgOut{
					msg: &testv1.DownloadResponse{
						File: &httpbody.HttpBody{
							ContentType: "text/plain",
							Data:        []byte("hello"),
						},
					},
				}},
				{out: &testMsgOut{
					msg: &testv1.DownloadResponse{
						File: &httpbody.HttpBody{
							Data: []byte(" world"),
						},
					},
				}},
			},
		},
		output: output{
			code:    http.StatusOK,
			rawBody: `hello world`,
			meta: http.Header{
				"Content-Type": []string{"text/plain"},
			},
		},
	}, {
		name: "Download-Empty",
		input: input{
			method: http.MethodGet,
			path:   "/message.txt:download",
		},
		stream: testStream{
			method: testv1connect.ContentServiceDownloadProcedure,
			rspHeader: http.Header{
				"Message": []string{"world"},
			},
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.DownloadRequest{
						Filename: "message.txt",
					},
				}},
			},
		},
		output: output{
			code: http.StatusOK,
			meta: http.Header{
				"Message": []string{"world"},
			},
		},
	}, {
		name: "DiscardUnknownQueryParams",
		input: input{
			method: http.MethodGet,
			path:   "/message.txt:download?unknownParam=1",
		},
		stream: testStream{
			method: testv1connect.ContentServiceDownloadProcedure,
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &testv1.DownloadRequest{
						Filename: "message.txt",
					},
				}},
				{out: &testMsgOut{
					msg: &testv1.DownloadResponse{
						File: &httpbody.HttpBody{
							ContentType: "text/plain",
							Data:        []byte("hello"),
						},
					},
				}},
				{out: &testMsgOut{
					msg: &testv1.DownloadResponse{
						File: &httpbody.HttpBody{
							Data: []byte(" world"),
						},
					},
				}},
			},
		},
		output: output{
			code:    http.StatusOK,
			rawBody: `hello world`,
			meta: http.Header{
				"Content-Type": []string{"text/plain"},
			},
		},
	}}

	scripts := &testScripts{}
	server := connect.NewServer()
	for _, serviceName := range []protoreflect.FullName{
		testv1connect.LibraryServiceName,
		testv1connect.ContentServiceName,
	} {
		desc, err := protoregistry.GlobalFiles.FindDescriptorByName(serviceName)
		require.NoError(t, err)
		service, ok := desc.(protoreflect.ServiceDescriptor)
		require.True(t, ok)
		for i := range service.Methods().Len() {
			server.Register(connect.Method{Spec: methodSpec(service.Methods().Get(i)), Handler: scripts.serve})
		}
	}
	mux := http.NewServeMux()
	require.NoError(t, Mount(mux, server, WithDiscardUnknownQueryParams(true)))
	codec := NewJSONCodec(protoregistry.GlobalTypes)

	buildRequest := func(t *testing.T, input input, compress bool) *http.Request {
		t.Helper()
		var contentType string
		var body io.Reader
		if input.body != nil {
			var data []byte
			if msg, ok := input.body.(*httpbody.HttpBody); ok {
				data = msg.GetData()
				contentType = msg.GetContentType()
			} else {
				var buf bytes.Buffer
				require.NoError(t, codec.MarshalWrite(t.Context(), &buf, input.body))
				data = buf.Bytes()
				contentType = "application/" + codec.Name()
			}
			if compress {
				data = gzipBytes(t, data)
			}
			body = bytes.NewReader(data)
		}
		req := httptest.NewRequestWithContext(t.Context(), input.method, input.path, body)
		maps.Copy(req.Header, input.meta)
		if compress {
			if body != nil {
				req.Header.Set("Content-Encoding", "gzip")
			}
			req.Header.Set("Accept-Encoding", "gzip")
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		query := req.URL.Query()
		maps.Copy(query, input.values)
		req.URL.RawQuery = query.Encode()
		return req
	}

	for _, compress := range []bool{false, true} {
		name := "identity"
		if compress {
			name = "gzip"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, testCase := range testRequests {
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()
					scripts.set(t, testCase.stream)
					defer scripts.del(t)

					req := buildRequest(t, testCase.input, compress)
					req.Header.Set("Test", t.Name())
					rsp := httptest.NewRecorder()
					mux.ServeHTTP(rsp, req)

					want := testCase.output
					if !assert.Equal(t, want.code, rsp.Code, "status code: %s", rsp.Body.String()) {
						return
					}
					for key, vals := range want.meta {
						assert.Equal(t, vals, rsp.Header().Values(key), "header %s", key)
					}
					body := rsp.Body.Bytes()
					if compress && rsp.Code == http.StatusOK && len(body) > 0 {
						assert.Equal(t, "gzip", rsp.Header().Get("Content-Encoding"))
					}
					if rsp.Header().Get("Content-Encoding") == "gzip" {
						body = gunzipBytes(t, body)
					}
					if want.body == nil {
						assert.Equal(t, want.rawBody, string(body), "body")
						return
					}
					require.NotEmpty(t, body, "body")
					switch expect := want.body.(type) {
					case proto.Message:
						got := expect.ProtoReflect().New().Interface()
						require.NoError(t, codec.UnmarshalRead(t.Context(), bytes.NewReader(body), got), "unmarshal body")
						assert.Empty(t, cmp.Diff(want.body, got, protocmp.Transform()))
					case string:
						var got, want any
						require.NoError(t, json.Unmarshal(body, &got))
						require.NoError(t, json.Unmarshal(([]byte)(expect), &want))
						assert.Equal(t, want, got)
					default:
						t.Fatalf("unsupported body type: %T", expect)
					}
				})
			}
		})
	}
}

func TestMount_SkipsForeignSchema(t *testing.T) {
	t.Parallel()

	server := connect.NewServer()
	server.Register(connect.Method{
		Spec: connect.Spec{
			StreamType: connect.StreamTypeUnary,
			Schema:     "not-a-method-descriptor",
			Procedure:  "/example.CustomService/Method",
		},
		Handler: func(context.Context, connect.Spec, connect.ServerStream) error { return nil },
	})

	mux := http.NewServeMux()
	require.NoError(t, Mount(mux, server))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/example.CustomService/Method", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMount_RejectsUnsupportedStreamType(t *testing.T) {
	t.Parallel()

	server := connect.NewServer()
	testv1connect.RegisterContentServiceHandler(server, testv1connect.UnimplementedContentServiceHandler{})

	require.NoError(t, Mount(http.NewServeMux(), server))

	err := Mount(http.NewServeMux(), server, WithRules(&annotations.HttpRule{
		Selector: "vanguard.test.v1.ContentService.Subscribe",
		Pattern:  &annotations.HttpRule_Post{Post: "/subscribe"},
		Body:     "*",
	}))
	require.ErrorContains(t, err, "stream type bidi not supported")

	err = Mount(http.NewServeMux(), server, WithRules(&annotations.HttpRule{
		Selector: "vanguard.test.v1.ContentService.Download",
		Pattern:  &annotations.HttpRule_Get{Get: "/download/{filename=**}"},
	}))
	require.ErrorContains(t, err, "requires a google.api.HttpBody response body")
}

func TestMount_UploadChunks(t *testing.T) {
	t.Parallel()

	var got []*testv1.UploadRequest
	serve := func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
		got = nil
		for {
			req := &testv1.UploadRequest{}
			err := stream.Receive(req)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			got = append(got, req)
		}
		return stream.Send(&emptypb.Empty{})
	}
	handler := mountTestHandler(t, methodSpec(methodDesc(t, "vanguard.test.v1.ContentService.Upload")), serve, WithMaxReadBytes(0))

	payload := bytes.Repeat([]byte("0123456789abcdef"), 5*1024)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/big.bin:upload", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, got, 3)
	assert.Equal(t, "big.bin", got[0].GetFilename())
	assert.Empty(t, got[1].GetFilename())
	var joined []byte
	for _, msg := range got {
		assert.Equal(t, "application/octet-stream", msg.GetFile().GetContentType())
		joined = append(joined, msg.GetFile().GetData()...)
	}
	assert.Equal(t, payload, joined)

	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/empty.bin:upload", http.NoBody)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Len(t, got, 1)
	assert.Equal(t, "empty.bin", got[0].GetFilename())
	assert.Empty(t, got[0].GetFile().GetData())
}

func TestMount_CompressionOptions(t *testing.T) {
	t.Parallel()

	body := `{"title":"tiny"}`
	post := func(handler http.Handler, encoding string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/shelves/s/books", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if encoding != "" {
			req.Header.Set("Content-Encoding", encoding)
		}
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	rec := post(bookEchoHandler(t), "zstd")
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
	assert.Equal(t, "gzip", rec.Header().Get("Accept-Encoding"))
	assert.Contains(t, rec.Body.String(), `unknown compression \"zstd\"`)

	rec = post(bookEchoHandler(t, WithCompressors()), "")
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	rec = post(bookEchoHandler(t, WithCompressors()), "gzip")
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestMount_CallInfoEncoding(t *testing.T) {
	t.Parallel()

	var info connect.CallInfo
	serve := func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
		got, ok := connect.CallInfoForServerContext(ctx)
		require.True(t, ok)
		info = *got
		req := &testv1.CreateBookRequest{}
		if err := stream.Receive(req); err != nil {
			return err
		}
		return stream.Send(req.GetBook())
	}
	handler := mountTestHandler(t, methodSpec(methodDesc(t, "vanguard.test.v1.LibraryService.CreateBook")), serve)
	body := []byte(`{"title":"tiny"}`)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/shelves/s/books", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "rest", info.Protocol)
	assert.Equal(t, "json", info.Codec)
	assert.Equal(t, "identity", info.RequestEncoding)
	assert.Equal(t, "identity", info.ResponseEncoding)

	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/shelves/s/books", bytes.NewReader(gzipBytes(t, body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept-Encoding", "gzip")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, "gzip", info.RequestEncoding)
	assert.Equal(t, "gzip", info.ResponseEncoding)
}

func TestMount_MaxReadBytes(t *testing.T) {
	t.Parallel()

	handler := bookEchoHandler(t, WithMaxReadBytes(64))
	body := `{"title":"` + strings.Repeat("a", 4096) + `"}`
	compressed := gzipBytes(t, []byte(body))
	require.Less(t, len(compressed), 64)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/shelves/s/books", bytes.NewReader(compressed))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":8`)
	assert.Contains(t, rec.Body.String(), "request body exceeds 64 bytes")

	handler = bookEchoHandler(t, WithMaxReadBytes(0))
	body = `{"title":"` + strings.Repeat("a", 5*1024*1024) + `"}`
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/shelves/s/books", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String()[:min(200, rec.Body.Len())])
}

type testStream struct {
	method    string
	reqHeader http.Header
	rspHeader http.Header
	msgs      []testMsg
}

type testMsg struct {
	in  *testMsgIn
	out *testMsgOut
}

type testMsgIn struct {
	msg proto.Message
	err *connect.Error
}

type testMsgOut struct {
	msg proto.Message
	err *connect.Error
}

type testScript struct {
	*testing.T
	testStream
}

type testScripts struct {
	scripts sync.Map
}

func (s *testScripts) set(t *testing.T, stream testStream) {
	t.Helper()
	s.scripts.Store(t.Name(), &testScript{T: t, testStream: stream})
}

func (s *testScripts) del(t *testing.T) {
	t.Helper()
	s.scripts.Delete(t.Name())
}

func (s *testScripts) serve(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return errors.New("no CallInfo")
	}
	val, _ := s.scripts.Load(info.RequestHeader().Get("Test"))
	script, ok := val.(*testScript)
	if !ok {
		return fmt.Errorf("no script for %q", info.RequestHeader().Get("Test"))
	}
	if !assert.Equal(script.T, script.method, spec.Procedure) {
		return connect.Errorf(connect.CodeFailedPrecondition, "expected %s, got %s", script.method, spec.Procedure)
	}
	for key, vals := range script.reqHeader {
		assert.Equal(script.T, vals[0], info.RequestHeader().Get(key), "request header %s", key)
	}
	for key, vals := range script.rspHeader {
		info.ResponseHeader().SetValues(key, vals)
	}
	for _, msg := range script.msgs {
		switch {
		case msg.in != nil:
			got := msg.in.msg.ProtoReflect().New().Interface()
			err := stream.Receive(got)
			if msg.in.err != nil {
				assert.Equal(script.T, msg.in.err.Code(), connect.CodeOf(err), "receive error")
				if err == nil {
					err = errors.New("expecting an error receiving message but got none")
				}
				return err
			}
			if err != nil {
				return err
			}
			if diff := cmp.Diff(msg.in.msg, got, protocmp.Transform()); diff != "" {
				assert.Fail(script.T, "message didn't match", diff)
				return fmt.Errorf("message didn't match: %s", diff)
			}
		case msg.out != nil && msg.out.err != nil:
			return msg.out.err
		case msg.out != nil:
			if err := stream.Send(msg.out.msg); err != nil {
				return err
			}
		}
	}
	return nil
}

func methodSpec(desc protoreflect.MethodDescriptor) connect.Spec {
	var streamType connect.StreamType
	if desc.IsStreamingClient() {
		streamType |= connect.StreamTypeClient
	}
	if desc.IsStreamingServer() {
		streamType |= connect.StreamTypeServer
	}
	return connect.Spec{
		Procedure:  "/" + string(desc.Parent().FullName()) + "/" + string(desc.Name()),
		Schema:     desc,
		StreamType: streamType,
	}
}

func methodDesc(t *testing.T, name protoreflect.FullName) protoreflect.MethodDescriptor {
	t.Helper()
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
	require.NoError(t, err)
	methodDesc, ok := desc.(protoreflect.MethodDescriptor)
	require.True(t, ok)
	return methodDesc
}

func mountTestHandler(t *testing.T, spec connect.Spec, serve connect.ServerFunc, opts ...Option) http.Handler {
	t.Helper()
	server := connect.NewServer()
	server.Register(connect.Method{Spec: spec, Handler: serve})
	mux := http.NewServeMux()
	require.NoError(t, Mount(mux, server, opts...))
	return mux
}

func bookEchoHandler(t *testing.T, opts ...Option) http.Handler {
	t.Helper()
	serve := func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
		req := &testv1.CreateBookRequest{}
		if err := stream.Receive(req); err != nil {
			return err
		}
		return stream.Send(req.GetBook())
	}
	return mountTestHandler(t, methodSpec(methodDesc(t, "vanguard.test.v1.LibraryService.CreateBook")), serve, opts...)
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, err := writer.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func gunzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	out, err := io.ReadAll(reader)
	require.NoError(t, err)
	return out
}
