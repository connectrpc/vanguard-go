// Copyright 2023-2025 Buf Technologies, Inc.
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestMux_RESTxRPC(t *testing.T) {
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

	server := httptest.NewServer(serveMux)
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(serverURL)
	proxy.Transport = server.Client().Transport

	type testMux struct {
		name    string
		handler http.Handler
	}
	wrapHandler := func(protocol Protocol, codec, compression string, handler http.Handler) http.Handler {
		opts := []ServiceOption{
			WithTargetProtocols(protocol),
			WithTargetCodecs(codec),
		}
		if compression != CompressionIdentity {
			opts = append(opts, WithTargetCompression(compression))
		} else {
			opts = append(opts, WithNoTargetCompression())
		}

		opts = append(opts, WithRESTUnmarshalOptions(RESTUnmarshalOptions{DiscardUnknownQueryParams: true}))

		svcHandler := protocolAssertMiddleware(protocol, codec, compression, handler)

		services := make([]*Service, len(serviceNames))
		for i, svcName := range serviceNames {
			services[i] = NewService(svcName, svcHandler, opts...)
		}
		handler, err := NewTranscoder(services)
		require.NoError(t, err)
		return handler
	}
	var muxes []testMux
	for _, protocol := range protocols {
		for _, codec := range codecs {
			for _, compression := range compressions {
				muxes = append(muxes, testMux{
					name:    fmt.Sprintf("%s_%s_%s", protocol, codec, compression),
					handler: wrapHandler(protocol, codec, compression, serveMux),
				})
				muxes = append(muxes, testMux{
					name:    fmt.Sprintf("proxy/%s_%s_%s", protocol, codec, compression),
					handler: wrapHandler(protocol, codec, compression, proxy),
				})
			}
		}
	}

	type input struct {
		method string
		path   string
		values url.Values
		body   proto.Message
		meta   http.Header
	}
	buildRequest := func(t *testing.T, input input, codec Codec, comp *compressionPool) *http.Request {
		t.Helper()

		var contentType string
		var isCompressed bool
		var body io.Reader
		if input.body != nil { //nolint:nestif
			if restIsHTTPBody(input.body.ProtoReflect().Descriptor(), nil) {
				msg, _ := input.body.(*httpbody.HttpBody)
				body = bytes.NewReader(msg.GetData())
				contentType = msg.GetContentType()
			} else {
				b, err := codec.MarshalAppend(nil, input.body)
				if err != nil {
					t.Fatal(err)
				}
				buf := bytes.NewBuffer(b)
				if comp != nil {
					out := &bytes.Buffer{}
					require.NoError(t, comp.compress(out, buf))
					buf = out
					isCompressed = true
				}
				body = buf
				contentType = "application/" + codec.Name() // JSON
			}
		}
		req := httptest.NewRequest(input.method, input.path, body)
		for key, values := range input.meta {
			req.Header[key] = values
		}
		req.Header["X-Server-Timeout"] = []string{"30"}
		if isCompressed {
			req.Header["Content-Encoding"] = []string{comp.Name()}
		}
		if contentType != "" {
			req.Header["Content-Type"] = []string{contentType}
		}
		query := req.URL.Query()
		for key, values := range input.values {
			query[key] = values
		}
		req.URL.RawQuery = query.Encode()
		return req
	}
	type output struct {
		code    int
		body    any
		rawBody string // if not proto.Message
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
		},
	}, {
		name: "GetBook-NotAllowed",
		input: input{
			method: http.MethodPut,
			path:   "/v1/shelves/1/books/1",
		},
		output: output{
			code:    http.StatusMethodNotAllowed,
			rawBody: "Method Not Allowed\n",
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
					err: newConnectError(connect.CodePermissionDenied, "permission denied"),
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
		name: "CreateBook",
		input: input{
			method: http.MethodPost,
			path:   "/v1/shelves/1/books",
			values: url.Values{
				// Fields by JSON name or proto name are supported.
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
		// Checks errors on decoding the request body.
		name: "GetCheckout-Error",
		input: input{
			method: http.MethodGet,
			path:   "/v2/checkouts/nan", // invalid ID
			body:   nil,
			meta: http.Header{
				"Message": []string{"hello"},
			},
		},
		stream: testStream{
			method: testv1connect.LibraryServiceGetBookProcedure,
			msgs:   []testMsg{},
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

	type testOpt struct {
		name string
		mux  testMux
		comp *compressionPool
	}
	var testOpts []testOpt
	for _, compression := range compressions {
		for _, mux := range muxes {
			var comp *compressionPool
			switch compression {
			case CompressionGzip:
				comp = newCompressionPool(
					CompressionGzip, defaultGzipCompressor, defaultGzipDecompressor,
				)
			case CompressionIdentity:
				// nil
			default:
				t.Fatalf("unknown compression %q", compression)
			}

			testOpts = append(testOpts, testOpt{
				name: fmt.Sprintf("%s/%s", compression, mux.name),
				mux:  mux,
				comp: comp,
			})
		}
	}
	codec := NewJSONCodec(protoregistry.GlobalTypes)
	for _, opts := range testOpts {
		t.Run(opts.name, func(t *testing.T) {
			t.Parallel()
			for _, testCase := range testRequests {
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()

					interceptor.set(t, testCase.stream)
					defer interceptor.del(t)

					req := buildRequest(t, testCase.input, codec, opts.comp)
					req.Header.Set("Test", t.Name()) // for interceptor
					t.Log(req.Method, req.URL.String())

					debug, _ := httputil.DumpRequest(req, true)
					t.Log("req:", string(debug))

					rsp := httptest.NewRecorder()

					func() {
						// Capture http.ErrAbortHandler panics.
						defer func() {
							if recovered := recover(); recovered != nil {
								if err, ok := recovered.(error); ok {
									if errors.Is(err, http.ErrAbortHandler) {
										return
									}
								}
								t.Error("unexpected panic:", recovered)
							}
						}()
						// Inject http.Server into the request context to convince
						// httputil.ReverseProxy we are in a server context.
						svr := &http.Server{} //nolint:gosec // dummy server for testing
						req = req.WithContext(context.WithValue(req.Context(), http.ServerContextKey, svr))
						opts.mux.handler.ServeHTTP(rsp, req)
					}()

					result := rsp.Result()
					defer result.Body.Close()
					debug, _ = httputil.DumpResponse(result, true)
					t.Log("rsp:", string(debug))

					// Check response
					want := testCase.output
					decomp := opts.comp

					if !assert.Equal(t, want.code, rsp.Code, "status code") {
						return
					}
					assert.Subset(t, rsp.Header(), want.meta, "headers")
					if want.body == nil {
						assert.Equal(t, want.rawBody, rsp.Body.String(), "body")
						return
					}
					require.NotEmpty(t, rsp.Body.String(), "body")
					body := rsp.Body
					if decomp != nil && rsp.Header().Get("Content-Encoding") != "" {
						out := &bytes.Buffer{}
						require.NoError(t, decomp.decompress(out, body))
						body = out
					}
					switch expect := want.body.(type) {
					case proto.Message:
						got := expect.ProtoReflect().New().Interface()
						require.NoError(t, codec.Unmarshal(body.Bytes(), got), "unmarshal body")
						assert.Empty(t, cmp.Diff(want.body, got, protocmp.Transform()))
					case string:
						var got, want any
						require.NoError(t, json.Unmarshal(body.Bytes(), &got))
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

// testStreamingServiceHandler implements StreamingService for testing SSE.
type testStreamingServiceHandler struct {
	testv1connect.UnimplementedStreamingServiceHandler
}

func (h *testStreamingServiceHandler) CountToTen(
	_ context.Context,
	req *connect.Request[testv1.CountRequest],
	stream *connect.ServerStream[testv1.CountResponse],
) error {
	for i := int32(1); i <= 10; i++ {
		if err := stream.Send(&testv1.CountResponse{
			Number: i,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (h *testStreamingServiceHandler) WatchBooks(
	_ context.Context,
	req *connect.Request[testv1.WatchBooksRequest],
	stream *connect.ServerStream[testv1.BookUpdate],
) error {
	parent := req.Msg.Parent
	if parent == "" {
		parent = "shelves/default"
	}

	updates := []struct {
		updateType testv1.BookUpdate_UpdateType
		bookName   string
	}{
		{testv1.BookUpdate_CREATED, parent + "/books/book-1"},
		{testv1.BookUpdate_UPDATED, parent + "/books/book-1"},
		{testv1.BookUpdate_DELETED, parent + "/books/book-1"},
	}

	for _, upd := range updates {
		if err := stream.Send(&testv1.BookUpdate{
			Type:     upd.updateType,
			BookName: upd.bookName,
		}); err != nil {
			return err
		}
	}
	return nil
}

func TestMux_RESTxRPC_SSE(t *testing.T) {
	t.Parallel()

	// Test server-streaming with SSE enabled
	protocols := []Protocol{
		ProtocolGRPC,
		ProtocolGRPCWeb,
		ProtocolConnect,
	}

	for _, protocol := range protocols {
		protocol := protocol
		t.Run(protocol.String(), func(t *testing.T) {
			t.Parallel()

			// Set up backend RPC server
			serveMux := http.NewServeMux()
			serveMux.Handle(testv1connect.NewStreamingServiceHandler(
				&testStreamingServiceHandler{},
			))

			server := httptest.NewServer(serveMux)
			t.Cleanup(server.Close)
			serverURL, err := url.Parse(server.URL)
			require.NoError(t, err)
			proxy := httputil.NewSingleHostReverseProxy(serverURL)
			proxy.Transport = server.Client().Transport

			// Create transcoder with SSE enabled
			handler := protocolAssertMiddleware(protocol, CodecJSON, CompressionIdentity, proxy)
			svcHandler := NewService(
				testv1connect.StreamingServiceName,
				handler,
				WithTargetProtocols(protocol),
				WithTargetCodecs(CodecJSON),
				WithNoTargetCompression(),
				WithRESTServerSentEvents())
			services := []*Service{svcHandler}
			transcoder, err := NewTranscoder(services)
			require.NoError(t, err)

			t.Run("CountToTen_WithSSE", func(t *testing.T) {
				t.Parallel()

				req := httptest.NewRequest(http.MethodGet, "/v1/count?delay_ms=10", nil)
				req.Header.Set("Accept", "text/event-stream")

				rsp := httptest.NewRecorder()
				transcoder.ServeHTTP(rsp, req)

				result := rsp.Result()
				defer result.Body.Close()

				// Verify SSE response headers
				assert.Equal(t, http.StatusOK, result.StatusCode)
				assert.Equal(t, "text/event-stream", result.Header.Get("Content-Type"))
				assert.Equal(t, "no-cache", result.Header.Get("Cache-Control"))
				assert.Equal(t, "keep-alive", result.Header.Get("Connection"))

				// Read and parse SSE events
				body, err := io.ReadAll(result.Body)
				require.NoError(t, err)

				// Verify SSE format
				bodyStr := string(body)
				t.Log("SSE response:", bodyStr)

				// Should have open event at the beginning
				assert.Contains(t, bodyStr, "event: open")

				// Should have 10 message events
				assert.Contains(t, bodyStr, `data: {"number":1}`)
				assert.Contains(t, bodyStr, `data: {"number":10}`)

				// Should have completion event
				assert.Contains(t, bodyStr, "event: complete")

				// There is no id field specified on this service, so it shouldn't appear
				assert.NotContains(t, bodyStr, "id:")
				// Message is the default event type, is inferable does not need to be included
				assert.NotContains(t, bodyStr, `event: message`)
			})

			t.Run("WatchBooks_WithSSE", func(t *testing.T) {
				t.Parallel()

				req := httptest.NewRequest(http.MethodGet, "/v1/shelves/test-shelf/books:watch", nil)
				req.Header.Set("Accept", "text/event-stream")

				rsp := httptest.NewRecorder()
				transcoder.ServeHTTP(rsp, req)

				result := rsp.Result()
				defer result.Body.Close()

				// Verify SSE response
				assert.Equal(t, http.StatusOK, result.StatusCode)
				assert.Equal(t, "text/event-stream", result.Header.Get("Content-Type"))

				body, err := io.ReadAll(result.Body)
				require.NoError(t, err)

				bodyStr := string(body)
				t.Log("SSE response:", bodyStr)

				// Verify open event
				assert.Contains(t, bodyStr, "event: open")

				// WatchBooks uses custom event names, and omits fields from response_body: SSE_EVENT=type,SSE_OMIT
				// So we should see event: CREATED, event: UPDATED, etc. instead of event: message
				// and the field type should NOT be in the data.
				assert.Contains(t, bodyStr, "event: CREATED")
				assert.NotContains(t, bodyStr, "event: message")
				assert.NotContains(t, bodyStr, `"type":"`)
				assert.Contains(t, bodyStr, `"bookName":"shelves/test-shelf/books/`)
				assert.Contains(t, bodyStr, "event: complete")
			})

			t.Run("CountToTen_WithoutSSE", func(t *testing.T) {
				t.Parallel()

				// Without Accept: text/event-stream, should fail
				req := httptest.NewRequest(http.MethodGet, "/v1/count", nil)

				rsp := httptest.NewRecorder()
				transcoder.ServeHTTP(rsp, req)

				result := rsp.Result()
				defer result.Body.Close()

				// Should return 415 Unsupported Media Type
				assert.Equal(t, http.StatusUnsupportedMediaType, result.StatusCode)

				body, err := io.ReadAll(result.Body)
				require.NoError(t, err)

				assert.Contains(t, string(body), "stream type server not supported")
			})
		})
	}
}

func TestMux_RESTxRPC_SSE_CustomEventField(t *testing.T) {
	t.Parallel()

	protocols := []Protocol{ProtocolConnect, ProtocolGRPC, ProtocolGRPCWeb}
	for _, protocol := range protocols {
		protocol := protocol
		t.Run(protocol.String(), func(t *testing.T) {
			t.Parallel()

			// Create handler
			handler := &testStreamingServiceHandler{}
			connectPath, connectHandler := testv1connect.NewStreamingServiceHandler(handler)
			serveMux := http.NewServeMux()
			serveMux.Handle(connectPath, connectHandler)
			server := httptest.NewServer(serveMux)
			t.Cleanup(server.Close)
			serverURL, err := url.Parse(server.URL)
			require.NoError(t, err)
			proxy := httputil.NewSingleHostReverseProxy(serverURL)
			proxy.Transport = server.Client().Transport

			// Create transcoder with SSE enabled
			// Event field configuration is now per-RPC via response_body directives
			handler2 := protocolAssertMiddleware(protocol, CodecJSON, CompressionIdentity, proxy)
			svcHandler := NewService(
				testv1connect.StreamingServiceName,
				handler2,
				WithTargetProtocols(protocol),
				WithTargetCodecs(CodecJSON),
				WithNoTargetCompression(),
				WithRESTServerSentEvents(), // SSE enabled, field config comes from proto annotations
			)
			services := []*Service{svcHandler}
			transcoder, err := NewTranscoder(services)
			require.NoError(t, err)

			t.Run("WatchBooks_WithCustomEventField", func(t *testing.T) {
				t.Parallel()

				req := httptest.NewRequest(http.MethodGet, "/v1/shelves/test-shelf/books:watch", nil)
				req.Header.Set("Accept", "text/event-stream")

				rsp := httptest.NewRecorder()
				transcoder.ServeHTTP(rsp, req)

				result := rsp.Result()
				defer result.Body.Close()

				// Verify SSE response
				assert.Equal(t, http.StatusOK, result.StatusCode)
				assert.Equal(t, "text/event-stream", result.Header.Get("Content-Type"))

				body, err := io.ReadAll(result.Body)
				require.NoError(t, err)

				bodyStr := string(body)
				t.Log("SSE response with custom event field:", bodyStr)

				// Verify open event
				assert.Contains(t, bodyStr, "event: open")

				// Verify custom event names from "type" field
				assert.Contains(t, bodyStr, "event: CREATED")
				assert.Contains(t, bodyStr, "event: UPDATED")
				assert.Contains(t, bodyStr, "event: DELETED")

				// Verify completion event
				assert.Contains(t, bodyStr, "event: complete")

				// Verify book names are still in data
				assert.Contains(t, bodyStr, `"bookName":"shelves/test-shelf/books/`)
			})
		})
	}
}

func TestMux_RESTxRPC_SSE_WithRESTStreaming(t *testing.T) {
	t.Parallel()

	// Test the new WithRESTServerSentEvents API
	handler := &testStreamingServiceHandler{}
	connectPath, connectHandler := testv1connect.NewStreamingServiceHandler(handler)
	serveMux := http.NewServeMux()
	serveMux.Handle(connectPath, connectHandler)
	server := httptest.NewServer(serveMux)
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(serverURL)
	proxy.Transport = server.Client().Transport

	// Create transcoder using the WithRESTServerSentEvents API
	// Field-specific config comes from proto response_body directives
	handler2 := protocolAssertMiddleware(ProtocolConnect, CodecJSON, CompressionIdentity, proxy)
	svcHandler := NewService(
		testv1connect.StreamingServiceName,
		handler2,
		WithTargetProtocols(ProtocolConnect),
		WithTargetCodecs(CodecJSON),
		WithNoTargetCompression(),
		WithRESTServerSentEvents(),
	)
	services := []*Service{svcHandler}
	transcoder, err := NewTranscoder(services)
	require.NoError(t, err)

	t.Run("WatchBooks_WithNewAPI", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/v1/shelves/test-shelf/books:watch", nil)
		req.Header.Set("Accept", "text/event-stream")

		rsp := httptest.NewRecorder()
		transcoder.ServeHTTP(rsp, req)

		result := rsp.Result()
		defer result.Body.Close()

		assert.Equal(t, http.StatusOK, result.StatusCode)
		assert.Equal(t, "text/event-stream", result.Header.Get("Content-Type"))

		body, err := io.ReadAll(result.Body)
		require.NoError(t, err)

		bodyStr := string(body)

		// Verify custom event names work with new API
		assert.Contains(t, bodyStr, "event: CREATED")
		assert.Contains(t, bodyStr, "event: UPDATED")
		assert.Contains(t, bodyStr, "event: DELETED")
		assert.Contains(t, bodyStr, "event: complete")
	})

	t.Run("CountToTen_WithNewAPI", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/v1/count?delay_ms=10", nil)
		req.Header.Set("Accept", "text/event-stream")

		rsp := httptest.NewRecorder()
		transcoder.ServeHTTP(rsp, req)

		result := rsp.Result()
		defer result.Body.Close()

		assert.Equal(t, http.StatusOK, result.StatusCode)

		body, err := io.ReadAll(result.Body)
		require.NoError(t, err)

		bodyStr := string(body)

		// Should fallback to "message" when field doesn't exist
		assert.Contains(t, bodyStr, `"number":10`)
	})
}
