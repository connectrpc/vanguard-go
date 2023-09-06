// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

//nolint:gocritic
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
	"github.com/bufbuild/vanguard"
	testv1 "github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1"
	"github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1/testv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ExampleMux_connectToGRPC() {
	log := log.New(os.Stdout, "" /* prefix */, 0 /* flags */)

	// RPC service implementing testv1connect.LibraryService annotations.
	svc := &libraryRPC{}

	httpMux := http.NewServeMux()
	httpMux.Handle(testv1connect.NewLibraryServiceHandler(svc))

	// Create a Mux and register the service as a gRPC service.
	mux := &vanguard.Mux{}
	if err := mux.RegisterServiceByName(
		httpMux, testv1connect.LibraryServiceName,
		vanguard.WithProtocols(vanguard.ProtocolGRPC),
		vanguard.WithNoCompression(),
	); err != nil {
		log.Fatal(err)
	}

	// Create the server.
	// (NB: This is a httptest.Server, but it could be any http.Server)
	svr := httptest.NewUnstartedServer(mux.AsHandler())
	svr.EnableHTTP2 = true
	svr.StartTLS()
	defer svr.Close()

	// Create a connect client and call the service.
	client := testv1connect.NewLibraryServiceClient(svr.Client(), svr.URL)

	// Call the service using Connect translated by the middleware to
	// gRPC.
	rsp, err := client.GetBook(
		context.Background(),
		connect.NewRequest(&testv1.GetBookRequest{
			Name: "shelves/top/books/1",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	log.Println(rsp.Msg.Title)
	// Output: Do Androids Dream of Electric Sheep?
}

func ExampleMux_restToGRPC() {
	log := log.New(os.Stdout, "" /* prefix */, 0 /* flags */)

	// RPC service implementing testv1connect.LibraryService annotations.
	svc := &libraryRPC{}

	httpMux := http.NewServeMux()
	httpMux.Handle(testv1connect.NewLibraryServiceHandler(svc))

	// Create a Mux and register the service as a gRPC service.
	mux := &vanguard.Mux{}
	if err := mux.RegisterServiceByName(
		httpMux, testv1connect.LibraryServiceName,
		vanguard.WithProtocols(vanguard.ProtocolGRPC),
		vanguard.WithNoCompression(),
	); err != nil {
		log.Fatal(err)
	}

	// Create the server.
	// (NB: This is a httptest.Server, but it could be any http.Server)
	svr := httptest.NewServer(mux.AsHandler())
	defer svr.Close()
	client := svr.Client()

	book := &testv1.Book{
		Title:       "2001: A Space Odyssey",
		Author:      "Arthur C. Clarke",
		Description: "A space voyage to Jupiter awakens the crew's intelligence.",
		Labels: map[string]string{
			"genre": "science fiction",
		},
	}
	body, _ := protojson.Marshal(book)

	// Create the POST request.
	req, _ := http.NewRequestWithContext(
		context.Background(), http.MethodPost,
		svr.URL+"/v1/shelves/top/books",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req.URL.RawQuery = "book_id=2&request_id=123"

	rsp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer rsp.Body.Close()
	log.Println(rsp.Status)
	log.Println(rsp.Header.Get("Content-Type"))

	// Decode the response.
	body, _ = io.ReadAll(rsp.Body)

	_ = protojson.Unmarshal(body, book)
	log.Println(book.Author)
	// Output: 200 OK
	// application/json
	// Arthur C. Clarke
}

func ExampleMux_connectToREST() {
	log := log.New(os.Stdout, "" /* prefix */, 0 /* flags */)

	// REST service implementing testv1connect.LibraryService annotations.
	svc := &libraryREST{}

	// Create a Mux and register the service as a REST service.
	mux := &vanguard.Mux{}
	if err := mux.RegisterServiceByName(
		svc, testv1connect.LibraryServiceName,
		vanguard.WithProtocols(vanguard.ProtocolREST),
		vanguard.WithCodecs(vanguard.CodecJSON),
		vanguard.WithNoCompression(),
	); err != nil {
		log.Fatal(err)
	}

	// Create the server.
	// (NB: This is a httptest.Server, but it could be any http.Server)
	svr := httptest.NewServer(mux.AsHandler())
	defer svr.Close()

	// Create a connect client and call the service.
	client := testv1connect.NewLibraryServiceClient(svr.Client(), svr.URL)
	rsp, err := client.GetBook(
		context.Background(),
		connect.NewRequest(&testv1.GetBookRequest{
			Name: "shelves/top/books/123",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	log.Println(rsp.Msg.Description)
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