// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard_test

import (
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

func ExampleMux_rpcRpc() {
	log := log.New(os.Stdout, "" /* prefix */, 0 /* flags */)

	svc := &libraryRPC{} // implements RPC testv1connect.LibraryServiceHandler

	httpMux := http.NewServeMux()
	httpMux.Handle(testv1connect.NewLibraryServiceHandler(svc))

	// Create a Mux and register the service as a gRPC service.
	mux := &vanguard.Mux{}
	mux.RegisterServiceByName(
		httpMux, testv1connect.LibraryServiceName,
		vanguard.WithProtocols(vanguard.ProtocolGRPC),
		vanguard.WithNoCompression(),
	)

	// Create a grpc-web client and call the service.
	client := testv1connect.NewLibraryServiceClient(
		newExampleClient(mux.AsHandler()), "",
		connect.WithGRPCWeb(),
		connect.WithAcceptCompression(vanguard.CompressionGzip, nil, nil),
	)

	// Call the service.
	rsp, err := client.GetBook(
		context.Background(),
		connect.NewRequest(&testv1.GetBookRequest{
			Name: "shelves/top/books/123",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	log.Println(rsp.Msg.Title)
	// Output: Do Androids Dream of Electric Sheep?
}

func ExampleMux_restRpc() {
	log := log.New(os.Stdout, "" /* prefix */, 0 /* flags */)
	svc := &libraryRPC{} // implements RPC testv1connect.LibraryServiceHandler

	httpMux := http.NewServeMux()
	httpMux.Handle(testv1connect.NewLibraryServiceHandler(svc))

	// Create a Mux and register the service as a gRPC service.
	mux := &vanguard.Mux{}
	mux.RegisterServiceByName(
		httpMux, testv1connect.LibraryServiceName,
		vanguard.WithProtocols(vanguard.ProtocolGRPC),
		vanguard.WithNoCompression(),
	)

	// Build a REST request.
	req, _ := http.NewRequest(http.MethodGet, "/v1/shelves/top/books/123", http.NoBody)
	req.Header.Set("Accept-Encoding", "identity")

	client := newExampleClient(mux.AsHandler())
	rsp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer rsp.Body.Close()
	log.Println(rsp.Status)
	log.Println(rsp.Header.Get("Content-Type"))

	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		log.Fatal(err)
	}

	var book testv1.Book
	if err := protojson.Unmarshal(body, &book); err != nil {
		log.Fatal(err)
	}
	log.Println(book.Author)
	// Output: 200 OK
	// application/json
	// Philip K. Dick
}

func ExampleMux_rpcRest() {
	//func TestMux_rpcRest(t *testing.T) {
	log := log.New(os.Stdout, "" /* prefix */, 0 /* flags */)
	svc := &libraryREST{} // implements REST service

	mux := &vanguard.Mux{}
	mux.RegisterServiceByName(
		svc, testv1connect.LibraryServiceName,
		vanguard.WithProtocols(vanguard.ProtocolREST),
		vanguard.WithCodecs(vanguard.CodecJSON),
		vanguard.WithNoCompression(),
	)

	client := testv1connect.NewLibraryServiceClient(
		newExampleClient(mux.AsHandler()), "",
		connect.WithGRPC(),
		//connect.WithAcceptCompression(vanguard.CompressionGzip, nil, nil),
	)
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
	default:
		err = connect.NewError(connect.CodeNotFound, fmt.Errorf("method not found"))
	}
	rsp.Header().Set("Content-Type", "application/json")
	rsp.Header().Set("Content-Encoding", "identity")
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
	rsp.Write(body)
	//fmt.Println("wrote body", string(body))
}

type libraryRPC struct {
	testv1connect.UnimplementedLibraryServiceHandler
}

func (s *libraryRPC) GetBook(ctx context.Context, req *connect.Request[testv1.GetBookRequest]) (*connect.Response[testv1.Book], error) {
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

type exampleClient struct {
	hdlr http.Handler
	rec  *httptest.ResponseRecorder
}

func (c *exampleClient) Do(req *http.Request) (*http.Response, error) {
	c.hdlr.ServeHTTP(c.rec, req)
	return c.rec.Result(), nil
}
func newExampleClient(svr http.Handler) connect.HTTPClient {
	return &exampleClient{
		hdlr: svr,
		rec:  httptest.NewRecorder(),
	}
}
