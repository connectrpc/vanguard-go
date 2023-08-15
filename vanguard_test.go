// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/bufbuild/vanguard"
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

	var interceptor testInterceptor
	serveMux := http.NewServeMux()
	serveMux.Handle(testv1connect.NewLibraryServiceHandler(
		testv1connect.UnimplementedLibraryServiceHandler{},
		connect.WithInterceptors(interceptor.unary()),
	))

	services := []protoreflect.FullName{
		"buf.vanguard.test.v1.LibraryService",
	}
	codecs := []string{
		"json",
		"proto",
	}
	compressions := []string{
		"gzip",
		"identity",
	}
	protocols := []vanguard.Protocol{
		vanguard.ProtocolGRPC,
		// TODO: grpc-web & connect
	}

	// protocolMiddelware asserts the request headers for the given protocol.
	protocolMiddelware := func(
		protocol vanguard.Protocol, codec string, compression string,
		next http.Handler,
	) http.HandlerFunc {
		return func(rsp http.ResponseWriter, req *http.Request) {
			var wantHdr http.Header
			switch protocol {
			case vanguard.ProtocolGRPC:
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
		mux  *vanguard.Mux
	}
	makeMux := func(protocol vanguard.Protocol, codec, compression string) testMux {
		t.Helper()

		opts := []vanguard.ServiceOption{
			vanguard.WithProtocols(protocol),
			vanguard.WithCodecs(codec),
		}
		if compression != "identity" {
			opts = append(opts, vanguard.WithCompression(compression))
		}
		hdlr := protocolMiddelware(protocol, codec, compression, serveMux)
		name := fmt.Sprintf("%s_%s_%s", protocol, codec, compression)

		mux := &vanguard.Mux{}
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
	buildRequest := func(t *testing.T, input input, codec vanguard.Codec, comp connect.Compressor) *http.Request {
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
	checkResponse := func(t *testing.T, rsp *httptest.ResponseRecorder, want output, codec vanguard.Codec, decomp connect.Decompressor) {
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
				case "gzip":
					comp = vanguard.DefaultGzipCompressor()
					decomp = vanguard.DefaultGzipDecompressor()
				case "identity":
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
		codec := vanguard.DefaultJSONCodec(protoregistry.GlobalTypes)
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			t.Skip("TODO: implement")

			interceptor.set(t.Name(), testCase.req.stream)
			defer interceptor.del(t.Name())

			req := buildRequest(t, testCase.req.input, codec, testCase.compressor)
			req.Header.Set("Test", t.Name()) // for interceptor
			t.Log(req.Method, req.URL.String())

			rsp := httptest.NewRecorder()
			testCase.mux.mux.AsHandler().ServeHTTP(rsp, req)

			checkResponse(t, rsp, testCase.req.output, codec, testCase.decompressor)
		})
	}
}

type testStream struct {
	reqHeader  http.Header // expected
	rspHeader  http.Header // out
	rspTrailer http.Header // out
	msgs       []testMsg   // in, out
}

type testMsgIn struct {
	method string
	msg    proto.Message
}
type testMsgOut struct {
	msg proto.Message
	err error
}
type testMsg struct {
	in  *testMsgIn
	out *testMsgOut
}

func (o *testMsg) getIn() (*testMsgIn, error) {
	if o == nil || o.in == nil {
		return nil, fmt.Errorf("missing input message")
	}
	return o.in, nil
}
func (o *testMsg) getOut() (*testMsgOut, error) {
	if o == nil || o.out == nil {
		return nil, fmt.Errorf("missing output message")
	}
	return o.out, nil
}

type testInterceptor struct {
	sync.Map
}

func (o *testInterceptor) get(testcase string) (testStream, bool) {
	val, ok := o.Load(testcase)
	if !ok {
		return testStream{}, false
	}
	stream, ok := val.(testStream)
	return stream, ok
}
func (o *testInterceptor) set(testcase string, stream testStream) {
	o.Store(testcase, stream)
}
func (o *testInterceptor) del(testcase string) {
	o.Delete(testcase)
}

func (o *testInterceptor) unary() connect.UnaryInterceptorFunc {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return connect.UnaryFunc(func(
			ctx context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			val := req.Header().Get("test")
			if val == "" {
				return next(ctx, req)
			}
			stream, ok := o.get(val)
			if !ok {
				return nil, fmt.Errorf("invalid testCase header: %s", val)
			}
			if err := equalHeaders(stream.reqHeader, req.Header()); err != nil {
				return nil, err
			}
			if len(stream.msgs) != 2 {
				return nil, fmt.Errorf("expected 2 messages, got %d", len(stream.msgs))
			}
			inn, err := stream.msgs[0].getIn()
			if err != nil {
				return nil, err
			}
			out, err := stream.msgs[1].getOut()
			if err != nil {
				return nil, err
			}
			if inn.method != "" && req.Spec().Procedure != inn.method {
				err := fmt.Errorf("expected %s, got %s", inn.method, req.Spec().Procedure)
				return nil, err
			}
			msg, ok := req.Any().(proto.Message)
			if !ok {
				return nil, fmt.Errorf("expected proto.Message, got %T", req.Any())
			}
			diff := cmp.Diff(msg, inn.msg, protocmp.Transform())
			if diff != "" {
				return nil, fmt.Errorf("message didn't match: %s", diff)
			}
			if out.err != nil {
				return nil, out.err
			}

			// Build response with headers.
			rsp := &AnyResponse{msg: out.msg}
			for key, values := range stream.rspHeader {
				rsp.Header()[key] = values
			}
			for key, values := range stream.rspTrailer {
				rsp.Trailer()[key] = values
			}
			return rsp, nil
		})
	})
}

type unusedType struct{}

type AnyResponse struct {
	connect.Response[unusedType]
	msg proto.Message
}

func (a *AnyResponse) Any() any { return a.msg }

func equalHeaders(a, b http.Header) error {
	for key, values := range a {
		if !equalSlices(values, b[key]) {
			return fmt.Errorf(
				"header %s: want %v got %v", key, a[key], b[key],
			)
		}
	}
	return nil
}
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index, values := range a {
		if values != b[index] {
			return false
		}
	}
	return true
}
