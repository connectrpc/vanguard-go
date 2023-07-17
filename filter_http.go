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
	*http.ResponseController

	path              string
	contentType       string
	encInput          streamType
	inputProtocol     protocol
	outputProtocol    protocol
	outputContentType string
	methodDesc        protoreflect.MethodDescriptor
}

func (f filterHTTP) ServeHTTP(rsp http.ResponseWriter, req *http.Request) {
	state := f.state.Load()
	if state == nil {
		http.Error(rsp, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	f.path = req.URL.Path
	f.contentType = req.Header.Get("Content-Type")

	f.inputProtocol = classifyProtocol(headerMap(req.Header))
	f.outputProtocol = f.config.outputProtocol

	lexer := lexer{input: req.URL.Path}
	if err := lexPath(&lexer); err != nil {
		f.encError(rsp, err)
		return
	}
	toks := lexer.tokens()
	switch f.inputProtocol {
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
			isWeb:      f.inputProtocol == protocolGRPCWeb,
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
		f.encError(rsp, errUnsupportedProtocol(f.inputProtocol))
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
}
