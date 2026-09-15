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
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func TestForwardService(t *testing.T) {
	t.Parallel()

	backend := connect.NewServer()
	testv1connect.RegisterLibraryServiceHandler(backend, forwardBackend{t: t})
	files := &memoryContentServer{files: map[string]*httpbody.HttpBody{}}
	testv1connect.RegisterContentServiceHandler(backend, files)
	backendMux := http.NewServeMux()
	connecthttp.Mount(backendMux, backend)
	backendSrv := newHTTP2Server(t, backendMux)

	upstream := connect.NewClient(connecthttp.NewTransport(backendSrv.Client(), backendSrv.URL))
	proxy := connect.NewServer()
	for _, name := range []protoreflect.FullName{testv1connect.LibraryServiceName, testv1connect.ContentServiceName} {
		desc, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		require.NoError(t, err)
		service, ok := desc.(protoreflect.ServiceDescriptor)
		require.True(t, ok)
		proxy.Register(ForwardService(upstream, service)...)
	}
	proxyMux := http.NewServeMux()
	connecthttp.Mount(proxyMux, proxy)
	require.NoError(t, Mount(proxyMux, proxy))
	proxySrv := newHTTP2Server(t, proxyMux)

	viaProxy := connect.NewClient(connecthttp.NewTransport(proxySrv.Client(), proxySrv.URL))
	library := testv1connect.NewLibraryServiceClient(viaProxy)
	content := testv1connect.NewContentServiceClient(viaProxy)
	rest := func(t *testing.T, method, path string, body io.Reader, headers http.Header) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, proxySrv.URL+path, body)
		require.NoError(t, err)
		maps.Copy(req.Header, headers)
		rsp, err := proxySrv.Client().Do(req)
		require.NoError(t, err)
		defer rsp.Body.Close()
		data, err := io.ReadAll(rsp.Body)
		require.NoError(t, err)
		return rsp.StatusCode, rsp.Header, data
	}

	t.Run("unary", func(t *testing.T) {
		t.Parallel()
		code, header, body := rest(t, http.MethodGet, "/v1/shelves/s/books/b", nil, http.Header{
			"X-Tenant":   {"acme"},
			"Connection": {"keep-alive"},
		})
		require.Equal(t, http.StatusOK, code, "body=%s", body)
		assert.Equal(t, "application/json", header.Get("Content-Type"))
		assert.Equal(t, "yes", header.Get("X-Backend"))
		assert.Contains(t, string(body), `"name":"shelves/s/books/b"`)

		code, _, body = rest(t, http.MethodGet, "/v1/shelves/s/books/denied", nil, http.Header{"X-Tenant": {"acme"}})
		assert.Equal(t, http.StatusForbidden, code)
		assert.Contains(t, string(body), `"code":7`)
		assert.Contains(t, string(body), `"message":"denied"`)
		assert.Contains(t, string(body), `"@type":"type.googleapis.com/vanguard.test.v1.Book"`)

		ctx, info := connect.NewClientContext(t.Context())
		info.RequestHeader().Set("X-Tenant", "acme")
		book, err := library.GetBook(ctx, &testv1.GetBookRequest{Name: "shelves/s/books/b"})
		require.NoError(t, err)
		assert.Equal(t, "shelves/s/books/b", book.GetName())
		assert.Equal(t, "yes", info.ResponseHeader().Get("X-Backend"))
		assert.Equal(t, "bye", info.ResponseTrailer().Get("X-Backend-Trailer"))

		ctx, info = connect.NewClientContext(t.Context())
		info.RequestHeader().Set("X-Tenant", "acme")
		_, err = library.GetBook(ctx, &testv1.GetBookRequest{Name: "shelves/s/books/denied"})
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("client_stream", func(t *testing.T) {
		t.Parallel()
		code, _, body := rest(t, http.MethodPost, "/rest.txt:upload", strings.NewReader("via rest"), http.Header{
			"Content-Type": {"text/plain"},
		})
		require.Equal(t, http.StatusOK, code, "body=%s", body)
		assert.Equal(t, "via rest", string(files.get("rest.txt").GetData()))
		assert.Equal(t, "text/plain", files.get("rest.txt").GetContentType())

		stream, err := content.Upload(t.Context())
		require.NoError(t, err)
		require.NoError(t, stream.Send(&testv1.UploadRequest{
			Filename: "connect.txt",
			File:     &httpbody.HttpBody{ContentType: "text/plain", Data: []byte("hello")},
		}))
		require.NoError(t, stream.Send(&testv1.UploadRequest{File: &httpbody.HttpBody{Data: []byte(" world")}}))
		_, err = stream.CloseAndReceive()
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(files.get("connect.txt").GetData()))
	})

	t.Run("server_stream", func(t *testing.T) {
		t.Parallel()
		big := bytes.Repeat([]byte("0123456789abcdef"), 5*1024)
		files.put("big.bin", &httpbody.HttpBody{ContentType: "application/octet-stream", Data: big})

		code, header, body := rest(t, http.MethodGet, "/big.bin:download", nil, nil)
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, "application/octet-stream", header.Get("Content-Type"))
		assert.Equal(t, big, body)

		stream, err := content.Download(t.Context(), &testv1.DownloadRequest{Filename: "big.bin"})
		require.NoError(t, err)
		var got []byte
		for {
			res, err := stream.Receive()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
			got = append(got, res.GetFile().GetData()...)
		}
		assert.Equal(t, big, got)

		code, _, body = rest(t, http.MethodGet, "/missing.bin:download", nil, nil)
		assert.Equal(t, http.StatusNotFound, code)
		assert.Contains(t, string(body), `"code":5`)
	})

	t.Run("bidi", func(t *testing.T) {
		t.Parallel()
		stream, err := content.Subscribe(t.Context())
		require.NoError(t, err)
		require.NoError(t, stream.Send(&testv1.SubscribeRequest{FilenamePatterns: []string{"a", "b"}}))
		for _, want := range []string{"a", "b"} {
			res, err := stream.Receive()
			require.NoError(t, err)
			assert.Equal(t, want, res.GetFilenameChanged())
		}
		require.NoError(t, stream.Send(&testv1.SubscribeRequest{FilenamePatterns: []string{"c"}}))
		res, err := stream.Receive()
		require.NoError(t, err)
		assert.Equal(t, "c", res.GetFilenameChanged())
		require.NoError(t, stream.CloseSend())
		_, err = stream.Receive()
		require.ErrorIs(t, err, io.EOF)
		require.NoError(t, stream.Close())
	})
}

type forwardBackend struct {
	testv1connect.UnimplementedLibraryServiceHandler

	t *testing.T
}

func (b forwardBackend) GetBook(ctx context.Context, req *testv1.GetBookRequest) (*testv1.Book, error) {
	info, ok := connect.CallInfoForServerContext(ctx)
	require.True(b.t, ok)
	assert.Equal(b.t, "acme", info.RequestHeader().Get("X-Tenant"))
	assert.Empty(b.t, info.RequestHeader().Get("Connection"))
	info.ResponseHeader().Set("X-Backend", "yes")
	info.ResponseTrailer().Set("X-Backend-Trailer", "bye")
	if req.GetName() == "shelves/s/books/denied" {
		detail, err := proto.Marshal(&testv1.Book{Name: req.GetName()})
		require.NoError(b.t, err)
		return nil, connect.NewError(connect.CodePermissionDenied, "denied").
			WithDetail(&connect.ErrorDetail{Type: "vanguard.test.v1.Book", Value: detail})
	}
	return &testv1.Book{Name: req.GetName()}, nil
}

func newHTTP2Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}
