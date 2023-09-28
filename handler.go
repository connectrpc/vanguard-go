// Copyright 2023 Buf Technologies, Inc.
//J
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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

// ServeHTTP dispatches the request to the service converting protocols,
// encoding and compression as needed.
func (m *Mux) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if err := m.serveHTTP(response, request); err != nil {
		if herr := asHTTPError(err); herr != nil {
			herr.Encode(response)
		} else {
			http.Error(response, err.Error(), http.StatusInternalServerError)
		}
		return
	}
}

func (m *Mux) serveHTTP(response http.ResponseWriter, request *http.Request) error {
	// Identify the method being invoked.
	method, err := m.resolveMethod(request)
	if err != nil {
		if errors.Is(err, errNotFound) && m.UnknownHandler != nil {
			m.UnknownHandler.ServeHTTP(response, request)
			return nil
		}
		return err
	}

	flusher, ok := response.(http.Flusher)
	if !ok {
		return errors.New("http.ResponseWriter must implement http.Flusher")
	}

	op := &operation{
		bufferPool: &m.bufferPool,
		request:    request,
		response:   response,
		flusher:    flusher,
		method:     method,
		requestMeta: requestMeta{
			Body:       request.Body,
			Header:     httpHeader(request.Header),
			URL:        request.URL,
			Method:     request.Method,
			ProtoMajor: request.ProtoMajor,
			ProtoMinor: request.ProtoMinor,
		},
		responseMeta: responseMeta{
			Header:     httpHeader(response.Header()),
			StatusCode: http.StatusOK,
		},
		requestBuffer: messageBuffer{
			Buf: m.bufferPool.Get(),
		},
		responseBuffer: messageBuffer{
			Buf: m.bufferPool.Get(),
		},
	}

	if err := op.handle(); err != nil {
		if httperr := (*httpError)(nil); errors.As(err, &httperr) {
			return httperr
		}

		// Protocol error, encode the error message.
		buf := op.requestBuffer.Buf
		buf.Reset()
		op.method.client.EncodeError(buf, &op.responseMeta, err)
		if !op.responseMeta.WroteStatus {
			op.responseMeta.WroteStatus = true
			op.response.WriteHeader(op.responseMeta.StatusCode)
		}
		// Write the error message if it was buffered.
		if buf.Len() > 0 {
			_, _ = op.response.Write(buf.Bytes())
			op.flusher.Flush()
		}
	}

	// Free buffers.
	m.bufferPool.Put(op.requestBuffer.Buf)
	m.bufferPool.Put(op.responseBuffer.Buf)
	if op.responseEnd != nil {
		m.bufferPool.Put(op.responseEnd)
	}
	return nil
}

// operation represents a single HTTP operation, which maps to an incoming HTTP request.
// It tracks properties needed to implement protocol transformation.
type operation struct {
	bufferPool *bufferPool
	request    *http.Request
	response   http.ResponseWriter
	flusher    http.Flusher

	method          method
	requestMeta     requestMeta
	responseMeta    responseMeta
	requestBuffer   messageBuffer
	responseBuffer  messageBuffer
	responseEnd     *bytes.Buffer // Buffer waiting on trailers.
	requestMessage  proto.Message
	responseMessage proto.Message

	codecName      string
	compressorName string
	hasResponse    bool
	wroteHeader    bool
}

func (o *operation) handle() error {
	if err := o.decodeRequestHeader(); err != nil {
		return err
	}
	if err := o.encodeRequestHeader(); err != nil {
		return err
	}

	// Init message types.
	requestType, err := o.method.config.resolver.FindMessageByName(
		o.method.config.descriptor.Input().FullName(),
	)
	if err != nil {
		return err
	}
	o.requestMessage = requestType.New().Interface()
	responseType, err := o.method.config.resolver.FindMessageByName(
		o.method.config.descriptor.Output().FullName(),
	)
	if err != nil {
		return err
	}
	o.responseMessage = responseType.New().Interface()

	// If request parameters are partially encoded in the body, read them.
	if o.requestMeta.RequiresBody {
		// Trigger a read to buffer the first request message.
		if _, err := o.read(nil); err != nil {
			// io.EOF on the first read is okay, it just means the request
			// body was empty.
			if !errors.Is(err, io.EOF) {
				return err
			}
		}
	}

	// Build the request.
	o.request.Body = requestReader{
		operation: o,
	}
	o.request.GetBody = nil // TODO: support GetBody
	o.request.ProtoMajor = o.requestMeta.ProtoMajor
	o.request.ProtoMinor = o.requestMeta.ProtoMinor
	o.request.Method = o.requestMeta.Method
	o.request.RequestURI = o.request.URL.RequestURI() // override

	// Build the response writer.
	response := responseWriter{
		operation: o,
	}

	// Serve the request.
	o.method.config.handler.ServeHTTP(response, o.request)

	// Flush the response EOF.
	if _, err := o.write(nil, true); err != nil {
		return err
	}

	// Encode trailers.
	trailer := o.responseBuffer.Buf
	if !o.responseBuffer.Src.IsTrailer {
		trailer.Reset()
	}
	if err := o.method.server.DecodeResponseTrailer(trailer, &o.responseMeta); err != nil {
		return err
	}
	trailer.Reset()
	if err := o.method.client.EncodeResponseTrailer(trailer, &o.responseMeta); err != nil {
		return err
	}

	// Ensure the status is written.
	o.writeStatus()

	// Encode message waiting on trailers.
	if o.responseEnd != nil {
		_, _ = o.responseEnd.WriteTo(o.response)
		o.flusher.Flush()
	}
	// Encode trailers if needed.
	if trailer.Len() > 0 {
		_, _ = trailer.WriteTo(o.response)
		o.flusher.Flush()
	}
	return nil
}

func (o *operation) read(data []byte) (readN int, err error) {
	msgBuf := &o.requestBuffer
	meta := &o.requestMeta
	for {
		switch msgBuf.stage {
		case stageEmpty:
			// Read the first partial bytes for EOF detection.
			if msgBuf.Buf.Len() == 0 {
				if _, err := read(msgBuf.Buf, o.requestMeta.Body); err != nil {
					if !errors.Is(err, io.EOF) {
						return 0, err
					}
					msgBuf.Src.IsEOF = true
					if msgBuf.Index > 0 {
						return 0, io.EOF
					}
				}
			}

			if err := o.method.client.PrepareRequestMessage(msgBuf, meta); err != nil {
				if !errors.Is(err, io.ErrShortBuffer) {
					return 0, err
				}
				if _, err := read(msgBuf.Buf, o.requestMeta.Body); err != nil {
					if !errors.Is(err, io.EOF) {
						return 0, err
					}
					if msgBuf.Src.IsEOF {
						return 0, io.EOF
					}
					msgBuf.Src.IsEOF = true
				}
				continue
			}
			if msgBuf.Src.IsTrailer {
				return 0, fmt.Errorf("unexpected trailer in request")
			}
			if msgBuf.Index > 0 && o.method.config.streamType == connect.StreamTypeUnary {
				return 0, fmt.Errorf("unexpected message in request")
			}
			if err := o.method.server.PrepareRequestMessage(msgBuf, meta); err != nil {
				return 0, err
			}
			// TODO: optimize streaming case to avoid buffering.
			msgBuf.stage = stageRead

		case stageRead:
			size := o.resolveSize(msgBuf.Src)
			remN := size - int64(msgBuf.Buf.Len())

			var excessBuf *bytes.Buffer
			if remN < 0 {
				// Excess bytes in the buffer, so we need to split the buffer.
				buf := o.bufferPool.Get()
				_, _ = buf.ReadFrom(io.LimitReader(msgBuf.Buf, size))
				excessBuf, msgBuf.Buf = msgBuf.Buf, buf // swap
				defer o.bufferPool.Put(excessBuf)
			} else {
				if _, err := msgBuf.Buf.ReadFrom(
					io.LimitReader(o.requestMeta.Body, remN),
				); err != nil {
					return 0, err
				}
			}
			if msgBuf.Src.ReadMode == readModeSize &&
				int64(msgBuf.Buf.Len()) < int64(msgBuf.Src.Size) {
				return 0, io.ErrUnexpectedEOF
			}

			if err := msgBuf.Convert(
				o.bufferPool,
				o.requestMessage,
				meta.Client,
				meta.Server,
			); err != nil {
				return 0, err
			}

			if excessBuf != nil {
				// Append excess, if any.
				msgBuf.Buf.Write(excessBuf.Bytes())
			}

		case stageBuffered:
			readN, err = msgBuf.Read(data)
			if errors.Is(err, io.EOF) {
				msgBuf.Flush()
				continue
			}
			return readN, err

		default:
			return 0, errors.New("invalid message stage")
		}
	}
}

func (o *operation) decodeRequestHeader() error {
	if err := o.method.client.DecodeRequestHeader(&o.requestMeta); err != nil {
		return err
	}
	o.codecName = o.requestMeta.CodecName
	o.compressorName = o.requestMeta.CompressionName
	o.requestMeta.CodecName = o.method.config.ResolveServerCodecName(
		o.requestMeta.CodecName)
	o.requestMeta.CompressionName = o.method.config.ResolveServerCompressorName(
		o.requestMeta.CompressionName)
	return nil
}
func (o *operation) encodeRequestHeader() error {
	o.responseMeta.CodecName = o.requestMeta.CodecName
	o.responseMeta.CompressionName = o.requestMeta.CompressionName
	return o.method.server.EncodeRequestHeader(&o.requestMeta)
}
func (o *operation) decodeResponseHeader() error {
	if err := o.method.server.DecodeRequestHeader(&o.responseMeta); err != nil {
		return err
	}
	return nil
}
func (o *operation) encodeResponseHeader() error {
	if o.codecName != "" {
		o.responseMeta.CodecName = o.codecName
	}
	if o.compressorName != "" {
		o.responseMeta.CompressionName = o.compressorName
	}
	return o.method.client.EncodeRequestHeader(&o.responseMeta)
}

func (o *operation) writeHeader() error {
	if o.wroteHeader {
		return nil
	}
	if err := o.decodeResponseHeader(); err != nil {
		return err
	}
	if err := o.encodeResponseHeader(); err != nil {
		return err
	}
	o.wroteHeader = true
	return nil
}

func (o *operation) writeStatus() {
	_ = o.writeHeader() // ignore error, should be written already
	if o.responseMeta.WroteStatus {
		return
	}
	o.responseMeta.WroteStatus = true
	o.response.WriteHeader(o.responseMeta.StatusCode)
}

func (o *operation) resolveSize(param srcParams) int64 {
	switch param.ReadMode {
	case readModeSize:
		return int64(param.Size)
	case readModeEOF:
		return int64(o.method.config.maxMsgBufferBytes)
	case readModeChunk:
		return chunkMessageSize
	default:
		return -1
	}
}

func (o *operation) write(data []byte, isEOF bool) (wroteN int, err error) {
	msgBuf := &o.responseBuffer
	meta := &o.responseMeta

	if err := o.writeHeader(); err != nil {
		return 0, err
	}

	// Buffer the first partial bytes of the response message.
	wroteN, _ = msgBuf.Buf.Write(data)
	msgBuf.Src.IsEOF = isEOF

	for {
		switch msgBuf.stage {
		case stageEmpty:
			if isEOF {
				if msgBuf.Buf.Len() > 0 {
					return wroteN, io.ErrUnexpectedEOF
				}
				return wroteN, nil
			}
			if err := o.method.server.PrepareResponseMessage(msgBuf, meta); err != nil {
				if errors.Is(err, io.ErrShortBuffer) {
					return wroteN, nil // okay to return partial write
				}
				return 0, err
			}
			if err := o.method.client.PrepareResponseMessage(msgBuf, meta); err != nil {
				return 0, err
			}
			// TODO: optimize streaming case to avoid buffering.
			msgBuf.stage = stageRead

		case stageRead:
			size := o.resolveSize(msgBuf.Src)
			remN := size - int64(msgBuf.Buf.Len())

			if (msgBuf.Src.ReadMode == readModeEOF ||
				msgBuf.Src.ReadMode == readModeChunk) && isEOF {
				if remN > 0 {
					remN = 0
				}
			}

			var excessBuf *bytes.Buffer
			if remN < 0 {
				// Excess bytes in the buffer, so we need to split the buffer.
				buf := o.bufferPool.Get()
				_, _ = buf.ReadFrom(io.LimitReader(msgBuf.Buf, size))
				excessBuf, msgBuf.Buf = msgBuf.Buf, buf // swap
				defer o.bufferPool.Put(excessBuf)
			} else if remN > 0 {
				return wroteN, nil // okay to return partial write
			}

			if msgBuf.Src.IsTrailer {
				// For compressored trailers handle decompression.
				var compressor compressor
				if msgBuf.Src.IsCompressed {
					compressor = meta.Server.Compressor
				}
				// Got trailer, done.
				msgBuf.stage = stageEOF
				// Decompress the trailer if needed.
				return wroteN, decodeBuffer(
					o.bufferPool,
					msgBuf.Buf,
					nil, // Empty message
					nil, // No codec
					compressor,
				)
			}

			if err := msgBuf.Convert(
				o.bufferPool,
				o.responseMessage,
				meta.Server,
				meta.Client,
			); err != nil {
				return 0, err
			}

			// Append excess, if any.
			if excessBuf != nil {
				msgBuf.Buf.Write(excessBuf.Bytes())
			}

		case stageBuffered:
			// Wait for trailers stores the message buffer to be written
			// after the trailers are written.
			if msgBuf.Dst.WaitForTrailer {
				if o.responseEnd == nil {
					o.responseEnd = o.bufferPool.Get()
				}
				if _, err := msgBuf.WriteTo(o.responseEnd); err != nil {
					return wroteN, err
				}
				msgBuf.Flush()
				continue
			}
			// Otherwise, write the message buffer.
			o.writeStatus()
			_, err := msgBuf.WriteTo(o.response)
			if err != nil {
				return wroteN, err
			}
			o.flusher.Flush()
			msgBuf.Flush()
			// Loop back to process the next message if excess bytes were
			// in the buffer.

		case stageEOF:
			if !isEOF {
				return wroteN, io.ErrShortWrite
			}
			return wroteN, nil

		default:
			return 0, errors.New("invalid message stage")
		}
	}
}

type method struct {
	config *methodConfig
	client clientProtocolHandler
	server serverProtocolHandler
}

func (m *Mux) resolveMethod(request *http.Request) (method, error) {
	// Identify the protocol.
	clientProtocol := classifyRequest(request)
	if clientProtocol == ProtocolUnknown {
		return method{}, newHTTPError(http.StatusUnsupportedMediaType, "could not classify protocol")
	}

	var (
		config     *methodConfig
		restTarget *routeTarget
		restVars   []routeTargetVarMatch
	)
	uriPath := request.URL.Path
	switch clientProtocol {
	case ProtocolREST:
		var restMethods routeMethods
		restTarget, restVars, restMethods = m.restRoutes.match(uriPath, request.Method)
		if restTarget == nil {
			if len(restMethods) == 0 {
				return method{}, errNotFound
			}
			var sb strings.Builder
			for method := range restMethods {
				if sb.Len() > 0 {
					sb.WriteByte(',')
				}
				sb.WriteString(method)
			}
			return method{}, &httpError{
				code: http.StatusMethodNotAllowed,
				header: http.Header{
					"Allow": []string{sb.String()},
				},
			}
		}
		config = restTarget.config
	default:
		config = m.methods[uriPath]
		if config == nil {
			return method{}, errNotFound
		}
		restTarget = config.httpRule
	}

	var client clientProtocolHandler
	switch clientProtocol {
	case ProtocolConnect:
		if config.streamType == connect.StreamTypeUnary {
			client = connectUnaryClientProtocol{
				config: config,
			}
		} else {
			client = connectStreamClientProtocol{
				config: config,
			}
		}
	case ProtocolGRPC:
		client = grpcClientProtocol{
			config: config,
		}
	case ProtocolGRPCWeb:
		client = grpcWebClientProtocol{
			config: config,
		}
	case ProtocolREST:
		client = restClientProtocol{
			target: restTarget,
			vars:   restVars,
		}
	default:
		return method{}, &httpError{
			code: http.StatusInternalServerError,
			err:  errors.ErrUnsupported,
		}
	}

	// Identity the protocol to convert to.
	serverProtocol := clientProtocol
	if _, isSupported := config.protocols[serverProtocol]; !isSupported {
		for protocol := protocolMin; protocol <= protocolMax; protocol++ {
			if _, isSupported := config.protocols[protocol]; isSupported {
				serverProtocol = protocol
				break
			}
		}
	}
	var server serverProtocolHandler
	switch serverProtocol {
	case ProtocolConnect:
		if config.streamType == connect.StreamTypeUnary {
			server = &connectUnaryServerProtocol{
				config: config,
			}
		} else {
			server = connectStreamServerProtocol{
				config: config,
			}
		}
	case ProtocolGRPC:
		server = grpcServerProtocol{
			config: config,
		}
	case ProtocolGRPCWeb:
		server = grpcWebServerProtocol{
			config: config,
		}
	case ProtocolREST:
		server = &restServerProtocol{
			target: restTarget,
		}
	default:
		return method{}, &httpError{
			code: http.StatusInternalServerError,
			err:  errors.ErrUnsupported,
		}
	}
	return method{
		config: config,
		client: client,
		server: server,
	}, nil
}

type requestReader struct {
	operation *operation
}

func (r requestReader) Read(data []byte) (int, error) {
	return r.operation.read(data)
}
func (r requestReader) Close() error {
	return r.operation.requestMeta.Body.Close()
}

// responseWriter wraps the original writer and performs the protocol
// transformation. When headers and data are written to this writer,
// they may be modified before being written to the underlying writer,
// which accomplishes the protocol change.
//
// When the headers are written, the actual transformation that is
// needed is determined and a writer decorator created.
type responseWriter struct {
	operation *operation
}

func (w responseWriter) Header() http.Header {
	return w.operation.response.Header()
}

func (w responseWriter) Write(data []byte) (int, error) {
	if !w.operation.hasResponse {
		w.WriteHeader(http.StatusOK)
	}
	return w.operation.write(data, false)
}

func (w responseWriter) WriteHeader(statusCode int) {
	if w.operation.hasResponse {
		return
	}
	w.operation.hasResponse = true
	w.operation.responseMeta.StatusCode = statusCode
}

// Unwrap provides access to the underlying response writer. This plays nicely
// with ResponseController functionality introduced in Go 1.21 without actually
// depending on Go 1.21.
func (w responseWriter) Unwrap() http.ResponseWriter {
	return w.operation.response
}

func (w responseWriter) Flush() {
	// We expose this method so server can call it and won't panic
	// or blow-up when doing type conversion. But it's a no-op
	// since we automatically flush at message boundaries when
	// transforming the response body.
}

func classifyRequest(req *http.Request) Protocol {
	contentTypes := req.Header["Content-Type"]
	if len(contentTypes) == 0 { //nolint:nestif
		// Empty bodies should still have content types. So this should only
		// happen for requests with NO body at all. That's only allowed for
		// REST calls and Connect GET calls.
		connectVersion := req.Header["Connect-Protocol-Version"]
		// If this header is present, the intent is clear. But Connect GET
		// requests should actually encode this via query string (see below).
		if len(connectVersion) == 1 && connectVersion[0] == "1" {
			if req.Method == http.MethodGet {
				return ProtocolConnect
			}
			return ProtocolUnknown
		}
		values := req.URL.Query()
		if values.Get("connect") == "v1" {
			if req.Method != http.MethodGet {
				return ProtocolUnknown
			}
			return ProtocolConnect
		}
		return ProtocolREST
	}

	if len(contentTypes) > 1 {
		return ProtocolUnknown // Don't allow this.
	}
	contentType := contentTypes[0]
	switch {
	case strings.HasPrefix(contentType, "application/connect+"):
		return ProtocolConnect
	case contentType == "application/grpc", strings.HasPrefix(contentType, "application/grpc+"):
		return ProtocolGRPC
	case contentType == "application/grpc-web", strings.HasPrefix(contentType, "application/grpc-web+"):
		return ProtocolGRPCWeb
	case strings.HasPrefix(contentType, "application/"):
		connectVersion := req.Header["Connect-Protocol-Version"]
		if len(connectVersion) == 1 && connectVersion[0] == "1" {
			if req.Method == http.MethodGet {
				return ProtocolConnect
			}
			return ProtocolConnect
		}
		values := req.URL.Query()
		if values.Get("connect") == "v1" {
			if req.Method != http.MethodGet {
				return ProtocolUnknown
			}
			return ProtocolConnect
		}
		// REST usually uses application/json, but use of google.api.HttpBody means it could
		// also use *any* content-type.
		fallthrough
	default:
		return ProtocolREST
	}
}
