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
	clientProtoHandler := classifyRequest(request)
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
		writer:        writer,
		request:       request,
		reqMeta:       reqMeta,
		cancel:        cancel,
		bufferPool:    h.bufferPool,
		canDecompress: h.canDecompress,
	}
	op.client.protocol = clientProtoHandler

	if reqMeta.compression != "" {
		var ok bool
		op.client.reqCompression, ok = h.mux.compressionPools[reqMeta.compression]
		if !ok {
			// This might be okay, like if the transformation doesn't require decoding.
			op.client.reqCompression = nil
			op.cannotDecompressRequest = true
		}
	}
	newCodec := h.mux.codecImpls[reqMeta.codec]
	if newCodec == nil {
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

	op.client.codec = newCodec(methodConf.resolver)

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
		// REST always uses JSON
		op.server.codec = h.mux.codecImpls[CodecJSON](methodConf.resolver)
	} else if _, supportsCodec := methodConf.codecNames[reqMeta.codec]; supportsCodec {
		op.server.codec = op.client.codec
	} else {
		op.server.codec = h.mux.codecImpls[CodecProto](methodConf.resolver)
	}

	if reqMeta.compression != "" && !op.cannotDecompressRequest {
		if _, supportsCompression := methodConf.compressorNames[reqMeta.compression]; supportsCompression {
			op.server.reqCompression = op.client.reqCompression
		} // else: no compression
	}

	if op.client.protocol.protocol() == op.server.protocol.protocol() &&
		op.client.codec.Name() == op.server.codec.Name() &&
		(op.cannotDecompressRequest || op.client.reqCompression.Name() == op.server.reqCompression.Name()) {
		// No transformation needed.
		methodConf.handler.ServeHTTP(writer, request)
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
		allowGet := op.client.protocol.allowsGetRequests()
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

type encodingDetails struct {
	codec           Codec
	reqCompression  *compressionPool
	respCompression *compressionPool
}

type protocolDetails[H any] struct {
	protocol H
	encodingDetails
}

func classifyRequest(req *http.Request) (h clientProtocolHandler) {
	contentTypes := req.Header["Content-Type"]

	if len(contentTypes) == 0 {
		// Empty bodies should still have content types. So this should only
		// happen for requests with NO body at all. That's only allowed for
		// REST calls and Connect GET calls.
		connectVersion := req.Header["Connect-Protocol-Version"]
		if len(connectVersion) == 1 && connectVersion[0] == "1" {
			if req.Method == http.MethodGet {
				return connectUnaryGetClientProtocol{}
			}
			return nil
		}
		return restClientProtocol{}
	}

	if len(contentTypes) > 1 {
		return nil // Ick. Don't allow this.
	}
	switch {
	case strings.HasPrefix(contentTypes[0], "application/connect+"):
		return connectStreamClientProtocol{}
	case contentTypes[0] == "application/grpc" || strings.HasPrefix(contentTypes[0], "application/grpc+"):
		return grpcClientProtocol{}
	case contentTypes[0] == "application/grpc-web" || strings.HasPrefix(contentTypes[0], "application/grpc-web+"):
		return grpcWebClientProtocol{}
	case strings.HasPrefix(contentTypes[0], "application/"):
		connectVersion := req.Header["Connect-Protocol-Version"]
		if len(connectVersion) == 1 && connectVersion[0] == "1" {
			if req.Method == http.MethodGet {
				return connectUnaryGetClientProtocol{}
			}
			return connectUnaryPostClientProtocol{}
		}
		if contentTypes[0] == "application/json" || contentTypes[0] == "application/json; charset=utf-8" {
			return restClientProtocol{}
		}
		return nil
	default:
		return nil
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

type operation struct {
	writer        http.ResponseWriter
	request       *http.Request
	reqMeta       requestMeta
	cancel        context.CancelFunc
	bufferPool    *bufferPool
	delegate      http.Handler
	resolver      TypeResolver
	canDecompress []string

	method     protoreflect.MethodDescriptor
	methodPath string
	streamType connect.StreamType

	client                   protocolDetails[clientProtocolHandler]
	server                   protocolDetails[serverProtocolHandler]
	cannotDecompressRequest  bool
	cannotDecompressResponse bool

	// only used when clientProtocolDetails.protocol == ProtocolREST
	restTarget *routeTarget
	restVars   []routeTargetVarMatch

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

	sameRequestCompression := op.cannotDecompressRequest || (op.client.reqCompression.Name() == op.server.reqCompression.Name())
	sameCodec := op.client.codec.Name() == op.server.codec.Name()
	// even if body encoding uses same content type, we can't treat them as the same
	// (which means re-using encoded data) if either side needs to prep the data first
	sameRequestCodec := sameCodec && !op.clientReqNeedsPrep && !op.serverReqNeedsPrep
	sameResponseCodec := sameCodec && !op.clientRespNeedsPrep && !op.serverRespNeedsPrep
	mustDecodeRequest := !sameRequestCodec || requireMessageForRequestLine
	mustDecodeResponse := !sameResponseCodec

	if mustDecodeRequest && op.cannotDecompressRequest {
		// TODO: should result in 415 Unsupported Media Type
		// TODO: should send back accept-encoding (or relevant protocol header)
		op.handleError(errors.New("unable to decode"))
		return
	}
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
		reqMsg.msgPrototype = messageType.New().Interface()
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
	allowedResponseCompression := op.reqMeta.acceptCompression
	if mustDecodeResponse {
		allowedResponseCompression = op.canDecompress
	}
	op.server.protocol.addProtocolRequestHeaders(op.reqMeta, op.writer.Header(), allowedResponseCompression)

	// Now we can define the transformed request body.
	if skipBody {
		// drain any contents of body so downstream handler sees empty
		op.drainBody(op.request.Body)
	} else {
		if reqMsg.saveCompressed && reqMsg.msgPrototype == nil {
			// we do not need to decompress or decode
			op.request.Body = op.serverBody(nil)
		} else {
			op.request.Body = op.serverBody(&reqMsg)
		}
	}

	// Finally, define the transforming response writer (which
	// must delay most logic until it sees WriteHeader).
	var err error
	op.writer, err = op.serverWriter()
	if err != nil {
		op.handleError(err)
	}
	op.delegate.ServeHTTP(op.writer, op.request)
}

func (op *operation) handleError(err error) {
	// TODO: determine status code from error and then send error to op.writer
}

func (op *operation) readRequestMessage(msg *message) error {
	msg.reset(op.bufferPool)
	// TODO: buffer and read one message
	return nil
}

func (op *operation) readAndDecodeRequestMessage(msg *message) error {
	if err := op.readRequestMessage(msg); err != nil {
		return err
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
		return &envelopingReader{op: op}
	}
	ret := &transformingReader{op: op, msg: msg}
	if !msg.isZero() {
		// TODO: we have the first message already; setup ret to use it
		_ = msg.msg
	}
	return ret
}

func (op *operation) serverWriter() (http.ResponseWriter, error) {
	if _, ok := op.writer.(http.Flusher); !ok {
		return nil, errors.New("http.ResponseWriter must implement http.Flusher")
	}
	return &responseWriter{op: op}, nil
}

func (op *operation) drainBody(body io.ReadCloser) {
	buf := op.bufferPool.Get()
	defer op.bufferPool.Put(buf)
	b := buf.Bytes()[0:buf.Cap()]
	for {
		_, err := body.Read(b)
		if err != nil {
			_ = body.Close()
			return
		}
	}
}

type envelopingReader struct {
	op *operation
}

func (er envelopingReader) Read(p []byte) (n int, err error) {
	//TODO implement me
	panic("implement me")
}

func (er envelopingReader) Close() error {
	//TODO implement me
	panic("implement me")
}

type transformingReader struct {
	op  *operation
	msg *message
}

func (tr *transformingReader) Read(p []byte) (n int, err error) {
	//TODO implement me
	panic("implement me")
}

func (tr *transformingReader) Close() error {
	//TODO implement me
	panic("implement me")
}

type responseWriter struct {
	op   *operation
	code int
	w    io.Writer
}

func (rw *responseWriter) Header() http.Header {
	//TODO implement me
	panic("implement me")
}

func (rw *responseWriter) Write(i []byte) (int, error) {
	//TODO implement me
	panic("implement me")
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	//TODO implement me
	panic("implement me")
}

func (rw *responseWriter) Flush() {
	// We expose this method so server can call it and won't panic
	// or blow-up when doing type conversion. But it's a no-op
	// since we automatically flush at message boundaries when
	// transforming the response body.
}

type envelopingWriter struct {
	op *operation
}

func (ew envelopingWriter) Write(i []byte) (int, error) {
	//TODO implement me
	panic("implement me")
}

type transformingWriter struct {
	op  *operation
	msg *message
}

func (tw *transformingWriter) Write(i []byte) (int, error) {
	//TODO implement me
	panic("implement me")
}

type pipeWriter struct {
	op          *operation
	pipe        *io.PipeWriter
	transformer *pipeTransformer
}

func (pw *pipeWriter) Write(i []byte) (int, error) {
	//TODO implement me
	panic("implement me")
}

type pipeTransformer struct {
	op   *operation
	pipe *io.PipeReader
	msg  *message
}

type message struct {
	msgPrototype proto.Message
	// compressed is the compressed bytes; data is the serialized but uncompressed bytes
	compressed, data *bytes.Buffer
	// msg is the plain message
	msg proto.Message
	// flags indicating if compressed and data should be preserved after use
	saveCompressed, saveData bool
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
	if msg.msg != nil {
		return nil // no-op
	}
	if msg.data == nil {
		return errors.New("no uncompressed source")
	}
	msg.msg = msg.msgPrototype
	proto.Reset(msg.msg)
	if err := codec.Unmarshal(msg.data.Bytes(), msg.msg); err != nil {
		return err
	}
	if !msg.saveData {
		pool.Put(msg.data)
		msg.data = nil
	}
	return nil
}

func encode(codec Codec, pool *bufferPool, msg *message) error {
	if msg.data != nil {
		return nil // no-op
	}
	if msg.msg == nil {
		return errors.New("no decoded message source")
	}
	buf := pool.Get().Bytes()
	buf, err := codec.MarshalAppend(buf, msg.msg)
	msg.data = bytes.NewBuffer(buf)
	return err
}
