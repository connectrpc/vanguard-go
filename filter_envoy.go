// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

// https://pkg.go.dev/github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api

import (
	"bytes"
	"fmt"

	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

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
	stream chunkstreamer
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

	f.stream.onMsg = func(msg proto.Message) error {
		fmt.Printf("onMsg: %v\n", msg)
		return nil
	}

	lexer := lexer{input: f.path}
	if err := lexPath(&lexer); err != nil {
		return f.encError(err)
	}
	toks := lexer.tokens()
	switch f.srcProtocol {
	case protocolGRPC, protocolGRPCWeb:
		name := toks.String()
		methodDesc, err := state.getMethod(name)
		if err != nil {
			return f.encError(err)
		}
		argsDesc := methodDesc.Input()
		replyDesc := methodDesc.Output()

		f.stream.args = dynamicpb.NewMessage(argsDesc)
		f.stream.reply = dynamicpb.NewMessage(replyDesc)
		f.stream.up = &upstreamGRPC{
			mux:   f.mux,
			isWeb: f.srcProtocol == protocolGRPCWeb,
		}
		f.decode = envelopeChunker{
			buffer:  &bytes.Buffer{},
			onChunk: f.stream.Decode,
		}
	case protocolHTTPRule:
		verb := header.Method()
		method, params, err := state.path.search(toks, verb)
		if err != nil {
			return f.encError(err)
		}
		argsDesc := method.desc.Input()
		replyDesc := method.desc.Output()

		f.stream.args = dynamicpb.NewMessage(argsDesc)
		f.stream.reply = dynamicpb.NewMessage(replyDesc)
		f.stream.up = &upstreamHTTPRule{
			mux:    f.mux,
			method: method,
			params: params,
		}
		f.decode = endStreamChunker{
			buffer:  &bytes.Buffer{},
			onChunk: f.stream.Decode,
		}
	default:
		return f.encError(errUnsupportedProtocol(f.srcProtocol))
	}

	switch f.dstProtocol {
	case protocolGRPC, protocolGRPCWeb:
		f.stream.down = &downstreamGRPC{
			mux:   f.mux,
			isWeb: f.dstProtocol == protocolGRPCWeb,
		}
		f.encode = envelopeChunker{
			buffer:  &bytes.Buffer{},
			onChunk: f.stream.Encode,
		}
	default:
		return f.encError(errUnsupportedProtocol(f.dstProtocol))
	}
	if err := f.stream.up.DecodeHeader(header); err != nil {
		return f.encError(err)
	}
	decodeHeader(f.srcProtocol, f.dstProtocol, header)
	if err := f.stream.down.EncodeHeader(header); err != nil {
		return f.encError(err)
	}
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
	header.Set("Rsp-Header-From-Go", "bar-test")

	if err := f.stream.down.DecodeHeader(header); err != nil {
		return f.encError(err)
	}
	encodeHeader(f.srcProtocol, f.dstProtocol, header)
	if err := f.stream.up.EncodeHeader(header); err != nil {
		return f.encError(err)
	}
	return api.Continue
}

// Callbacks which are called in response path
func (f *filterEnvoy) EncodeData(buffer api.BufferInstance, endStream bool) api.StatusType {
	if err := f.encode.Next(buffer, endStream); err != nil {
		return f.encError(err)
	}
	return api.Continue
}

func (f *filterEnvoy) EncodeTrailers(trailers api.ResponseTrailerMap) api.StatusType {
	encodeTrailer(f.srcProtocol, f.dstProtocol, trailers)
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
