// Copyright 2023 Buf Technologies, Inc.
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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/bufbuild/vanguard-go/internal/gen/buf/vanguard/test/v1"
	"github.com/bufbuild/vanguard-go/internal/gen/buf/vanguard/test/v1/testv1connect"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestMux_RPCxREST(t *testing.T) {
	t.Parallel()

	var interceptor testInterceptor
	services := []protoreflect.FullName{
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
		// We use an "always-stable" codec for determinism in tests.
		codec := &stableJSONCodec{}
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
		mux.AddCodec(CodecJSON, func(res TypeResolver) Codec {
			codec := DefaultJSONCodec(res).(*jsonCodec) //nolint:errcheck,forcetypeassert
			return &stableJSONCodec{jsonCodec: *codec}
		})
		for _, service := range services {
			if err := mux.RegisterServiceByName(
				hdlr, service, opts...,
			); err != nil {
				t.Fatal(err)
			}
		}
		server := httptest.NewUnstartedServer(mux.AsHandler())
		server.EnableHTTP2 = true
		server.StartTLS()
		disableCompression(server)
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
		contentClient testv1connect.ContentServiceClient
		libClient     testv1connect.LibraryServiceClient
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
			method:    "/v1/shelves/1/books/1",
			reqHeader: http.Header{"Message": []string{"hello"}},
			rspHeader: http.Header{"Message": []string{"world"}},
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: nil, // GET request.
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
			method: "/v1/shelves/1/books/1",
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: nil, // GET request.
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
			method: "/v1/shelves/1/books?book_id=1&request_id=2",
			msgs: []testMsg{
				{in: &testMsgIn{
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
	}, {
		name: "MoveBooks",
		input: func(clients testClients, hdr http.Header) (http.Header, []proto.Message, http.Header, error) {
			msgs := []proto.Message{
				&testv1.MoveBooksRequest{
					NewParent: "shelves/1",
					Books:     []string{"book1", "book2", "book3", "book4"},
				},
			}
			return outputFromUnary(ctx, clients.libClient.MoveBooks, hdr, msgs)
		},
		stream: testStream{
			method: "/v2/shelves/1/books:move",
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &httpbody.HttpBody{
						ContentType: "application/json",
						Data:        ([]byte)(`["book1","book2","book3","book4"]`),
					},
				}},
				{out: &testMsgOut{
					msg: &testv1.MoveBooksResponse{},
				}},
			},
		},
		output: output{
			messages: []proto.Message{&testv1.MoveBooksResponse{}},
		},
	}, {
		name: "ListCheckouts",
		input: func(clients testClients, hdr http.Header) (http.Header, []proto.Message, http.Header, error) {
			msgs := []proto.Message{
				&testv1.ListCheckoutsRequest{
					Name: "shelves/1/books/abc",
				},
			}
			return outputFromUnary(ctx, clients.libClient.ListCheckouts, hdr, msgs)
		},
		stream: testStream{
			method: "/v2/shelves/1/books/abc:checkouts",
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: nil, // GET request.
				}},
				{out: &testMsgOut{
					msg: &httpbody.HttpBody{
						ContentType: "application/json",
						Data: ([]byte)(`[
							{
								"id": "123",
								"books": [
									{"name": "shelves/1/books/abc", "parent": "shelves/1"},
									{"name": "shelves/1/books/def", "parent": "shelves/1"}
								]
							}
						]`),
					},
				}},
			},
		},
		output: output{
			messages: []proto.Message{&testv1.ListCheckoutsResponse{
				Checkouts: []*testv1.Checkout{
					{
						Id: 123,
						Books: []*testv1.Book{
							{
								Name: "shelves/1/books/abc", Parent: "shelves/1",
							},
							{
								Name: "shelves/1/books/def", Parent: "shelves/1",
							},
						},
					},
				},
			}},
		},
	}, {
		name: "Index",
		input: func(clients testClients, hdr http.Header) (http.Header, []proto.Message, http.Header, error) {
			msgs := []proto.Message{
				&testv1.IndexRequest{Page: "page.html"},
			}
			return outputFromUnary(ctx, clients.contentClient.Index, hdr, msgs)
		},
		stream: testStream{
			method: "/page.html",
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: nil, // GET request.
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
			messages: []proto.Message{&httpbody.HttpBody{
				ContentType: "text/html",
				Data:        []byte("<html>hello</html>"),
			}},
		},
	}, {
		name: "Upload",
		input: func(clients testClients, hdr http.Header) (http.Header, []proto.Message, http.Header, error) {
			msgs := []proto.Message{
				&testv1.UploadRequest{
					Filename: "message.txt",
					File: &httpbody.HttpBody{
						ContentType: "text/plain",
						Data:        []byte("hello"),
					},
				},
				&testv1.UploadRequest{
					File: &httpbody.HttpBody{
						Data: []byte(" world"),
					},
				},
			}
			return outputFromClientStream(ctx, clients.contentClient.Upload, hdr, msgs)
		},
		stream: testStream{
			method: "/message.txt:upload",
			msgs: []testMsg{
				{in: &testMsgIn{
					msg: &httpbody.HttpBody{
						ContentType: "text/plain",
						Data:        []byte("hello world"),
					},
				}},
				{out: &testMsgOut{msg: &emptypb.Empty{}}},
			},
		},
		output: output{
			messages: []proto.Message{&emptypb.Empty{}},
		},
	}, {
		name: "Download",
		input: func(clients testClients, hdr http.Header) (http.Header, []proto.Message, http.Header, error) {
			msgs := []proto.Message{
				&testv1.DownloadRequest{Filename: "message.txt"},
			}
			return outputFromServerStream(ctx, clients.contentClient.Download, hdr, msgs)
		},
		stream: testStream{
			method: "/message.txt:download",
			msgs: []testMsg{
				{in: &testMsgIn{}},
				{out: &testMsgOut{
					msg: &httpbody.HttpBody{
						ContentType: "text/plain",
						Data:        []byte("hello world"),
					},
				}},
			},
		},
		output: output{
			messages: []proto.Message{&testv1.DownloadResponse{
				File: &httpbody.HttpBody{
					ContentType: "text/plain",
					Data:        []byte("hello world"),
				},
			}},
		},
	}}

	for _, opts := range testOpts {
		opts := opts
		clients := testClients{
			libClient: testv1connect.NewLibraryServiceClient(
				opts.svr.Client(), opts.svr.URL, opts.opts...,
			),
			contentClient: testv1connect.NewContentServiceClient(
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
					} else {
						assert.NoError(t, err)
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

type stableJSONCodec struct {
	jsonCodec
}

func (s stableJSONCodec) MarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	// Always use stable method
	return s.jsonCodec.MarshalAppendStable(b, msg)
}

func (s stableJSONCodec) MarshalAppendField(base []byte, msg proto.Message, field protoreflect.FieldDescriptor) ([]byte, error) {
	data, err := s.jsonCodec.MarshalAppendField(base, msg, field)
	if err != nil {
		return nil, err
	}
	return jsonStabilize(data)
}
