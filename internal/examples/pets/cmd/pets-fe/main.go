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
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/grpcreflect"
	"connectrpc.com/vanguard"
	"connectrpc.com/vanguard/internal/examples/pets/internal"
	"connectrpc.com/vanguard/internal/examples/pets/internal/gen/io/swagger/petstore/v2/petstorev2connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"
)

func main() {
	// We use three separate handlers to serve with three different ports.
	// Each is configured to transform requests to a different protocol.
	serverOptions := map[int][]vanguard.ServiceOption{
		// All requests to port 30301 will get translated to the Connect protocol
		// and then sent to the pets-be server.
		30301: {
			vanguard.WithTargetProtocols(vanguard.ProtocolConnect),
		},
		// All requests to port 30302 will get translated to the gRPC protocol
		// and JSON data will get transcoded to the Protobuf binary format.
		30302: {
			vanguard.WithTargetProtocols(vanguard.ProtocolGRPC),
			vanguard.WithTargetCodecs(vanguard.CodecProto),
		},
		// And requests to port 30303 will get translated to gRPC-Web, Protobuf binary.
		30303: {
			vanguard.WithTargetProtocols(vanguard.ProtocolGRPCWeb),
			vanguard.WithTargetCodecs(vanguard.CodecProto),
		},
	}

	// This server proxies requests to the backend (after translating to a particular RPC protocol).
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:30304"})
	// The gRPC protocol *requires* HTTP/2 and can't work with HTTP 1.1.
	// So we make the proxy smart enough to always use H2C (to use HTTP/2
	// without TLS) when the protocol is gRPC.
	proxy.Transport = h2cIfGRPCTransport{h2c: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}

	listeners := make([]net.Listener, 0, len(serverOptions))
	svrs := make([]*http.Server, 0, len(serverOptions))
	for serverPort, opts := range serverOptions {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", serverPort))
		if err != nil {
			log.Fatal(err)
		}

		// Wrap the proxy handler with Vanguard, so it can accept any kind of request and then
		// transform it to a particular protocol (based on the port used to send the request)
		// before sending to the pets-be server.
		handler, err := vanguard.NewTranscoder(
			vanguard.WithService(petstorev2connect.PetServiceName, proxy, opts...),
		)
		if err != nil {
			log.Fatal(err)
		}

		serveMux := http.NewServeMux()
		// Note: the handler will trace incoming requests to stdout. This provides insight
		// into the protocol transformation, when compared to the corresponding requests
		// in the output of the pets-be backend server.
		serveMux.Handle("/", internal.TraceHandler(handler))
		serveMux.Handle(grpcreflect.NewHandlerV1(grpcreflect.NewStaticReflector(petstorev2connect.PetServiceName)))

		listeners = append(listeners, listener)
		svrs = append(svrs, &http.Server{
			Addr:              ":http",
			Handler:           h2c.NewHandler(serveMux, &http2.Server{}),
			ReadHeaderTimeout: 15 * time.Second,
		})
	}

	// Start the HTTP servers for all three ports.
	grp, _ := errgroup.WithContext(context.Background())
	for i := range svrs {
		listener := listeners[i]
		svr := svrs[i]
		grp.Go(func() error {
			err := svr.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("server failed: %w", err)
			}
			return nil
		})
	}
	if err := grp.Wait(); err != nil {
		log.Fatal(err)
	}
}

// h2cIfGRPCTransport is a round tripper that will use its configured
// h2c transport for requests in the gRPC protocol. It will use
// http.DefaultTransport for other requests.
type h2cIfGRPCTransport struct {
	h2c http.RoundTripper
}

func (h h2cIfGRPCTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Header.Get("Content-Type") == "application/grpc" ||
		strings.HasPrefix(request.Header.Get("Content-Type"), "application/grpc+") {
		// For gRPC, we must use HTTP/2.
		return h.h2c.RoundTrip(request)
	}
	// Otherwise, we can use HTTP 1.1.
	return http.DefaultTransport.RoundTrip(request)
}
