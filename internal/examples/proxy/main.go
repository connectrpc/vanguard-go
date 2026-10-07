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

// This program puts a REST front end on an RPC backend it has no Go
// types for. It builds one forwarding method per RPC from the service
// descriptor alone, using dynamicpb messages, so the same code proxies
// any service whose schema it can load. Demonstrates combining the
// inbound REST binding with an outbound RPC client.
//
// Both halves run in one process over HTTP/1.1, which the Connect
// protocol needs no special setup for. Talking gRPC to the backend
// instead would require giving the client an h2c transport.
//
// Run: go run ./internal/examples/proxy -p 8102
// Then: curl http://localhost:8102/v1/shelves/tolkien/books/hobbit
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/vanguard"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"connectrpc.com/vanguard/internal/gen/vanguard/test/v1/testv1connect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

const serviceName = "vanguard.test.v1.LibraryService"

func main() {
	flagset := flag.NewFlagSet("proxy", flag.ExitOnError)
	port := flagset.String("p", "8102", "port to serve REST on")
	backendPort := flagset.String("b", "8103", "port the RPC backend listens on")
	if err := flagset.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	// 1. A stand-in RPC backend, speaking Connect over HTTP/1.1. In a
	//    real deployment this is a separate service and the proxy only
	//    needs its URL.
	go serveBackend(*backendPort)

	// 2. Look up the schema. Any protoreflect.ServiceDescriptor works,
	//    so this could equally come from a descriptor set on disk or
	//    from a schema registry rather than the global registry.
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(serviceName)
	if err != nil {
		log.Fatalf("find %s: %v", serviceName, err)
	}
	service, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		log.Fatalf("%s is not a service", serviceName)
	}

	// 3. Register one forwarding method per RPC, then expose them as
	//    REST. The proxy never sees a generated Go type.
	upstream := connect.NewClient(
		connecthttp.NewTransport(http.DefaultClient, "http://localhost:"+*backendPort),
	)
	proxy := connect.NewServer()
	proxy.Register(vanguard.ForwardService(upstream, service)...)

	// REST covers the annotated methods. Mounting connecthttp on the
	// same mux gives Connect/gRPC clients the whole service, including
	// any streaming RPC REST cannot carry.
	mux := http.NewServeMux()
	connecthttp.Mount(mux, proxy)
	if err := vanguard.Mount(mux, proxy); err != nil {
		log.Fatalf("vanguard.Mount: %v", err)
	}

	log.Printf("proxying REST on http://localhost:%s to RPC on :%s\n", *port, *backendPort)
	log.Fatal(http.ListenAndServe(":"+*port, mux))
}

// serveBackend runs the RPC service the proxy forwards to. It is an
// ordinary connect server with no REST binding of its own.
func serveBackend(port string) {
	server := connect.NewServer()
	testv1connect.RegisterLibraryServiceHandler(server, libraryServer{})

	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

type libraryServer struct {
	testv1connect.UnimplementedLibraryServiceHandler
}

func (libraryServer) GetBook(_ context.Context, req *testv1.GetBookRequest) (*testv1.Book, error) {
	return &testv1.Book{
		Name:   req.GetName(),
		Title:  "The Hobbit",
		Author: "J.R.R. Tolkien",
	}, nil
}
