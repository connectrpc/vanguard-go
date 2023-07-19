// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"net/http"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func NewFilterHandler(config *Config, handler http.Handler) http.Handler {
	mux := newMux(config)

	return http.HandlerFunc(func(rsp http.ResponseWriter, req *http.Request) {
		filter := &filterHTTP{
			mux:                mux,
			ResponseWriter:     rsp,
			ResponseController: http.NewResponseController(rsp), //nolint:bodyclose
		}

		handler.ServeHTTP(rsp, req)
		_ = filter.Flush()
	})
}

type filterHTTP struct {
	*mux
	http.ResponseWriter
	*http.ResponseController

	handler           http.Handler
	path              string
	contentType       string
	srcProtocol       protocol
	dstProtocol       protocol
	outputContentType string
	methodDesc        protoreflect.MethodDescriptor
}

func (f *filterHTTP) Unwrap() http.ResponseWriter {
	return f.ResponseWriter
}

func (f filterHTTP) ServeHTTP(rsp http.ResponseWriter, req *http.Request) {
	state := f.state.Load()
	if state == nil {
		http.Error(rsp, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	f.path = req.URL.Path
	f.contentType = req.Header.Get("Content-Type")

	f.srcProtocol = classifyProtocol(headerMap(req.Header))
	f.dstProtocol = f.config.outputProtocol

	lexer := lexer{input: req.URL.Path}
	if err := lexPath(&lexer); err != nil {
		f.encError(rsp, err)
		return
	}
	toks := lexer.tokens()
	switch f.srcProtocol {
	case protocolGRPC, protocolGRPCWeb:
		name := toks.String()
		methodDesc, err := state.getMethod(name)
		if err != nil {
			f.encError(rsp, err)
			return
		}
		f.methodDesc = methodDesc
		filter := &filterHTTPGRPC{
			filterHTTP: f,
			isWeb:      f.srcProtocol == protocolGRPCWeb,
		}
		filter.ServeHTTP(rsp, req)
	case protocolHTTPRule:
		verb := req.Method
		method, params, err := state.path.search(toks, verb)
		if err != nil {
			f.encError(rsp, err)
			return
		}
		f.methodDesc = method.desc
		filter := &filterHTTPRule{
			filterHTTP: f,
			method:     method,
			params:     params,
		}
		filter.ServeHTTP(rsp, req)
	default:
		f.encError(rsp, errUnsupportedProtocol(f.srcProtocol))
		return
	}
}

func (f filterHTTP) encError(rsp http.ResponseWriter, err error) {
	serr := asStatusError(err)
	code := serr.Code()
	msg := serr.Error()
	// TODO: encode error on f.protocolType
	http.Error(rsp, msg, code)
}

type filterHTTPRule struct {
	filterHTTP

	method *method
	params params
}

func (f *filterHTTPRule) ServeHTTP(rsp http.ResponseWriter, req *http.Request) {
	// encode header
	// encode body

	// decode header
	// decode body
	// decode trailers
}

type filterHTTPGRPC struct {
	filterHTTP

	isWeb bool
}

func (f *filterHTTPGRPC) ServeHTTP(rsp http.ResponseWriter, req *http.Request) {
	// encode header
	// encode body

	// decode header
	// decode body
	// decode trailers

