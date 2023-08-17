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
	"net/url"
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1"
	"github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1/testv1connect"
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
		"buf.vanguard.test.v1.LibraryService",
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
		// TODO: grpc-web & connect
	}

	var interceptor testInterceptor
	serveMux := http.NewServeMux()
	serveMux.Handle(testv1connect.NewLibraryServiceHandler(
		testv1connect.UnimplementedLibraryServiceHandler{},
		connect.WithInterceptors(&interceptor),
	))

	// protocolMiddelware asserts the request headers for the given protocol.
	protocolMiddelware := func(
		protocol Protocol, codec string, compression string,
		next http.Handler,
	) http.HandlerFunc {
		return func(rsp http.ResponseWriter, req *http.Request) {
			var wantHdr http.Header
			switch protocol {
			case ProtocolGRPC:
				wantHdr = http.Header{
					"Content-Type": []string{
						fmt.Sprintf("application/grpc+%s", codec),
					},
					"Grpc-Encoding": []string{compression},
				}
			default:
				http.Error(rsp, "unknown protocol", http.StatusInternalServerError)
				return
			}
			if err := equalHeaders(wantHdr, req.Header); err != nil {
				http.Error(rsp, err.Error(), http.StatusInternalServerError)
				return
			}
			next.ServeHTTP(rsp, req)
		}
	}

	type testMux struct {
		name string
		mux  *Mux
	}
	makeMux := func(protocol Protocol, codec, compression string) testMux {
		opts := []ServiceOption{
			WithProtocols(protocol),
			WithCodecs(codec),
		}
		if compression != "identity" {
			opts = append(opts, WithCompression(compression))
		}
		hdlr := protocolMiddelware(protocol, codec, compression, serveMux)
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
	buildRequest := func(t *testing.T, input input, codec Codec, comp connect.Compressor) *http.Request {
		t.Helper()

		var body io.Reader
		if input.body != nil {
			b, err := codec.MarshalAppend(nil, input.body)
			if err != nil {
				t.Fatal(err)
			}
			buf := bytes.NewBuffer(b)
			if comp != nil {
				out := &bytes.Buffer{}
				comp.Reset(out)
				_, err := io.Copy(out, buf)
				require.NoError(t, err)
				require.NoError(t, comp.Close())
				buf = out
			}
			body = buf
		}
		req := httptest.NewRequest(input.method, input.path, body)
		for key, values := range input.meta {
			req.Header[key] = values
		}
		for key, values := range input.values {
			req.URL.Query()[key] = values
		}
		return req
	}
	type output struct {
		code int
		body proto.Message
		meta http.Header
	}
	checkResponse := func(t *testing.T, rsp *httptest.ResponseRecorder, want output, codec Codec, decomp connect.Decompressor) {
		t.Helper()

		if !assert.Equal(t, want.code, rsp.Code, "status code") {
			return
		}
		assert.Equal(t, want.meta, rsp.Header(), "headers")
		if want.body == nil {
			assert.Empty(t, rsp.Body.String(), "body")
			return
		}
		require.NotEmpty(t, rsp.Body.String(), "body")
		body := rsp.Body
		if decomp != nil && rsp.Header().Get("Content-Encoding") != "" {
			out := &bytes.Buffer{}
			require.NoError(t, decomp.Reset(body))
			_, err := io.Copy(out, body)
			require.NoError(t, err)
			require.NoError(t, decomp.Close())
			body = out
		}
		got := want.body.ProtoReflect().New().Interface()
		require.NoError(t, codec.Unmarshal(body.Bytes(), got), "unmarshal body")
		assert.Empty(t, cmp.Diff(want.body, got, protocmp.Transform()))
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
					method: "/vanguard.library.v1.LibraryService/GetBook",
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
			code: http.StatusMethodNotAllowed,
			// TODO: status?
			// msg: &status.Status{
			// 	Code:    int32(connect.CodeUnimplemented),
			// 	Message: "method not allowed",
			// },
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
					method: "/vanguard.library.v1.LibraryService/GetBook",
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
	}}

	type testCase struct {
		name         string
		req          testRequest
		mux          testMux
		compressor   connect.Compressor
		decompressor connect.Decompressor
	}
	var testCases []testCase
	for _, testRequest := range testRequests {
		for _, compression := range compressions {
			for _, mux := range muxes {
				var comp connect.Compressor
				var decomp connect.Decompressor
				switch compression {
				case CompressionGzip:
					comp = DefaultGzipCompressor()
					decomp = DefaultGzipDecompressor()
				case CompressionIdentity:
					// nil
				default:
					t.Fatalf("unknown compression %q", compression)
				}

				testCases = append(testCases, testCase{
					name:         fmt.Sprintf("%s_%s/%s", testRequest.name, compression, mux.name),
					req:          testRequest,
					mux:          mux,
					compressor:   comp,
					decompressor: decomp,
				})
			}
		}
	}
	for _, testCase := range testCases {
		testCase := testCase
		codec := DefaultJSONCodec(protoregistry.GlobalTypes)
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			t.Skip("TODO: implement")

			interceptor.set(t, testCase.req.stream)
			defer interceptor.del(t)

			req := buildRequest(t, testCase.req.input, codec, testCase.compressor)
			req.Header.Set("Test", t.Name()) // for interceptor
			t.Log(req.Method, req.URL.String())

			rsp := httptest.NewRecorder()
			testCase.mux.mux.AsHandler().ServeHTTP(rsp, req)

			checkResponse(t, rsp, testCase.req.output, codec, testCase.decompressor)
		})
	}
}
