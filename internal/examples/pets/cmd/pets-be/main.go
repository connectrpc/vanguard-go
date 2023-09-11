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
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
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
	mux := &vanguard.Mux{
		Protocols: []vanguard.Protocol{vanguard.ProtocolREST},
		Codecs:    []string{vanguard.CodecJSON},
	}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "https", Host: "petstore.swagger.io", Path: "/v2/"})
	proxy.Transport = internal.TraceTransport(http.DefaultTransport)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = r.URL.Host
	}
	if err := mux.RegisterServiceByName(proxy, petstorev2connect.PetServiceName); err != nil {
		log.Fatal(err)
	}

	serveMux := http.NewServeMux()
	serveMux.Handle("/", internal.TraceHandler(mux.AsHandler()))
	serveMux.Handle(grpcreflect.NewHandlerV1(grpcreflect.NewStaticReflector(petstorev2connect.PetServiceName)))

	listener, err := net.Listen("tcp", "127.0.0.1:30304")
	if err != nil {
		log.Fatal(err)
	}
	svr := &http.Server{
		Addr:              ":http",
		Handler:           h2c.NewHandler(serveMux, &http2.Server{}),
		ReadHeaderTimeout: 15 * time.Second,
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
		if err := svr.Shutdown(ctx); err != nil {
			return errors.New("failed to shutdown gracefully after 5 seconds")
		}
		log.Println("Shutdown complete.")
		return nil
	})
	grp.Go(func() error {
		err := svr.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server failed: %w", err)
		}
		return nil
	})
	if err := grp.Wait(); err != nil {
		log.Fatal(err)
	}
}
