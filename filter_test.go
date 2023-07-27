// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"context"
	"fmt"
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

func TestFilterRuleGRPC(t *testing.T) {
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
			body: `{"code":12,"message":"method not allowed"}`,
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

type msgIn struct {
	method string
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
