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
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestMux_RPCxREST(t *testing.T) {
	t.Parallel()

	var interceptor testInterceptor
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
		// TODO: grpc-web & connect
	}

	makeServer := func(compression string) testServer {
		var comp *compressionPool
		if compression != CompressionIdentity {
			comp = newCompressionPool(
				compression,
				func() connect.Compressor {
					return getCompressor(t, compression)
				}, func() connect.Decompressor {
					return getDecompressor(t, compression)
				},
			)
		}
		codec := DefaultJSONCodec(protoregistry.GlobalTypes)
		opts := []ServiceOption{
			WithProtocols(ProtocolREST),
			WithCodecs(codec.Name()),
		}
		if compression == CompressionIdentity {
			opts = append(opts, WithNoCompression())
		} else {
			opts = append(opts, WithCompression(compression))
		}
		hdlr := interceptor.restUnaryHandler(codec, comp)
		name := fmt.Sprintf("%s_%s_%s", ProtocolREST, codec.Name(), compression)

		mux := &Mux{}
		for _, service := range services {
			if err := mux.RegisterServiceByName(
				hdlr, service, opts...,
			); err != nil {
				t.Fatal(err)
			}
		}
		server := httptest.NewUnstartedServer(mux.AsHandler())
		server.Start()
		t.Cleanup(server.Close)
		return testServer{name: name, svr: server}
	}
	servers := []testServer{}
	for _, compression := range compressions {
		servers = append(servers, makeServer(compression))
	}

	type testOpt struct {
		name string
		svr  *httptest.Server
		opts []connect.ClientOption
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
						method: "/v1/shelves/1/books/1",
						msg:    nil, // GET request.
					}},
					{out: &testMsgOut{
						msg: &testv1.Book{Name: "shelves/1/books/1"},
					}},
				},
			},
			output: output{
				header: http.Header{"Message": []string{"world"}},
				messages: []proto.Message{
					&testv1.Book{Name: "shelves/1/books/1"},
				},
			},
		}}...)

		// Add more tests...
	}

	passingCases := map[string]struct{}{
		"GetBook_gRPC_proto_identity/REST_json_identity": {},
		"GetBook_gRPC_proto_gzip/REST_json_gzip":         {},
		"GetBook_gRPC_proto_gzip/REST_json_identity":     {},
		"GetBook_gRPC_json_gzip/REST_json_identity":      {},
		"GetBook_gRPC_json_gzip/REST_json_gzip":          {},
		"GetBook_gRPC_json_identity/REST_json_identity":  {},

		// TODO: fix identity to compressed
		// "GetBook_gRPC_proto_identity/REST_json_gzip": {},
		// "GetBook_gRPC_json_identity/REST_json_gzip":  {},
	}
	_ = passingCases
	for _, testCase := range testRequests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := passingCases[testCase.name]; !ok {
				t.Skip()
			}

			interceptor.set(t, testCase.stream)
			defer interceptor.del(t)

			header, messages, trailer := testCase.input(t)
			assert.Subset(t, header, testCase.output.header)
			assert.Subset(t, trailer, testCase.output.trailer)
			require.Len(t, messages, len(testCase.output.messages))
			for i, msg := range messages {
				want := testCase.output.messages[i]
				assert.Empty(t, cmp.Diff(want, msg, protocmp.Transform()))
			}
		})
	}
}
