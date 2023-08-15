// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

//nolint:forbidigo,unused,revive,gocritic // this is temporary, will be removed when implementation is complete
package vanguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type handler struct {
	mux           *Mux
	bufferPool    *bufferPool
	codecs        map[codecKey]Codec
	canDecompress []string
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	clientProtoHandler, originalContentType := classifyRequest(request)
	if clientProtoHandler == nil {
		http.Error(writer, "could not classify protocol", http.StatusUnsupportedMediaType)
		return
	}
	reqMeta, err := clientProtoHandler.extractProtocolRequestHeaders(request.Header)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	request = request.WithContext(ctx)
	op := operation{
		muxConfig:     h.mux,
		writer:        writer,
		request:       request,
		contentType:   originalContentType,
		reqMeta:       reqMeta,
		cancel:        cancel,
		bufferPool:    h.bufferPool,
		canDecompress: h.canDecompress,
	}
	op.client.protocol = clientProtoHandler

	var cannotDecompressRequest bool
	if reqMeta.compression != "" {
		var ok bool
		op.client.reqCompression, ok = h.mux.compressionPools[reqMeta.compression]
		if !ok {
			// This might be okay, like if the transformation doesn't require decoding.
			op.client.reqCompression = nil
			cannotDecompressRequest = true
		}
	}

	if newCodec := h.mux.codecImpls[reqMeta.codec]; newCodec == nil {
		http.Error(writer, fmt.Sprintf("%q sub-format not supported", reqMeta.codec), http.StatusUnsupportedMediaType)
		return
	}
	methodConf, httpErr := h.findMethod(&op)
	if httpErr != nil {
		if httpErr.headers != nil {
			httpErr.headers(writer.Header())
		}
		http.Error(writer, http.StatusText(httpErr.code), httpErr.code)
		return
	}
	op.method = methodConf.descriptor
	op.methodPath = methodConf.methodPath
	op.delegate = methodConf.handler
	op.resolver = methodConf.resolver
	switch {
	case op.method.IsStreamingClient() && op.method.IsStreamingServer():
		op.streamType = connect.StreamTypeBidi
	case op.method.IsStreamingClient():
		op.streamType = connect.StreamTypeClient
	case op.method.IsStreamingServer():
		op.streamType = connect.StreamTypeServer
	default:
		op.streamType = connect.StreamTypeUnary
	}
	if op.client.protocol.acceptsStreamType(op.streamType) {
		http.Error(
			writer,
			fmt.Sprintf("stream type %s not supported with %s protocol", op.streamType, op.client.protocol),
			http.StatusNotImplemented)
		return
	}
	if op.streamType == connect.StreamTypeBidi && request.ProtoMajor < 2 {
		http.Error(writer, "bidi streams require HTTP/2", http.StatusHTTPVersionNotSupported)
		return
	}

	op.client.codec = h.codecs[codecKey{res: methodConf.resolver, name: reqMeta.codec}]

	// Now we can determine the destination protocol details
	if _, supportsProtocol := methodConf.protocols[clientProtoHandler.protocol()]; supportsProtocol {
		op.server.protocol = clientProtoHandler.protocol().serverHandler(&op)
	} else {
		for protocol := protocolMin; protocol <= protocolMax; protocol++ {
			if _, supportsProtocol := methodConf.protocols[protocol]; supportsProtocol {
				op.server.protocol = protocol.serverHandler(&op)
				break
			}
		}
	}

	if op.server.protocol.protocol() == ProtocolREST {
		// REST always uses JSON.
		// TODO: allow non-JSON encodings with REST? Would require registering content-types with codecs.
		//
		// NB: This is fine to set even if a custom content-type is used via
		//     the use of google.api.HttpBody. The actual content-type and body
		//     data will be written via serverBodyPreparer implementation.
		op.server.codec = h.mux.codecImpls[CodecJSON](methodConf.resolver)
	} else if _, supportsCodec := methodConf.codecNames[reqMeta.codec]; supportsCodec {
		op.server.codec = op.client.codec
	} else {
		op.server.codec = h.mux.codecImpls[CodecProto](methodConf.resolver)
	}

	if reqMeta.compression != "" && !cannotDecompressRequest {
		if _, supportsCompression := methodConf.compressorNames[reqMeta.compression]; supportsCompression {
			op.server.reqCompression = op.client.reqCompression
		} // else: no compression
	}

	if op.client.protocol.protocol() == op.server.protocol.protocol() &&
		op.client.codec.Name() == op.server.codec.Name() &&
		(cannotDecompressRequest || op.client.reqCompression.Name() == op.server.reqCompression.Name()) {
		// No transformation needed.
		methodConf.handler.ServeHTTP(writer, request)
		return
	}

	if cannotDecompressRequest {
		// At this point, we have to perform some transformation, so we'll need to
		// be able to decompress/compress.
		// TODO: should result in 415 Unsupported Media Type
		// TODO: should send back accept-encoding (or relevant protocol header)
		op.handleError(errors.New("unable to decode"))
		return
	}

	op.handle()
}

func (h *handler) findMethod(op *operation) (*methodConfig, *httpError) {
	uriPath := op.request.URL.Path
	switch op.client.protocol.protocol() {
	case ProtocolREST:
		var methods routeMethods
		op.restTarget, op.restVars, methods = h.mux.restRoutes.match(uriPath, op.request.Method)
		if op.restTarget != nil {
			return op.restTarget.config, nil
		}
		if len(methods) == 0 {
			return nil, &httpError{code: http.StatusNotFound}
		}
		var sb strings.Builder
		for method := range methods {
			if sb.Len() > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(method)
		}
		return nil, &httpError{
			code: http.StatusMethodNotAllowed,
			headers: func(hdrs http.Header) {
				hdrs.Set("Allow", sb.String())
			},
		}
	default:
		// The other protocols just use the URI path as the method name and don't allow query params
		if len(uriPath) == 0 || uriPath[0] != '/' {
			// no starting slash? won't match any known route
			return nil, &httpError{code: http.StatusNotFound}
		}
		methodConf := h.mux.methods[uriPath[1:]]
		if methodConf == nil {
			return nil, &httpError{code: http.StatusNotFound}
		}
		_, allowGet := op.client.protocol.(clientProtocolAllowsGet)
		if allowGet && op.request.Method != http.MethodPost && op.request.Method != http.MethodGet {
			return nil, &httpError{
				code: http.StatusMethodNotAllowed,
				headers: func(hdrs http.Header) {
					hdrs.Set("Allow", http.MethodGet+","+http.MethodPost)
				},
			}
		} else if !allowGet && op.request.Method != http.MethodPost {
			return nil, &httpError{
				code: http.StatusMethodNotAllowed,
				headers: func(hdrs http.Header) {
					hdrs.Set("Allow", http.MethodPost)
				},
			}
		}
		return methodConf, nil
	}
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

func classifyRequest(req *http.Request) (h clientProtocolHandler, contentType string) {
	contentTypes := req.Header["Content-Type"]

	if len(contentTypes) == 0 {
		// Empty bodies should still have content types. So this should only
		// happen for requests with NO body at all. That's only allowed for
		// REST calls and Connect GET calls.
		connectVersion := req.Header["Connect-Protocol-Version"]
		if len(connectVersion) == 1 && connectVersion[0] == "1" {
			if req.Method == http.MethodGet {
				return connectUnaryGetClientProtocol{}, ""
			}
			return nil, ""
		}
		return restClientProtocol{}, ""
	}

	if len(contentTypes) > 1 {
		return nil, "" // Ick. Don't allow this.
	}
	contentType = contentTypes[0]
	switch {
	case strings.HasPrefix(contentType, "application/connect+"):
		return connectStreamClientProtocol{}, contentType
	case contentType == "application/grpc" || strings.HasPrefix(contentType, "application/grpc+"):
		return grpcClientProtocol{}, contentType
	case contentType == "application/grpc-web" || strings.HasPrefix(contentType, "application/grpc-web+"):
		return grpcWebClientProtocol{}, contentType
	case strings.HasPrefix(contentType, "application/"):
		connectVersion := req.Header["Connect-Protocol-Version"]
		if len(connectVersion) == 1 && connectVersion[0] == "1" {
			if req.Method == http.MethodGet {
				return connectUnaryGetClientProtocol{}, contentType
			}
			return connectUnaryPostClientProtocol{}, contentType
		}
		// REST usually uses application/json, but use of google.api.HttpBody means it could
		// also use *any* content-type.
		fallthrough
	default:
		return restClientProtocol{}, contentType
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

type httpError struct {
	code    int
	headers func(header http.Header)
}

// operation represents a single HTTP operation, which maps to an incoming HTTP request.
// It tracks properties needed to implement protocol transformation.
type operation struct {
	muxConfig     *Mux
	writer        http.ResponseWriter
	request       *http.Request
	contentType   string // original content-type in incoming request headers
	reqMeta       requestMeta
	cancel        context.CancelFunc
	bufferPool    *bufferPool
	delegate      http.Handler
	resolver      TypeResolver
	canDecompress []string

	method     protoreflect.MethodDescriptor
	methodPath string
	streamType connect.StreamType

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
	serverEnveloper     envelopedProtocolHandler
	serverPreparer      serverBodyPreparer
	serverReqNeedsPrep  bool
	serverRespNeedsPrep bool
}

func (op *operation) handle() {
	op.clientEnveloper, _ = op.client.protocol.(envelopedProtocolHandler)
	op.clientPreparer, _ = op.client.protocol.(clientBodyPreparer)
	if op.clientPreparer != nil {
		op.clientReqNeedsPrep = op.clientPreparer.requestNeedsPrep(op)
		op.clientRespNeedsPrep = op.clientPreparer.responseNeedsPrep(op)
	}
	op.serverEnveloper, _ = op.server.protocol.(envelopedProtocolHandler)
	op.serverPreparer, _ = op.server.protocol.(serverBodyPreparer)
	if op.serverPreparer != nil {
		op.serverReqNeedsPrep = op.serverPreparer.requestNeedsPrep(op)
		op.serverRespNeedsPrep = op.serverPreparer.responseNeedsPrep(op)
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
		saveCompressed: sameRequestCodec && sameRequestCompression,
		saveData:       sameRequestCodec && !sameRequestCompression,
	}

	if mustDecodeRequest {
		// Need the message type to decode
		messageType, err := op.resolver.FindMessageByName(op.method.Input().FullName())
		if err != nil {
			op.handleError(err)
			return
		}
		reqMsg.msg = messageType.New().Interface()
	}

	var skipBody bool
	if serverRequestBuilder != nil { //nolint:nestif
		if requireMessageForRequestLine {
			if err := op.readAndDecodeRequestMessage(&reqMsg); err != nil {
				op.handleError(err)
				return
			}
		}
		var hasBody bool
		var err error
		op.request.URL.Path, op.request.URL.RawQuery, op.request.Method, hasBody, err =
			serverRequestBuilder.requestLine(op, reqMsg.msg)
		if err != nil {
			op.handleError(err)
			return
		}
		skipBody = !hasBody
	} else {
		// if no request line builder, use simple request layout
		op.request.URL.Path = op.methodPath
		op.request.URL.RawQuery = ""
		op.request.Method = http.MethodPost
	}
	op.request.URL.ForceQuery = false
	allowedResponseCompression := intersect(op.reqMeta.acceptCompression, op.canDecompress)
	op.server.protocol.addProtocolRequestHeaders(op.reqMeta, op.writer.Header(), allowedResponseCompression)

	// Now we can define the transformed request body.
	if skipBody {
		// drain any contents of body so downstream handler sees empty
		op.drainBody(op.request.Body)
	} else {
		if reqMsg.saveCompressed && reqMsg.msg == nil {
			// we do not need to decompress or decode
			op.request.Body = op.serverBody(nil)
		} else {
			op.request.Body = op.serverBody(&reqMsg)
		}
	}

	// Finally, define the transforming response writer (which
	// must delay most logic until it sees WriteHeader).
	rw, err := op.serverWriter()
	if err != nil {
		op.handleError(err)
	}
	defer rw.close()
	op.writer = rw
	op.delegate.ServeHTTP(op.writer, op.request)
}

// handleError handles an error that occurs while setting up the operation. It should not be used
// once the underlying server handler has been invoked. For those errors, responseWriter.reportError
// must be used instead.
func (op *operation) handleError(_ error) {
	// TODO: determine status code from error
	http.Error(op.writer, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
}

func (op *operation) readRequestMessage(msg *message) error {
	msg.reset(op.bufferPool)
	// TODO: buffer and read one message, just compressed bytes
	if op.clientReqNeedsPrep {
		// TODO: decode it and prepare it, merging with content
		// from URI path and query params
		_ = op
	}
	return nil
}

func (op *operation) readAndDecodeRequestMessage(msg *message) error {
	if err := op.readRequestMessage(msg); err != nil {
		return err
	}
	if msg.msgAvailable {
		// no more work to decompress/decode; readRequestMessage provided a decoded message
		return nil
	}
	decompressor := op.client.reqCompression.getDecompressor()

	if err := decompress(decompressor, op.bufferPool, msg); err != nil {
		return err
	}
	codec := op.client.codec
	return decode(codec, op.bufferPool, msg)
}

func (op *operation) serverBody(msg *message) io.ReadCloser {
	if msg == nil {
		// no need to decompress or decode; just transforming envelopes
		return &envelopingReader{op: op, r: op.request.Body}
	}
	ret := &transformingReader{op: op, msg: msg, r: op.request.Body}
	if !msg.isZero() {
		// TODO: we have the first message already; setup ret to use it
		_ = msg.msg
	}
	return ret
}

func (op *operation) serverWriter() (*responseWriter, error) {
	if _, ok := op.writer.(http.Flusher); !ok {
		return nil, errors.New("http.ResponseWriter must implement http.Flusher")
	}
	return &responseWriter{op: op, delegate: op.writer}, nil
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
	op *operation
	r  io.ReadCloser
}

func (er envelopingReader) Read(p []byte) (n int, err error) {
	//TODO implement me
	panic("implement me")
}

func (er envelopingReader) Close() error {
	//TODO implement me
	panic("implement me")
}

// transformingReader transforms the data from the original request
// into a new protocol form as the data is read. It must decompress
// and deserialize each message and then re-serialize (and optionally
// recompress) each message.
type transformingReader struct {
	op  *operation
	msg *message
	r   io.ReadCloser
}

func (tr *transformingReader) Read(p []byte) (n int, err error) {
	//TODO implement me
	panic("implement me")
}

func (tr *transformingReader) Close() error {
	//TODO implement me
	panic("implement me")
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
	code     int
	// has WriteHeader or first call to Write occurred?
	headersWritten bool
	// have headers actually been flushed to delegate?
	headersFlushed bool
	respMeta       *responseMeta
	err            error
	// wraps op.writer; initialized after headers are written
	w io.WriteCloser
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
	rw.code = statusCode
	respMeta, processBody, err := rw.op.server.protocol.extractProtocolResponseHeaders(statusCode, rw.Header())
	if err != nil {
		rw.reportError(err)
		return
	}
	rw.respMeta = &respMeta
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
		return
	}

	if respMeta.compression != "" {
		var ok bool
		rw.op.respCompression, ok = rw.op.muxConfig.compressionPools[respMeta.compression]
		if !ok {
			rw.reportError(fmt.Errorf("response indicates unsupported compression encoding %q", respMeta.compression))
			return
		}
	}
	if respMeta.codec != "" && respMeta.codec != rw.op.server.codec.Name() {
		// unexpected content-type for reply
		rw.reportError(fmt.Errorf("response uses incorrect codec: expecting %q but instead got %q", rw.op.server.codec.Name(), respMeta.codec))
		return
	}

	sameCodec := rw.op.client.codec.Name() == rw.op.server.codec.Name()
	// even if body encoding uses same content type, we can't treat them as the same
	// (which means re-using encoded data) if either side needs to prep the data first
	sameResponseCodec := sameCodec && !rw.op.clientRespNeedsPrep && !rw.op.serverRespNeedsPrep

	respMsg := message{saveCompressed: sameResponseCodec}

	if !sameResponseCodec {
		// We will have to decode and re-encode, so we need the message type.
		messageType, err := rw.op.resolver.FindMessageByName(rw.op.method.Output().FullName())
		if err != nil {
			rw.reportError(err)
			return
		}
		respMsg.msg = messageType.New().Interface()
	}

	rw.respMeta = &respMeta
	var endMustBeInHeaders bool
	if mustBe, ok := rw.op.server.protocol.(serverProtocolEndMustBeInHeaders); ok {
		endMustBeInHeaders = mustBe.endMustBeInHeaders()
	}
	if !endMustBeInHeaders {
		// We can go ahead and flush headers now. Otherwise, we'll wait until we've verified we
		// can handle the response data, so we still have an opportunity to send back an error.
		rw.flushHeaders()
	}

	// Now we can define the transformed response body.
	if respMsg.saveCompressed && respMsg.msg == nil {
		// we do not need to decompress or decode
		rw.w = &envelopingWriter{op: rw.op, w: rw.delegate}
	} else {
		rw.w = &transformingWriter{op: rw.op, msg: &respMsg, w: rw.delegate}
	}
}

func (rw *responseWriter) Flush() {
	// We expose this method so server can call it and won't panic
	// or blow-up when doing type conversion. But it's a no-op
	// since we automatically flush at message boundaries when
	// transforming the response body.
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
	switch {
	case rw.headersFlushed:
		// write error to body or trailers
		trailers := rw.op.client.protocol.encodeEnd(rw.op.client.codec, end, rw.delegate)
		if len(trailers) > 0 {
			hdrs := rw.Header()
			for k, v := range trailers {
				if !strings.HasPrefix(k, http.TrailerPrefix) {
					k = http.TrailerPrefix + k
				}
				hdrs[k] = v
			}
		}
	case rw.respMeta != nil:
		rw.respMeta.end = end
		rw.flushHeaders()
	default:
		rw.respMeta = &responseMeta{end: end}
		rw.flushHeaders()
	}
	// response is done
	rw.op.cancel()
	rw.err = context.Canceled
}

func (rw *responseWriter) flushHeaders() {
	if rw.headersFlushed {
		return // already flushed
	}
	allowedRequestCompression := intersect(rw.respMeta.acceptCompression, rw.op.canDecompress)
	rw.op.client.protocol.addProtocolResponseHeaders(*rw.respMeta, rw.Header(), allowedRequestCompression)
	if rw.respMeta.end != nil {
		// response is done
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
	rw.flushHeaders()
}

// envelopingWriter will translate between envelope styles as data is
// written. It does not do any decompressing or deserializing of data.
type envelopingWriter struct {
	op *operation
	w  io.Writer
}

func (ew envelopingWriter) Write(i []byte) (int, error) {
	//TODO implement me
	panic("implement me")
}

func (ew envelopingWriter) Close() error {
	//TODO implement me
	panic("implement me")
}

// transformingWriter transforms the data from the original response
// into a new protocol form as the data is written. It must decompress
// and deserialize each message and then re-serialize (and optionally
// recompress) each message.
type transformingWriter struct {
	op  *operation
	msg *message
	w   io.Writer
}

func (tw *transformingWriter) Write(i []byte) (int, error) {
	//TODO implement me
	panic("implement me")
}

func (tw *transformingWriter) Close() error {
	//TODO implement me
	panic("implement me")
}

type errorWriter struct {
	rw          *responseWriter
	respMeta    *responseMeta
	processBody func(io.Reader, *responseEnd)
	buffer      *bytes.Buffer
}

func (ew *errorWriter) Write(data []byte) (int, error) {
	// TODO: limit on size of the error body and how much we'll buffer?
	return ew.buffer.Write(data)
}

func (ew *errorWriter) Close() error {
	if ew.respMeta.end == nil {
		ew.respMeta.end = &responseEnd{}
	}
	ew.processBody(ew.buffer, ew.respMeta.end)
	ew.rw.flushHeaders()
	return nil
}

// message represents a single message in an RPC stream. It can be re-used in a stream,
// so we only allocate one and then re-use it for subsequent messages (if stream has
// more than one).
type message struct {
	// flags indicating if compressed and data should be preserved after use
	saveCompressed, saveData bool

	// compressed is the compressed bytes; may be nil if the contents have
	// already been decompressed into the data field.
	compressed *bytes.Buffer
	// data is the serialized but uncompressed bytes; may be nil if the
	// contents have not yet been decompressed or have been de-serialized
	// into the msg field.
	data *bytes.Buffer
	// msg is the plain message
	msg proto.Message
	// if true, msg has been decoded and can be used. If false, msg is
	// an empty value and data must first be decoded into it.
	msgAvailable bool
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
func (m *message) reset(pool *bufferPool) {
	m.msg = nil
	if m.data != nil && m.compressed == nil {
		m.compressed = m.data
		m.compressed.Reset()
		m.data = nil
		return
	}
	if m.data != nil {
		if m.data != m.compressed {
			pool.Put(m.data)
		}
		m.data = nil
	}
	if m.compressed == nil {
		m.compressed = pool.Get()
	}
}

func (m *message) isZero() bool {
	return m.compressed == nil && m.data == nil && m.msg == nil
}

func decompress(decompressor connect.Decompressor, pool *bufferPool, msg *message) error {
	if msg.data != nil {
		return nil // no-op
	}
	if msg.compressed == nil {
		return errors.New("no compressed source")
	}
	if decompressor == nil {
		// no actual compression
		msg.data = msg.compressed
		if !msg.saveCompressed {
			msg.compressed = nil
		}
		return nil
	}
	var src io.Reader
	if msg.saveCompressed {
		// we allocate a new reader, but it's cheaper than re-compressing later
		src = bytes.NewReader(msg.compressed.Bytes())
	} else {
		src = msg.compressed
	}
	msg.data = pool.Get()
	if err := decompressor.Reset(src); err != nil {
		return err
	}
	if _, err := msg.data.ReadFrom(decompressor); err != nil {
		return err
	}
	if !msg.saveCompressed {
		pool.Put(msg.compressed)
		msg.compressed = nil
	}
	return decompressor.Close()
}

func compress(compressor connect.Compressor, pool *bufferPool, msg *message) error {
	if msg.compressed != nil {
		return nil // no-op
	}
	if msg.data == nil {
		return errors.New("no uncompressed source")
	}
	if compressor == nil {
		// no actual compression
		msg.compressed = msg.data
		if !msg.saveData {
			msg.data = nil
		}
		return nil
	}
	msg.compressed = pool.Get()
	compressor.Reset(msg.compressed)
	var err error
	if msg.saveData {
		_, err = compressor.Write(msg.data.Bytes())
	} else {
		_, err = msg.data.WriteTo(compressor)
	}
	if err != nil {
		return err
	}
	if !msg.saveData {
		pool.Put(msg.data)
		msg.data = nil
	}
	return compressor.Close()
}

func decode(codec Codec, pool *bufferPool, msg *message) error {
	if msg.msgAvailable {
		return nil // no-op
	}
	if msg.data == nil {
		return errors.New("no uncompressed source")
	}
	if err := codec.Unmarshal(msg.data.Bytes(), msg.msg); err != nil {
		return err
	}
	if !msg.saveData {
		pool.Put(msg.data)
		msg.data = nil
	}
	msg.msgAvailable = true
	return nil
}

func encode(codec Codec, pool *bufferPool, msg *message) error {
	if msg.data != nil {
		return nil // no-op
	}
	if !msg.msgAvailable {
		return errors.New("no decoded message source")
	}
	buf := pool.Get().Bytes()
	buf, err := codec.MarshalAppend(buf, msg.msg)
	msg.data = bytes.NewBuffer(buf)
	return err
}

func intersect(setA, setB []string) []string {
	length := len(setA)
	if len(setB) < length {
		length = len(setB)
	}
	if length == 0 {
		// if either set is empty, the intersection is empty
		return nil
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
