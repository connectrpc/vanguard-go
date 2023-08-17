// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1"
	"github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1/testv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestMux_RPCxRPC(t *testing.T) {
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
			var wantHdr map[string]string
			switch protocol {
			case ProtocolGRPC:
				wantHdr = map[string]string{
					"Content-Type":  fmt.Sprintf("application/grpc+%s", codec),
					"Grpc-Encoding": compression,
				}
			default:
				http.Error(rsp, "unknown protocol", http.StatusInternalServerError)
				return
			}
			for key, val := range wantHdr {
				if req.Header.Get(key) != val {
					http.Error(rsp, fmt.Sprintf("missing header %s: %s", key, val), http.StatusInternalServerError)
					return
				}
			}
			next.ServeHTTP(rsp, req)
		}
	}

	makeServer := func(protocol Protocol, codec, compression string) testServer {
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
		server := httptest.NewUnstartedServer(hdlr)
		server.Start()
		t.Cleanup(server.Close)
		return testServer{name: name, svr: server}
	}
	servers := []testServer{}
	for _, protocol := range protocols {
		for _, codec := range codecs {
			for _, compression := range compressions {
				servers = append(servers, makeServer(protocol, codec, compression))
			}
		}
	}

	testOpts := []testOpt{}
	for _, server := range servers {
		opts := []connect.ClientOption{}
		for _, protocol := range protocols {
			opts := appendClientProtocolOptions(t, opts, protocol)
			for _, codec := range codecs {
				opts := appendClientCodecOptions(t, opts, codec)
				for _, compression := range compressions {
					opts := appendClientCompressionOptions(t, opts, compression)
					copyOpts := make([]connect.ClientOption, len(opts))
					copy(copyOpts, opts)
					testOpts = append(testOpts, testOpt{
						name: fmt.Sprintf("%s_%s_%s/%s", protocol, codec, compression, server.name),
						svr:  server.svr,
						opts: copyOpts,
					})
				}
			}
		}
	}

	type output struct {
		header   http.Header
		messages []proto.Message
		trailer  http.Header
	}
	type testRequest struct {
		name   string
		input  func(t *testing.T) (http.Header, []proto.Message, http.Header)
		stream testStream
		output output
	}
	testRequests := []testRequest{}
	for _, opts := range testOpts {
		libClient := testv1connect.NewLibraryServiceClient(
			opts.svr.Client(), opts.svr.URL, opts.opts...,
		)

		testRequests = append(testRequests, []testRequest{{
			name: "GetBook_" + opts.name,
			input: func(t *testing.T) (http.Header, []proto.Message, http.Header) {
				t.Helper()
				req := connect.NewRequest(&testv1.GetBookRequest{Name: "shelves/1/books/1"})
				req.Header().Set("Test", t.Name()) // test header
				req.Header().Set("Message", "hello")
				rsp, err := libClient.GetBook(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				return rsp.Header(), []proto.Message{rsp.Msg}, rsp.Trailer()
			},
			stream: testStream{
				reqHeader: http.Header{"Message": []string{"hello"}},
				rspHeader: http.Header{"Message": []string{"world"}},
				msgs: []testMsg{
					{in: &testMsgIn{
						method: "/vanguard.library.v1.LibraryService/GetBook",
						msg:    &testv1.GetBookRequest{Name: "shelves/1/books/1"},
					}},
					{out: &testMsgOut{
						msg: &testv1.Book{Name: "shelves/1/books/1"},
					}},
				},
				rspTrailer: http.Header{"Trailer": []string{"end"}},
			},
			output: output{
				header:  http.Header{"Message": []string{"world"}},
				trailer: http.Header{"Trailer": []string{"end"}},
				messages: []proto.Message{
					&testv1.Book{Name: "shelves/1/books/1"},
				},
			},
		}}...)

		// Add more tests...
	}

	for _, testCase := range testRequests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			t.Skip()

			interceptor.set(t, testCase.stream)
			defer interceptor.del(t)

			header, messages, trailer := testCase.input(t)
			assert.Subset(t, testCase.output.header, header)
			assert.Subset(t, testCase.output.trailer, trailer)
			require.Len(t, messages, len(testCase.output.messages))
			for i, msg := range messages {
				want := testCase.output.messages[i]
				assert.Empty(t, cmp.Diff(want, msg, protocmp.Transform()))
			}
		})
	}
}
