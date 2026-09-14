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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
)

// clientStream implements [connect.ClientStream] for an outbound REST
// request. A unary request is marshaled into a buffer and dispatched by
// CloseSend. A client stream is dispatched on the first Send and each
// google.api.HttpBody is written straight to the request body. Receive
// decodes the response, in chunks for a server stream.
type clientStream struct {
	ctx       context.Context //nolint:containedctx // stream methods take no ctx; scoped to one RPC
	transport *transport
	method    *method
	spec      connect.Spec

	sendClosed bool
	received   bool

	reqURL         *url.URL // set by the first Send
	contentType    string
	body           bytes.Buffer   // unary request body
	pipe           *io.PipeWriter // client-stream request body
	compressWriter io.WriteCloser
	inflight       chan struct{} // closed once the piped request has a response

	response *http.Response
	dispatch error
}

func newClientStream(ctx context.Context, t *transport, method *method, spec connect.Spec) *clientStream {
	return &clientStream{ctx: ctx, transport: t, method: method, spec: spec}
}

// SendHeaders is a no-op: headers go with the first Send or CloseSend.
func (s *clientStream) SendHeaders() error { return nil }

func (s *clientStream) Send(msg any) error {
	if s.sendClosed {
		return io.EOF
	}
	first := s.reqURL == nil
	if !first && s.spec.StreamType&connect.StreamTypeClient == 0 {
		return errors.New("vanguard: Send called more than once on REST unary stream")
	}
	pmsg, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("vanguard: Send expects proto.Message, got %T", msg)
	}
	target := s.method.httpRule
	if target == nil {
		return connect.Errorf(connect.CodeInternal, "no httpRule on %s", s.spec.Procedure)
	}
	fields := target.requestBodyFields
	if first {
		reqURL, err := s.requestURL(pmsg, target)
		if err != nil {
			return wrapError(connect.CodeInvalidArgument, "encode request URL", err)
		}
		s.reqURL = reqURL
		if fields != nil {
			s.contentType = bodyContentType(fields, pmsg, s.transport.codec)
		}
		if err := s.openBody(target); err != nil {
			return err
		}
	}
	if fields == nil {
		return nil // the rule maps no body
	}
	if err := encodeBody(s.ctx, s.writer(), fields, pmsg, s.transport.codec); err != nil {
		if errors.Is(err, io.ErrClosedPipe) || s.requestDone() {
			return io.EOF // the server already replied or the request failed; Receive has the verdict
		}
		return wrapError(connect.CodeInvalidArgument, "encode request body", err)
	}
	return nil
}

// requestDone reports whether a piped request has finished, successfully or not.
func (s *clientStream) requestDone() bool {
	if s.inflight == nil {
		return false
	}
	select {
	case <-s.inflight:
		return true
	default:
		return false
	}
}

// openBody sets up the request body. A client stream gets a pipe and the
// HTTP request starts now, so Send writes to the wire.
func (s *clientStream) openBody(target *routeTarget) error {
	if target.requestBodyFields == nil {
		return nil
	}
	sink := io.Writer(&s.body)
	if s.spec.StreamType&connect.StreamTypeClient != 0 {
		reader, writer := io.Pipe()
		req, err := s.newRequest(reader)
		if err != nil {
			return err
		}
		s.pipe = writer
		s.inflight = make(chan struct{})
		go func() {
			resp, err := s.transport.httpClient.Do(req) //nolint:bodyclose // closed by Receive or Close
			if err != nil {
				s.dispatch = connect.Errorf(connect.CodeUnavailable, "http do: %s", err).WithCause(err)
			}
			s.response = resp
			close(s.inflight)
			if err != nil {
				reader.CloseWithError(err) // unblock a pending Send
			}
		}()
		sink = writer
	}
	if compressor := s.transport.sendCompressor; compressor != nil {
		writer, err := compressor.Compress(sink)
		if err != nil {
			return connect.Errorf(connect.CodeInternal, "get compressor: %s", err).WithCause(err)
		}
		s.compressWriter = writer
	}
	return nil
}

// writer is where Send marshals: the compressor, else the pipe or buffer.
func (s *clientStream) writer() io.Writer {
	switch {
	case s.compressWriter != nil:
		return s.compressWriter
	case s.pipe != nil:
		return s.pipe
	default:
		return &s.body
	}
}

// requestURL resolves the rule's path template and query against msg.
func (s *clientStream) requestURL(msg proto.Message, target *routeTarget) (*url.URL, error) {
	path, query, err := httpEncodePathValues(msg.ProtoReflect(), target)
	if err != nil {
		return nil, err
	}
	reqURL := *s.transport.baseURL
	reqURL.Path = joinPath(reqURL.Path, path)
	if q := query.Encode(); q != "" {
		if reqURL.RawQuery == "" {
			reqURL.RawQuery = q
		} else {
			reqURL.RawQuery += "&" + q
		}
	}
	return &reqURL, nil
}

func (s *clientStream) newRequest(body io.Reader) (*http.Request, error) {
	target := s.method.httpRule
	req, err := http.NewRequestWithContext(s.ctx, target.httpMethod, s.reqURL.String(), body)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "build request: %s", err).WithCause(err)
	}
	if info, ok := connect.CallInfoForClientContext(s.ctx); ok {
		maps.Insert(req.Header, info.RequestHeader().All())
	}
	if s.contentType != "" {
		req.Header.Set("Content-Type", s.contentType)
	}
	if compressor := s.transport.sendCompressor; compressor != nil && target.requestBodyFields != nil {
		req.Header.Set("Content-Encoding", compressor.Name())
	}
	// Always set, so net/http neither adds its own gzip nor decodes for us.
	acceptEncoding := s.transport.compressors.names
	if acceptEncoding == "" {
		acceptEncoding = connect.CompressionNameIdentity
	}
	req.Header.Set("Accept-Encoding", acceptEncoding)
	req.Header.Set("Accept", "application/"+s.transport.codec.Name())
	return req, nil
}

// CloseSend finishes the request. Its error is also returned by Receive.
func (s *clientStream) CloseSend() error {
	if s.sendClosed {
		return s.dispatch
	}
	s.sendClosed = true
	s.dispatch = s.finishSend()
	return s.dispatch
}

func (s *clientStream) finishSend() error {
	if s.reqURL == nil {
		return connect.NewError(connect.CodeInvalidArgument, "vanguard: CloseSend before Send on REST stream")
	}
	if s.compressWriter != nil {
		// A pipe write fails only once the request is over; Receive has the verdict.
		if err := s.compressWriter.Close(); err != nil && s.pipe == nil {
			return connect.Errorf(connect.CodeInternal, "compress request: %s", err).WithCause(err)
		}
	}
	if s.pipe != nil {
		_ = s.pipe.Close()
		<-s.inflight
		if s.dispatch != nil {
			return s.dispatch
		}
		return s.finishResponse(s.response)
	}
	var bodyReader io.Reader
	if s.method.httpRule.requestBodyFields != nil {
		bodyReader = bytes.NewReader(s.body.Bytes())
	}
	req, err := s.newRequest(bodyReader)
	if err != nil {
		return err
	}
	resp, err := s.transport.httpClient.Do(req)
	if err != nil {
		return connect.Errorf(connect.CodeUnavailable, "http do: %s", err).WithCause(err)
	}
	return s.finishResponse(resp)
}

// finishResponse records resp, decompressing its body if needed.
func (s *clientStream) finishResponse(resp *http.Response) error {
	s.response = resp
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != connect.CompressionNameIdentity {
		compressor := s.transport.compressors.get(encoding)
		if compressor == nil {
			_ = resp.Body.Close()
			return connect.Errorf(connect.CodeInternal,
				"unknown encoding %q: accepted encodings are %v", encoding, s.transport.compressors.names)
		}
		decompressed, err := decompressBody(compressor, resp.Body)
		if err != nil {
			_ = resp.Body.Close()
			return connect.Errorf(connect.CodeInternal, "decompress response: %s", err).WithCause(err)
		}
		resp.Body = decompressed
	}
	if limit := s.transport.options.maxReadBytes; limit > 0 {
		resp.Body = http.MaxBytesReader(nil, resp.Body, limit)
	}
	if info, ok := connect.CallInfoForClientContext(s.ctx); ok {
		info.ResponseEncoding = connect.CompressionNameIdentity
		if encoding := resp.Header.Get("Content-Encoding"); encoding != "" {
			info.ResponseEncoding = encoding
		}
		for key, vals := range resp.Header {
			info.ResponseHeader().SetValues(key, vals)
		}
	}
	return nil
}

func (s *clientStream) Receive(msg any) error {
	if s.received {
		return io.EOF
	}
	if !s.sendClosed {
		_ = s.CloseSend() // error surfaces via s.dispatch below
	}
	if s.dispatch != nil {
		s.received = true
		return s.dispatch
	}
	contentType := s.response.Header.Get("Content-Type")
	if s.response.StatusCode/100 != 2 {
		s.received = true
		defer s.response.Body.Close()
		body, err := io.ReadAll(s.response.Body)
		if err != nil {
			return s.readError(err)
		}
		return httpErrorFromResponse(s.response.StatusCode, contentType, body)
	}
	pmsg, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("vanguard: Receive expects proto.Message, got %T", msg)
	}
	if s.spec.StreamType&connect.StreamTypeServer != 0 {
		return s.receiveChunk(pmsg, contentType)
	}
	s.received = true
	defer s.response.Body.Close()
	target := s.method.httpRule
	if err := decodeBody(s.ctx, s.response.Body, contentType, target.responseBodyFields, s.transport.codec, pmsg); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return s.readError(err)
		}
		return wrapError(connect.CodeInternal, "decode response", err)
	}
	return nil
}

// readError classifies a failure reading the response body.
func (s *clientStream) readError(err error) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return connect.Errorf(connect.CodeResourceExhausted, "response body exceeds %d bytes", s.transport.options.maxReadBytes)
	}
	return connect.Errorf(connect.CodeUnavailable, "read response: %s", err).WithCause(err)
}

// receiveChunk delivers the next slice of a server-streaming response body.
func (s *clientStream) receiveChunk(pmsg proto.Message, contentType string) error {
	data, err := readChunk(s.response.Body)
	if err != nil {
		s.received = true
		_ = s.response.Body.Close()
		if errors.Is(err, io.EOF) {
			return io.EOF
		}
		return s.readError(err)
	}
	if err := setBodyHTTPBody(s.method.httpRule.responseBodyFields, pmsg, contentType, data); err != nil {
		return wrapError(connect.CodeInternal, "decode response", err)
	}
	return nil
}

// Close releases the request pipe and any response body not yet drained.
func (s *clientStream) Close() error {
	if s.pipe != nil && !s.sendClosed {
		s.sendClosed = true
		_ = s.pipe.CloseWithError(errors.New("vanguard: stream closed"))
		<-s.inflight
	}
	if s.response != nil {
		_ = s.response.Body.Close()
	}
	return nil
}

func joinPath(base, suffix string) string {
	switch {
	case base == "":
		return suffix
	case base[len(base)-1] == '/' && len(suffix) > 0 && suffix[0] == '/':
		return base + suffix[1:]
	case base[len(base)-1] != '/' && (len(suffix) == 0 || suffix[0] != '/'):
		return base + "/" + suffix
	default:
		return base + suffix
	}
}

var _ connect.ClientStream = (*clientStream)(nil)
