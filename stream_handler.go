// Copyright 2023-2026 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vanguard

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
)

// serverStream implements [connect.ServerStream] for an inbound REST
// request. The request body is one message, so Receive succeeds once
// then returns io.EOF. Unary methods Send once. Server-streaming methods
// append each google.api.HttpBody chunk to the response.
//
// Terminal-error encoding is owned by the dispatcher, not the stream.
// When [connect.Server.Call] returns an error, restHandler.ServeHTTP
// inspects committed() and either writes a google.rpc.Status body
// (uncommitted) or drops the error (the status line is already on the
// wire).
type serverStream struct {
	method  *method
	target  *routeTarget
	vars    []routeTargetVarMatch
	options *options
	codec   RESTCodec
	info    *connect.CallInfo

	request  *http.Request
	response http.ResponseWriter

	requestCompressor  connect.Compressor // nil for identity
	responseCompressor connect.Compressor // nil for identity
	compressWriter     io.WriteCloser     // set when the response is compressed

	body io.Reader // opened on the first Receive
	done bool      // receive side exhausted

	sent       bool
	headersSet bool
}

func newServerStream(
	method *method,
	target *routeTarget,
	vars []routeTargetVarMatch,
	opts *options,
	codec RESTCodec,
	info *connect.CallInfo,
	responseWriter http.ResponseWriter,
	request *http.Request,
) *serverStream {
	return &serverStream{
		method:   method,
		target:   target,
		vars:     vars,
		options:  opts,
		codec:    codec,
		info:     info,
		request:  request,
		response: responseWriter,
	}
}

// committed reports whether the response status line has been written.
// The dispatcher uses this to decide whether a handler-returned error
// can be encoded on the wire.
func (s *serverStream) committed() bool { return s.headersSet }

// Receive decodes the request into msg: the body via the codec, then path
// variables and query parameters on top. A client stream of
// google.api.HttpBody is delivered one chunk per message, with the URL
// fields on the first.
func (s *serverStream) Receive(msg any) error {
	if s.done {
		return io.EOF
	}
	pmsg, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("vanguard: Receive expects proto.Message, got %T", msg)
	}
	first := s.body == nil
	if first {
		if err := s.openBody(); err != nil {
			return err
		}
	}
	fields := s.target.requestBodyFields
	contentType := s.request.Header.Get("Content-Type")
	if s.method.spec.StreamType&connect.StreamTypeClient == 0 {
		s.done = true
		if fields != nil {
			if err := decodeBody(s.request.Context(), s.body, contentType, fields, s.codec, pmsg); err != nil {
				return s.decodeError(err)
			}
		}
		return s.decodeURL(pmsg)
	}
	data, err := readChunk(s.body)
	if errors.Is(err, io.EOF) {
		s.done = true
		if !first {
			return io.EOF
		}
		// An empty upload is still one message, carrying the URL fields.
	} else if err != nil {
		return s.decodeError(err)
	}
	if err := setBodyHTTPBody(fields, pmsg, contentType, data); err != nil {
		return s.decodeError(err)
	}
	if !first {
		return nil
	}
	return s.decodeURL(pmsg)
}

// openBody asserts a rule without a body mapping got none, and otherwise
// prepares the request body: decompressed, then capped at maxReadBytes.
func (s *serverStream) openBody() error {
	if s.target.requestBodyFields == nil {
		if hasBody(s.request) {
			return connect.Errorf(connect.CodeInvalidArgument, "request should have no body")
		}
		s.body = http.NoBody
		return nil
	}
	body := s.request.Body
	if s.requestCompressor != nil {
		decompressed, err := decompressBody(s.requestCompressor, body)
		if err != nil {
			return connect.Errorf(connect.CodeInvalidArgument, "decompress request: %s", err).WithCause(err)
		}
		body = decompressed
	}
	if limit := s.options.maxReadBytes; limit > 0 {
		body = http.MaxBytesReader(nil, body, limit)
	}
	s.body = body
	return nil
}

// hasBody reports whether request carries body bytes. An unknown length
// (chunked, or HTTP/2 without END_STREAM) costs a one-byte read.
func hasBody(request *http.Request) bool {
	if request.ContentLength >= 0 {
		return request.ContentLength > 0
	}
	var probe [1]byte
	n, _ := io.ReadFull(request.Body, probe[:])
	return n > 0
}

func (s *serverStream) decodeURL(msg proto.Message) error {
	if err := decodeRequestURL(s.request, s.vars, s.options, msg); err != nil {
		return s.decodeError(err)
	}
	return nil
}

func (s *serverStream) decodeError(err error) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return connect.Errorf(connect.CodeResourceExhausted, "request body exceeds %d bytes", s.options.maxReadBytes)
	}
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return connectErr // e.g. an invalid path or query parameter
	}
	return wrapError(connect.CodeInvalidArgument, "decode request", err)
}

// SendHeaders flushes the response headers and commits the status line.
func (s *serverStream) SendHeaders() error {
	return s.flushHeaders(s.responseCompressor != nil)
}

func (s *serverStream) Send(msg any) error {
	streaming := s.method.spec.StreamType&connect.StreamTypeServer != 0
	if s.sent && !streaming {
		return errors.New("vanguard: Send called more than once on REST unary stream")
	}
	s.sent = true
	pmsg, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("vanguard: Send expects proto.Message, got %T", msg)
	}
	fields := s.target.responseBodyFields
	if !s.headersSet {
		contentType := bodyContentType(fields, pmsg, s.codec)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		s.response.Header().Set("Content-Type", contentType)
	}
	if err := s.flushHeaders(s.responseCompressor != nil); err != nil {
		return err
	}
	writer := io.Writer(s.response)
	if s.compressWriter != nil {
		writer = s.compressWriter
	}
	if err := encodeBody(s.request.Context(), writer, fields, pmsg, s.codec); err != nil {
		return connect.Errorf(connect.CodeInternal, "encode response: %s", err).WithCause(err)
	}
	if !streaming {
		return nil
	}
	if flusher, ok := s.compressWriter.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return connect.Errorf(connect.CodeInternal, "flush compressor: %s", err).WithCause(err)
		}
	}
	if err := http.NewResponseController(s.response).Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// flushHeaders copies handler-set response metadata onto the
// http.Header and commits the status line. After WriteHeader,
// response.Header() mutations are no-ops.
func (s *serverStream) flushHeaders(compress bool) error {
	if s.headersSet {
		return nil
	}
	setMetadataHeaders(s.response.Header(), s.info.ResponseHeader())
	if s.response.Header().Get("Content-Type") == "" {
		// Headers committed before the first Send. An HttpBody's type
		// comes from the message, so it falls back like Send's default.
		contentType := "application/" + s.codec.Name()
		if isHTTPBodyResponse(s.target, s.method.descriptor.Output()) {
			contentType = "application/octet-stream"
		}
		s.response.Header().Set("Content-Type", contentType)
	}
	if compress {
		writer, err := s.responseCompressor.Compress(s.response)
		if err != nil {
			return connect.Errorf(connect.CodeInternal, "get compressor: %s", err).WithCause(err)
		}
		s.compressWriter = writer
		s.response.Header().Set("Content-Encoding", s.responseCompressor.Name())
	}
	s.response.WriteHeader(http.StatusOK)
	s.headersSet = true
	return nil
}

// close commits the headers of an empty response and finishes a compressed body.

// protocolHeaders are owned by the wire protocol, never by RPC metadata. It
// mirrors connecthttp so metadata crosses either transport the same way.
//
//nolint:gochecknoglobals
var protocolHeaders = map[string]struct{}{
	// HTTP headers.
	"Content-Type":     {},
	"Content-Length":   {},
	"Content-Encoding": {},
	"Trailer":          {},
	"Date":             {},
	// Connect headers.
	"Accept-Encoding":          {},
	"Connect-Content-Encoding": {},
	"Connect-Accept-Encoding":  {},
	"Connect-Timeout-Ms":       {},
	"Connect-Protocol-Version": {},
	// gRPC headers.
	"Grpc-Encoding":           {},
	"Grpc-Accept-Encoding":    {},
	"Grpc-Timeout":            {},
	"Grpc-Status":             {},
	"Grpc-Message":            {},
	"Grpc-Status-Details-Bin": {},
}

// setMetadataHeaders copies RPC metadata onto an HTTP request or response,
// leaving the headers the REST encoding owns to the stream.
func setMetadataHeaders(dst http.Header, src *connect.Header) {
	for key, vals := range src.All() {
		if _, isProtocolHeader := protocolHeaders[key]; isProtocolHeader {
			continue
		}
		dst[key] = vals
	}
}

func (s *serverStream) close() error {
	if err := s.flushHeaders(false); err != nil {
		return err
	}
	if s.compressWriter == nil {
		return nil
	}
	return s.compressWriter.Close()
}

var _ connect.ServerStream = (*serverStream)(nil)
