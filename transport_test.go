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

package vanguard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectgzip"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestTransport_Unary(t *testing.T) {
	t.Parallel()

	spec := methodSpec(methodDesc(t, "vanguard.test.v1.LibraryService.GetBook"))
	echo := func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
		req := &testv1.GetBookRequest{}
		if err := stream.Receive(req); err != nil {
			return err
		}
		return stream.Send(&testv1.Book{Name: req.GetName(), Title: "round-trip"})
	}
	srv := httptest.NewServer(mountTestHandler(t, spec, echo))
	t.Cleanup(srv.Close)

	restTransport, err := NewTransport(srv.Client(), srv.URL)
	require.NoError(t, err)
	stream, err := restTransport.NewClientStream(t.Context(), spec)
	require.NoError(t, err)
	t.Cleanup(func() { _ = stream.Close() })
	require.NoError(t, stream.Send(&testv1.GetBookRequest{Name: "shelves/s/books/b"}))
	require.NoError(t, stream.CloseSend())
	var got testv1.Book
	require.NoError(t, stream.Receive(&got))
	assert.Equal(t, "shelves/s/books/b", got.GetName())
	assert.Equal(t, "round-trip", got.GetTitle())
	require.ErrorIs(t, stream.Receive(&got), io.EOF)

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(empty.Close)
	restTransport, err = NewTransport(empty.Client(), empty.URL)
	require.NoError(t, err)
	stream, err = restTransport.NewClientStream(t.Context(), spec)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&testv1.GetBookRequest{Name: "shelves/s/books/b"}))
	err = stream.Receive(&got)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "zero-length payload")

	stream, err = restTransport.NewClientStream(t.Context(), spec)
	require.NoError(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(stream.CloseSend()))
}

func TestTransport_ClientStream_RequestFails(t *testing.T) {
	t.Parallel()

	spec := methodSpec(methodDesc(t, "vanguard.test.v1.ContentService.Upload"))
	for _, compression := range []string{"", connect.CompressionNameGzip} {
		restTransport, err := NewTransport(failingHTTPClient{}, "http://example.invalid", WithSendCompression(compression))
		require.NoError(t, err)
		stream, err := restTransport.NewClientStream(t.Context(), spec)
		require.NoError(t, err)
		t.Cleanup(func() { _ = stream.Close() })

		for i := 0; ; i++ {
			err = stream.Send(&testv1.UploadRequest{
				Filename: "message.txt",
				File:     &httpbody.HttpBody{ContentType: "text/plain", Data: []byte(strings.Repeat("x", 1024))},
			})
			if err != nil || i == 8 {
				break
			}
		}
		require.ErrorIs(t, err, io.EOF, "compression %q", compression)
		err = stream.CloseSend()
		assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "compression %q: %v", compression, err)
		var got emptypb.Empty
		err = stream.Receive(&got)
		assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err), "compression %q: %v", compression, err)
	}
}

func TestTransport_MaxReadBytes(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("a", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, ":download"):
			responseWriter.Header().Set("Content-Type", "text/plain")
			_, _ = responseWriter.Write([]byte(big))
		case request.URL.Path == "/v1/shelves/s/books/small":
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{"title":"tiny"}`))
		case request.URL.Path == "/v1/shelves/s/books/error":
			responseWriter.WriteHeader(http.StatusTeapot)
			_, _ = responseWriter.Write([]byte(big))
		default:
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{"title":"` + big + `"}`))
		}
	}))
	t.Cleanup(srv.Close)

	restTransport, err := NewTransport(srv.Client(), srv.URL, WithMaxReadBytes(64))
	require.NoError(t, err)
	client := connect.NewClient(restTransport)
	ctx := t.Context()
	library := testv1connect.NewLibraryServiceClient(client)
	content := testv1connect.NewContentServiceClient(client)

	book, err := library.GetBook(ctx, &testv1.GetBookRequest{Name: "shelves/s/books/small"})
	require.NoError(t, err)
	assert.Equal(t, "tiny", book.GetTitle())

	_, err = library.GetBook(ctx, &testv1.GetBookRequest{Name: "shelves/s/books/big"})
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), "%v", err)
	assert.Contains(t, err.Error(), "response body exceeds 64 bytes")

	_, err = library.GetBook(ctx, &testv1.GetBookRequest{Name: "shelves/s/books/error"})
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), "%v", err)

	downloadStream, err := content.Download(ctx, &testv1.DownloadRequest{Filename: "big.bin"})
	require.NoError(t, err)
	for err == nil {
		_, err = downloadStream.Receive()
	}
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), "%v", err)

	restTransport, err = NewTransport(srv.Client(), srv.URL, WithMaxReadBytes(0))
	require.NoError(t, err)
	book, err = testv1connect.NewLibraryServiceClient(connect.NewClient(restTransport)).GetBook(ctx, &testv1.GetBookRequest{Name: "shelves/s/books/big"})
	require.NoError(t, err)
	assert.Len(t, book.GetTitle(), len(big))
}

func TestTransport_ContentService_RoundTrip(t *testing.T) {
	t.Parallel()

	server := connect.NewServer()
	svc := &memoryContentServer{files: map[string]*httpbody.HttpBody{}}
	testv1connect.RegisterContentServiceHandler(server, svc)
	mux := http.NewServeMux()
	require.NoError(t, Mount(mux, server))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	restTransport, err := NewTransport(srv.Client(), srv.URL)
	require.NoError(t, err)
	client := testv1connect.NewContentServiceClient(connect.NewClient(restTransport))
	ctx := t.Context()

	uploadStream, err := client.Upload(ctx)
	require.NoError(t, err)
	require.NoError(t, uploadStream.Send(&testv1.UploadRequest{
		Filename: "message.txt",
		File:     &httpbody.HttpBody{ContentType: "text/plain", Data: []byte("hello")},
	}))
	require.NoError(t, uploadStream.Send(&testv1.UploadRequest{
		File: &httpbody.HttpBody{Data: []byte(" world")},
	}))
	_, err = uploadStream.CloseAndReceive()
	require.NoError(t, err)
	require.NotNil(t, svc.get("message.txt"))
	assert.Equal(t, "text/plain", svc.get("message.txt").GetContentType())
	assert.Equal(t, "hello world", string(svc.get("message.txt").GetData()))

	big := bytes.Repeat([]byte("0123456789abcdef"), 5*1024)
	svc.put("big.bin", &httpbody.HttpBody{ContentType: "application/octet-stream", Data: big})
	downloadStream, err := client.Download(ctx, &testv1.DownloadRequest{Filename: "big.bin"})
	require.NoError(t, err)
	var got []byte
	chunks := 0
	for {
		res, err := downloadStream.Receive()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		assert.Equal(t, "application/octet-stream", res.GetFile().GetContentType())
		got = append(got, res.GetFile().GetData()...)
		chunks++
	}
	assert.Equal(t, big, got)
	assert.Greater(t, chunks, 1)
	require.NoError(t, downloadStream.Close())

	downloadStream, err = client.Download(ctx, &testv1.DownloadRequest{Filename: "missing"})
	require.NoError(t, err)
	_, err = downloadStream.Receive()
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestTransport_Compression(t *testing.T) {
	t.Parallel()

	var sawEncoding, sawAccept string
	var sawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		sawEncoding = request.Header.Get("Content-Encoding")
		sawAccept = request.Header.Get("Accept-Encoding")
		var err error
		sawBody, err = io.ReadAll(request.Body)
		assert.NoError(t, err)
		switch {
		case strings.HasPrefix(request.URL.Path, "/gzip.bin"):
			responseWriter.Header().Set("Content-Type", "text/plain")
			responseWriter.Header().Set("Content-Encoding", "gzip")
			_, _ = responseWriter.Write(gzipBytes(t, bytes.Repeat([]byte("0123456789abcdef"), 5*1024)))
		case strings.HasPrefix(request.URL.Path, "/zstd.bin"):
			responseWriter.Header().Set("Content-Type", "text/plain")
			responseWriter.Header().Set("Content-Encoding", "zstd")
			_, _ = responseWriter.Write([]byte("opaque"))
		default:
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)

	restTransport, err := NewTransport(srv.Client(), srv.URL, WithSendCompression("gzip"))
	require.NoError(t, err)
	client := testv1connect.NewContentServiceClient(connect.NewClient(restTransport))
	ctx, info := connect.NewClientContext(t.Context())

	uploadStream, err := client.Upload(ctx)
	require.NoError(t, err)
	require.NoError(t, uploadStream.Send(&testv1.UploadRequest{
		Filename: "message.txt",
		File:     &httpbody.HttpBody{ContentType: "text/plain", Data: []byte("hello world")},
	}))
	_, err = uploadStream.CloseAndReceive()
	require.NoError(t, err)
	assert.Equal(t, "gzip", sawEncoding)
	assert.Equal(t, "gzip", sawAccept)
	assert.Equal(t, "hello world", string(gunzipBytes(t, sawBody)))
	assert.Equal(t, "rest", info.Protocol)
	assert.Equal(t, "json", info.Codec)
	assert.Equal(t, "gzip", info.RequestEncoding)
	assert.Equal(t, "identity", info.ResponseEncoding)

	downloadStream, err := client.Download(ctx, &testv1.DownloadRequest{Filename: "gzip.bin"})
	require.NoError(t, err)
	var got []byte
	chunks := 0
	for {
		res, err := downloadStream.Receive()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		got = append(got, res.GetFile().GetData()...)
		chunks++
	}
	assert.Equal(t, bytes.Repeat([]byte("0123456789abcdef"), 5*1024), got)
	assert.Greater(t, chunks, 1)
	assert.Equal(t, "gzip", info.ResponseEncoding)

	_, err = client.Download(ctx, &testv1.DownloadRequest{Filename: "zstd.bin"})
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	assert.Contains(t, err.Error(), `unknown encoding "zstd"`)

	_, err = NewTransport(srv.Client(), srv.URL, WithCompressors(), WithSendCompression("gzip"))
	require.ErrorContains(t, err, `unknown compression "gzip"`)
	_, err = NewTransport(srv.Client(), srv.URL, WithCompressors(connectgzip.New()), WithSendCompression("gzip"))
	require.NoError(t, err)
}

type memoryContentServer struct {
	testv1connect.UnimplementedContentServiceHandler

	mu    sync.Mutex
	files map[string]*httpbody.HttpBody
}

func (m *memoryContentServer) get(name string) *httpbody.HttpBody {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.files[name]
}

func (m *memoryContentServer) put(name string, file *httpbody.HttpBody) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = file
}

func (m *memoryContentServer) Upload(_ context.Context, stream testv1connect.ContentServiceUploadServerStream) (*emptypb.Empty, error) {
	var name string
	file := &httpbody.HttpBody{}
	for {
		req, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if name == "" {
			name = req.GetFilename()
			file.ContentType = req.GetFile().GetContentType()
		}
		file.Data = append(file.Data, req.GetFile().GetData()...)
	}
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "no upload message received")
	}
	m.put(name, file)
	return &emptypb.Empty{}, nil
}

func (m *memoryContentServer) Subscribe(_ context.Context, stream testv1connect.ContentServiceSubscribeServerStream) error {
	for {
		req, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, pattern := range req.GetFilenamePatterns() {
			if err := stream.Send(&testv1.SubscribeResponse{FilenameChanged: pattern}); err != nil {
				return err
			}
		}
	}
}

func (m *memoryContentServer) Download(_ context.Context, req *testv1.DownloadRequest, stream testv1connect.ContentServiceDownloadServerStream) error {
	file := m.get(req.GetFilename())
	if file == nil {
		return connect.Errorf(connect.CodeNotFound, "no file %q", req.GetFilename())
	}
	const piece = 20 * 1024
	for data := file.GetData(); len(data) > 0; {
		size := min(piece, len(data))
		if err := stream.Send(&testv1.DownloadResponse{
			File: &httpbody.HttpBody{ContentType: file.GetContentType(), Data: data[:size]},
		}); err != nil {
			return err
		}
		data = data[size:]
	}
	return nil
}

type failingHTTPClient struct{}

func (failingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	_, _ = io.CopyN(io.Discard, req.Body, 3)
	return nil, errors.New("connection reset")
}
