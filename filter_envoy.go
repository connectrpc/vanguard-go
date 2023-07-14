// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

// https://pkg.go.dev/github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api

import (
	"fmt"
	"net/http"

	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api"
)

var UpdateUpstreamBody = "upstream response body updated by vanguard plugin"

type filterEnvoy struct {
	*mux
	//api.PassThroughStreamFilter

	callbacks      api.FilterCallbackHandler
	path           string
	contentType    string
	inputProtocol  protocolType
	outputProtocol protocolType
}

func (f *filterEnvoy) encError(code int, msg string) api.StatusType {
	headers := make(map[string]string)
	f.callbacks.SendLocalReply(code, msg, headers, -1, "test-from-go")
	return api.LocalReply
}

// Callbacks which are called in request path
func (f *filterEnvoy) DecodeHeaders(header api.RequestHeaderMap, endStream bool) api.StatusType {
	state := f.state.Load()
	if state == nil {
		return api.Continue
	}
	fmt.Println("DecodeHeaders")
	fmt.Println("protocol:", header.Protocol())
	fmt.Println("scheme:", header.Scheme())
	fmt.Println("method:", header.Method())
	fmt.Println("host:", header.Host())
	fmt.Println("path:", header.Path())

	f.path, _ = header.Get(":path")
	f.contentType, _ = header.Get("content-type")

	switch {
	case isGRPC(header.Protocol(), f.contentType):
		f.inputProtocol = protocolTypeGRPC
	case isGRPCWeb(f.contentType):
		f.inputProtocol = protocolTypeGRPCWeb
	case isREST(f.contentType):
		f.inputProtocol = protocolTypeREST
	default:
		return f.encError(http.StatusUnsupportedMediaType, "Unsupported Media Type")
	}

	lexer := lexer{input: f.path}
	if err := lexPath(&lexer); err != nil {
		return f.encError(http.StatusInternalServerError, err.Error())
	}
	toks := lexer.tokens()
	switch f.inputProtocol {
	case protocolTypeGRPC, protocolTypeGRPCWeb:
		name := toks.String()
		md, err := state.getMethod(name)
		if err != nil {
			return f.encError(http.StatusInternalServerError, err.Error())
		}
		_ = md
	case protocolTypeREST:
		verb := header.Method()
		method, params, err := state.path.search(toks, verb)
		if err != nil {
			return f.encError(http.StatusInternalServerError, err.Error())
		}
		_ = method
		_ = params
	}

	// TODO: config for output protocol
	f.outputProtocol = f.config.outputProtocol

	return api.Continue
}

//The callbacks can be implemented on demand

func (f *filterEnvoy) DecodeData(buffer api.BufferInstance, endStream bool) api.StatusType {

	return api.Continue
}

func (f *filterEnvoy) DecodeTrailers(trailers api.RequestTrailerMap) api.StatusType {
	return api.Continue
}

func (f *filterEnvoy) EncodeHeaders(header api.ResponseHeaderMap, endStream bool) api.StatusType {
	//if f.path == "/update_upstream_response" {
	//	header.Set("Content-Length", strconv.Itoa(len(UpdateUpstreamBody)))
	//}
	header.Set("Rsp-Header-From-Go", "bar-test")
	return api.Continue
}

// Callbacks which are called in response path
func (f *filterEnvoy) EncodeData(buffer api.BufferInstance, endStream bool) api.StatusType {
	//if f.path == "/update_upstream_response" {
	//	if endStream {
	//		buffer.SetString(UpdateUpstreamBody)
	//	} else {
	//		// TODO implement buffer->Drain, buffer.SetString means buffer->Drain(buffer.Len())
	//		buffer.SetString("")
	//	}
	//}
	return api.Continue
}

func (f *filterEnvoy) EncodeTrailers(trailers api.ResponseTrailerMap) api.StatusType {
	return api.Continue
}

func (f *filterEnvoy) OnDestroy(reason api.DestroyReason) {}
