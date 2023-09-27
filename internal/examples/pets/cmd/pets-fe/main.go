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
	"os"
	"os/signal"
	"strings"
	"syscall"
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
	muxes := []*vanguard.Mux{
		{
			Protocols: []vanguard.Protocol{vanguard.ProtocolConnect},
		},
		{
			Protocols: []vanguard.Protocol{vanguard.ProtocolGRPC},
			Codecs:    []string{vanguard.CodecProto},
		},
		{
			Protocols: []vanguard.Protocol{vanguard.ProtocolGRPCWeb},
			Codecs:    []string{vanguard.CodecProto},
		},
	}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:30304"})
	proxy.Transport = h2cIfGRPCTransport{h2c: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
	listeners := make([]net.Listener, len(muxes))
	svrs := make([]*http.Server, len(muxes))
	for i, mux := range muxes {
		proxy := proxy
		if i == 1 {
			// HACK: for the gRPC one, make sure the proxy uses h2c to talk to gRPC server.
			clone := *proxy
			proxy.Transport = &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			}
			proxy = &clone
		}
		err := mux.RegisterServiceByName(proxy, petstorev2connect.PetServiceName)
		if err != nil {
			log.Fatal(err)
		}
		port := 30301 + i
		listeners[i], err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			log.Fatal(err)
		}
		serveMux := http.NewServeMux()
		serveMux.Handle("/", internal.TraceHandler(mux))
		serveMux.Handle(grpcreflect.NewHandlerV1(grpcreflect.NewStaticReflector(petstorev2connect.PetServiceName)))
		svrs[i] = &http.Server{
			Addr:              ":http",
			Handler:           h2c.NewHandler(serveMux, &http2.Server{}),
			ReadHeaderTimeout: 15 * time.Second,
		}
	}

	grp, ctx := errgroup.WithContext(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	grp.Go(func() error {
		select {
		case <-ctx.Done():
			return nil
		case <-signals:
		}

		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownGroup, ctx := errgroup.WithContext(ctx)
		for i := range svrs {
			svr := svrs[i]
			shutdownGroup.Go(func() error {
				if err := svr.Shutdown(ctx); err != nil {
					return errors.New("failed to shutdown gracefully after 5 seconds")
				}
				return nil
			})
		}
		err := shutdownGroup.Wait()
		if err == nil {
			log.Println("Shutdown complete.")
		}
		return err
	})

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
