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

package vanguardgrpc_test

import (
	"context"
	"log"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/vanguard"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"connectrpc.com/vanguard/vanguardgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ExampleNewTranscoder_connectToGRPC() {
	log := log.New(os.Stdout, "" /* prefix */, 0 /* flags */)

	// Configure gRPC servers to support JSON.
	encoding.RegisterCodec(vanguardgrpc.NewCodec(&vanguard.JSONCodec{
		MarshalOptions:   protojson.MarshalOptions{EmitUnpopulated: true},
		UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: true},
	}))

	// Now create a gRPC server that provides the test LibraryService.
	svc := &libraryRPC{}
	grpcServer := grpc.NewServer()
	testv1.RegisterLibraryServiceServer(grpcServer, svc)

	// Create a vanguard handler for all services registered in grpcServer
	handler, err := vanguardgrpc.NewTranscoder(grpcServer)
	if err != nil {
		log.Println("error:", err)
		return
	}

	// Create the server.
	// (This is a httptest.Server, but it could be any http.Server)
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true // HTTP/2 required for gRPC
	server.StartTLS()
	defer server.Close()

	// Create a connect client and call the service.
	client := testv1connect.NewLibraryServiceClient(server.Client(), server.URL, connect.WithProtoJSON())

	// Call the service using Connect, translated by the middleware to gRPC.
	rsp, err := client.GetBook(
		context.Background(),
		connect.NewRequest(&testv1.GetBookRequest{
			Name: "shelves/top/books/1",
		}),
	)
	if err != nil {
		log.Println("error:", err)
		return
	}
	log.Println(rsp.Msg.GetTitle())
	// Output: Do Androids Dream of Electric Sheep?
}

type libraryRPC struct {
	testv1.UnimplementedLibraryServiceServer
}

func (s *libraryRPC) GetBook(_ context.Context, req *testv1.GetBookRequest) (*testv1.Book, error) {
	return &testv1.Book{
		Name:        req.GetName(),
		Parent:      strings.Join(strings.Split(req.GetName(), "/")[:2], "/"),
		CreateTime:  timestamppb.New(time.Date(1968, 1, 1, 0, 0, 0, 0, time.UTC)),
		Title:       "Do Androids Dream of Electric Sheep?",
		Author:      "Philip K. Dick",
		Description: "Have you seen Blade Runner?",
		Labels: map[string]string{
			"genre": "science fiction",
		},
	}, nil
}

func (s *libraryRPC) CreateBook(_ context.Context, req *testv1.CreateBookRequest) (*testv1.Book, error) {
	book := req.GetBook()
	return &testv1.Book{
		Name:        strings.Join([]string{req.GetParent(), "books", req.GetBookId()}, "/"),
		Parent:      req.GetParent(),
		CreateTime:  timestamppb.New(time.Date(1968, 1, 1, 0, 0, 0, 0, time.UTC)),
		Title:       book.GetTitle(),
		Author:      book.GetAuthor(),
		Description: book.GetDescription(),
		Labels:      book.GetLabels(),
	}, nil
}
