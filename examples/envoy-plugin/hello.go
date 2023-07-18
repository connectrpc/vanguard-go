// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package main

import (
	"net/http"
	"os"
	"time"

	"golang.org/x/exp/slog"
)

func main() {
	svr := &http.Server{
		Addr: ":8080",
		Handler: http.HandlerFunc(func(rsp http.ResponseWriter, _ *http.Request) {
			_, _ = rsp.Write([]byte("Hello, World!"))
		}),
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
