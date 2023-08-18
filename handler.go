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
	"net/url"
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
		muxConfig:     h.mux,
		writer:        writer,
		request:       request,
		contentType:   originalContentType,
		cancel:        cancel,
		buffers:       h.bufferPool,
		canDecompress: h.canDecompress,
	}
	op.client.protocol = clientProtoHandler
	if queryVars != nil {
		// memoize this, so we don't have to parse query string again later
		op.queryVars = queryVars
	}
	originalHeaders := request.Header.Clone()

	// Identify the method being invoked.
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
	if !op.client.protocol.acceptsStreamType(&op, op.streamType) {
		http.Error(
			writer,
			fmt.Sprintf("stream type %s not supported with %s protocol", op.streamType, op.client.protocol),
			http.StatusUnsupportedMediaType)
		return
	}
	if op.streamType == connect.StreamTypeBidi && request.ProtoMajor < 2 {
		http.Error(writer, "bidi streams require HTTP/2", http.StatusHTTPVersionNotSupported)
		return
	}

	// Identify the request encoding and compression.
	reqMeta, err := clientProtoHandler.extractProtocolRequestHeaders(&op, request.Header)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
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
	if newCodec := h.mux.codecImpls[reqMeta.codec]; newCodec == nil {
		http.Error(writer, fmt.Sprintf("%q sub-format not supported", reqMeta.codec), http.StatusUnsupportedMediaType)
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

	// Now we know enough to handle the request.
	if op.client.protocol.protocol() == op.server.protocol.protocol() &&
		op.client.codec.Name() == op.server.codec.Name() &&
		(cannotDecompressRequest || op.client.reqCompression.Name() == op.server.reqCompression.Name()) {
		// No transformation needed. But we do  need to restore the original headers first
		// since extracting request metadata may have removed keys.
		request.Header = originalHeaders
		methodConf.handler.ServeHTTP(writer, request)
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
			// TODO: if the service is known, but the method is not, we should send to the client
			//       a proper RPC error (encoded per protocol handler) with an Unimplemented code.
			return nil, &httpError{code: http.StatusNotFound}
		}
		if op.request.Method != http.MethodPost {
			mayAllowGet, ok := op.client.protocol.(clientProtocolAllowsGet)
			allowsGet := ok && mayAllowGet.allowsGetRequests(methodConf)
			if !allowsGet {
				return nil, &httpError{
					code: http.StatusMethodNotAllowed,
					headers: func(hdrs http.Header) {
						hdrs.Set("Allow", http.MethodPost)
					},
				}
			}
			if allowsGet && op.request.Method != http.MethodGet {
				return nil, &httpError{
					code: http.StatusMethodNotAllowed,
					headers: func(hdrs http.Header) {
						hdrs.Set("Allow", http.MethodGet+","+http.MethodPost)
					},
				}
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
		vals := req.URL.Query()
		if vals.Get("connect") == "v1" {
			if req.Method == http.MethodGet {
				return connectUnaryGetClientProtocol{}, "", nil
			}
			return nil, "", nil
		}
		return restClientProtocol{}, "", vals
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
		// REST usually uses application/json, but use of google.api.HttpBody means it could
		// also use *any* content-type.
		fallthrough
	default:
		return restClientProtocol{}, contentType, nil
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
	queryVars     url.Values
	contentType   string // original content-type in incoming request headers
	reqMeta       requestMeta
	cancel        context.CancelFunc
	buffers       *bufferPool
	delegate      http.Handler
	resolver      TypeResolver
	canDecompress []string

	method     protoreflect.MethodDescriptor
	methodPath string
	streamType connect.StreamType

	requestMessage  proto.Message // request message, can be nil
	responseMessage proto.Message // response message, can be nil

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
		codec: op.client.codec,
		comp:  op.client.reqCompression,
	}

	if mustDecodeRequest {
		// Need the message type to decode
		messageType, err := op.resolver.FindMessageByName(op.method.Input().FullName())
		if err != nil {
			op.earlyError(err)
			return
		}
		op.requestMessage = messageType.New().Interface()
	}

	var skipBody bool
	if serverRequestBuilder != nil { //nolint:nestif
		if requireMessageForRequestLine {
			if err := op.readAndDecodeRequestMessage(op.request.Body, &reqMsg); err != nil {
				op.earlyError(err)
				return
			}
		}
		var hasBody bool
		var err error
		op.request.URL.Path, op.request.URL.RawQuery, op.request.Method, hasBody, err =
			serverRequestBuilder.requestLine(op, op.requestMessage)
		if err != nil {
			op.earlyError(err)
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
		if sameRequestCompression && sameRequestCodec && !mustDecodeRequest {
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
		op.earlyError(err)
	}
	defer rw.close()
	op.writer = rw
	op.delegate.ServeHTTP(op.writer, op.request)
}

// earlyError handles an error that occurs while setting up the operation. It should not be used
// once the underlying server handler has been invoked. For those errors, responseWriter.reportError
// must be used instead.
func (op *operation) earlyError(_ error) {
	// TODO: determine status code from error
	// TODO: if a *connect.Error, use protocol handler to write RPC response
	http.Error(op.writer, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
}

func (op *operation) readRequestMessage(dst *bytes.Buffer, reader io.Reader) (isCompressed bool, err error) {
	msgLen := -1
	isCompressed = op.client.reqCompression != nil
	if op.clientEnveloper != nil {
		var envBuf [5]byte
		_, err := io.ReadFull(reader, envBuf[:])
		if err != nil {
			return false, err
		}
		env, err := op.clientEnveloper.decodeEnvelope(envBuf)
		if err != nil {
			return false, err
		}
		if env.trailer {
			return false, fmt.Errorf("client stream cannot include status/trailer message")
		}
		msgLen, isCompressed = int(env.length), env.compressed
	}

	if msgLen == -1 {
		// TODO: apply some limit to request message size to avoid unlimited memory use
		_, err = io.Copy(dst, reader)
	} else {
		_, err = io.CopyN(dst, reader, int64(msgLen))
	}
	if err != nil {
		return false, err
	}
	return isCompressed, nil
}

func (op *operation) readAndDecodeRequestMessage(r io.Reader, msg *message) error {
	buf := msg.reset(op.buffers)
	isCompressed, err := op.readRequestMessage(buf, r)
	if err != nil {
		return err
	}
	if isCompressed {
		msg.comp = op.client.reqCompression
	}
	msg.codec = op.client.codec

	if op.clientReqNeedsPrep {
		// Uncompress the data, but hand over decoding to the client preparer.
		if err := msg.convert(op.buffers, nil, op.client.codec, op.requestMessage, false); err != nil {
			return err
		}
		if err := op.clientPreparer.prepareUnmarshalledRequest(op, buf.Bytes(), op.requestMessage); err != nil {
			return err
		}
		msg.codec = nil // Force re-encoding
	}
	// Convert the message to the server's encoding.
	return msg.convert(op.buffers, op.server.reqCompression, op.server.codec, op.requestMessage, false)
}

func (op *operation) serverBody(msg *message) io.ReadCloser {
	if msg == nil {
		// no need to decompress or decode; just transforming envelopes
		return &envelopingReader{op: op, r: op.request.Body}
	}
	return &transformingReader{op: op, msg: msg, r: op.request.Body}
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
	buf := op.buffers.Get()
	defer op.buffers.Put(buf)
	b := buf.Bytes()[0:buf.Cap()]
	_, _ = io.CopyBuffer(io.Discard, body, b)
}

// envelopingReader will translate between envelope styles as data is read.
// It does not do any decompressing or deserializing of data.
type envelopingReader struct {
	op *operation
	r  io.ReadCloser
}

func (er envelopingReader) Read(data []byte) (n int, err error) {
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

	buf *bytes.Buffer // buffer for writing transformed data
}

func (tr *transformingReader) Read(data []byte) (n int, err error) {
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
	if rw.respMeta.compression == CompressionIdentity {
		rw.respMeta.compression = "" // normalize to empty string
	}
	rw.respMeta = &respMeta
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

	if respMeta.end != nil {
		// RPC failed immediately.
		if processBody != nil {
			// We have to wait until we receive the body in order to process the error.
			rw.w = &errorWriter{
				rw:          rw,
				respMeta:    rw.respMeta,
				processBody: processBody,
				buffer:      rw.op.buffers.Get(),
			}
			return
		}
		// We can send back error response immediately.
		rw.flushHeaders()
		return
	}

	sameCodec := rw.op.client.codec.Name() == rw.op.server.codec.Name()
	// even if body encoding uses same content type, we can't treat them as the same
	// (which means re-using encoded data) if either side needs to prep the data first
	sameResponseCodec := sameCodec && !rw.op.clientRespNeedsPrep && !rw.op.serverRespNeedsPrep

	respMsg := message{
		codec: rw.op.server.codec,
		comp:  rw.op.respCompression,
	}
	if !sameResponseCodec {
		// We will have to decode and re-encode, so we need the message type.
		messageType, err := rw.op.resolver.FindMessageByName(rw.op.method.Output().FullName())
		if err != nil {
			rw.reportError(err)
			return
		}
		rw.op.responseMessage = messageType.New().Interface()
	}

	rw.respMeta = &respMeta
	var endMustBeInHeaders bool
	if mustBe, ok := rw.op.client.protocol.(clientProtocolEndMustBeInHeaders); ok {
		endMustBeInHeaders = mustBe.endMustBeInHeaders()
	}
	if !endMustBeInHeaders {
		// We can go ahead and flush headers now. Otherwise, we'll wait until we've verified we
		// can handle the response data, so we still have an opportunity to send back an error.
		rw.flushHeaders()
	}

	// Now we can define the transformed response body.
	if sameResponseCodec {
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
		trailers := rw.op.client.protocol.encodeEnd(rw.op.client.codec, end, rw.delegate, false)
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
	statusCode := rw.op.client.protocol.addProtocolResponseHeaders(*rw.respMeta, rw.Header(), allowedRequestCompression)
	rw.delegate.WriteHeader(statusCode)
	if rw.respMeta.end != nil {
		// response is done
		rw.op.client.protocol.encodeEnd(rw.op.client.codec, rw.respMeta.end, rw.delegate, true)
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

func (ew envelopingWriter) Write(data []byte) (int, error) {
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

func (tw *transformingWriter) Write(data []byte) (int, error) {
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
	processBody responseEndUnmarshaler
	buffer      *bytes.Buffer
}

func (ew *errorWriter) Write(data []byte) (int, error) {
	if ew.buffer == nil {
		return 0, errors.New("writer already closed")
	}
	// TODO: limit on size of the error body and how much we'll buffer?
	return ew.buffer.Write(data)
}

func (ew *errorWriter) Close() error {
	if ew.respMeta.end == nil {
		ew.respMeta.end = &responseEnd{}
	}
	buffers := ew.rw.op.buffers
	defer buffers.Put(ew.buffer)
	body := ew.buffer
	if compressPool := ew.rw.op.respCompression; compressPool != nil {
		uncompressed := buffers.Get()
		defer buffers.Put(uncompressed)
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

// message is a buffered message of compressed and encoded data.
type message struct {
	buf   *bytes.Buffer // nil if message is empty
	comp  compressor    // nil if message isn't compressed
	codec Codec         // nil if message isn't encoded
	off   int           // offset into buf, for non destructive reading
}

func (m *message) Read(p []byte) (n int, err error) {
	if m.buf == nil || m.off >= m.buf.Len() {
		return 0, io.EOF
	}
	n = copy(p, m.buf.Bytes()[m.off:])
	m.off += n
	return n, nil
}

func (m *message) reset(buffers *bufferPool) *bytes.Buffer {
	if m.buf == nil {
		m.buf = buffers.Get()
	}
	m.buf.Reset()
	m.comp = nil
	m.codec = nil
	m.off = 0
	return m.buf
}

// encode the message into the buffer, compressing and encoding as needed.
func (m *message) encode(buffers *bufferPool, comp compressor, codec Codec, msg proto.Message) error {
	m.codec = nil
	m.comp = nil
	return m.convert(buffers, comp, codec, msg, false)
}

// convert the message in the buffer to the new compression and encoding.
// If mustDecode is true the buffer will be unmarshalled into the message,
// otherwise it will only be used if required to convert the encoding.
func (m *message) convert(buffers *bufferPool, comp compressor, codec Codec, msg proto.Message, mustDecode bool) error {
	if m.buf == nil {
		m.buf = buffers.Get()
	}
	var tmp *bytes.Buffer
	defer func() {
		if tmp != nil {
			buffers.Put(tmp)
		}
	}()
	needsRecoding := getName(m.codec) != getName(codec)
	needsRecompressing := getName(m.comp) != getName(comp) || needsRecoding
	isCompressedSrc := mustDecode && !needsRecompressing && m.comp != nil
	if m.comp != nil && (needsRecompressing || mustDecode) {
		// Decompress
		tmp = buffers.Get()
		// Read from m, don't mutate m.buf
		if err := m.comp.decompress(tmp, m); err != nil {
			return err
		}
		m.off = 0 // reset offset of message
		m.buf, tmp = tmp, m.buf
		m.comp = nil
	}
	if m.codec != nil && (needsRecoding || mustDecode) {
		// Decode
		if err := m.codec.Unmarshal(m.buf.Bytes(), msg); err != nil {
			return err
		}
	}
	if codec != nil && needsRecoding {
		// Encode
		m.buf.Reset()
		if err := marshal(m.buf, msg, codec); err != nil {
			return err
		}
		m.codec = codec
	}
	if comp != nil && needsRecompressing {
		// Compress
		if tmp == nil {
			tmp = buffers.Get()
		} else {
			tmp.Reset()
		}
		if err := comp.compress(tmp, m.buf); err != nil {
			return err
		}
		m.buf, tmp = tmp, m.buf
		m.comp = comp
	} else if isCompressedSrc {
		// Source buffer is compressed with the same compression
		// algorithm as the destination. We had to decode to get
		// the message, but now we can just swap the buffers back.
		m.buf, tmp = tmp, m.buf
	}
	return nil
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

func getName(thing interface{ Name() string }) string {
	if thing == nil {
		return ""
	}
	return thing.Name()
}
