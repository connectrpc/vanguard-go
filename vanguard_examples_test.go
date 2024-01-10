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

package vanguard_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/vanguard"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func Example_restClientToRpcServer() {
	// This example shows Vanguard adding REST support to an RPC server built
	// with Connect. (To add REST, gRPC-Web, and Connect support to servers built
	// with grpc-go, use the connectrpc.com/vanguard/vanguardgrpc sub-package.)
	logger := log.New(os.Stdout, "" /* prefix */, 0 /* flags */)

	// libraryRPC is an implementation of the testv1connect.LibraryService RPC
	// server. It's a pure RPC server, without any hand-written translation to or
	// from RESTful semantics.
	svc := &libraryRPC{}
	rpcRoute, rpcHandler := testv1connect.NewLibraryServiceHandler(svc)

	// Using Vanguard, the server can also accept RESTful requests. The Vanguard
	// Transcoder handles both REST and RPC traffic, so there's no need to mount
	// the RPC-only handler.
	services := []*vanguard.Service{vanguard.NewService(rpcRoute, rpcHandler)}
	transcoder, err := vanguard.NewTranscoder(services)
	if err != nil {
		logger.Println(err)
		return
	}

	// We can use any server that works with http.Handlers. Since this is a
	// testable example, we're using httptest.
	server := httptest.NewServer(transcoder)
	defer server.Close()

	// With the server running, we can make a RESTful call.
	client := server.Client()
	book := &testv1.Book{
		Title:       "2001: A Space Odyssey",
		Author:      "Arthur C. Clarke",
		Description: "A space voyage to Jupiter awakens the crew's intelligence.",
		Labels: map[string]string{
			"genre": "science fiction",
		},
	}
	body, err := protojson.Marshal(book)
	if err != nil {
		logger.Println(err)
		return
	}

	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost,
		server.URL+"/v1/shelves/top/books",
		bytes.NewReader(body),
	)
	if err != nil {
		logger.Println(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.URL.RawQuery = "book_id=2&request_id=123"

	rsp, err := client.Do(req)
	if err != nil {
		logger.Println(err)
		return
	}
	defer rsp.Body.Close()
	logger.Println(rsp.Status)
	logger.Println(rsp.Header.Get("Content-Type"))

	body, err = io.ReadAll(rsp.Body)
	if err != nil {
		logger.Println(err)
		return
	}
	if err := protojson.Unmarshal(body, book); err != nil {
		logger.Println(err)
		return
	}
	logger.Println(book.Author)
	// Output: 200 OK
	// application/json
	// Arthur C. Clarke
}

func Example_rpcClientToRestServer() {
	// This example shows Vanguard adding RPC support to an REST server. This
	// lets organizations use RPC clients in new codebases without rewriting
	// existing REST services.
	logger := log.New(os.Stdout, "" /* prefix */, 0 /* flags */)

	// libraryREST is an http.Handler that implements a RESTful server. The
	// implementation doesn't use Protobuf or RPC directly.
	restHandler := &libraryREST{}

	// Using Vanguard, the server can also accept RPC traffic. The Vanguard
	// Transcoder handles both REST and RPC traffic, so there's no need to mount
	// the REST-only handler.
	services := []*vanguard.Service{vanguard.NewService(
		testv1connect.LibraryServiceName,
		restHandler,
		// This tells vanguard that the service implementation only supports REST.
		vanguard.WithTargetProtocols(vanguard.ProtocolREST),
	)}
	transcoder, err := vanguard.NewTranscoder(services)
	if err != nil {
		logger.Println(err)
		return
	}

	// We can serve RPC and REST traffic using any server that works with
	// http.Handlers. Since this is a testable example, we're using httptest.
	server := httptest.NewServer(transcoder)
	defer server.Close()

	// With the server running, we can make an RPC call using a generated client.
	client := testv1connect.NewLibraryServiceClient(server.Client(), server.URL)
	rsp, err := client.GetBook(
		context.Background(),
		connect.NewRequest(&testv1.GetBookRequest{
			Name: "shelves/top/books/123",
		}),
	)
	if err != nil {
		logger.Println(err)
		return
	}
	logger.Println(rsp.Msg.Description)
	// Output: Have you seen Blade Runner?
}

type libraryREST struct {
	libraryRPC
}

func (s *libraryREST) ServeHTTP(rsp http.ResponseWriter, req *http.Request) {
	urlPath := []byte(req.URL.Path)
	ctx := req.Context()
	var msg proto.Message
	var err error
	switch req.Method {
	case http.MethodGet:
		switch {
		case regexp.MustCompile("/v1/shelves/.*/books/.*").Match(urlPath):
			got, gotErr := s.GetBook(ctx, connect.NewRequest(&testv1.GetBookRequest{
				Name: req.URL.Path[len("/v1/"):],
			}))
			msg, err = got.Msg, gotErr
		default:
			err = connect.NewError(connect.CodeNotFound, fmt.Errorf("method not found"))
		}
	case http.MethodPost:
		switch {
		case regexp.MustCompile("/v1/shelves/.*").Match(urlPath):
			var book testv1.Book
			body, _ := io.ReadAll(req.Body)
			_ = protojson.Unmarshal(body, &book)
			got, gotErr := s.CreateBook(ctx, connect.NewRequest(&testv1.CreateBookRequest{
				Parent:    req.URL.Path[len("/v1/"):],
				BookId:    req.URL.Query().Get("book_id"),
				Book:      &book,
				RequestId: req.URL.Query().Get("request_id"),
			}))
			msg, err = got.Msg, gotErr
		default:
			err = connect.NewError(connect.CodeNotFound, fmt.Errorf("method not found"))
		}
	default:
		err = connect.NewError(connect.CodeNotFound, fmt.Errorf("method not found"))
	}
	rsp.Header().Set("Content-Type", "application/json")
	var body []byte
	if err != nil {
		code := connect.CodeInternal
		if ce := (*connect.Error)(nil); errors.As(err, &ce) {
			code = ce.Code()
		}
		body = []byte(`{"code":` + strconv.Itoa(int(code)) +
			`, "message":"` + err.Error() + `"}`)
	} else {
		body, _ = protojson.Marshal(msg)
	}
	rsp.WriteHeader(http.StatusOK)
	_, _ = rsp.Write(body)
}

type libraryRPC struct {
	testv1connect.UnimplementedLibraryServiceHandler
}

func (s *libraryRPC) GetBook(_ context.Context, req *connect.Request[testv1.GetBookRequest]) (*connect.Response[testv1.Book], error) {
	msg := req.Msg
	rsp := connect.NewResponse(&testv1.Book{
		Name:        msg.Name,
		Parent:      strings.Join(strings.Split(msg.Name, "/")[:2], "/"),
		CreateTime:  timestamppb.New(time.Date(1968, 1, 1, 0, 0, 0, 0, time.UTC)),
		Title:       "Do Androids Dream of Electric Sheep?",
		Author:      "Philip K. Dick",
		Description: "Have you seen Blade Runner?",
		Labels: map[string]string{
			"genre": "science fiction",
		},
	})
	return rsp, nil
}

func (s *libraryRPC) CreateBook(_ context.Context, req *connect.Request[testv1.CreateBookRequest]) (*connect.Response[testv1.Book], error) {
	msg := req.Msg
	book := req.Msg.Book
	rsp := connect.NewResponse(&testv1.Book{
		Name:        strings.Join([]string{msg.Parent, "books", msg.BookId}, "/"),
		Parent:      msg.Parent,
		CreateTime:  timestamppb.New(time.Date(1968, 1, 1, 0, 0, 0, 0, time.UTC)),
		Title:       book.GetTitle(),
		Author:      book.GetAuthor(),
		Description: book.GetDescription(),
		Labels:      book.GetLabels(),
	})
	return rsp, nil
}
