// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bufbuild/connect-go"
	library "github.com/bufbuild/vanguard/internal/gen/library/v1"
	"github.com/bufbuild/vanguard/internal/gen/library/v1/libraryv1connect"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestFilterRestToRPC(t *testing.T) {
	t.Parallel()

	var overide overrideMap

	mux := http.NewServeMux()
	mux.Handle(libraryv1connect.NewLibraryServiceHandler(
		libraryv1connect.UnimplementedLibraryServiceHandler{},
		connect.WithInterceptors(overide.unary()),
	))

	gRPCOnlyMux := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		contentType := request.Header.Get("Content-Type")
		if request.Method != http.MethodPost || !strings.HasPrefix(contentType, "application/grpc+") {
			responseWriter.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(responseWriter, "invalid content:%v", contentType)
			return
		}
		mux.ServeHTTP(responseWriter, request)
	})

	fmux := NewMux(
		&Config{
			//outputProtocol: protocolGRPC,
			maxRecvMsgSize: 1024 * 1024 * 1024,
		}, //nolint:exhaustivestruct
	)

	t.Log("adding services:")
	services := []protoreflect.ServiceDescriptor{}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		sds := fd.Services()
		for i := 0; i < sds.Len(); i++ {
			services = append(services, sds.Get(i))
			t.Log("  ", sds.Get(i).FullName())
			return true
		}
		return true
	})
	fmux.RegisterHTTPHandler(gRPCOnlyMux, services, protocolGRPC)

	type want struct {
		code int
		body string
		msg  proto.Message
	}
	tests := []struct {
		name    string
		request *http.Request
		stream  []msgDir
		want    want
	}{{
		name:    "get",
		request: httptest.NewRequest("GET", "/v1/shelves/1/books/1", nil),
		stream: []msgDir{
			msgIn{
				method: "/vanguard.library.v1.LibraryService/GetBook",
				msg:    &library.GetBookRequest{Name: "shelves/1/books/1"},
			},
			msgOut{
				msg: &library.Book{Name: "shelves/1/books/1"},
			},
		},
		want: want{
			code: http.StatusOK,
			msg:  &library.Book{Name: "shelves/1/books/1"},
		},
	}, {
		name:    "methodNotAllowed",
		request: httptest.NewRequest("PUT", "/v1/shelves/1/books/1", nil),
		want: want{
			code: http.StatusMethodNotAllowed,
			msg: &status.Status{
				Code:    int32(connect.CodeUnimplemented),
				Message: "method not allowed",
			},
		},
	}, {
		name:    "error",
		request: httptest.NewRequest("GET", "/v1/shelves/1/books/1", nil),
		stream: []msgDir{
			msgIn{
				method: "/vanguard.library.v1.LibraryService/GetBook",
				msg:    &library.GetBookRequest{Name: "shelves/1/books/1"},
			},
			msgOut{
				err: connect.NewError(
					connect.CodePermissionDenied,
					fmt.Errorf("permission denied")),
			},
		},
		want: want{
			code: http.StatusForbidden,
			msg: &status.Status{
				Code:    int32(connect.CodePermissionDenied),
				Message: "permission denied",
			},
		},
	}}
	opts := cmp.Options{protocmp.Transform()}
	for _, testcase := range tests {
		testcase := testcase
		t.Run(testcase.name, func(t *testing.T) {
			t.Parallel()

			overide.set(t.Name(), testcase.stream)
			defer overide.del(t.Name())

			req := testcase.request
			req.Header.Set("Test", t.Name())
			t.Log(req.Method, req.URL.String())

			rsp := httptest.NewRecorder()
			fmux.ServeHTTP(rsp, req)

			//t.Log(rsp.Code)
			//t.Log(rsp.Body.String())
			t.Log(rsp)

			if sc := testcase.want.code; sc != rsp.Code {
				t.Errorf("expected %d got %d", testcase.want.code, rsp.Code)
				var msg status.Status
				if err := protojson.Unmarshal(rsp.Body.Bytes(), &msg); err != nil {
					t.Error(err, rsp.Body.String())
					return
				}
				t.Error("status.code", msg.Code)
				t.Error("status.message", msg.Message)
				return
			}

			if testcase.want.body != "" {
				if rsp.Body.String() != testcase.want.body {
					t.Errorf("body %s != %s", testcase.want.body, rsp.Body.String())
				}
			}

			if testcase.want.msg != nil {
				if rsp.Body.Len() == 0 {
					t.Error("expected body")
					return
				}
				msg := testcase.want.msg.ProtoReflect().New().Interface()
				if err := protojson.Unmarshal(rsp.Body.Bytes(), msg); err != nil {
					t.Error(err, rsp.Body.String())
					return
				}
				t.Log("got msg", msg)
				diff := cmp.Diff(msg, testcase.want.msg, opts...)
				if diff != "" {
					t.Error(diff)
				}
			}
		})
	}
}

func TestFilterRPCToRest(t *testing.T) {
	t.Parallel()

	mux := NewMux(
		&Config{
			maxRecvMsgSize: 1024 * 1024 * 1024,
		}, //nolint:exhaustivestruct
	)

	var overide overrideMap
	rest := http.HandlerFunc(func(rsp http.ResponseWriter, req *http.Request) {
		stream, ok := overide.get(req.Header.Get("test"))
		if !ok {
			http.Error(rsp, "missing testcase", http.StatusBadRequest)
			return
		}

		urlstr := req.URL.String()
		contentType := req.Header.Get("Content-Type")
		encoding := req.Header.Get("Content-Encoding")
		acceptEncoding := req.Header.Get("Accept-Encoding")

		var input io.Reader = req.Body
		if encoding != "" {
			comp, err := mux.getCompressor(encoding)
			if err != nil {
				rsp.WriteHeader(http.StatusBadRequest)
				t.Error(err)
				return
			}
			input, err = comp.Decompress(input)
			if err != nil {
				rsp.WriteHeader(http.StatusBadRequest)
				t.Error(err)
				return
			}
		}

		b, err := io.ReadAll(input)
		if err != nil {
			http.Error(rsp, err.Error(), http.StatusBadRequest)
			return
		}

		in, out := stream[0].(msgIn), stream[1].(msgOut)
		if in.method != "" && urlstr != in.method {
			err := fmt.Errorf("rest url expected %s, got %s", in.method, urlstr)
			http.Error(rsp, err.Error(), http.StatusBadRequest)
			return
		}
		if in.verb != "" && req.Method != in.verb {
			err := fmt.Errorf("rest verb expected %s, got %s", in.method, urlstr)
			http.Error(rsp, err.Error(), http.StatusBadRequest)
			return
		}

		msg := in.msg.ProtoReflect().New().Interface()
		if len(b) > 0 {
			var codec codec = codecJSON{}
			if contentType == "application/protobuf" {
				codec = codecProto{}
			}
			if err := codec.Unmarshal(b, msg); err != nil {
				http.Error(rsp, err.Error(), http.StatusBadRequest)
				return
			}
		}

		if out.err != nil {
			rspHdr := makeResponseHeaderHTTP(rsp)
			errWriter := newHTTPErrorWriter(contentType)
			errWriter(rsp, rspHdr, out.err)
			return
		}

		var output io.Writer = rsp
		codec := codecJSON{}
		if acceptEncoding != "" {
			comp, err := mux.getCompressor(acceptEncoding)
			if err != nil {
				rsp.WriteHeader(http.StatusInternalServerError)
				t.Error(err)
				return
			}
			xoutput, err := comp.Compress(output)
			if err != nil {
				rsp.WriteHeader(http.StatusInternalServerError)
				t.Error(err)
				return
			}
			defer xoutput.Close()
			output = xoutput
		}
		b, err = codec.MarshalAppend(nil, out.msg)
		if err != nil {
			rsp.WriteHeader(http.StatusInternalServerError)
			t.Error(err)
		}
		output.Write(b)
	})

	t.Log("adding services:")
	services := []protoreflect.ServiceDescriptor{}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		sds := fd.Services()
		for i := 0; i < sds.Len(); i++ {
			services = append(services, sds.Get(i))
			t.Log("  ", sds.Get(i).FullName())
			return true
		}
		return true
	})
	mux.RegisterHTTPHandler(rest, services, protocolHTTP)

	svr := httptest.NewUnstartedServer(mux)
	svr.EnableHTTP2 = true
	svr.StartTLS()
	defer t.Cleanup(svr.Close)

	client := libraryv1connect.NewLibraryServiceClient(svr.Client(), svr.URL, connect.WithGRPC())

	type want struct {
		code connect.Code
		body string
		msg  proto.Message
	}
	tests := []struct {
		name   string
		call   func(t *testing.T) (connect.AnyResponse, error)
		stream []msgDir
		want   want
	}{{
		name: "get",
		call: func(t *testing.T) (connect.AnyResponse, error) {
			req := connect.NewRequest(&library.GetBookRequest{Name: "shelves/1/books/1"})
			req.Header().Set("test", t.Name())
			return client.GetBook(context.Background(), req)
		},
		stream: []msgDir{
			msgIn{
				method: "/vanguard.library.v1.LibraryService/GetBook",
				msg:    &library.GetBookRequest{Name: "shelves/1/books/1"},
			},
			msgOut{
				msg: &library.Book{Name: "shelves/1/books/1"},
			},
		},
		want: want{
			code: http.StatusOK,
			msg:  &library.Book{Name: "shelves/1/books/1"},
		},
		/*}, {
		name: "error",
		call: func(t *testing.T) (connect.AnyResponse, error) {
			req := connect.NewRequest(&library.GetBookRequest{Name: "shelves/1/books/1"})
			req.Header().Set("test", t.Name())
			return client.GetBook(context.Background(), req)
		},
		stream: []msgDir{
			msgIn{
				method: "/vanguard.library.v1.LibraryService/GetBook",
				msg:    &library.GetBookRequest{Name: "shelves/1/books/1"},
			},
			msgOut{
				err: connect.NewError(
					connect.CodePermissionDenied,
					fmt.Errorf("permission denied")),
			},
		},
		want: want{
			code: http.StatusForbidden,
			body: `{"code":7,"message":"permission denied","details":[]}`,
		},*/
	}}
	opts := cmp.Options{protocmp.Transform()}
	for _, testcase := range tests {
		testcase := testcase
		t.Run(testcase.name, func(t *testing.T) {
			t.Parallel()

			overide.set(t.Name(), testcase.stream)
			defer overide.del(t.Name())

			rsp, err := testcase.call(t)
			t.Log(rsp)
			t.Log(err)

			if wantCode := testcase.want.code; wantCode != 0 {
				cerr := &connect.Error{}
				if !errors.As(err, &cerr) {
					t.Fatal(err)
				}
				code := cerr.Code()
				if wantCode != code {
					t.Errorf("expected %d got %d", wantCode, code)
				}
				return
			}

			if testcase.want.msg != nil {
				msg := rsp.Any()
				t.Log("got msg", msg)
				diff := cmp.Diff(msg, testcase.want.msg, opts...)
				if diff != "" {
					t.Error(diff)
				}
			}
		})
	}
}

type msgIn struct {
	method string
	verb   string // POST, GET, etc.
	msg    proto.Message
}

func (m msgIn) direction() bool { return false }

type msgOut struct {
	msg proto.Message
	err error
}

func (m msgOut) direction() bool { return true }

type msgDir interface{ direction() bool }

type overrideMap struct {
	sync.Map
}

func (o *overrideMap) get(testcase string) ([]msgDir, bool) {
	val, ok := o.Load(testcase)
	if !ok {
		return nil, false
	}
	return val.([]msgDir), true
}
func (o *overrideMap) set(testcase string, stream []msgDir) {
	o.Store(testcase, stream)
}
func (o *overrideMap) del(testcase string) {
	o.Delete(testcase)
}

func (o *overrideMap) unary() connect.UnaryInterceptorFunc {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return connect.UnaryFunc(func(
			ctx context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			val := req.Header().Get("test")
			if val == "" {
				return next(ctx, req)
			}
			data, ok := o.Load(val)
			if !ok {
				return nil, fmt.Errorf("missing testcase %s", val)
			}

			stream := data.([]msgDir)
			in, out := stream[0].(msgIn), stream[1].(msgOut)
			if in.method != "" && req.Spec().Procedure != in.method {
				err := fmt.Errorf("grpc expected %s, got %s", in.method, req.Spec().Procedure)
				return nil, err
			}

			msg := req.Any().(proto.Message)
			diff := cmp.Diff(msg, in.msg, protocmp.Transform())
			if diff != "" {
				return nil, fmt.Errorf("message didn't match")
			}
			return &AnyResponse{msg: out.msg}, out.err
		})
	})
}

type bogus struct{}

type AnyResponse struct {
	connect.Response[bogus]
	msg proto.Message
}

func (a *AnyResponse) Any() any { return a.msg }
