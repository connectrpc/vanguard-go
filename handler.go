// Copyright 2023 Buf Technologies, Inc.
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
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

type handler struct {
	mux           *Mux
	bufferPool    *bufferPool
	codecs        map[codecKey]Codec
	canDecompress []string
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) { //nolint:gocyclo
	// Identify the protocol.
	clientProtoHandler, originalContentType, queryVars := classifyRequest(request)
	if clientProtoHandler == nil {
		http.Error(writer, "could not classify protocol", http.StatusUnsupportedMediaType)
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	request = request.WithContext(ctx)
	op := operation{
		muxConfig:      h.mux,
		writer:         writer,
		request:        request,
		reqContentType: originalContentType,
		cancel:         cancel,
		bufferPool:     h.bufferPool,
		canDecompress:  h.canDecompress,
	}
	op.client.protocol = clientProtoHandler
	if queryVars != nil {
		// memoize this, so we don't have to parse query string again later
		op.queryVars = queryVars
	}
	originalHeaders := request.Header.Clone()
	op.contentLen = request.ContentLength
	request.ContentLength = -1 // transforming it will likely change it

	// Identify the method being invoked.
	err := op.resolveMethod(h.mux)
	if err != nil {
		// If the method is not found, we'll try the unknown handler, if there is one.
		if h.mux.UnknownHandler != nil && errors.Is(err, errNotFound) {
			h.mux.UnknownHandler.ServeHTTP(writer, request)
			return
		}
		asHTTPError(err).Encode(writer)
		return
	}
	if !op.client.protocol.acceptsStreamType(&op, op.methodConf.streamType) {
		http.Error(
			writer,
			fmt.Sprintf("stream type %s not supported with %s protocol", op.methodConf.streamType, op.client.protocol),
			http.StatusUnsupportedMediaType)
		return
	}
	if op.methodConf.streamType == connect.StreamTypeBidi && request.ProtoMajor < 2 {
		http.Error(writer, "bidi streams require HTTP/2", http.StatusHTTPVersionNotSupported)
		return
	}
	if clientProtoHandler.protocol() == ProtocolGRPC && request.ProtoMajor != 2 {
		http.Error(writer, "gRPC requires HTTP/2", http.StatusHTTPVersionNotSupported)
		return
	}

	// Identify the request encoding and compression.
	reqMeta, err := clientProtoHandler.extractProtocolRequestHeaders(&op, request.Header)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	// Remove other headers that might mess up the next leg
	if enc := request.Header.Get("Content-Encoding"); enc != "" && enc != CompressionIdentity {
		// If the protocol didn't remove the "Content-Encoding" header in above step,
		// that's because it models encoding in a different way. In that case, encoding
		// of the whole response with this header is not valid.
		http.Error(writer, err.Error(), http.StatusUnsupportedMediaType)
		return
	}
	request.Header.Del("Content-Encoding")
	request.Header.Del("Accept-Encoding")
	request.Header.Del("Content-Length")

	op.reqMeta = reqMeta
	var cannotDecompressRequest bool
	if reqMeta.compression == CompressionIdentity {
		reqMeta.compression = "" // normalize to empty string
	}
	if reqMeta.compression != "" {
		var ok bool
		op.client.reqCompression, ok = h.mux.compressionPools[reqMeta.compression]
		if !ok {
			// This might be okay, like if the transformation doesn't require decoding.
			op.client.reqCompression = nil
			cannotDecompressRequest = true
		}
	}
	op.client.codec = h.codecs[codecKey{res: op.methodConf.resolver, name: reqMeta.codec}]
	if op.client.codec == nil {
		http.Error(writer, fmt.Sprintf("%q sub-format not supported", reqMeta.codec), http.StatusUnsupportedMediaType)
		return
	}

	// Now we can determine the destination protocol details
	if _, supportsProtocol := op.methodConf.protocols[clientProtoHandler.protocol()]; supportsProtocol {
		op.server.protocol = clientProtoHandler.protocol().serverHandler(&op)
	} else {
		for protocol := protocolMin; protocol <= protocolMax; protocol++ {
			if _, supportsProtocol := op.methodConf.protocols[protocol]; supportsProtocol {
				op.server.protocol = protocol.serverHandler(&op)
				break
			}
		}
	}

	// Now that we've ruled out the use of bidi streaming above, it's safe to simulate HTTP/2
	// for the benefit of gRPC handlers, which require HTTP/2.
	if op.server.protocol.protocol() == ProtocolGRPC {
		request.Proto, request.ProtoMajor, request.ProtoMinor = "HTTP/2", 2, 0
	}

	if op.server.protocol.protocol() == ProtocolREST {
		// REST always uses JSON.
		// TODO: allow non-JSON encodings with REST? Would require registering content-types with codecs.
		//
		// NB: This is fine to set even if a custom content-type is used via
		//     the use of google.api.HttpBody. The actual content-type and body
		//     data will be written via serverBodyPreparer implementation.
		op.server.codec = h.mux.codecImpls[CodecJSON](op.methodConf.resolver)
	} else if _, supportsCodec := op.methodConf.codecNames[reqMeta.codec]; supportsCodec {
		op.server.codec = op.client.codec
	} else {
		op.server.codec = h.codecs[codecKey{res: op.methodConf.resolver, name: op.methodConf.preferredCodec}]
	}

	if reqMeta.compression != "" && !cannotDecompressRequest {
		if _, supportsCompression := op.methodConf.compressorNames[reqMeta.compression]; supportsCompression {
			op.server.reqCompression = op.client.reqCompression
		}
		// else: we'll just decompress and not recompress
		// TODO: should we instead pick a supported compression scheme (if there is one)?
	}

	// Now we know enough to handle the request.
	if op.client.protocol.protocol() == op.server.protocol.protocol() &&
		op.client.codec.Name() == op.server.codec.Name() &&
		(cannotDecompressRequest || op.client.reqCompression.Name() == op.server.reqCompression.Name()) {
		// No transformation needed. But we do  need to restore the original headers first
		// since extracting request metadata may have removed keys.
		request.Header = originalHeaders
		op.methodConf.handler.ServeHTTP(writer, request)
		return
	}

	if cannotDecompressRequest {
		// At this point, we have to perform some transformation, so we'll need to
		// be able to decompress/compress.
		http.Error(writer, fmt.Sprintf("%q compression not supported", reqMeta.compression), http.StatusUnsupportedMediaType)
		return
	}

	op.handle()
}

type clientProtocolDetails struct {
	protocol       clientProtocolHandler
	codec          Codec
	reqCompression *compressionPool
}

type serverProtocolDetails struct {
	protocol       serverProtocolHandler
	codec          Codec
	reqCompression *compressionPool
}

func classifyRequest(req *http.Request) (h clientProtocolHandler, contentType string, values url.Values) {
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
				return connectUnaryGetClientProtocol{}, "", nil
			}
			return nil, "", nil
		}
		values = req.URL.Query()
		if values.Get("connect") == "v1" {
			if req.Method != http.MethodGet {
				return nil, "", nil
			}
			return connectUnaryGetClientProtocol{}, "", values
		}
		return restClientProtocol{}, "", values
	}

	if len(contentTypes) > 1 {
		return nil, "", nil // Ick. Don't allow this.
	}
	contentType = contentTypes[0]
	switch {
	case strings.HasPrefix(contentType, "application/connect+"):
		return connectStreamClientProtocol{}, contentType, nil
	case contentType == "application/grpc" || strings.HasPrefix(contentType, "application/grpc+"):
		return grpcClientProtocol{}, contentType, nil
	case contentType == "application/grpc-web" || strings.HasPrefix(contentType, "application/grpc-web+"):
		return grpcWebClientProtocol{}, contentType, nil
	case strings.HasPrefix(contentType, "application/"):
		connectVersion := req.Header["Connect-Protocol-Version"]
		if len(connectVersion) == 1 && connectVersion[0] == "1" {
			if req.Method == http.MethodGet {
				return connectUnaryGetClientProtocol{}, contentType, nil
			}
			return connectUnaryPostClientProtocol{}, contentType, nil
		}
		values = req.URL.Query()
		if values.Get("connect") == "v1" {
			if req.Method != http.MethodGet {
				return nil, "", nil
			}
			return connectUnaryGetClientProtocol{}, "", values
		}
		// REST usually uses application/json, but use of google.api.HttpBody means it could
		// also use *any* content-type.
		fallthrough
	default:
		return restClientProtocol{}, contentType, values
	}
}

type codecKey struct {
	res  TypeResolver
	name string
}

func newCodecMap(methodConfigs map[string]*methodConfig, codecs map[string]func(TypeResolver) Codec) map[codecKey]Codec {
	result := make(map[codecKey]Codec, len(codecs))
	for _, conf := range methodConfigs {
		for codecName, codecFactory := range codecs {
			key := codecKey{res: conf.resolver, name: codecName}
			if _, exists := result[key]; !exists {
				result[key] = codecFactory(conf.resolver)
			}
		}
	}
	return result
}

// operation represents a single HTTP operation, which maps to an incoming HTTP request.
// It tracks properties needed to implement protocol transformation.
type operation struct {
	muxConfig      *Mux
	writer         http.ResponseWriter
	request        *http.Request
	queryVars      url.Values
	reqContentType string // original content-type in incoming request headers
	rspContentType string // original content-type in outging response headers
	contentLen     int64  // original content-length in incoming request headers or -1
	reqMeta        requestMeta
	cancel         context.CancelFunc
	bufferPool     *bufferPool
	canDecompress  []string

	methodConf *methodConfig

	client clientProtocolDetails
	server serverProtocolDetails
	// response compression won't vary between response received from
	// server and response sent to client because we tell server handler
	// that we only accept encodings that both the middleware and the
	// client can decompress.
	respCompression *compressionPool

	// only used when clientProtocolDetails.protocol == ProtocolREST
	restTarget *routeTarget
	restVars   []routeTargetVarMatch

	// these fields memoize the results of type assertions and some method calls
	clientEnveloper     envelopedProtocolHandler
	clientPreparer      clientBodyPreparer
	clientReqNeedsPrep  bool
	clientRespNeedsPrep bool
	serverEnveloper     serverEnvelopedProtocolHandler
	serverPreparer      serverBodyPreparer
	serverReqNeedsPrep  bool
	serverRespNeedsPrep bool
}

func (op *operation) queryValues() url.Values {
	if op.queryVars == nil && op.request.URL.RawQuery != "" {
		op.queryVars = op.request.URL.Query()
	}
	return op.queryVars
}

func (op *operation) handle() {
	op.clientEnveloper, _ = op.client.protocol.(envelopedProtocolHandler)
	op.clientPreparer, _ = op.client.protocol.(clientBodyPreparer)
	if op.clientPreparer != nil {
		op.clientReqNeedsPrep = op.clientPreparer.requestNeedsPrep(op)
	}
	op.serverEnveloper, _ = op.server.protocol.(serverEnvelopedProtocolHandler)
	op.serverPreparer, _ = op.server.protocol.(serverBodyPreparer)
	if op.serverPreparer != nil {
		op.serverReqNeedsPrep = op.serverPreparer.requestNeedsPrep(op)
	}

	serverRequestBuilder, _ := op.server.protocol.(requestLineBuilder)
	var requireMessageForRequestLine bool
	if serverRequestBuilder != nil {
		requireMessageForRequestLine = serverRequestBuilder.requiresMessageToProvideRequestLine(op)
	}

	sameRequestCompression := op.client.reqCompression.Name() == op.server.reqCompression.Name()
	sameCodec := op.client.codec.Name() == op.server.codec.Name()
	// even if body encoding uses same content type, we can't treat them as the same
	// (which means re-using encoded data) if either side needs to prep the data first
	sameRequestCodec := sameCodec && !op.clientReqNeedsPrep && !op.serverReqNeedsPrep
	mustDecodeRequest := !sameRequestCodec || requireMessageForRequestLine

	reqMsg := message{
		sameCompression: sameRequestCompression,
		sameCodec:       sameRequestCodec,
	}

	if mustDecodeRequest {
		// Need the message type to decode
		messageType, err := op.methodConf.resolver.FindMessageByName(op.methodConf.descriptor.Input().FullName())
		if err != nil {
			op.reportError(err)
			return
		}
		reqMsg.msg = messageType.New().Interface()
	}

	var skipBody bool
	if serverRequestBuilder != nil { //nolint:nestif
		if requireMessageForRequestLine {
			if err := op.readRequestMessage(nil, op.request.Body, &reqMsg); err != nil {
				if errors.Is(err, io.EOF) {
					// okay for the first message: means empty message data
					reqMsg.stage = stageRead
				} else {
					op.reportError(err)
					return
				}
			}
			if err := reqMsg.advanceToStage(op, stageDecoded); err != nil {
				op.reportError(err)
				return
			}
		}
		var hasBody bool
		var err error
		op.request.URL.Path, op.request.URL.RawQuery, op.request.Method, hasBody, err =
			serverRequestBuilder.requestLine(op, reqMsg.msg)
		if err != nil {
			op.reportError(err)
			return
		}
		skipBody = !hasBody
		// Recompute if the server needs to prep the request, now that we've modified
		// properties of op.request.
		if op.serverPreparer != nil {
			op.serverReqNeedsPrep = op.serverPreparer.requestNeedsPrep(op)
		}
	} else {
		// if no request line builder, use simple request layout
		op.request.URL.Path = op.methodConf.methodPath
		op.request.URL.RawQuery = ""
		op.request.Method = http.MethodPost
	}
	op.request.URL.ForceQuery = false
	svrReqMeta := op.reqMeta
	svrReqMeta.codec = op.server.codec.Name()
	svrReqMeta.compression = op.server.reqCompression.Name()
	svrReqMeta.acceptCompression = intersect(op.reqMeta.acceptCompression, op.canDecompress)
	op.server.protocol.addProtocolRequestHeaders(svrReqMeta, op.request.Header)

	// Now we can define the transformed response writer (which delays
	// much of its logic until it sees the response headers).
	flusher := asFlusher(op.writer)
	if flusher == nil {
		op.reportError(errors.New("http.ResponseWriter must implement http.Flusher"))
		return
	}
	rw := &responseWriter{op: op, delegate: op.writer, flusher: flusher}
	defer rw.close()
	op.writer = rw

	// And finally we can define the transformed request bodies.
	switch {
	case skipBody:
		// drain any contents of body so downstream handler sees empty
		op.drainBody(op.request.Body)
	case sameRequestCompression && sameRequestCodec && !mustDecodeRequest:
		// we do not need to decompress or decode; just transforming envelopes
		op.request.Body = &envelopingReader{rw: rw, r: op.request.Body}
	default:
		tw := &transformingReader{rw: rw, msg: &reqMsg, r: op.request.Body}
		op.request.Body = tw
		if reqMsg.stage != stageEmpty {
			if err := tw.prepareMessage(); err != nil {
				tw.err = err
			}
		}
	}

	op.methodConf.handler.ServeHTTP(op.writer, op.request)
}

func (op *operation) resolveMethod(mux *Mux) error {
	uriPath := op.request.URL.Path
	switch op.client.protocol.protocol() {
	case ProtocolREST:
		var methods routeMethods
		op.restTarget, op.restVars, methods = mux.restRoutes.match(uriPath, op.request.Method)
		if op.restTarget != nil {
			op.methodConf = op.restTarget.config
			return nil
		}
		if len(methods) == 0 {
			return errNotFound
		}
		var sb strings.Builder
		for method := range methods {
			if sb.Len() > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(method)
		}
		return &httpError{
			code: http.StatusMethodNotAllowed,
			header: http.Header{
				"Allow": []string{sb.String()},
			},
		}
	default:
		methodConf := mux.methods[uriPath]
		if methodConf == nil {
			// TODO: if the service is known, but the method is not, we should send to the client
			//       a proper RPC error (encoded per protocol handler) with an Unimplemented code.
			return errNotFound
		}
		op.restTarget = methodConf.httpRule
		if op.request.Method != http.MethodPost {
			mayAllowGet, ok := op.client.protocol.(clientProtocolAllowsGet)
			allowsGet := ok && mayAllowGet.allowsGetRequests(methodConf)
			if !allowsGet {
				return &httpError{
					code: http.StatusMethodNotAllowed,
					header: http.Header{
						"Allow": []string{http.MethodPost},
					},
				}
			}
			if allowsGet && op.request.Method != http.MethodGet {
				return &httpError{
					code: http.StatusMethodNotAllowed,
					header: http.Header{
						"Allow": []string{http.MethodGet + "," + http.MethodPost},
					},
				}
			}
		}
		op.methodConf = methodConf
		return nil
	}
}

// reportError handles an error that occurs while setting up the operation. It should not be used
// once the underlying server handler has been invoked. For those errors, responseWriter.reportError
// must be used instead.
func (op *operation) reportError(err error) {
	rw, ok := op.writer.(*responseWriter)
	if ok {
		rw.reportError(err)
		return
	}
	// No responseWriter created yet, so we duplicate some of its behavior to write an error
	httpErr := asHTTPError(err)
	httpErr.EncodeHeaders(op.writer.Header())
	connErr := asConnectError(err)
	end := &responseEnd{err: connErr, httpCode: httpErr.code}
	code := op.client.protocol.addProtocolResponseHeaders(responseMeta{end: end}, op.writer.Header())
	op.writer.WriteHeader(code)
	trailers := op.client.protocol.encodeEnd(op, end, op.writer, true)
	httpMergeTrailers(op.writer.Header(), trailers)
}

func (op *operation) readRequestMessage(rw *responseWriter, reader io.Reader, msg *message) error {
	msgLen := -1
	compressed := op.client.reqCompression != nil
	if op.clientEnveloper != nil {
		var envBuf envelopeBytes
		_, err := io.ReadFull(reader, envBuf[:])
		if err != nil {
			return err
		}
		msgLen, compressed, err = op.processRequestEnvelope(envBuf)
		if err != nil {
			if rw != nil {
				rw.reportError(err)
			}
			return err
		}
	}

	buffer := msg.reset(op.bufferPool, true, compressed)
	var err error
	if msgLen == -1 { //nolint:nestif
		limit, grow, makeError, limitErr := op.determineReadLimit()
		if limitErr != nil {
			if rw != nil {
				rw.reportError(limitErr)
			}
			return limitErr
		}
		if grow {
			buffer.Grow(int(limit))
		}
		_, err = io.Copy(buffer, &hardLimitReader{r: reader, rw: rw, limit: limit, makeError: makeError})
		if err == nil && buffer.Len() == 0 {
			err = io.EOF
		}
	} else {
		buffer.Grow(msgLen)
		_, err = io.CopyN(buffer, reader, int64(msgLen))
		if errors.Is(err, io.EOF) {
			// EOF is a sentinel that means normal end of stream; replace it so callers know an error occurred
			err = io.ErrUnexpectedEOF
		}
	}
	if err != nil {
		return err
	}
	msg.stage = stageRead
	return nil
}

func (op *operation) processRequestEnvelope(envBuf envelopeBytes) (msgLen int, compressed bool, err error) {
	env, err := op.clientEnveloper.decodeEnvelope(envBuf)
	if err != nil {
		return 0, false, malformedRequestError(err)
	}
	if env.trailer {
		return 0, false, malformedRequestError(fmt.Errorf("client stream cannot include status/trailer message"))
	}
	if limit := op.methodConf.maxMsgBufferBytes; env.length > limit {
		return 0, false, bufferLimitError(int64(limit))
	}
	return int(env.length), env.compressed, nil
}

func (op *operation) determineReadLimit() (limit int64, grow bool, makeError func(int64) error, err error) {
	limit = int64(op.methodConf.maxMsgBufferBytes)
	if op.contentLen == -1 {
		return limit, false, bufferLimitError, nil
	}
	if op.contentLen > limit {
		// content-length header tells us that entity is too large
		err := bufferLimitError(limit)
		return 0, false, nil, err
	}
	return op.contentLen, true, contentLengthError, nil
}

func (op *operation) drainBody(body io.ReadCloser) {
	if wt, ok := body.(io.WriterTo); ok {
		_, _ = wt.WriteTo(io.Discard)
		return
	}
	buf := op.bufferPool.Get()
	defer op.bufferPool.Put(buf)
	b := buf.Bytes()[0:buf.Cap()]
	_, _ = io.CopyBuffer(io.Discard, body, b)
}

// envelopingReader will translate between envelope styles as data is read.
// It does not do any decompressing or deserializing of data.
type envelopingReader struct {
	rw *responseWriter
	r  io.ReadCloser

	err                error
	current            io.Reader
	mustReleaseCurrent bool
	env                envelopeBytes
	envRemain          int
}

func (er *envelopingReader) Read(data []byte) (n int, err error) {
	if er.err != nil {
		return 0, er.err
	}
	if er.current != nil {
		bytesRead, err := er.current.Read(data)
		isEOF := errors.Is(err, io.EOF)
		if bytesRead > 0 && (err == nil || isEOF) {
			return bytesRead, nil
		}
		if err != nil && !isEOF {
			er.err = err
			return bytesRead, err
		}
		// otherwise EOF, fall through
	}

	if err := er.prepareNext(); err != nil {
		er.err = err
		return 0, err
	}

	if len(data) < er.envRemain {
		copy(data, er.env[envelopeLen-er.envRemain:])
		er.envRemain -= len(data)
		return len(data), nil
	}
	var offset int
	if er.envRemain > 0 {
		copy(data, er.env[envelopeLen-er.envRemain:])
		offset = er.envRemain
		er.envRemain = 0
	}
	if len(data) > offset {
		n, err = er.current.Read(data[offset:])
	}
	return offset + n, err
}

func (er *envelopingReader) Close() error {
	if er.mustReleaseCurrent {
		buf, ok := er.current.(*bytes.Buffer)
		if ok {
			er.rw.op.bufferPool.Put(buf)
		}
		er.current = nil
		er.mustReleaseCurrent = false
	}
	er.err = errors.New("body is closed")
	return er.r.Close()
}

func (er *envelopingReader) prepareNext() error {
	var env envelope
	switch {
	case er.rw.op.clientEnveloper == nil && er.rw.op.serverEnveloper == nil:
		// no envelopes to transform, just pass the body through w/ no change
		er.current = er.r
		er.envRemain = 0
		return nil
	case er.rw.op.clientEnveloper == nil:
		env.compressed = er.rw.op.client.reqCompression != nil
		if er.rw.op.contentLen != -1 {
			er.current = &hardLimitReader{r: er.r, rw: er.rw, limit: er.rw.op.contentLen, makeError: contentLengthError}
			env.length = uint32(er.rw.op.contentLen)
		} else {
			// Oof. We have to buffer entire request in order to measure it.
			limit := int64(er.rw.op.methodConf.maxMsgBufferBytes)
			buf := er.rw.op.bufferPool.Get()
			_, err := io.Copy(buf, &hardLimitReader{r: er.r, rw: er.rw, limit: limit})
			if err != nil {
				er.rw.op.bufferPool.Put(buf)
				er.err = err
				return err
			}
			er.current = buf
			er.mustReleaseCurrent = true
			env.length = uint32(buf.Len())
		}
	default: // clientEnveloper != nil
		var envBytes envelopeBytes
		_, err := io.ReadFull(er.r, envBytes[:])
		if err != nil {
			return err
		}
		env, err = er.rw.op.clientEnveloper.decodeEnvelope(envBytes)
		if err != nil {
			err = malformedRequestError(err)
			er.rw.reportError(err)
			return err
		}
		er.current = io.LimitReader(er.r, int64(env.length))
	}

	if er.rw.op.serverEnveloper == nil {
		er.envRemain = 0
	} else {
		er.envRemain = envelopeLen
		er.env = er.rw.op.serverEnveloper.encodeEnvelope(env)
	}
	return nil
}

// transformingReader transforms the data from the original request
// into a new protocol form as the data is read. It must decompress
// and deserialize each message and then re-serialize (and optionally
// recompress) each message. Since the original incoming protocol may
// have different envelope conventions than the outgoing protocol, it
// also rewrites envelopes.
type transformingReader struct {
	rw  *responseWriter
	msg *message
	r   io.ReadCloser

	consumedFirst bool
	err           error
	buffer        *bytes.Buffer
	env           envelopeBytes
	envRemain     int
}

func (tr *transformingReader) Read(data []byte) (n int, err error) {
	if tr.err != nil {
		return 0, tr.err
	}
	if tr.buffer != nil {
		n, err := tr.buffer.Read(data)
		if n > 0 {
			return n, err
		}
		// otherwise EOF, fall through
	}
	if err := tr.rw.op.readRequestMessage(tr.rw, tr.r, tr.msg); err != nil {
		// If this is the first request message, the error is EOF, and there's a body
		// preparer, we'll allow it and let the preparer produce a message from zero
		// request bytes.
		if !tr.consumedFirst && errors.Is(err, io.EOF) && tr.rw.op.clientReqNeedsPrep {
			tr.msg.stage = stageRead
		} else {
			tr.err = err
			return 0, err
		}
	}
	if err := tr.prepareMessage(); err != nil {
		tr.err = err
		return 0, err
	}

	if len(data) < tr.envRemain {
		copy(data, tr.env[envelopeLen-tr.envRemain:])
		tr.envRemain -= len(data)
		return len(data), nil
	}
	var offset int
	if tr.envRemain > 0 {
		copy(data, tr.env[envelopeLen-tr.envRemain:])
		offset = tr.envRemain
		tr.envRemain = 0
	}
	if len(data) > offset {
		n, err = tr.buffer.Read(data[offset:])
	}
	return offset + n, err
}

func (tr *transformingReader) Close() error {
	tr.err = errors.New("body is closed")
	tr.msg.release(tr.rw.op.bufferPool)
	return tr.r.Close()
}

func (tr *transformingReader) prepareMessage() error {
	tr.consumedFirst = true
	if err := tr.msg.advanceToStage(tr.rw.op, stageSend); err != nil {
		return err
	}
	tr.buffer = tr.msg.sendBuffer()
	if tr.rw.op.serverEnveloper == nil {
		tr.envRemain = 0
		return nil
	}
	// Need to prefix the buffer with an envelope
	env := envelope{
		compressed: tr.msg.wasCompressed && tr.rw.op.server.reqCompression != nil,
		length:     uint32(tr.buffer.Len()),
	}
	tr.env = tr.rw.op.serverEnveloper.encodeEnvelope(env)
	tr.envRemain = envelopeLen
	return nil
}

// responseWriter wraps the original writer and performs the protocol
// transformation. When headers and data are written to this writer,
// they may be modified before being written to the underlying writer,
// which accomplishes the protocol change.
//
// When the headers are written, the actual transformation that is
// needed is determined and a writer decorator created.
type responseWriter struct {
	op       *operation
	delegate http.ResponseWriter
	flusher  http.Flusher
	code     int
	// has WriteHeader or first call to Write occurred?
	headersWritten bool
	contentLen     int
	// have headers actually been flushed to delegate?
	headersFlushed bool
	// have we already written the end of the stream (error/trailers/etc)?
	endWritten bool
	respMeta   *responseMeta
	err        error
	// wraps op.writer; initialized after headers are written
	w io.WriteCloser
	// may be used in place of op.writer for protocols that must see
	// trailers before writing the first bytes of data (like Connect
	// and REST unary).
	buf *bytes.Buffer
}

func (rw *responseWriter) Header() http.Header {
	return rw.delegate.Header()
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	if !rw.headersWritten {
		rw.WriteHeader(http.StatusOK)
	}
	if rw.err != nil {
		return 0, rw.err
	}
	return rw.w.Write(data)
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	if rw.headersWritten {
		return
	}
	rw.headersWritten = true
	rw.code = statusCode
	var err error
	rw.contentLen, err = httpExtractContentLength(rw.Header())
	if err != nil {
		rw.reportError(err)
		return
	}
	rw.op.rspContentType = rw.Header().Get("Content-Type")
	respMeta, processBody, err := rw.op.server.protocol.extractProtocolResponseHeaders(statusCode, rw.Header())
	if err != nil {
		rw.reportError(err)
		return
	}
	// snapshot trailer keys
	trailerKeys := parseMultiHeader(rw.Header().Values("Trailer"))
	if len(trailerKeys) > 0 {
		respMeta.pendingTrailerKeys = make(headerKeys, len(trailerKeys))
		for _, k := range trailerKeys {
			respMeta.pendingTrailerKeys.add(k)
		}
		rw.Header().Del("Trailer")
	}

	// Remove other headers that might mess up the next leg
	rw.Header().Del("Content-Encoding")
	rw.Header().Del("Accept-Encoding")

	rw.respMeta = &respMeta
	if respMeta.compression == CompressionIdentity {
		respMeta.compression = "" // normalize to empty string
	}
	if respMeta.compression != "" {
		var ok bool
		rw.op.respCompression, ok = rw.op.muxConfig.compressionPools[respMeta.compression]
		if !ok {
			rw.reportError(fmt.Errorf("response indicates unsupported compression encoding %q", respMeta.compression))
			return
		}
	}
	if respMeta.codec != "" && respMeta.codec != rw.op.server.codec.Name() &&
		!restHTTPBodyResponse(rw.op) {
		// unexpected content-type for reply
		rw.reportError(fmt.Errorf("response uses incorrect codec: expecting %q but instead got %q", rw.op.server.codec.Name(), respMeta.codec))
		return
	}

	if respMeta.end != nil {
		// RPC failed immediately.
		if processBody != nil {
			// We have to wait until we receive the body in order to process the error.
			rw.w = &errorWriter{
				rw:          rw,
				respMeta:    rw.respMeta,
				processBody: processBody,
				buffer:      rw.op.bufferPool.Get(),
			}
			return
		}
		// We can send back error response immediately.
		rw.flushHeaders()
		rw.w = noResponseBodyWriter{}
		return
	}

	if rw.op.clientPreparer != nil {
		rw.op.clientRespNeedsPrep = rw.op.clientPreparer.responseNeedsPrep(rw.op)
	}
	if rw.op.serverPreparer != nil {
		rw.op.serverRespNeedsPrep = rw.op.serverPreparer.responseNeedsPrep(rw.op)
	}

	sameCodec := rw.op.client.codec.Name() == rw.op.server.codec.Name()
	// even if body encoding uses same content type, we can't treat them as the same
	// (which means re-using encoded data) if either side needs to prep the data first
	sameResponseCodec := sameCodec && !rw.op.clientRespNeedsPrep && !rw.op.serverRespNeedsPrep

	respMsg := message{sameCompression: true, sameCodec: sameResponseCodec}

	if !sameResponseCodec {
		// We will have to decode and re-encode, so we need the message type.
		messageType, err := rw.op.methodConf.resolver.FindMessageByName(rw.op.methodConf.descriptor.Output().FullName())
		if err != nil {
			rw.reportError(err)
			return
		}
		respMsg.msg = messageType.New().Interface()
	}

	var endMustBeInHeaders bool
	if mustBe, ok := rw.op.client.protocol.(clientProtocolEndMustBeInHeaders); ok {
		endMustBeInHeaders = mustBe.endMustBeInHeaders()
	}
	var delegate io.Writer
	if endMustBeInHeaders {
		// We must await the end before we can write headers, which means we have to
		// buffer the entire response.
		rw.buf = rw.op.bufferPool.Get()
		delegate = &limitWriter{buf: rw.buf, limit: rw.op.methodConf.maxMsgBufferBytes, rw: rw}
	} else {
		// We can go ahead and flush headers now.
		rw.flushHeaders()
		delegate = rw.delegate
	}

	// Now we can define the transformed response body.
	if sameResponseCodec {
		// we do not need to decompress or decode
		rw.w = &envelopingWriter{rw: rw, w: delegate}
	} else {
		rw.w = &transformingWriter{rw: rw, msg: &respMsg, w: delegate}
	}
}

// Unwrap provides access to the underlying response writer. This plays nicely
// with ResponseController functionality introduced in Go 1.21 without actually
// depending on Go 1.21.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.delegate
}

func (rw *responseWriter) Flush() {
	// We expose this method so server can call it and won't panic
	// or blow-up when doing type conversion. But it's a no-op
	// since we automatically flush at message boundaries when
	// transforming the response body.
}

func (rw *responseWriter) flushMessage() {
	if rw.buf != nil {
		// we are buffering until we see trailers, so we don't
		// want to actually flush the underlying response writer yet
		return
	}
	rw.flusher.Flush()
}

func (rw *responseWriter) reportError(err error) {
	var end responseEnd
	if errors.As(err, &end.err) {
		end.httpCode = httpStatusCodeFromRPC(end.err.Code())
	} else {
		// TODO: maybe this should be CodeUnknown instead?
		end.err = connect.NewError(connect.CodeInternal, err)
		end.httpCode = http.StatusBadGateway
	}
	rw.reportEnd(&end)
}

func (rw *responseWriter) reportEnd(end *responseEnd) {
	if rw.endWritten {
		// ruh-roh... this should not happen
		return
	}
	if rw.respMeta != nil && len(rw.respMeta.pendingTrailers) > 0 && len(end.trailers) == 0 {
		// add any pending trailers to the end
		end.trailers = rw.respMeta.pendingTrailers
	}
	switch {
	case rw.headersFlushed:
		// write error to body or trailers
		rw.writeEnd(end, false)
	case rw.respMeta != nil:
		rw.respMeta.end = end
		rw.flushHeaders()
	default:
		rw.respMeta = &responseMeta{end: end}
		rw.flushHeaders()
	}
	rw.flusher.Flush()
	// response is done
	rw.op.cancel()
	rw.err = context.Canceled
}

func (rw *responseWriter) flushHeaders() {
	if rw.headersFlushed {
		return // already flushed
	}
	cliRespMeta := *rw.respMeta
	cliRespMeta.codec = rw.op.client.codec.Name()
	cliRespMeta.compression = rw.op.respCompression.Name()
	cliRespMeta.acceptCompression = intersect(rw.respMeta.acceptCompression, rw.op.canDecompress)
	statusCode := rw.op.client.protocol.addProtocolResponseHeaders(cliRespMeta, rw.Header())
	hasErr := rw.respMeta.end != nil && rw.respMeta.end.err != nil
	// We only buffer full response for unary operations, so if we have an error,
	// we ignore anything already written to the buffer.
	if rw.buf != nil && !hasErr {
		rw.Header().Set("Content-Length", strconv.Itoa(rw.buf.Len()))
	}
	// TODO: At this point, if the server was gRPC but the client is not, we may have "Trailer"
	//       headers reserving the use of various metadata keys in trailers. It would be
	//       cleaner if they were culled and only remained present for sneding to gRPC clients.
	rw.delegate.WriteHeader(statusCode)
	if rw.buf != nil {
		if !hasErr {
			_, _ = rw.buf.WriteTo(rw.delegate)
		}
		rw.op.bufferPool.Put(rw.buf)
		rw.buf = nil
	}
	if rw.respMeta.end != nil {
		// response is done
		rw.writeEnd(rw.respMeta.end, true)
		rw.err = context.Canceled
	}

	rw.headersFlushed = true
}

func (rw *responseWriter) close() {
	if !rw.headersWritten {
		// treat as empty successful response
		rw.WriteHeader(http.StatusOK)
	}
	if rw.w != nil {
		_ = rw.w.Close()
	}
	if rw.endWritten {
		return // all done
	}
	if rw.respMeta.end != nil {
		// got end in headers
		rw.reportEnd(rw.respMeta.end)
		return
	}
	// try to get end from trailers
	trailer := httpExtractTrailers(rw.Header(), rw.respMeta.pendingTrailerKeys)
	end, err := rw.op.server.protocol.extractEndFromTrailers(rw.op, trailer)
	if err != nil {
		rw.reportError(err)
		return
	}
	rw.reportEnd(&end)
}

func (rw *responseWriter) writeEnd(end *responseEnd, wasInHeaders bool) {
	trailers := rw.op.client.protocol.encodeEnd(rw.op, end, rw.delegate, wasInHeaders)
	httpMergeTrailers(rw.Header(), trailers)
	rw.endWritten = true
}

// envelopingWriter will translate between envelope styles as data is
// written. It does not do any decompressing or deserializing of data.
type envelopingWriter struct {
	rw *responseWriter
	w  io.Writer

	initialized         bool
	err                 error
	writingEnvelope     bool
	env                 envelopeBytes
	remainingBytes      int
	current             io.Writer
	mustReleaseCurrent  bool
	currentIsTrailer    bool
	trailerIsCompressed bool
}

func (ew *envelopingWriter) Write(data []byte) (int, error) {
	ew.maybeInit()
	if ew.err != nil {
		return 0, ew.err
	}
	if ew.remainingBytes == -1 {
		n, err := ew.current.Write(data)
		if err != nil {
			ew.err = err
		}
		return n, err
	}

	var written int
	for {
		if ew.err != nil {
			return written, ew.err
		}
		if len(data) < ew.remainingBytes {
			// not enough data to trigger next action; ingest data and return
			n, err := ew.writeBytes(data)
			ew.remainingBytes -= n
			written += n
			if err != nil {
				ew.err = err
			}
			return written, err
		}
		// ingest remaining needed and trigger next action
		n, err := ew.writeBytes(data[:ew.remainingBytes])
		written += n
		data = data[ew.remainingBytes:]
		ew.remainingBytes -= n
		if err != nil {
			ew.err = err
			return written, err
		}
		if ew.writingEnvelope {
			if err := ew.handleEnvelopeWritten(); err != nil {
				return written, err
			}
			continue
		}

		if ew.currentIsTrailer {
			err := ew.handleTrailer()
			if err != nil {
				return written, err
			}
		} else {
			// flush after each message and reset for next envelope
			ew.rw.flushMessage()
			ew.writingEnvelope = true
			ew.remainingBytes = envelopeLen
		}
	}
}

func (ew *envelopingWriter) writeBytes(data []byte) (int, error) {
	if ew.writingEnvelope {
		copy(ew.env[envelopeLen-ew.remainingBytes:], data)
		return len(data), nil
	}
	return ew.current.Write(data)
}

func (ew *envelopingWriter) handleEnvelopeWritten() error {
	ew.writingEnvelope = false
	env, err := ew.rw.op.serverEnveloper.decodeEnvelope(ew.env)
	if err != nil {
		err = malformedRequestError(err)
		ew.rw.reportError(err)
		return err
	}
	if env.trailer {
		// buffer final message, so we can transform it to a responseEnd
		if limit := ew.rw.op.methodConf.maxMsgBufferBytes; env.length > limit {
			err := bufferLimitError(int64(limit))
			ew.rw.reportError(err)
			return err
		}
		buf := ew.rw.op.bufferPool.Get()
		buf.Grow(int(env.length))
		ew.current = buf
		ew.mustReleaseCurrent = true
		ew.currentIsTrailer = true
		ew.trailerIsCompressed = env.compressed
		ew.remainingBytes = int(env.length)
		return nil
	}
	if ew.rw.op.clientEnveloper != nil {
		envBytes := ew.rw.op.clientEnveloper.encodeEnvelope(env)
		_, err := ew.w.Write(envBytes[:])
		if err != nil {
			ew.err = err
			return err
		}
	}
	ew.current = ew.w
	ew.remainingBytes = int(env.length)
	return nil
}

func (ew *envelopingWriter) Close() error {
	var buf *bytes.Buffer
	if ew.mustReleaseCurrent {
		var ok bool
		buf, ok = ew.current.(*bytes.Buffer)
		if !ok {
			lw, ok := ew.current.(*limitWriter)
			if ok {
				buf = lw.buf
			}
		}
		if buf == nil {
			return fmt.Errorf("current sink must be *limitWriter or *bytes.Buffer but instead is %T", ew.current)
		}
		defer ew.rw.op.bufferPool.Put(buf)
	}
	if ew.remainingBytes == -1 && ew.mustReleaseCurrent && ew.err == nil {
		// We were buffering in order to measure size and create envelope,
		// so do that now.
		env := envelope{compressed: ew.rw.op.respCompression != nil, length: uint32(buf.Len())}
		envBytes := ew.rw.op.clientEnveloper.encodeEnvelope(env)
		_, err := ew.w.Write(envBytes[:])
		if err != nil {
			ew.err = err
			return err
		}
		_, err = buf.WriteTo(ew.w)
		if err != nil {
			ew.err = err
			return err
		}
	}
	var normalEOF bool
	if ew.writingEnvelope && ew.remainingBytes == envelopeLen {
		// We were looking for envelope of next message, but no next message in the stream
		normalEOF = true
	}
	if ew.remainingBytes > 0 && !normalEOF {
		// Unfinished body!
		if ew.writingEnvelope {
			ew.rw.reportError(fmt.Errorf("handler only wrote %d out of %d bytes of message envelope", envelopeLen-ew.remainingBytes, envelopeLen))
		} else {
			ew.rw.reportError(fmt.Errorf("handler failed to write final %d bytes of message", ew.remainingBytes))
		}
	}
	ew.remainingBytes = 0
	ew.current = nil
	ew.err = errors.New("body is closed")
	return nil
}

func (ew *envelopingWriter) maybeInit() {
	if ew.initialized {
		return
	}
	ew.initialized = true
	if ew.rw.op.serverEnveloper != nil {
		ew.writingEnvelope = true
		ew.remainingBytes = envelopeLen
		return
	}
	if ew.rw.op.clientEnveloper == nil {
		// just pass everything through
		ew.remainingBytes = -1
		ew.current = ew.w
		return
	}
	if ew.rw.contentLen == -1 {
		// Oof, we have to buffer everything to measure the request size
		// to construct an envelope.
		ew.remainingBytes = -1
		buf := ew.rw.op.bufferPool.Get()
		ew.current = &limitWriter{buf: buf, limit: ew.rw.op.methodConf.maxMsgBufferBytes, rw: ew.rw}
		ew.mustReleaseCurrent = true
		return
	}
	// synthesize envelope
	var env envelope
	env.compressed = ew.rw.op.respCompression != nil
	env.length = uint32(ew.rw.contentLen)
	envBytes := ew.rw.op.clientEnveloper.encodeEnvelope(envelope{})
	_, err := ew.w.Write(envBytes[:])
	if err != nil {
		ew.err = err
		return
	}
	ew.current = ew.w
	ew.remainingBytes = envelopeLen
}

func (ew *envelopingWriter) handleTrailer() error {
	data, ok := ew.current.(*bytes.Buffer)
	if !ok {
		// should not be possible
		return fmt.Errorf("trailer must be *limitWriter but instead is %T", ew.current)
	}
	defer ew.rw.op.bufferPool.Put(data)
	ew.mustReleaseCurrent = false
	if ew.trailerIsCompressed {
		uncompressed := ew.rw.op.bufferPool.Get()
		defer ew.rw.op.bufferPool.Put(uncompressed)
		if err := ew.rw.op.respCompression.decompress(uncompressed, data); err != nil {
			return err
		}
		data = uncompressed
	}
	end, err := ew.rw.op.serverEnveloper.decodeEndFromMessage(ew.rw.op, data)
	if err != nil {
		ew.rw.reportError(err)
		return err
	}
	end.wasCompressed = ew.trailerIsCompressed
	ew.rw.reportEnd(&end)
	ew.err = errors.New("final data already written")
	return nil
}

// transformingWriter transforms the data from the original response
// into a new protocol form as the data is written. It must decompress
// and deserialize each message and then re-serialize (and optionally
// recompress) each message. Since the original incoming protocol may
// have different envelope conventions than the outgoing protocol, it
// also rewrites envelopes.
type transformingWriter struct {
	rw  *responseWriter
	msg *message
	w   io.Writer

	err             error
	buffer          *bytes.Buffer
	expectingBytes  int
	writingEnvelope bool
	latestEnvelope  envelope
}

func (tw *transformingWriter) Write(data []byte) (int, error) {
	if tw.err != nil {
		return 0, tw.err
	}
	if tw.buffer == nil {
		tw.reset()
	}
	if tw.expectingBytes == -1 {
		if limit := int64(tw.rw.op.methodConf.maxMsgBufferBytes); int64(len(data))+int64(tw.buffer.Len()) > limit {
			err := bufferLimitError(limit)
			tw.rw.reportError(err)
			return 0, err
		}
		return tw.buffer.Write(data)
	}

	var written int
	// For enveloped protocols, it's possible that data contains
	// multiple messages, so we need to process in a loop.
	for {
		if tw.err != nil {
			return written, tw.err
		}
		remainingBytes := tw.expectingBytes - tw.buffer.Len()
		if len(data) < remainingBytes {
			// not enough data to trigger next action; ingest data and return
			tw.buffer.Write(data)
			written += len(data)
			break
		}
		// ingest remaining needed and trigger next action
		tw.buffer.Write(data[:remainingBytes])
		written += remainingBytes
		data = data[remainingBytes:]
		if tw.writingEnvelope {
			var envBytes envelopeBytes
			_, _ = tw.buffer.Read(envBytes[:])
			var err error
			tw.latestEnvelope, err = tw.rw.op.serverEnveloper.decodeEnvelope(envBytes)
			if err != nil {
				err = malformedRequestError(err)
				tw.rw.reportError(err)
				return written, err
			}
			if limit := tw.rw.op.methodConf.maxMsgBufferBytes; tw.latestEnvelope.length > limit {
				err = bufferLimitError(int64(limit))
				tw.rw.reportError(err)
				return written, err
			}
			tw.buffer = tw.msg.reset(tw.rw.op.bufferPool, false, tw.latestEnvelope.compressed)
			tw.buffer.Grow(int(tw.latestEnvelope.length))
			tw.expectingBytes = int(tw.latestEnvelope.length)
			tw.writingEnvelope = false
		} else {
			if err := tw.flushMessage(); err != nil {
				tw.rw.reportError(err)
				return written, err
			}
			tw.expectingBytes = envelopeLen
			tw.writingEnvelope = true
		}
	}
	return written, nil
}

func (tw *transformingWriter) Close() error {
	if tw.expectingBytes == -1 {
		if err := tw.flushMessage(); err != nil {
			tw.rw.reportError(err)
		}
	} else if tw.buffer != nil && tw.buffer.Len() > 0 {
		// Unfinished body!
		if tw.writingEnvelope {
			tw.rw.reportError(fmt.Errorf("handler only wrote %d out of %d bytes of message envelope", tw.buffer.Len(), envelopeLen))
		} else {
			tw.rw.reportError(fmt.Errorf("handler only wrote %d out of %d bytes of message", tw.buffer.Len(), tw.expectingBytes))
		}
	}
	tw.expectingBytes = 0
	tw.msg.release(tw.rw.op.bufferPool)
	tw.buffer = nil
	tw.err = errors.New("body is closed")
	return nil
}

func (tw *transformingWriter) flushMessage() error {
	if tw.latestEnvelope.trailer {
		data := tw.buffer
		if tw.latestEnvelope.compressed {
			data = tw.rw.op.bufferPool.Get()
			defer tw.rw.op.bufferPool.Put(data)
			if err := tw.rw.op.respCompression.decompress(data, tw.buffer); err != nil {
				return err
			}
		}
		end, err := tw.rw.op.serverEnveloper.decodeEndFromMessage(tw.rw.op, data)
		if err != nil {
			tw.rw.reportError(err)
			return err
		}
		end.wasCompressed = tw.latestEnvelope.compressed
		tw.rw.reportEnd(&end)
		tw.err = errors.New("final data already written")
		return nil
	}

	// We've finished reading the message, so we can manually set the stage
	tw.msg.stage = stageRead
	if err := tw.msg.advanceToStage(tw.rw.op, stageSend); err != nil {
		return err
	}
	buffer := tw.msg.sendBuffer()
	if enveloper := tw.rw.op.clientEnveloper; enveloper != nil {
		env := envelope{
			compressed: tw.msg.wasCompressed && tw.rw.op.respCompression != nil,
			length:     uint32(buffer.Len()),
		}
		envBytes := enveloper.encodeEnvelope(env)
		if _, err := tw.w.Write(envBytes[:]); err != nil {
			tw.err = err
			return err
		}
	}
	if _, err := buffer.WriteTo(tw.w); err != nil {
		tw.err = err
		return err
	}
	// flush after each message
	tw.rw.flushMessage()

	tw.reset()
	return nil
}

func (tw *transformingWriter) reset() {
	if tw.rw.op.serverEnveloper != nil {
		tw.buffer = tw.msg.reset(tw.rw.op.bufferPool, false, false)
		tw.expectingBytes = envelopeLen
		tw.writingEnvelope = true
	} else {
		isCompressed := tw.rw.respMeta.compression != ""
		tw.buffer = tw.msg.reset(tw.rw.op.bufferPool, false, isCompressed)
		tw.expectingBytes = -1
	}
}

type errorWriter struct {
	rw          *responseWriter
	respMeta    *responseMeta
	processBody responseEndUnmarshaller
	buffer      *bytes.Buffer
}

func (ew *errorWriter) Write(data []byte) (int, error) {
	if ew.buffer == nil {
		return 0, errors.New("writer already closed")
	}
	if limit := int64(ew.rw.op.methodConf.maxMsgBufferBytes); int64(len(data))+int64(ew.buffer.Len()) > limit {
		err := bufferLimitError(limit)
		ew.rw.reportError(err)
		return 0, err
	}
	return ew.buffer.Write(data)
}

func (ew *errorWriter) Close() error {
	if ew.respMeta.end == nil {
		ew.respMeta.end = &responseEnd{}
	}
	bufferPool := ew.rw.op.bufferPool
	defer bufferPool.Put(ew.buffer)
	body := ew.buffer
	if compressPool := ew.rw.op.respCompression; compressPool != nil {
		uncompressed := bufferPool.Get()
		defer bufferPool.Put(uncompressed)
		if err := compressPool.decompress(uncompressed, body); err != nil {
			// can't really just return an error; we have to encode the
			// error into the RPC response, so we populate respMeta.end
			if ew.respMeta.end.httpCode == 0 || ew.respMeta.end.httpCode == http.StatusOK {
				ew.respMeta.end.httpCode = http.StatusInternalServerError
			}
			ew.respMeta.end.err = connect.NewError(connect.CodeInternal, fmt.Errorf("failed to decompress body: %w", err))
			body = nil
		} else {
			body = uncompressed
		}
	}
	if body != nil {
		ew.processBody(ew.rw.op.server.codec, body, ew.respMeta.end)
	}
	ew.rw.flushHeaders()
	ew.buffer = nil
	return nil
}

type noResponseBodyWriter struct {
}

func (c noResponseBodyWriter) Write([]byte) (int, error) {
	return 0, errors.New("final data already written")
}

func (c noResponseBodyWriter) Close() error {
	return nil
}

type limitWriter struct {
	buf   *bytes.Buffer
	limit uint32
	rw    *responseWriter
}

func (l *limitWriter) Write(data []byte) (n int, err error) {
	if uint32(l.buf.Len()+len(data)) > l.limit {
		err := bufferLimitError(int64(l.limit))
		l.rw.reportError(err)
		return 0, err
	}
	return l.buf.Write(data)
}

type hardLimitReader struct {
	r         io.Reader
	limit     int64
	read      int64
	rw        *responseWriter
	makeError func(int64) error
}

func (h *hardLimitReader) Read(data []byte) (n int, err error) {
	remaining := h.limit - h.read
	if remaining < 0 {
		return 0, h.error()
	}
	if int64(len(data)) > remaining {
		// allow reading one byte over the limit, so we can distinguish between
		// reading exactly the limit vs. reading too much.
		data = data[:remaining+1]
	}
	n, err = h.r.Read(data)
	h.read += int64(n)
	if h.read > h.limit && (err == nil || errors.Is(err, io.EOF)) {
		err := h.error()
		if h.rw != nil {
			h.rw.reportError(err)
		}
		return n, err
	}
	return n, err
}

func (h *hardLimitReader) error() error {
	if h.makeError == nil {
		return bufferLimitError(h.limit)
	}
	return h.makeError(h.limit)
}

type messageStage int

const (
	stageEmpty = messageStage(iota)
	// This is the stage of a message after the raw data has been read from the client
	// or written by the server handler.
	//
	// At this point either compressed or data fields of the message will be populated
	// (depending on whether message data was compressed or not).
	stageRead
	// This is the stage of a message after the data has been decompressed and decoded.
	//
	// The msg field of the message is usable at this point. The compressed and data
	// fields of the message will remain populated if their values can be re-used.
	stageDecoded
	// This is the stage of a message after it has been re-encoded and re-compressed
	// and is ready to send (to be read by server handler or to be written to client).
	//
	// Either compressed or data fields of the message will be populated (depending on
	// whether message data was compressed or not).
	stageSend
)

func (s messageStage) String() string {
	switch s {
	case stageEmpty:
		return "empty"
	case stageRead:
		return "read"
	case stageDecoded:
		return "decoded"
	case stageSend:
		return "send"
	default:
		return "unknown"
	}
}

// message represents a single message in an RPC stream. It can be re-used in a stream,
// so we only allocate one and then re-use it for subsequent messages (if stream has
// more than one).
type message struct {
	// true if this is a request message read from the client; false if
	// this is a response message written by the server.
	isRequest bool

	// flags indicating if compressed and data should be preserved after use.
	sameCompression, sameCodec bool
	// wasCompressed is true if the data was originally compressed; this can
	// be false in a stream when the stream envelope's compressed bit is unset.
	wasCompressed bool

	stage messageStage

	// compressed is the compressed bytes; may be nil if the contents have
	// already been decompressed into the data field.
	compressed *bytes.Buffer
	// data is the serialized but uncompressed bytes; may be nil if the
	// contents have not yet been decompressed or have been de-serialized
	// into the msg field.
	data *bytes.Buffer
	// msg is the plain message; not valid unless stage is stageDecoded
	msg proto.Message
}

// sendBuffer returns the buffer to use to read message data to be sent.
func (m *message) sendBuffer() *bytes.Buffer {
	if m.stage != stageSend {
		return nil
	}
	if m.wasCompressed {
		return m.compressed
	}
	return m.data
}

// release releases all buffers associated with message to the given pool.
func (m *message) release(pool *bufferPool) {
	if m.compressed != nil {
		pool.Put(m.compressed)
	}
	if m.data != nil && m.data != m.compressed {
		pool.Put(m.data)
	}
	m.data, m.compressed, m.msg = nil, nil, nil
}

// reset arranges for message to be re-used by making sure it has
// a compressed buffer that is ready to accept bytes and no data
// buffer.
func (m *message) reset(pool *bufferPool, isRequest, isCompressed bool) *bytes.Buffer {
	m.stage = stageEmpty
	m.isRequest = isRequest
	m.wasCompressed = isCompressed
	// we only need one buffer to start, so put
	// a non-nil buffer into buffer1 and if we
	// have a second non-nil buffer, release it
	buffer1, buffer2 := m.compressed, m.data
	if buffer1 == nil && buffer2 != nil {
		buffer1, buffer2 = buffer2, buffer1
	}
	if buffer2 != nil && buffer2 != buffer1 {
		pool.Put(buffer2)
	}
	if buffer1 == nil {
		buffer1 = pool.Get()
	} else {
		buffer1.Reset()
	}
	if isCompressed {
		m.compressed, m.data = buffer1, nil
	} else {
		m.data, m.compressed = buffer1, nil
	}
	return buffer1
}

func (m *message) advanceToStage(op *operation, newStage messageStage) error {
	if m.stage == stageEmpty {
		return errors.New("message has not yet been read")
	}
	if m.stage > newStage {
		return fmt.Errorf("cannot advance message stage backwards: stage %v > target %v", m.stage, newStage)
	}
	if newStage == m.stage {
		return nil // no-op
	}

	if newStage == stageSend && m.sameCodec &&
		(!m.wasCompressed || (m.wasCompressed && m.sameCompression)) {
		// We can re-use existing buffer; no more action to take.
		m.stage = newStage
		return nil // no more action to take
	}

	switch {
	case m.stage == stageRead && newStage == stageSend:
		if !m.sameCodec {
			// If the codec is different we have to fully decode the message and
			// then fully re-encode.
			if err := m.advanceToStage(op, stageDecoded); err != nil {
				return err
			}
			return m.advanceToStage(op, newStage)
		}

		// We must de-compress and re-compress the data.
		if err := m.decompress(op, false); err != nil {
			return err
		}
		if err := m.compress(op); err != nil {
			return err
		}
		m.stage = newStage
		return nil

	case m.stage == stageRead && newStage == stageDecoded:
		if m.wasCompressed {
			if err := m.decompress(op, m.sameCompression && m.sameCodec); err != nil {
				return err
			}
		}
		if err := m.decode(op, m.sameCodec); err != nil {
			return err
		}
		m.stage = newStage
		return nil

	case m.stage == stageDecoded && newStage == stageSend:
		if !m.sameCodec {
			// re-encode
			if err := m.encode(op); err != nil {
				return err
			}
		}
		if m.wasCompressed {
			// re-compress
			if err := m.compress(op); err != nil {
				return err
			}
		}
		m.stage = newStage
		return nil

	default:
		return fmt.Errorf("unknown stage transition: stage %v to target %v", m.stage, newStage)
	}
}

// decompress will decompress data in m.compressed into m.data,
// acquiring a new buffer from op's bufferPool if necessary.
// If saveBuffer is true, m.compressed will be unmodified on
// return; otherwise, the buffer will be released to op's
// bufferPool and the field set to nil.
//
// This method should not be called directly as the message's
// buffers could get out of sync with its stage. It should
// only be called from m.advanceToStage.
func (m *message) decompress(op *operation, saveBuffer bool) error {
	var pool *compressionPool
	if m.isRequest {
		pool = op.client.reqCompression
	} else {
		pool = op.respCompression
	}
	if pool == nil {
		// identity compression, so nothing to do
		m.data = m.compressed
		if !saveBuffer {
			m.compressed = nil
		}
		return nil
	}

	var src *bytes.Buffer
	if saveBuffer {
		// we allocate a new buffer, but not the underlying byte slice
		// (it's cheaper than re-compressing later)
		src = bytes.NewBuffer(m.compressed.Bytes())
	} else {
		src = m.compressed
	}
	m.data = op.bufferPool.Get()
	if err := pool.decompress(m.data, src); err != nil {
		return err
	}
	if !saveBuffer {
		op.bufferPool.Put(m.compressed)
		m.compressed = nil
	}
	return nil
}

// compress will compress data in m.data into m.compressed,
// acquiring a new buffer from op's bufferPool if necessary.
//
// This method should not be called directly as the message's
// buffers could get out of sync with its stage. It should
// only be called from m.advanceToStage.
func (m *message) compress(op *operation) error {
	var pool *compressionPool
	if m.isRequest {
		pool = op.server.reqCompression
	} else {
		pool = op.respCompression
	}
	if pool == nil {
		// identity compression, so nothing to do
		m.compressed = m.data
		m.data = nil
		return nil
	}

	m.compressed = op.bufferPool.Get()
	if err := pool.compress(m.compressed, m.data); err != nil {
		return err
	}
	op.bufferPool.Put(m.data)
	m.data = nil
	return nil
}

// decode will unmarshal data in m.data into m.msg. If
// saveBuffer is true, m.data will be unmodified on return;
// otherwise, the buffer will be released to op's bufferPool
// and the field set to nil.
//
// This method should not be called directly as the message's
// buffers could get out of sync with its stage. It should
// only be called from m.advanceToStage.
func (m *message) decode(op *operation, saveBuffer bool) error {
	switch {
	case m.isRequest && op.clientReqNeedsPrep:
		return op.clientPreparer.prepareUnmarshalledRequest(op, m.data.Bytes(), m.msg)
	case !m.isRequest && op.serverRespNeedsPrep:
		return op.serverPreparer.prepareUnmarshalledResponse(op, m.data.Bytes(), m.msg)
	}

	var codec Codec
	if m.isRequest {
		codec = op.client.codec
	} else {
		codec = op.server.codec
	}

	if err := codec.Unmarshal(m.data.Bytes(), m.msg); err != nil {
		return err
	}
	if !saveBuffer {
		op.bufferPool.Put(m.data)
		m.data = nil
	}
	return nil
}

// encode will marshal data in m.msg into m.data.
//
// This method should not be called directly as the message's
// buffers could get out of sync with its stage. It should
// only be called from m.advanceToStage.
func (m *message) encode(op *operation) error {
	buf := op.bufferPool.Get()
	var data []byte
	var err error

	switch {
	case m.isRequest && op.serverReqNeedsPrep:
		data, err = op.serverPreparer.prepareMarshalledRequest(op, buf.Bytes(), m.msg, op.request.Header)
	case !m.isRequest && op.clientRespNeedsPrep:
		data, err = op.clientPreparer.prepareMarshalledResponse(op, buf.Bytes(), m.msg, op.writer.Header())
	default:
		var codec Codec
		if m.isRequest {
			codec = op.server.codec
		} else {
			codec = op.client.codec
		}
		data, err = codec.MarshalAppend(buf.Bytes(), m.msg)
	}

	if err != nil {
		op.bufferPool.Put(buf)
		m.data = nil
		return err
	}
	m.data = op.bufferPool.Wrap(data, buf)
	return nil
}

func intersect(setA, setB []string) []string {
	length := len(setA)
	if len(setB) < length {
		length = len(setB)
	}
	if length == 0 {
		// If either set is empty, the intersection is empty.
		// We don't use nil since it is used in places as a sentinel.
		return make([]string, 0)
	}
	result := make([]string, 0, length)
	for _, item := range setA {
		for _, other := range setB {
			if other == item {
				result = append(result, item)
				break
			}
		}
	}
	return result
}

type errorFlusher interface {
	FlushError() error
}

type flusherNoError struct {
	f errorFlusher
}

func (f flusherNoError) Flush() {
	_ = f.f.FlushError()
}

func asFlusher(respWriter http.ResponseWriter) http.Flusher {
	// This is similar to how http.ResponseController.Flush works. But
	// we can't use that since it isn't available prior to Go 1.21.
	for {
		switch typedWriter := respWriter.(type) {
		case http.Flusher:
			return typedWriter
		case errorFlusher:
			return flusherNoError{f: typedWriter}
		case interface{ Unwrap() http.ResponseWriter }:
			respWriter = typedWriter.Unwrap()
		default:
			return nil
		}
	}
}

func bufferLimitError(limit int64) error {
	return sizeLimitError("max buffer size", limit)
}

func contentLengthError(limit int64) error {
	return sizeLimitError("content length", limit)
}

func sizeLimitError(what string, limit int64) error {
	return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("%s (%d) exceeded", what, limit))
}

func malformedRequestError(err error) error {
	// Adds 400 Bad Request / InvalidArgument status codes to error
	return connect.NewError(connect.CodeInvalidArgument, err)
}
