// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"net/http"

	"google.golang.org/protobuf/reflect/protoreflect"
)

type filterHTTP struct {
	*mux

	path              string
	contentType       string
	inputProtocol     protocolType
	outputContentType string
	ouputProtocol     protocolType

	method *method
	params params
}

func (f *filterHTTP) ServeHTTP(rsp http.ResponseWriter, req *http.Request) {
	state := f.state.Load()
	if state == nil {
		http.Error(rsp, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	f.path = req.URL.Path
	f.contentType = req.Header.Get("Content-Type")

	switch {
	case isGRPC(req.Proto, f.contentType):
		f.inputProtocol = protocolTypeGRPC
	case isGRPCWeb(f.contentType):
		f.inputProtocol = protocolTypeGRPCWeb
	case isREST(f.contentType):
		f.inputProtocol = protocolTypeREST
	default:
		http.Error(rsp, "Unsupported Media Type", http.StatusUnsupportedMediaType)
		return // TODO
	}

	lexer := lexer{input: req.URL.Path}
	if err := lexPath(&lexer); err != nil {
		f.encError(rsp, err)
		return
	}
	toks := lexer.tokens()
	switch f.inputProtocol {
	case protocolTypeGRPC, protocolTypeGRPCWeb:
		name := toks.String()
		md, err := state.getMethod(name)
		if err != nil {
			f.encError(rsp, err)
			return
		}
		f.method = method
		f.params = nil // no URL params for gRPC
	case protocolTypeREST:
		verb := req.Method
		method, params, err := state.path.search(toks, verb)
		if err != nil {
			f.encError(rsp, err)
			return
		}
		f.method = method
		f.params = params
	}

	// TODO: config for output protocol
	f.ouputProtocol = f.config.outputProtocol
}

func (f *filterHTTP) encError(rsp http.ResponseWriter, err error) {
	// TODO: encode error on f.protocolType
	http.Error(rsp, err.Error(), http.StatusInternalServerError)
}

type filterHTTPRule struct {
	*filterHTTP

	method *method
	params params
}

func (f *filterHTTPRule) ServeHTTP(rsp http.ResponseWriter, req *http.Request) {
}

type filterHTTPGRPC struct {
	*filterHTTP

	desc protoreflect.MethodDescriptor
}

func (f *filterHTTPGRPC) ServeHTTP(rsp http.ResponseWriter, req *http.Request) {
}
