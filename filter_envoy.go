// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

// https://pkg.go.dev/github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api

import (
	"bytes"
	"fmt"

	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api"
)

var UpdateUpstreamBody = "upstream response body updated by vanguard plugin"

type filterEnvoy struct {
	*mux
	//api.PassThroughStreamFilter

	callbacks   api.FilterCallbackHandler
	path        string
	contentType string
	srcProtocol protocol
	dstProtocol protocol

	decode chunker
	encode chunker
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

	f.srcProtocol = classifyProtocol(header)
	f.dstProtocol = f.config.outputProtocol

	lexer := lexer{input: f.path}
	if err := lexPath(&lexer); err != nil {
		return f.encError(err)
	}
	toks := lexer.tokens()
	switch f.srcProtocol {
	case protocolGRPC, protocolGRPCWeb:
		name := toks.String()
		md, err := state.getMethod(name)
		if err != nil {
			return f.encError(err)
		}
		_ = md
		f.decode = &envelopeChunker{
			buffer: &bytes.Buffer{},
			onChunk: func(chunk []byte) ([]byte, error) {
				// ...

				return chunk, nil
			},
		}
	case protocolHTTPRule:
		verb := header.Method()
		method, params, err := state.path.search(toks, verb)
		if err != nil {
			return f.encError(err)
		}
		_ = method
		_ = params
	default:
		return f.encError(errUnsupportedProtocol(f.srcProtocol))
	}

	switch f.dstProtocol {
	case protocolGRPC, protocolGRPCWeb:
		f.encode = &envelopeChunker{
			buffer: &bytes.Buffer{},
			onChunk: func(chunk []byte) ([]byte, error) {
				// ...

				return chunk, nil
			},
		}
	default:
		return f.encError(errUnsupportedProtocol(f.dstProtocol))
	}

	decodeHeader(f.srcProtocol, f.dstProtocol, header)
	return api.Continue
}

//The callbacks can be implemented on demand

func (f *filterEnvoy) DecodeData(buffer api.BufferInstance, endStream bool) api.StatusType {
	if err := f.decode.Next(buffer, endStream); err != nil {
		return f.encError(err)
	}
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
	if err := f.encode.Next(buffer, endStream); err != nil {
		return f.encError(err)
	}
	return api.Continue
}

func (f *filterEnvoy) EncodeTrailers(trailers api.ResponseTrailerMap) api.StatusType {
	// TODO
	return api.Continue
}

func (f *filterEnvoy) OnDestroy(reason api.DestroyReason) {}

func (f *filterEnvoy) encError(err error) api.StatusType {
	serr := asStatusError(err)
	code := serr.Code()
	msg := serr.Error()

	// TODO: per protocol error handling
	headers := make(map[string]string)
	f.callbacks.SendLocalReply(code, msg, headers, -1, "test-from-go")
	return api.LocalReply
}
