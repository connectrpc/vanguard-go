package main

import (
	"context"
	"errors"
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
	"github.com/bufbuild/vanguard-go"
	"github.com/bufbuild/vanguard-go/examples/pets/internal"
	"github.com/bufbuild/vanguard-go/examples/pets/internal/gen/io/swagger/petstore/v2/petstorev2connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
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
		Addr:    ":http",
		Handler: h2c.NewHandler(serveMux, &http2.Server{}),
	}

	signals := make(chan os.Signal)
	go func() {
		<-signals
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := svr.Shutdown(ctx); err != nil {
			log.Fatal("Failed to shutdown gracefully after 5 seconds.")
		}
	}()
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	err = svr.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Server failed: %v", err)
	}
}
