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
	protocols := []Protocol{
		ProtocolGRPC,
		// TODO: grpc-web & connect
	}

	getCompressor := func(name string) connect.Compressor {
		switch name {
		case "gzip":
			return DefaultGzipCompressor()
		case "identity":
			return nil
		default:
			t.Fatalf("unknown compression: %s", name)
			return nil
		}
	}
	getDecompressor := func(name string) connect.Decompressor {
		switch name {
		case "gzip":
			return DefaultGzipDecompressor()
		case "identity":
			return nil
		default:
			t.Fatalf("unknown compression: %s", name)
			return nil
		}
	}

	type testServer struct {
		name string
		svr  *httptest.Server
	}
	makeServer := func(compression string) testServer {
		comp, decomp := getCompressor(compression), getDecompressor(compression)
		codec := DefaultJSONCodec(protoregistry.GlobalTypes)
		opts := []ServiceOption{
			WithProtocols(ProtocolREST),
			WithCodecs(codec.Name()),
		}
		hdlr := interceptor.restUnaryHandler(codec, comp, decomp)
		name := fmt.Sprintf("%s_%s_%s", ProtocolREST, codec, compression)

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
			opts := opts
			switch protocol {
			case ProtocolGRPC:
				opts = append(opts, connect.WithGRPC())
			default:
				t.Fatalf("unknown protocol: %s", protocol)
			}
			for _, codec := range codecs {
				opts := opts
				switch codec {
				case "json":
					opts = append(opts, connect.WithProtoJSON())
				case "proto":
					// default...
				default:
					t.Fatalf("unknown codec: %s", codec)
				}
				for _, compression := range compressions {
					opts := opts
					switch compression {
					case "identity":
						opts = append(opts,
							connect.WithAcceptCompression(
								"gzip", nil, nil,
							),
						)
					case "gzip":
						opts = append(opts,
							connect.WithAcceptCompression(
								"gzip",
								func() connect.Decompressor {
									return getDecompressor("gzip")
								},
								func() connect.Compressor {
									return getCompressor("gzip")
								},
							),
							connect.WithSendCompression(compression),
						)
					default:
						t.Fatalf("unknown compression: %s", compression)
					}
					testOpts = append(testOpts, testOpt{
						name: fmt.Sprintf("%s_%s_%s/%s", protocol, codec, compression, server.name),
						svr:  server.svr,
						opts: opts,
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
				req := connect.NewRequest(&testv1.GetBookRequest{Name: "shelves/1/books/1"})
				req.Header().Set("test", t.Name())
				rsp, err := libClient.GetBook(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				return rsp.Header(), []proto.Message{rsp.Msg}, rsp.Trailer()
			},
			stream: testStream{},
			output: output{
				header:   http.Header{},
				trailer:  http.Header{},
				messages: []proto.Message{},
			},
		}}...)

		// Add more tests...
	}

	for _, testCase := range testRequests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			interceptor.set(t, testCase.stream)
			defer interceptor.del(t)

			header, messages, trailer := testCase.input(t)
			assert.NoError(t, equalHeaders(testCase.output.header, header))
			assert.NoError(t, equalHeaders(testCase.output.trailer, trailer))
			require.Len(t, messages, len(testCase.output.messages))
			for i, msg := range messages {
				want := testCase.output.messages[i]
				assert.Empty(t, cmp.Diff(want, msg, protocmp.Transform()))
			}
		})
	}
}
