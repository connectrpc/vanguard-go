// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/bufbuild/vanguard-go/internal/gen/buf/vanguard/test/v1"
	"github.com/bufbuild/vanguard-go/internal/gen/buf/vanguard/test/v1/testv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestMux_RESTxRPC(t *testing.T) {
	t.Parallel()

	services := []protoreflect.FullName{
		testv1connect.LibraryServiceName,
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

	type testMux struct {
		name string
		mux  *Mux
	}
	makeMux := func(protocol Protocol, codec, compression string) testMux {
		opts := []ServiceOption{
			WithProtocols(protocol),
			WithCodecs(codec),
		}
		if compression != CompressionIdentity {
			opts = append(opts, WithCompression(compression))
		} else {
			opts = append(opts, WithNoCompression())
		}
		hdlr := protocolAssertMiddleware(protocol, codec, compression, serveMux)
		name := fmt.Sprintf("%s_%s_%s", protocol, codec, compression)

		mux := &Mux{}
		for _, service := range services {
			if err := mux.RegisterServiceByName(
				hdlr, service, opts...,
			); err != nil {
				t.Fatal(err)
			}
		}
		return testMux{name: name, mux: mux}
	}
	muxes := []testMux{}
	for _, protocol := range protocols {
		for _, codec := range codecs {
			for _, compression := range compressions {
				muxes = append(muxes, makeMux(protocol, codec, compression))
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

		var isCompressed bool
		var body io.Reader
		if input.body != nil {
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
		}
		req := httptest.NewRequest(input.method, input.path, body)
		for key, values := range input.meta {
			req.Header[key] = values
		}
		if isCompressed {
			req.Header["Content-Encoding"] = []string{comp.Name()}
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
		body    proto.Message
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
			reqHeader: http.Header{
				"Message": []string{"hello"},
			},
			rspHeader: http.Header{
				"Message": []string{"world"},
			},
			msgs: []testMsg{
				{in: &testMsgIn{
					method: testv1connect.LibraryServiceGetBookProcedure,
					msg:    &testv1.GetBookRequest{Name: "shelves/1/books/1"},
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
			msgs: []testMsg{
				{in: &testMsgIn{
					method: testv1connect.LibraryServiceGetBookProcedure,
					msg:    &testv1.GetBookRequest{Name: "shelves/1/books/1"},
				}},
				{out: &testMsgOut{
					err: connect.NewError(
						connect.CodePermissionDenied,
						fmt.Errorf("permission denied")),
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
				"book_id":    []string{"1"},
				"request_id": []string{"2"},
			},
			body: &testv1.Book{
				Title:  "The Art of Computer Programming",
				Author: "Donald E. Knuth",
			},
		},
		stream: testStream{
			msgs: []testMsg{
				{in: &testMsgIn{
					method: testv1connect.LibraryServiceCreateBookProcedure,
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
	}}
	// TODO: test download and upload streaming of google.api.httpbody.

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
					CompressionGzip, DefaultGzipCompressor, DefaultGzipDecompressor,
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
	codec := DefaultJSONCodec(protoregistry.GlobalTypes)
	for _, opts := range testOpts {
		opts := opts
		t.Run(opts.name, func(t *testing.T) {
			t.Parallel()
			for _, testCase := range testRequests {
				testCase := testCase
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
					opts.mux.mux.AsHandler().ServeHTTP(rsp, req)

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
					got := want.body.ProtoReflect().New().Interface()
					require.NoError(t, codec.Unmarshal(body.Bytes(), got), "unmarshal body")
					assert.Empty(t, cmp.Diff(want.body, got, protocmp.Transform()))
				})
			}
		})
	}
}
