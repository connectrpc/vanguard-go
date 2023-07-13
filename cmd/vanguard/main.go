// Copyright 2023 Buf Technologies, Inc.

package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"time"
)

func main() {
	s := &http.Server{
		Addr: ":8080",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := httputil.DumpRequest(r, true)
			log.Printf("%s", b)
			w.Write([]byte("Hello, World!"))
		}),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	log.Printf("Starting server on %s\n", s.Addr)
	log.Fatal(s.ListenAndServe())
}
