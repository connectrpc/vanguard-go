// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"net/http"
)

func NewFilterHandler(config *Config, handler http.Handler) http.Handler {
	mux := newMux(config)

	return http.HandlerFunc(func(rsp http.ResponseWriter, req *http.Request) {
		f := &filterHTTP{
			mux:                mux,
			ResponseWriter:     rsp,
			ResponseController: http.NewResponseController(rsp),
		}

		handler.ServeHTTP(rsp, req)
		_ = f.Flush()
	})
}

type filterHTTP struct {
	*mux
	http.ResponseWriter
	*http.ResponseController
}

func (f *filterHTTP) Unwrap() http.ResponseWriter {
	return f.ResponseWriter
}
