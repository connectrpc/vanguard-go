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
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func BenchmarkServeHTTP(b *testing.B) {
	ctx := context.Background()
	jsonCodec := NewJSONCodec(protoregistry.GlobalTypes)
	marshalJSON := func(msg proto.Message) []byte {
		var buf bytes.Buffer
		require.NoError(b, jsonCodec.MarshalWrite(ctx, &buf, msg))
		return buf.Bytes()
	}
	compress := func(data []byte) []byte {
		var buf bytes.Buffer
		writer := gzip.NewWriter(&buf)
		_, _ = writer.Write(data)
		_ = writer.Close()
		return buf.Bytes()
	}

	reqMsg := &testv1.CreateBookRequest{
		Parent: "shelves/456",
		BookId: "123",
		Book: &testv1.Book{
			CreateTime:  timestamppb.New(time.Date(1968, 1, 1, 0, 0, 0, 0, time.UTC)),
			Title:       "Lorem ipsum dolor sit amet",
			Author:      "Lorem ipsum",
			Description: strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ", 100),
		},
		RequestId: "abc",
	}
	reqMsgBookJSON := marshalJSON(reqMsg.GetBook())
	rspMsg := &testv1.Book{
		Name:        "books/123",
		Parent:      "shelves/456",
		CreateTime:  reqMsg.GetBook().GetCreateTime(),
		UpdateTime:  timestamppb.New(time.Date(2023, 9, 1, 0, 0, 0, 0, time.UTC)),
		Title:       reqMsg.GetBook().GetTitle(),
		Author:      reqMsg.GetBook().GetAuthor(),
		Description: reqMsg.GetBook().GetDescription(),
		Labels:      map[string]string{"genre": "science fiction"},
	}
	largePayload := make([]byte, 16*1024*1024)
	_, _ = rand.Read(largePayload)
	uploadPayload := largePayload[:1024*1024]
	const chunkSize = 1024 * 1024

	server := connect.NewServer()
	register := func(name protoreflect.FullName, streamType connect.StreamType, serve connect.ServerFunc) {
		desc, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		require.NoError(b, err)
		methodDesc, ok := desc.(protoreflect.MethodDescriptor)
		require.True(b, ok)
		server.Register(connect.Method{
			Spec: connect.Spec{
				StreamType: streamType,
				Schema:     methodDesc,
				Procedure:  "/" + string(desc.Parent().FullName()) + "/" + string(desc.Name()),
			},
			Handler: serve,
		})
	}
	register("vanguard.test.v1.LibraryService.CreateBook", connect.StreamTypeUnary,
		func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			if err := stream.Receive(&testv1.CreateBookRequest{}); err != nil {
				return err
			}
			return stream.Send(rspMsg)
		})
	register("vanguard.test.v1.ContentService.Index", connect.StreamTypeUnary,
		func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			if err := stream.Receive(&testv1.IndexRequest{}); err != nil {
				return err
			}
			return stream.Send(&httpbody.HttpBody{ContentType: "application/octet-stream", Data: largePayload})
		})
	register("vanguard.test.v1.ContentService.Upload", connect.StreamTypeClient,
		func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			for {
				if err := stream.Receive(&testv1.UploadRequest{}); errors.Is(err, io.EOF) {
					break
				} else if err != nil {
					return err
				}
			}
			return stream.Send(&emptypb.Empty{})
		})
	register("vanguard.test.v1.ContentService.Download", connect.StreamTypeServer,
		func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			if err := stream.Receive(&testv1.DownloadRequest{}); err != nil {
				return err
			}
			for data := largePayload; len(data) > 0; data = data[chunkSize:] {
				if err := stream.Send(&testv1.DownloadResponse{
					File: &httpbody.HttpBody{ContentType: "application/octet-stream", Data: data[:chunkSize]},
				}); err != nil {
					return err
				}
			}
			return nil
		})
	mux := http.NewServeMux()
	require.NoError(b, Mount(mux, server))

	run := func(b *testing.B, req *http.Request, body []byte, wantLen int) {
		b.Helper()
		newRequest := func() *http.Request {
			clone := req.Clone(ctx)
			if body != nil {
				clone.Body = io.NopCloser(bytes.NewReader(body))
			}
			return clone
		}
		rsp := httptest.NewRecorder()
		mux.ServeHTTP(rsp, newRequest())
		require.Equal(b, http.StatusOK, rsp.Code, "body=%s", rsp.Body.String())
		require.Len(b, rsp.Body.Bytes(), wantLen)

		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				rsp := httptest.NewRecorder()
				mux.ServeHTTP(rsp, newRequest())
				if rsp.Code != http.StatusOK || rsp.Body.Len() != wantLen {
					b.Errorf("code=%d len=%d", rsp.Code, rsp.Body.Len())
				}
			}
		})
	}

	b.Run("REST_json/Unary", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodPost, "/v1/shelves/456/books?book_id=123&request_id=abc", nil)
		req.Header.Set("Content-Type", "application/json")
		run(b, req, reqMsgBookJSON, len(marshalJSON(rspMsg)))
	})
	b.Run("REST_json/Unary_gzip", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodPost, "/v1/shelves/456/books?book_id=123&request_id=abc", nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", "gzip")
		req.Header.Set("Accept-Encoding", "gzip")
		run(b, req, compress(reqMsgBookJSON), len(compress(marshalJSON(rspMsg))))
	})
	b.Run("REST_HttpBody/Upload_1MiB", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodPost, "/file.bin:upload", nil)
		req.Header.Set("Content-Type", "application/octet-stream")
		run(b, req, uploadPayload, len("{}"))
	})
	b.Run("REST_HttpBody/Index_16MiB", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "/file.bin", nil)
		run(b, req, nil, len(largePayload))
	})
	b.Run("REST_HttpBody/Download_16MiB", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "/file.bin:download", nil)
		run(b, req, nil, len(largePayload))
	})
}
