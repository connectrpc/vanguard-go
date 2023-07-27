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
)

type RegisterOption func() // TODO

func (m *Mux) RegisterHTTPHandler(
	handler http.Handler,
	services []protoreflect.ServiceDescriptor,
	downstreamProtocol protocol,
	opts ...RegisterOption,
) error {

	hd := func(
		rsp http.ResponseWriter,
		req *http.Request,
		upstreamProtocol protocol,
		method *method,
		params params,
	) error {
		filter, err := newFilterHTTP(m, req, rsp, method, params, upstreamProtocol, downstreamProtocol)
		if err != nil {
			return err
		}

		reqHdr := makeRequestHeaderHTTP(req)
		rspHdr := makeResponseHeaderHTTP(rsp)
		if err := filter.decodeHeader(reqHdr); err != nil {
			return err
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

		if err := filter.encodeTrailer(rspHdr); err != nil {
			return err
		}

		if err := filterRsp.TryFlush(true); err != nil {
			return err
		}
		return nil
	}

	// Load the state for writing.
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state.Load().clone()

	for _, sd := range services {
		if err := state.addService(sd, hd); err != nil {
			return err
		}
	}

	m.state.Store(state)
	return nil
}

type filterHTTP struct {
	*Mux

	req *http.Request
	rsp http.ResponseWriter

	convert converter
	decode  io.Reader
	encode  io.Writer
	stream  chunkstreamer
}

func newFilterHTTP(
	mux *Mux, req *http.Request, rsp http.ResponseWriter,
	method *method, params params, upstreamProtocol, downstreamProtocol protocol,
) (*filterHTTP, error) {
	conv, err := mux.convert(upstreamProtocol, downstreamProtocol)
	if err != nil {
		return nil, err
	}

	f := &filterHTTP{
		Mux:     mux,
		req:     req,
		rsp:     rsp,
		convert: conv,
	}

	// Create the stream
	switch upstreamProtocol {
	case protocolGRPC, protocolGRPCWeb:
		panic("TODO")

	case protocolHTTP:
		f.stream.up = &upstreamHTTP{
			Mux:    mux,
			method: method,
			params: params,
		}
		f.stream.desc = method.desc
		f.decode = &endStreamReader{
			reader:      req.Body,
			buffer:      &bytes.Buffer{},
			onChunk:     f.stream.Decode,
			maxRecvSize: mux.config.maxRecvMsgSize,
		}
	default:
		return nil, errUnsupportedProtocol(upstreamProtocol)
	}

	switch downstreamProtocol {
	case protocolGRPC:
		f.stream.down = &downstreamGRPC{
			Mux:    f.Mux,
			method: method,
		}
		f.encode = &envelopeWriter{
			writer:      f.rsp,
			buffer:      &bytes.Buffer{},
			onChunk:     f.stream.Encode,
			maxRecvSize: f.config.maxRecvMsgSize,
		}
	default:
		return nil, errUnsupportedProtocol(downstreamProtocol)
	}
	// check
	if f.stream.up == nil {
		return nil, fmt.Errorf("missing upstream stream")
	}
	if f.stream.down == nil {
		return nil, fmt.Errorf("missing downstream stream")
	}
	if f.stream.desc == nil {
		return nil, fmt.Errorf("missing method descriptor")
	}
	if f.decode == nil {
		return nil, fmt.Errorf("missing decode reader")
	}
	if f.encode == nil {
		return nil, fmt.Errorf("missing encode writer")
	}
	return f, nil
}

func (f *filterHTTP) decodeHeader(hdr requestHeader) error {
	if err := f.stream.up.DecodeHeader(hdr); err != nil {
		return err
	}
	if err := f.convert.DecodeHeader(hdr); err != nil {
		return err
	}
	if err := f.stream.down.EncodeHeader(hdr); err != nil {
		return err
	}
	return nil
}

func (f *filterHTTP) encodeTrailer(hdr responseHeader) error {
	if err := f.stream.down.DecodeTrailer(hdr); err != nil {
		return err
	}
	if err := f.convert.EncodeTrailer(f.rsp, hdr); err != nil {
		return err
	}
	if err := f.stream.up.EncodeTrailer(hdr); err != nil {
		return err
	}
	return nil
}

type filterHTTPResponseWriter struct {
	*filterHTTP

	controller *http.ResponseController
}

func (f *filterHTTPResponseWriter) Header() http.Header {
	return f.rsp.Header()
}
func (f *filterHTTPResponseWriter) Write(data []byte) (int, error) {
	return f.encode.Write(data)
}
func (f *filterHTTPResponseWriter) WriteHeader(statusCode int) {
	hdr := makeResponseHeaderHTTP(f.rsp)
	if err := f.encodeHeader(hdr); err != nil {
		panic(err) // TODO
	}
	if statusCode != 200 {
		// Delay status code until body is written.
		f.rsp.WriteHeader(statusCode)
	}
}
func (f *filterHTTPResponseWriter) encodeHeader(hdr responseHeader) error {
	if err := f.stream.down.DecodeHeader(hdr); err != nil {
		return err
	}
	if err := f.convert.EncodeHeader(f.rsp, hdr); err != nil {
		return err
	}
	if err := f.stream.up.EncodeHeader(hdr); err != nil {
		return err
	}
	return nil
}
func (f *filterHTTPResponseWriter) Unwrap() http.ResponseWriter {
	return f.rsp
}
func (f *filterHTTPResponseWriter) TryFlush(eof bool) error {
	return f.controller.Flush()
}

// Flush is a no-op, buffering is handled by the filter.
func (f *filterHTTPResponseWriter) Flush() {}

type filterHTTPRequestReader struct {
	*filterHTTP

	body io.ReadCloser // reference to the original body
}

func (f *filterHTTPRequestReader) Read(p []byte) (int, error) {
	n, err := f.decode.Read(p)
	return n, err
}
func (f *filterHTTPRequestReader) Close() error {
	return f.body.Close()
}
