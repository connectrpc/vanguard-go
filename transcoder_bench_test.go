// Copyright 2023-2024 Buf Technologies, Inc.
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
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func BenchmarkServeHTTP(b *testing.B) {
	// This benchmark is intended to measure the overhead of the
	// ServeHTTP method of the server. It does not measure the
	// overhead of the underlying HTTP server.
	jsonCodec := NewJSONCodec(protoregistry.GlobalTypes)

	compress := func(b []byte) []byte {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		_, _ = w.Write(b)
		_ = w.Close()
		return buf.Bytes()
	}
	envelopePayload := func(flags uint8, msg []byte) []byte {
		head := [5]byte{}
		head[0] = flags
		binary.BigEndian.PutUint32(head[1:5], uint32(len(msg)))
		body := &bytes.Buffer{}
		body.Write(head[:])
		body.Write(msg)
		return body.Bytes()
	}
	marshalJSON := func(msg proto.Message) []byte {
		data, err := jsonCodec.MarshalAppend(nil, msg)
		assert.NoError(b, err)
		var buf bytes.Buffer
		assert.NoError(b, json.Compact(&buf, data))
		return buf.Bytes()
	}
	marshalProto := func(msg proto.Message) []byte {
		data, err := proto.Marshal(msg)
		assert.NoError(b, err)
		return data
	}

	reqRestURL := "/v1/shelves/456/books?book_id=123&request_id=abc"
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
	reqMsgProto := marshalProto(reqMsg)
	reqMsgProtoComp := compress(reqMsgProto)
	reqMsgJSON := marshalJSON(reqMsg)
	reqMsgBookJSON := marshalJSON(reqMsg.Book)

	rspMsg := &testv1.Book{
		Name:        "books/123",
		Parent:      "shelves/456",
		CreateTime:  reqMsg.Book.CreateTime,
		UpdateTime:  timestamppb.New(time.Date(2023, 9, 1, 0, 0, 0, 0, time.UTC)),
		Title:       reqMsg.Book.Title,
		Author:      reqMsg.Book.Author,
		Description: reqMsg.Book.Description,
		Labels: map[string]string{
			"genre": "science fiction",
		},
	}
	rspMsgProto := marshalProto(rspMsg)
	rspMsgProtoComp := compress(rspMsgProto)
	rspMsgJSON := marshalJSON(rspMsg)

	ctx := context.Background()
	benchHandler := func(_ testing.TB, rspBody []byte, rspHdr, rspTrl http.Header) http.HandlerFunc {
		return func(rsp http.ResponseWriter, req *http.Request) {
			_, _ = io.Copy(io.Discard, req.Body)
			hdr := rsp.Header()
			for key, vals := range rspHdr {
				hdr[key] = vals
			}
			rsp.WriteHeader(http.StatusOK)
			_, _ = rsp.Write(rspBody)
			for key, vals := range rspTrl {
				hdr[key] = vals
			}
		}
	}

	b.Run("gRPC_proto_gzip/gRPC_proto_gzip/PassThrough", func(b *testing.B) {
		reqGRPCBody := envelopePayload(1, reqMsgProtoComp)
		rspGRPCBody := envelopePayload(1, rspMsgProtoComp)

		handler, err := NewTranscoder(
			[]*Service{NewService(
				testv1connect.LibraryServiceName,
				benchHandler(b, rspGRPCBody, http.Header{
					"Grpc-Encoding": []string{"gzip"},
					"Content-Type":  []string{"application/grpc+proto"},
					"Trailer":       []string{"Grpc-Status, Grpc-Message"},
				}, http.Header{
					"Grpc-Status": []string{"0"},
				}),
				WithTargetProtocols(ProtocolGRPC),
			)},
		)
		require.NoError(b, err)

		req := httptest.NewRequest(http.MethodPost, testv1connect.LibraryServiceCreateBookProcedure, nil)
		req.ProtoMajor = 2
		req.ProtoMinor = 0
		req.Header.Set("Content-Type", "application/grpc+proto")
		req.Header.Set("Grpc-Encoding", "gzip")
		req.Header.Set("Grpc-Timeout", "1S")
		req.Header.Set("Grpc-Accept-Encoding", "gzip")

		b.StartTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req := req.Clone(ctx)
				req.Body = io.NopCloser(bytes.NewReader(reqGRPCBody))
				rsp := httptest.NewRecorder()

				handler.ServeHTTP(rsp, req)
				assert.Equal(b, http.StatusOK, rsp.Code, "response code")
				assert.Equal(b, "0", rsp.Header().Get("Grpc-Status"), "response status")
				assert.Equal(b, rspGRPCBody, rsp.Body.Bytes(), "response body")
			}
		})
		b.StopTimer()
	})

	b.Run("REST_json/gRPC_proto/Convert", func(b *testing.B) {
		rspGRPCBody := envelopePayload(0, rspMsgProto)

		handler, err := NewTranscoder(
			[]*Service{NewService(
				testv1connect.LibraryServiceName,
				benchHandler(b, rspGRPCBody, http.Header{
					"Content-Type": []string{"application/grpc+proto"},
					"Trailer":      []string{"Grpc-Status, Grpc-Message"},
				}, http.Header{
					"Grpc-Status": []string{"0"},
				}),
				WithTargetProtocols(ProtocolGRPC),
				WithTargetCodecs(CodecProto),
				WithNoTargetCompression(),
			)},
		)
		require.NoError(b, err)

		req := httptest.NewRequest(http.MethodPost, reqRestURL, bytes.NewReader(reqMsgBookJSON))
		req.Header.Set("Accept-Encoding", CompressionIdentity)
		req.Header.Set("Content-Encoding", CompressionIdentity)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Server-Timeout", "1000")

		b.StartTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req := req.Clone(ctx)
				req.Body = io.NopCloser(bytes.NewReader(reqMsgBookJSON))
				rsp := httptest.NewRecorder()

				handler.ServeHTTP(rsp, req)
				assert.Equal(b, http.StatusOK, rsp.Code, "response code")
				assert.Equal(b, "application/json", rsp.Header().Get("Content-Type"), "response content type")
				data := rsp.Body.Bytes()
				rsp.Body.Reset()
				assert.NoError(b, json.Compact(rsp.Body, data))
				assert.Equal(b, rspMsgJSON, rsp.Body.Bytes(), "response body")
			}
		})
		b.StopTimer()
	})

	b.Run("gRPC_proto/REST_json/Convert", func(b *testing.B) {
		reqGRPCBody := envelopePayload(0, reqMsgProto)
		rspGRPCBody := envelopePayload(0, rspMsgProto)

		handler, err := NewTranscoder(
			[]*Service{NewService(
				testv1connect.LibraryServiceName,
				benchHandler(b, rspMsgJSON, http.Header{
					"Content-Type": []string{"application/json"},
				}, nil),
				WithTargetProtocols(ProtocolREST),
				WithTargetCodecs(CodecJSON),
				WithNoTargetCompression(),
			)},
		)
		require.NoError(b, err)

		req := httptest.NewRequest(http.MethodPost, testv1connect.LibraryServiceCreateBookProcedure, nil)
		req.ProtoMajor = 2
		req.ProtoMinor = 0
		req.Header.Set("Content-Type", "application/grpc+proto")
		req.Header.Set("Grpc-Encoding", "gzip")
		req.Header.Set("Grpc-Timeout", "1S")
		req.Header.Set("Grpc-Accept-Encoding", "gzip")

		b.StartTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req := req.Clone(ctx)
				req.Body = io.NopCloser(bytes.NewReader(reqGRPCBody))
				rsp := httptest.NewRecorder()

				handler.ServeHTTP(rsp, req)
				assert.Equal(b, http.StatusOK, rsp.Code, "response code")
				assert.Equal(b, "application/grpc+proto", rsp.Header().Get("Content-Type"), "response content type")
				assert.Equal(b, rspGRPCBody, rsp.Body.Bytes(), "response body")
			}
		})
		b.StopTimer()
	})

	b.Run("connect_json/gRPC_proto/Convert", func(b *testing.B) {
		rspGRPCBody := envelopePayload(0, rspMsgProto)

		handler, err := NewTranscoder(
			[]*Service{NewService(
				testv1connect.LibraryServiceName,
				benchHandler(b, rspGRPCBody, http.Header{
					"Content-Type": []string{"application/grpc+proto"},
					"Trailer":      []string{"Grpc-Status, Grpc-Message"},
				}, http.Header{
					"Grpc-Status": []string{"0"},
				}),
				WithTargetProtocols(ProtocolGRPC),
				WithTargetCodecs(CodecProto),
				WithNoTargetCompression(),
			)},
		)
		require.NoError(b, err)

		req := httptest.NewRequest(http.MethodPost, testv1connect.LibraryServiceCreateBookProcedure, nil)
		req.ProtoMajor = 2
		req.ProtoMinor = 0
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Connect-Protocol-Version", "1")
		req.Header.Set("Connect-Timeout-Ms", "1000")
		req.ContentLength = int64(len(reqMsgJSON))

		b.StartTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			req := req.Clone(ctx)
			req.Body = io.NopCloser(bytes.NewReader(reqMsgJSON))
			rsp := httptest.NewRecorder()

			handler.ServeHTTP(rsp, req)
			assert.Equal(b, http.StatusOK, rsp.Code, "response code")
			assert.Equal(b, "application/json", rsp.Header().Get("Content-Type"), "response content type")
			data := rsp.Body.Bytes()
			rsp.Body.Reset()
			assert.NoError(b, json.Compact(rsp.Body, data))
			assert.Equal(b, rspMsgJSON, rsp.Body.Bytes(), "response body")
		}
		b.StopTimer()
	})

	b.Run("connect_proto_gzip/gRPC_proto_gzip/Translate", func(b *testing.B) {
		rspGRPCBody := envelopePayload(1, rspMsgProtoComp)

		handler, err := NewTranscoder(
			[]*Service{NewService(
				testv1connect.LibraryServiceName,
				benchHandler(b, rspGRPCBody, http.Header{
					"Content-Type":  []string{"application/grpc+proto"},
					"Trailer":       []string{"Grpc-Status, Grpc-Message"},
					"Grpc-Encoding": []string{"gzip"},
				}, http.Header{
					"Grpc-Status": []string{"0"},
				}),
				WithTargetProtocols(ProtocolGRPC),
				WithTargetCodecs(CodecProto),
			)},
		)
		require.NoError(b, err)

		req := httptest.NewRequest(http.MethodPost, testv1connect.LibraryServiceCreateBookProcedure, nil)
		req.ProtoMajor = 2
		req.ProtoMinor = 0
		req.Header.Set("Content-Type", "application/proto")
		req.Header.Set("Encoding", "gzip")
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Connect-Protocol-Version", "1")
		req.Header.Set("Connect-Timeout-Ms", "1000")
		req.ContentLength = int64(len(reqMsgProtoComp))

		b.StartTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req := req.Clone(ctx)
				req.Body = io.NopCloser(bytes.NewReader(reqMsgProtoComp))
				rsp := httptest.NewRecorder()

				handler.ServeHTTP(rsp, req)
				assert.Equal(b, http.StatusOK, rsp.Code, "response code")
				assert.Equal(b, "application/proto", rsp.Header().Get("Content-Type"), "response content type")
				assert.Equal(b, rspMsgProtoComp, rsp.Body.Bytes(), "response body")
			}
		})
		b.StopTimer()
	})

	b.Run("REST_json/gRPC_proto/LargeHttpBody", func(b *testing.B) {
		largePayload := make([]byte, maxRecycleBufferSize*2)
		_, _ = rand.Read(largePayload)
		rspGRPC := envelopePayload(0, marshalProto(&emptypb.Empty{}))

		handler, err := NewTranscoder(
			[]*Service{NewService(
				testv1connect.ContentServiceName,
				benchHandler(b, rspGRPC, http.Header{
					"Content-Type":  []string{"application/grpc+proto"},
					"Trailer":       []string{"Grpc-Status, Grpc-Message"},
					"Grpc-Encoding": []string{"gzip"},
				}, http.Header{
					"Grpc-Status": []string{"0"},
				}),
				WithTargetProtocols(ProtocolGRPC),
				WithTargetCodecs(CodecProto),
			)},
		)
		require.NoError(b, err)

		req := httptest.NewRequest(http.MethodPost, "/file.bin:upload", nil)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("X-Server-Timeout", "1000")
		req.ContentLength = int64(len(largePayload))

		b.StartTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req := req.Clone(ctx)
				req.Body = io.NopCloser(bytes.NewReader(largePayload))
				rsp := httptest.NewRecorder()

				handler.ServeHTTP(rsp, req)
				assert.Equal(b, http.StatusOK, rsp.Code, "response code")
				assert.Equal(b, "application/json", rsp.Header().Get("Content-Type"), "response content type")
				assert.Equal(b, "{}", rsp.Body.String(), "response body")
			}
		})
		b.StopTimer()
	})
}
