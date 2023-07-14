// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"net/http"
	"net/http/httptest"
	"testing"

	library "github.com/bufbuild/vanguard/internal/gen/library/v1"
	"github.com/bufbuild/vanguard/internal/gen/library/v1/libraryv1connect"
	"github.com/bufbuild/vanguard/internal/testlibrary"
)

func TestFilter(t *testing.T) {
	t.Parallel()

	// TODO: implement
	service := &testlibrary.Service{
		map[string]*library.Book{
			"books/book1": &library.Book{Name: "books/book1"},
		},
	}
	mux := http.NewServeMux()
	mux.Handle(libraryv1connect.NewLibraryServiceHandler(service))
	mux.Handle("/", service)

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.Start()
	t.Cleanup(server.Close)

	tests := []struct {
		name string
	}{{}}
	for _, testcase := range tests {
		t.Run(testcase.name, func(t *testing.T) {
			t.Parallel()

			// TODOO
		})
	}
}
