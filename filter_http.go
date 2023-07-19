// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func (m *Mux) WrapHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(rsp http.ResponseWriter, req *http.Request) {
		state := m.state.Load()
		if state == nil {
			http.Error(rsp, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		filter := &filterHTTP{
			Mux: m,
			req: req,
			rsp: rsp,
		}
		if err := filter.decodeHeader(headerMap(req.Header)); err != nil {
			filter.encodeError(err)
			return
		}

		filterRsp := &filterHTTPResponseWriter{
			filterHTTP: filter,
			controller: http.NewResponseController(rsp), //nolint:bodyclose
		}
		req.Body = &filterHTTPRequestReader{
			filterHTTP: filter,
			body:       req.Body,
		}

		handler.ServeHTTP(filterRsp, req)

		if err := filter.encodeTrailer(headerMap(rsp.Header())); err != nil {
			filter.encodeError(err)
			return
		}

		if err := filterRsp.TryFlush(true); err != nil {
			filter.encodeError(err)
			return
		}

	})
}

type filterHTTP struct {
	*Mux

	req *http.Request
	rsp http.ResponseWriter

	path              string
	contentType       string
	srcProtocol       protocol
	dstProtocol       protocol
	outputContentType string
	methodDesc        protoreflect.MethodDescriptor

	decode io.Reader
	encode io.Writer
	stream chunkstreamer
}

func (f *filterHTTP) decodeHeader(header header) error {
	state := f.state.Load()
	if state == nil {
		return fmt.Errorf("missing state")
	}

	f.path = f.req.URL.Path
	f.contentType, _ = header.Get("Content-Type")

	f.srcProtocol = classifyProtocol(headerMap(f.req.Header))
	f.dstProtocol = f.config.outputProtocol

	lexer := lexer{input: f.path}
	if err := lexPath(&lexer); err != nil {
		return err
	}
	toks := lexer.tokens()
	switch f.srcProtocol {
	case protocolGRPC, protocolGRPCWeb:
		name := toks.String()
		methodDesc, err := state.getMethod(name)
		if err != nil {
			return err
		}
		argsDesc := methodDesc.Input()
		replyDesc := methodDesc.Output()

		f.stream.args = dynamicpb.NewMessage(argsDesc)
		f.stream.reply = dynamicpb.NewMessage(replyDesc)
		f.methodDesc = methodDesc
		panic("TODO")

	case protocolHTTPRule:
		verb := f.req.Method
		method, params, err := state.path.search(toks, verb)
		if err != nil {
			return err
		}
		argsDesc := method.desc.Input()
		replyDesc := method.desc.Output()

		f.stream.args = dynamicpb.NewMessage(argsDesc)
		f.stream.reply = dynamicpb.NewMessage(replyDesc)
		f.stream.up = &upstreamHTTPRule{
			Mux:    f.Mux,
			method: method,
			params: params,
		}
		f.decode = &endStreamReader{
			reader:      f.req.Body,
			buffer:      &bytes.Buffer{},
			onChunk:     f.stream.Decode,
			maxRecvSize: f.config.maxRecvMsgSize,
		}
		f.methodDesc = method.desc
	default:
		return errUnsupportedProtocol(f.srcProtocol)
	}

	switch f.dstProtocol {
	case protocolGRPC:
		// TODO: move to DecodeHeader...
		f.req.ProtoMajor = 2
		f.req.ProtoMinor = 0
		f.req.Method = http.MethodPost
		serviceDesc := f.methodDesc.Parent()
		name := "/" + string(serviceDesc.FullName()) +
			"/" + string(f.methodDesc.Name())
		f.req.URL.Path = name
		f.stream.down = &downstreamGRPC{
			Mux: f.Mux,
		}
		f.encode = &envelopeWriter{
			writer:      f.rsp,
			buffer:      &bytes.Buffer{},
			onChunk:     f.stream.Encode,
			maxRecvSize: f.config.maxRecvMsgSize,
		}
	default:
		return errUnsupportedProtocol(f.srcProtocol)
	}
	if err := f.stream.up.DecodeHeader(header); err != nil {
		return err
	}
	decodeHeader(f.srcProtocol, f.dstProtocol, header)
	if err := f.stream.down.EncodeHeader(header); err != nil {
		return err
	}
	return nil
}

func (f *filterHTTP) encodeTrailer(trailers header) error {
	if err := f.stream.down.DecodeTrailer(trailers); err != nil {
		return err
	}
	encodeTrailer(f.srcProtocol, f.dstProtocol, trailers)
	if err := f.stream.up.EncodeTrailer(trailers); err != nil {
		return err
	}
	return nil
}

func (f *filterHTTP) encodeError(err error) {
	serr := asStatusError(err)
	code := serr.Code()
	msg := serr.Error()
	// TODO: encode error on f.protocolType
	http.Error(f.rsp, msg, code)
}

type filterHTTPResponseWriter struct {
	*filterHTTP

	controller *http.ResponseController
	header     http.Header
	buffer     *bytes.Buffer
}

func (f *filterHTTPResponseWriter) Header() http.Header {
	return f.rsp.Header()
}
func (f *filterHTTPResponseWriter) Write(data []byte) (int, error) {
	return f.encode.Write(data)
	//n, err := f.buffer.Write(data)
	//if err != nil {
	//	return n, err
	//}
	//if err := f.encode.Next(f.buffer, false); err != nil {
	//	return 0, err
	//}
	//if f.buffer.Len() > 0 {
	//	if _, err := f.buffer.WriteTo(f.rsp); err != nil {
	//		return n, err
	//	}
	//}
	//return n, nil
}
func (f *filterHTTPResponseWriter) WriteHeader(statusCode int) {
	header := headerMap(f.rsp.Header())
	for k, v := range f.header {
		header[k] = v
	}
	f.rsp.WriteHeader(statusCode)
	if err := f.encodeHeader(header); err != nil {
		f.encodeError(err)
		return
	}
}
func (f *filterHTTPResponseWriter) encodeHeader(header header) error {
	if err := f.stream.down.DecodeHeader(header); err != nil {
		return err
	}
	encodeHeader(f.srcProtocol, f.dstProtocol, header)
	if err := f.stream.up.EncodeHeader(header); err != nil {
		return err
	}
	return nil
}
func (f *filterHTTPResponseWriter) Unwrap() http.ResponseWriter {
	return f.rsp
}
func (f *filterHTTPResponseWriter) TryFlush(eof bool) error {
	//if err := f.encode.Next(f.buffer, eof); err != nil {
	//	return err
	//}
	//if _, err := f.buffer.WriteTo(f.rsp); err != nil {
	//	return nil
	//}
	return f.controller.Flush()
}
func (f *filterHTTPResponseWriter) Flush() {} // nop

type filterHTTPRequestReader struct {
	*filterHTTP

	buffer *bytes.Buffer
	body   io.ReadCloser
}

func (f *filterHTTPRequestReader) Read(p []byte) (int, error) {
	n, err := f.decode.Read(p)
	return n, err
	//if f.buffer.Len() == 0 {
	//	f.buffer.Reset() // reclaim

	//	n, err := read(f.buffer, f.body)
	//	if err != nil {
	//		return n, err
	//	}
	//	if err := f.decode.Next(f.buffer, false); err != nil {
	//		return 0, err
	//	}
	//}
	//return f.buffer.Read(p)
}
func (f *filterHTTPRequestReader) Close() error {
	//if err := f.decode.Next(f.buffer, true); err != nil {
	//	return err
	//}
	return f.body.Close()
}
