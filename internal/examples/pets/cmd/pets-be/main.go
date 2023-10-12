// Copyright 2023 Buf Technologies, Inc.
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
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"connectrpc.com/grpcreflect"
	"connectrpc.com/vanguard"
	"connectrpc.com/vanguard/internal/examples/pets/internal"
	"connectrpc.com/vanguard/internal/examples/pets/internal/gen/io/swagger/petstore/v2/petstorev2connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	// Create a reverse proxy, to forward requests to https://petstore.swagger.io/v2/.
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "https", Host: "petstore.swagger.io", Path: "/v2/"})
	// Note: we will trace proxied requests to stdout, so that you can see the details
	// of the transformation with actual requests by running this program and sending
	// RPC requests to it.
	proxy.Transport = internal.TraceTransport(http.DefaultTransport)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = r.URL.Host
	}

	// Wrap the proxy handler with Vanguard, so it can accept Connect, gRPC, or gRPC-Web
	// and transform the requests to REST+JSON.
	handler, err := vanguard.NewTranscoder(
		vanguard.WithService(petstorev2connect.PetServiceName, proxy),
		vanguard.WithDefaultServiceOptions(
			vanguard.WithTargetProtocols(vanguard.ProtocolREST),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	serveMux := http.NewServeMux()
	// Similar to above, we trace incoming requests to stdout. That way you can see the
	// original RPC request and the proxied REST request to see Vanguard in action.
	serveMux.Handle("/", internal.TraceHandler(handler))
	// We add gRPC reflection support so that you can use tools like `buf curl` or `grpcurl`
	// with this server.
	serveMux.Handle(grpcreflect.NewHandlerV1(grpcreflect.NewStaticReflector(petstorev2connect.PetServiceName)))

	listener, err := net.Listen("tcp", "127.0.0.1:30304")
	if err != nil {
		log.Fatal(err)
	}
	svr := &http.Server{
		Addr: ":http",
		// We use h2c to support HTTP/2 without TLS (and thus support the gRPC protocol).
		Handler:           h2c.NewHandler(serveMux, &http2.Server{}),
		ReadHeaderTimeout: 15 * time.Second,
	}
	err = svr.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
