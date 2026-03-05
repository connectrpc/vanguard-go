// Copyright 2023-2025 Buf Technologies, Inc.
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

package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/vanguard"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	flagset := flag.NewFlagSet("sse-streaming", flag.ExitOnError)
	port := flagset.String("p", "8080", "port to serve on")
	if err := flagset.Parse(flag.Args()); err != nil {
		log.Fatal(err)
	}

	// Create Connect handler for the streaming service.
	serviceHandler := &StreamingServiceHandler{}

	// Wrap it with Vanguard and enable REST streaming with SSE.
	// Field-specific configuration (event names, IDs, omission) is specified
	// per-RPC in the protobuf annotations using response_body directives.
	path, handler := testv1connect.NewStreamingServiceHandler(serviceHandler)
	service := vanguard.NewService(
		path,
		handler,
		vanguard.WithRESTServerSentEvents(),
	)

	transcoder, err := vanguard.NewTranscoder([]*vanguard.Service{service})
	if err != nil {
		log.Fatal(err)
	}

	// Add a simple HTML page at root for testing
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transcoder.ServeHTTP(w, r)
	}))
	mux.Handle("/v1/", transcoder)

	log.Printf("Server listening on http://localhost:%s", *port)
	log.Printf("Try: curl -N -H 'Accept: text/event-stream' http://localhost:%s/v1/count", *port)
	log.Fatal(http.ListenAndServe(":"+*port, mux))
}

type StreamingServiceHandler struct {
	testv1connect.UnimplementedStreamingServiceHandler
}

func (s *StreamingServiceHandler) CountToTen(
	ctx context.Context,
	req *connect.Request[testv1.CountRequest],
	stream *connect.ServerStream[testv1.CountResponse],
) error {
	delay := time.Duration(req.Msg.DelayMs) * time.Millisecond
	if delay == 0 {
		delay = 500 * time.Millisecond
	}

	log.Printf("Starting CountToTen with delay=%v", delay)

	for i := int32(1); i <= 10; i++ {
		if err := ctx.Err(); err != nil {
			log.Printf("Context cancelled: %v", err)
			return connect.NewError(connect.CodeCanceled, err)
		}

		if err := stream.Send(&testv1.CountResponse{
			Number:    i,
			Timestamp: timestamppb.Now(),
		}); err != nil {
			log.Printf("Error sending: %v", err)
			return err
		}

		log.Printf("Sent: %d", i)

		if i < 10 {
			time.Sleep(delay)
		}
	}

	log.Printf("CountToTen completed successfully")
	return nil
}

func (s *StreamingServiceHandler) WatchBooks(
	ctx context.Context,
	req *connect.Request[testv1.WatchBooksRequest],
	stream *connect.ServerStream[testv1.BookUpdate],
) error {
	parent := req.Msg.Parent
	if parent == "" {
		parent = "shelves/default"
	}

	log.Printf("Starting WatchBooks for %s", parent)

	// Simulate some book updates
	updates := []struct {
		updateType testv1.BookUpdate_UpdateType
		bookName   string
	}{
		{testv1.BookUpdate_CREATED, parent + "/books/go-programming"},
		{testv1.BookUpdate_CREATED, parent + "/books/distributed-systems"},
		{testv1.BookUpdate_UPDATED, parent + "/books/go-programming"},
		{testv1.BookUpdate_CREATED, parent + "/books/database-internals"},
		{testv1.BookUpdate_DELETED, parent + "/books/distributed-systems"},
	}

	for _, upd := range updates {
		if err := ctx.Err(); err != nil {
			return connect.NewError(connect.CodeCanceled, err)
		}

		if err := stream.Send(&testv1.BookUpdate{
			Type:      upd.updateType,
			BookName:  upd.bookName,
			Timestamp: timestamppb.Now(),
		}); err != nil {
			return err
		}

		log.Printf("Sent update: %v %s", upd.updateType, upd.bookName)
		time.Sleep(1 * time.Second)
	}

	log.Printf("WatchBooks completed")
	return nil
}

func (s *StreamingServiceHandler) StreamVideo(
	ctx context.Context,
	req *connect.Request[testv1.StreamVideoRequest],
	stream *connect.ServerStream[httpbody.HttpBody],
) error {
	videoID := req.Msg.VideoId
	log.Printf("Starting StreamVideo for %s", videoID)

	// Simulate video streaming with chunks of binary data
	// This demonstrates HttpBody streaming which works WITHOUT SSE
	chunks := []string{
		"CHUNK 1: [simulated video data...]\n",
		"CHUNK 2: [more video data...]\n",
		"CHUNK 3: [even more video data...]\n",
		"CHUNK 4: [final video data...]\n",
	}

	for i, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return connect.NewError(connect.CodeCanceled, err)
		}

		if err := stream.Send(&httpbody.HttpBody{
			ContentType: "application/octet-stream",
			Data:        []byte(chunk),
		}); err != nil {
			return err
		}

		log.Printf("Sent video chunk %d", i+1)
		time.Sleep(500 * time.Millisecond)
	}

	log.Printf("StreamVideo completed")
	return nil
}

func (s *StreamingServiceHandler) GetStatus(
	ctx context.Context,
	req *connect.Request[testv1.GetStatusRequest],
) (*connect.Response[testv1.StatusResponse], error) {
	log.Printf("GetStatus called")

	// This is a standard unary RPC - returns a single response
	return connect.NewResponse(&testv1.StatusResponse{
		Status:        "healthy",
		ActiveStreams: 0,
		Timestamp:     timestamppb.Now(),
	}), nil
}
