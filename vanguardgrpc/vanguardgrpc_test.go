// Copyright 2023-2026 Buf Technologies, Inc.
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

package vanguardgrpc

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/vanguard"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func TestBuildMethods_Library(t *testing.T) {
	t.Parallel()
	srv := &fakeServer{}
	methods := buildMethods(&testv1.LibraryService_ServiceDesc, srv)

	require.Len(t, methods, len(testv1.LibraryService_ServiceDesc.Methods))
	for _, m := range methods {
		assert.Equal(t, connect.StreamTypeUnary, m.Spec.StreamType)
		assert.NotEmpty(t, m.Spec.Procedure)
		assert.NotNil(t, m.Spec.Schema, "Spec.Schema for %s", m.Spec.Procedure)
		assert.NotNil(t, m.Handler, "Handler for %s", m.Spec.Procedure)
	}

	var getBook *connect.Method
	for i := range methods {
		if methods[i].Spec.Procedure == getBookProcedure {
			getBook = &methods[i]
			break
		}
	}
	require.NotNil(t, getBook, "GetBook should be present")
	assert.Equal(t, connect.IdempotencyNoSideEffects, getBook.Spec.IdempotencyLevel)
}

func TestBuildMethods_PanicsOnInterfaceMismatch(t *testing.T) {
	t.Parallel()
	type wrong struct{}
	defer func() {
		val := recover()
		require.NotNil(t, val, "expected panic")
		msg, ok := val.(string)
		require.True(t, ok, "panic value should be a string, got %T", val)
		assert.Contains(t, msg, "does not implement")
	}()
	buildMethods(&testv1.LibraryService_ServiceDesc, &wrong{})
}

func TestUnaryAdapter_RoundTrip(t *testing.T) {
	t.Parallel()
	srv := &fakeServer{}
	methods := buildMethods(&testv1.LibraryService_ServiceDesc, srv)

	var getBook connect.ServerFunc
	for _, m := range methods {
		if m.Spec.Procedure == getBookProcedure {
			getBook = m.Handler
			break
		}
	}
	require.NotNil(t, getBook)

	stream := &fakeServerStream{
		recvQueue: []proto.Message{&testv1.GetBookRequest{Name: "shelves/s/books/b"}},
	}
	err := getBook(context.Background(), connect.Spec{}, stream)
	require.NoError(t, err)
	require.Len(t, stream.sent, 1)
	book, ok := stream.sent[0].(*testv1.Book)
	require.True(t, ok)
	assert.Equal(t, "shelves/s/books/b", book.GetName())
	assert.Equal(t, "stub", book.GetTitle())
}

func TestUnaryAdapter_ConvertsGRPCStatusError(t *testing.T) {
	t.Parallel()
	srv := &fakeServer{echoErr: status.Error(codes.PermissionDenied, "nope")}
	methods := buildMethods(&testv1.LibraryService_ServiceDesc, srv)

	var getBook connect.ServerFunc
	for _, m := range methods {
		if m.Spec.Procedure == getBookProcedure {
			getBook = m.Handler
			break
		}
	}
	require.NotNil(t, getBook)

	stream := &fakeServerStream{
		recvQueue: []proto.Message{&testv1.GetBookRequest{Name: "any"}},
	}
	err := getBook(context.Background(), connect.Spec{}, stream)
	require.Error(t, err)
	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodePermissionDenied, cerr.Code())
	assert.Equal(t, "nope", cerr.Message())
}

func TestConvertError(t *testing.T) {
	t.Parallel()

	t.Run("nil_passes_through", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, convertError(nil))
	})

	t.Run("connect_error_passes_through", func(t *testing.T) {
		t.Parallel()
		orig := connect.Errorf(connect.CodeAlreadyExists, "x")
		got := convertError(orig)
		var cerr *connect.Error
		require.ErrorAs(t, got, &cerr)
		assert.Equal(t, connect.CodeAlreadyExists, cerr.Code())
	})

	t.Run("status_error_translates", func(t *testing.T) {
		t.Parallel()
		got := convertError(status.Error(codes.NotFound, "missing"))
		var cerr *connect.Error
		require.ErrorAs(t, got, &cerr)
		assert.Equal(t, connect.CodeNotFound, cerr.Code())
		assert.Equal(t, "missing", cerr.Message())
	})

	t.Run("plain_error_passes_through", func(t *testing.T) {
		t.Parallel()
		orig := errors.New("plain")
		got := convertError(orig)
		assert.Same(t, orig, got)
	})
}

func TestUnaryAdapter_GRPCContext(t *testing.T) {
	t.Parallel()
	server := connect.NewServer()
	library := &metadataLibraryServer{}
	testv1.RegisterLibraryServiceServer(NewServiceRegistrar(server), library)

	info := &connect.CallInfo{PeerAddr: "192.0.2.1:8080"}
	info.RequestHeader().Set("X-Tenant", "acme")
	info.RequestHeader().Set("X-Token-Bin", connect.EncodeBinaryHeader([]byte{0, 1, 2}))
	info.RequestHeader().Set("Grpc-Timeout", "1S")
	stream := &fakeServerStream{
		recvQueue: []proto.Message{&testv1.GetBookRequest{Name: "shelves/s/books/b"}},
	}
	err := server.Call(context.Background(), getBookProcedure, info, stream)
	require.NoError(t, err)

	assert.Equal(t, getBookProcedure, library.method)
	assert.Equal(t, []string{"acme"}, library.incoming.Get("x-tenant"))
	assert.Equal(t, []string{"\x00\x01\x02"}, library.incoming.Get("x-token-bin"))
	assert.Empty(t, library.incoming.Get("grpc-timeout"))
	require.NotNil(t, library.peer)
	assert.Equal(t, "192.0.2.1:8080", library.peer.Addr.String())
	assert.IsType(t, &net.TCPAddr{}, library.peer.Addr)

	assert.Equal(t, []string{"one", "two"}, info.ResponseHeader().Values("x-header"))
	assert.Equal(t, connect.EncodeBinaryHeader([]byte{0, 255}), info.ResponseHeader().Get("x-raw-bin"))
	assert.Equal(t, "three", info.ResponseTrailer().Get("x-trailer"))

	info = &connect.CallInfo{}
	info.RequestHeader().Set("X-Token-Bin", "not base64!")
	stream = &fakeServerStream{
		recvQueue: []proto.Message{&testv1.GetBookRequest{Name: "shelves/s/books/b"}},
	}
	err = server.Call(context.Background(), getBookProcedure, info, stream)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestServerStreamAdapter_Metadata(t *testing.T) {
	t.Parallel()
	server := connect.NewServer()
	content := &metadataContentServer{}
	testv1.RegisterContentServiceServer(NewServiceRegistrar(server), content)

	info := &connect.CallInfo{}
	info.RequestHeader().Set("X-Tenant", "acme")
	stream := &fakeServerStream{
		recvQueue: []proto.Message{&testv1.DownloadRequest{Filename: "f"}},
	}
	err := server.Call(
		context.Background(),
		"/vanguard.test.v1.ContentService/Download",
		info,
		stream,
	)
	require.NoError(t, err)

	assert.Equal(t, []string{"acme"}, content.incoming.Get("x-tenant"))
	assert.Equal(t, []string{"one", "two"}, info.ResponseHeader().Values("x-header"))
	assert.Equal(t, "three", info.ResponseTrailer().Get("x-trailer"))
}

func TestNewCodec(t *testing.T) {
	t.Parallel()

	codec := NewCodec(vanguard.NewJSONCodec(protoregistry.GlobalTypes))
	assert.Equal(t, "json", codec.Name())

	data, err := codec.Marshal(&testv1.Book{Name: "shelves/s/books/b", Title: "json"})
	require.NoError(t, err)
	assert.Contains(t, string(data), `"name":"shelves/s/books/b"`)
	assert.Contains(t, string(data), `"title":"json"`)

	var book testv1.Book
	require.NoError(t, codec.Unmarshal(data, &book))
	assert.Equal(t, "json", book.GetTitle())

	_, err = codec.Marshal("not a message")
	require.ErrorContains(t, err, "not a proto.Message")
	require.ErrorContains(t, codec.Unmarshal(data, "not a message"), "not a proto.Message")
}

const getBookProcedure = "/vanguard.test.v1.LibraryService/GetBook"

type fakeServer struct {
	testv1.UnimplementedLibraryServiceServer

	echoErr error
}

func (f *fakeServer) GetBook(_ context.Context, req *testv1.GetBookRequest) (*testv1.Book, error) {
	if f.echoErr != nil {
		return nil, f.echoErr
	}
	return &testv1.Book{Name: req.GetName(), Title: "stub"}, nil
}

type fakeServerStream struct {
	recvQueue []proto.Message
	sent      []proto.Message
	sendErr   error
	recvErr   error
}

func (s *fakeServerStream) Receive(msg any) error {
	if s.recvErr != nil {
		return s.recvErr
	}
	if len(s.recvQueue) == 0 {
		return io.EOF
	}
	next := s.recvQueue[0]
	s.recvQueue = s.recvQueue[1:]
	pmsg, ok := msg.(proto.Message)
	if !ok {
		return errors.New("fakeServerStream: not a proto.Message")
	}
	proto.Reset(pmsg)
	proto.Merge(pmsg, next)
	return nil
}

func (s *fakeServerStream) SendHeaders() error { return nil }

func (s *fakeServerStream) Send(msg any) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	out, ok := msg.(proto.Message)
	if !ok {
		return errors.New("fakeServerStream: not a proto.Message")
	}
	clone := proto.Clone(out)
	s.sent = append(s.sent, clone)
	return nil
}

type metadataLibraryServer struct {
	testv1.UnimplementedLibraryServiceServer

	method   string
	incoming metadata.MD
	peer     *peer.Peer
}

func (s *metadataLibraryServer) GetBook(ctx context.Context, req *testv1.GetBookRequest) (*testv1.Book, error) {
	s.method, _ = grpc.Method(ctx)
	s.incoming, _ = metadata.FromIncomingContext(ctx)
	s.peer, _ = peer.FromContext(ctx)
	if err := grpc.SetHeader(ctx, metadata.Pairs("x-header", "one", "x-raw-bin", "\x00\xff")); err != nil {
		return nil, err
	}
	if err := grpc.SendHeader(ctx, metadata.Pairs("x-header", "two")); err != nil {
		return nil, err
	}
	if err := grpc.SetTrailer(ctx, metadata.Pairs("x-trailer", "three")); err != nil {
		return nil, err
	}
	return &testv1.Book{Name: req.GetName()}, nil
}

type metadataContentServer struct {
	testv1.UnimplementedContentServiceServer

	incoming metadata.MD
}

func (s *metadataContentServer) Download(
	_ *testv1.DownloadRequest,
	stream grpc.ServerStreamingServer[testv1.DownloadResponse],
) error {
	s.incoming, _ = metadata.FromIncomingContext(stream.Context())
	if err := stream.SetHeader(metadata.Pairs("x-header", "one")); err != nil {
		return err
	}
	if err := stream.SendHeader(metadata.Pairs("x-header", "two")); err != nil {
		return err
	}
	stream.SetTrailer(metadata.Pairs("x-trailer", "three"))
	return nil
}
