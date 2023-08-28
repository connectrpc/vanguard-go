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
		ProtocolGRPCWeb,
		ProtocolConnect,
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

	ctx := context.Background()
	type testClients struct {
		libClient testv1connect.LibraryServiceClient
	}
	type output struct {
		header   http.Header
		messages []proto.Message
		trailer  http.Header
		wantErr  *connect.Error
	}
	type testRequest struct {
		name   string
		input  func(clients testClients, hdr http.Header) (http.Header, []proto.Message, http.Header, error)
		stream testStream
		output output
	}
	testRequests := []testRequest{{
		name: "GetBook",
		input: func(clients testClients, hdr http.Header) (http.Header, []proto.Message, http.Header, error) {
			hdr.Set("Message", "hello")
			msgs := []proto.Message{
				&testv1.GetBookRequest{Name: "shelves/1/books/1"},
			}
			return outputFromUnary(ctx, clients.libClient.GetBook, hdr, msgs)
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
	}, {
		name: "GetBook-Error",
		input: func(clients testClients, hdr http.Header) (http.Header, []proto.Message, http.Header, error) {
			hdr.Set("Message", "hello")
			msgs := []proto.Message{
				&testv1.GetBookRequest{Name: "shelves/1/books/1"},
			}
			return outputFromUnary(ctx, clients.libClient.GetBook, hdr, msgs)
		},
		stream: testStream{
			msgs: []testMsg{
				{in: &testMsgIn{
					method: "/v1/shelves/1/books/1",
					msg:    nil, // GET request.
				}},
				{out: &testMsgOut{
					err: connect.NewError(
						connect.CodePermissionDenied,
						fmt.Errorf("permission denied")),
				}},
			},
		},
		output: output{
			wantErr: connect.NewError(
				connect.CodePermissionDenied,
				fmt.Errorf("permission denied")),
		},
	}, {
		name: "CreateBook",
		input: func(clients testClients, hdr http.Header) (http.Header, []proto.Message, http.Header, error) {
			msgs := []proto.Message{
				&testv1.CreateBookRequest{
					Parent:    "shelves/1",
					BookId:    "1",
					RequestId: "2",
					Book: &testv1.Book{
						Title:  "The Art of Computer Programming",
						Author: "Donald E. Knuth",
					},
				},
			}
			return outputFromUnary(ctx, clients.libClient.CreateBook, hdr, msgs)
		},
		stream: testStream{
			msgs: []testMsg{
				{in: &testMsgIn{
					method: "/v1/shelves/1/books?book_id=1&request_id=2",
					msg: &testv1.Book{
						Title:  "The Art of Computer Programming",
						Author: "Donald E. Knuth",
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
			messages: []proto.Message{&testv1.Book{
				Title:  "The Art of Computer Programming",
				Author: "Donald E. Knuth",
			}},
		},
	}}
	// TODO: test download and upload streaming of google.api.httpbody.

	for _, opts := range testOpts {
		opts := opts
		clients := testClients{
			libClient: testv1connect.NewLibraryServiceClient(
				opts.svr.Client(), opts.svr.URL, opts.opts...,
			),
		}
		t.Run(opts.name, func(t *testing.T) {
			t.Parallel()
			for _, req := range testRequests {
				req := req
				t.Run(req.name, func(t *testing.T) {
					t.Parallel()

					interceptor.set(t, req.stream)
					defer interceptor.del(t)

					reqHdr := http.Header{}
					reqHdr.Set("Test", t.Name())

					header, messages, trailer, err := req.input(clients, reqHdr)
					if req.output.wantErr != nil {
						assert.Equal(t, req.output.wantErr.Code(), connect.CodeOf(err))
					}
					assert.Subset(t, header, req.output.header)
					assert.Subset(t, trailer, req.output.trailer)
					require.Len(t, messages, len(req.output.messages))
					for i, msg := range messages {
						want := req.output.messages[i]
						assert.Empty(t, cmp.Diff(want, msg, protocmp.Transform()))
					}
				})
			}
		})
	}
}
