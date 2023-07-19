// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package main

import (
	"net/http"
	"os"
	"time"

	"golang.org/x/exp/slog"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(rsp http.ResponseWriter, req *http.Request) {
		slog.Info("Request",
			slog.String("proto", req.Proto),
			slog.String("method", req.Method),
			slog.String("url", req.URL.String()))
		rsp.Header().Set("Hello-Header", "Hello, World!")
		rsp.Header().Set("Trailer", "Hello-Trailer")
		_, _ = rsp.Write([]byte("Hello, World!"))
		rsp.Header().Set("Hello-Trailer", "Hello, World!")

	})
	svr := &http.Server{
		Addr:           ":8080",
		Handler:        h2c.NewHandler(mux, &http2.Server{}),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	slog.Info("Starting server", slog.String("address", svr.Addr))
	if err := svr.ListenAndServe(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
