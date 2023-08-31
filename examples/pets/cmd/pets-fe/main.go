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

	"github.com/bufbuild/vanguard-go"
	"github.com/bufbuild/vanguard-go/examples/pets/internal"
	"github.com/bufbuild/vanguard-go/examples/pets/internal/gen/io/swagger/petstore/v2/petstorev2connect"
	"golang.org/x/sync/errgroup"
)

func main() {
	muxes := []*vanguard.Mux{
		{
			Protocols: []vanguard.Protocol{vanguard.ProtocolConnect},
		},
		{
			Protocols: []vanguard.Protocol{vanguard.ProtocolGRPC},
		},
		{
			Protocols: []vanguard.Protocol{vanguard.ProtocolGRPCWeb},
		},
	}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:30304"})
	listeners := make([]net.Listener, len(muxes))
	svrs := make([]*http.Server, len(muxes))
	for i, mux := range muxes {
		err := mux.RegisterServiceByName(proxy, petstorev2connect.PetServiceName)
		if err != nil {
			log.Fatal(err)
		}
		port := 30301 + i
		listeners[i], err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			log.Fatal(err)
		}
		svrs[i] = &http.Server{Handler: internal.TraceHandler(mux.AsHandler())}
	}

	signals := make(chan os.Signal)
	go func() {
		<-signals
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		grp, ctx := errgroup.WithContext(ctx)
		for i := range svrs {
			svr := svrs[i]
			grp.Go(func() error {
				return svr.Shutdown(ctx)
			})
		}
		if err := grp.Wait(); err != nil {
			log.Fatal("Failed to shutdown gracefully after 5 seconds.")
		}
	}()
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	grp, _ := errgroup.WithContext(context.Background())
	for i := range svrs {
		listener := listeners[i]
		svr := svrs[i]
		grp.Go(func() error {
			return svr.Serve(listener)
		})
	}
	err := grp.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Server failed: %v", err)
	}
}
