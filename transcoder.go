// Copyright 2023-2024 Buf Technologies, Inc.
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
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Transcoder is a Vanguard handler which acts like a router and a middleware. It transforms
// all supported input protocols (Connect, gRPC, gRPC-Web, REST) into a protocol that the
// service handlers support. It can do simple routing based on RPC method name, for simple
// protocols like Connect, gRPC, and gRPC-Web; but it can also route based on REST-ful URI
// paths configured with HTTP transcoding annotations.
//
// See the package-level examples for sample usage.
type Transcoder struct {
	bufferPool     bufferPool
	codecs         codecMap
	compressors    compressionMap
	methods        map[string]*methodConfig
	restRoutes     routeTrie
	unknownHandler http.Handler
}

// ServeHTTP implements http.Handler, dispatching requests for configured
// services and transcoding protocols and message encoding as needed.
func (t *Transcoder) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	op := t.newOperation(writer, request)
	err := op.validate(t)

	if t.unknownHandler != nil && errors.Is(err, errNotFound) {
		request.Header = op.originalHeaders // restore headers, just in case initialization removed keys
		t.unknownHandler.ServeHTTP(writer, request)
		return
	}

	if err != nil {
		op.reportError(err)
		return
	}

	if op.client.protocol.protocol() == op.server.protocol.protocol() &&
		op.client.codec.Name() == op.server.codec.Name() &&
		op.client.reqCompression.Name() == op.server.reqCompression.Name() {
		// No transformation needed. But we do need to restore the original headers first
		// since extracting request metadata may have removed keys.
		request.Header = op.originalHeaders
		op.methodConf.handler.ServeHTTP(writer, request)
		return
	}

	op.handle()
}

func (t *Transcoder) registerService(svc *Service, svcOpts serviceOptions) error {
	for _, opt := range svc.opts {
		opt.applyToService(&svcOpts)
	}

	if len(svcOpts.protocols) == 0 {
		return fmt.Errorf("service %s was configured with no target protocols", svc.schema.FullName())
	}
	for protocol := range svcOpts.protocols {
		_, isKnown := protocolToString[protocol]
		if !isKnown {
			return fmt.Errorf("protocol %d is not a valid value", protocol)
		}
	}

	if len(svcOpts.codecNames) == 0 {
		return fmt.Errorf("service %s was configured with no target codecs", svc.schema.FullName())
	}
	for codecName := range svcOpts.codecNames {
		if _, known := t.codecs[codecName]; !known {
			return fmt.Errorf("codec %s is not known; use WithCodec to configure known codecs", codecName)
		}
	}

	// empty svcOpts.compressorNames is okay
	for compressorName := range svcOpts.compressorNames {
		if _, known := t.compressors[compressorName]; !known {
			return fmt.Errorf("compression algorithm %s is not known; use WithCompression to configure known algorithms", compressorName)
		}
	}

	if svcOpts.maxMsgBufferBytes <= 0 {
		return fmt.Errorf("service %s is configured with an invalid max message buffer size: %d bytes", svc.schema.FullName(), svcOpts.maxMsgBufferBytes)
	}
	if svcOpts.maxGetURLBytes <= 0 {
		return fmt.Errorf("service %s is configured with an invalid max GET URL length: %d bytes", svc.schema.FullName(), svcOpts.maxGetURLBytes)
	}

	if svcOpts.resolver == nil {
		return fmt.Errorf("service %s is configured with a nil type resolver", svc.schema.FullName())
	}

	methods := svc.schema.Methods()
	for i, length := 0, methods.Len(); i < length; i++ {
		methodDesc := methods.Get(i)
		if err := t.registerMethod(svc.handler, methodDesc, &svcOpts); err != nil {
			return fmt.Errorf("failed to configure method %s: %w", methodDesc.FullName(), err)
		}
	}
	return nil
}

func (t *Transcoder) registerRules(rules []*annotations.HttpRule) error {
	if len(rules) == 0 {
		return nil
	}
	methodRules := make(map[*methodConfig][]*annotations.HttpRule)
	for _, rule := range rules {
		var applied bool
		selector := rule.GetSelector()
		if selector == "" {
			return errors.New("rule missing selector")
		}
		if i := strings.Index(selector, "*"); i >= 0 {
			if i != len(selector)-1 {
				return fmt.Errorf("wildcard selector %q must be at the end", rule.GetSelector())
			}
			selector = selector[:len(selector)-1]
			if len(selector) > 0 && !strings.HasSuffix(selector, ".") {
				return fmt.Errorf("wildcard selector %q must be whole component", rule.GetSelector())
			}
		}
		for _, methodConf := range t.methods {
			methodName := string(methodConf.descriptor.FullName())
			if !strings.HasPrefix(methodName, selector) {
				continue
			}
			methodRules[methodConf] = append(methodRules[methodConf], rule)
			applied = true
		}
		if !applied {
			return fmt.Errorf("rule %q does not match any methods", rule.GetSelector())
		}
	}
	for methodConf, rules := range methodRules {
		for _, rule := range rules {
			if err := t.addRule(rule, methodConf); err != nil {
				// TODO: use the multi-error type errors.Join()
				return err
			}
		}
	}
	return nil
}

func (t *Transcoder) registerMethod(handler http.Handler, methodDesc protoreflect.MethodDescriptor, opts *serviceOptions) error {
	methodPath := "/" + string(methodDesc.Parent().FullName()) + "/" + string(methodDesc.Name())
	if _, ok := t.methods[methodPath]; ok {
		return fmt.Errorf("duplicate registration: method %s has already been configured", methodDesc.FullName())
	}
	methodConf := &methodConfig{
		serviceOptions: opts,
		descriptor:     methodDesc,
		methodPath:     methodPath,
		handler:        handler,
	}
	if t.methods == nil {
		t.methods = make(map[string]*methodConfig, 1)
	}
	t.methods[methodPath] = methodConf

	switch {
	case methodDesc.IsStreamingClient() && methodDesc.IsStreamingServer():
		methodConf.streamType = connect.StreamTypeBidi
	case methodDesc.IsStreamingClient():
		methodConf.streamType = connect.StreamTypeClient
	case methodDesc.IsStreamingServer():
		methodConf.streamType = connect.StreamTypeServer
	default:
		methodConf.streamType = connect.StreamTypeUnary
	}

	if httpRule, ok := getHTTPRuleExtension(methodDesc); ok {
		if err := t.addRule(httpRule, methodConf); err != nil {
			return err
		}
	}
	return nil
}

func (t *Transcoder) addRule(httpRule *annotations.HttpRule, methodConf *methodConfig) error {
	methodPath := methodConf.methodPath
	firstTarget, err := t.restRoutes.addRoute(methodConf, httpRule)
	if err != nil {
		return fmt.Errorf("failed to add REST route for method %s: %w", methodPath, err)
	}
	methodConf.httpRule = firstTarget
	for i, rule := range httpRule.GetAdditionalBindings() {
		if len(rule.GetAdditionalBindings()) > 0 {
			return fmt.Errorf("nested additional bindings are not supported (method %s)", methodPath)
		}
		if _, err := t.restRoutes.addRoute(methodConf, rule); err != nil {
			return fmt.Errorf("failed to add REST route (add'l binding #%d) for method %s: %w", i+1, methodPath, err)
		}
	}
	return nil
}

func (t *Transcoder) newOperation(writer http.ResponseWriter, request *http.Request) *operation {
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	op := &operation{
		writer:      writer,
		request:     request,
		cancel:      cancel,
		bufferPool:  &t.bufferPool,
		compressors: t.compressors,
	}
	op.requestLine.fromRequest(request)
	return op
}

type clientProtocolDetails struct {
	protocol        clientProtocolHandler
	codec           Codec
	reqCompression  *compressionPool
	respCompression *compressionPool
}

type serverProtocolDetails struct {
	protocol        serverProtocolHandler
	codec           Codec
	reqCompression  *compressionPool
	respCompression *compressionPool
}

func classifyRequest(req *http.Request) (clientProtocolHandler, url.Values) {
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
				return connectUnaryGetClientProtocol{}, nil
			}
			return nil, nil
		}
		values := req.URL.Query()
		if values.Get("connect") == "v1" {
			if req.Method != http.MethodGet {
				return nil, nil
			}
			return connectUnaryGetClientProtocol{}, values
		}
		return restClientProtocol{}, values
	}

	if len(contentTypes) > 1 {
		return nil, nil // Ick. Don't allow this.
	}
	contentType := contentTypes[0]
	var values url.Values
	switch {
	case strings.HasPrefix(contentType, "application/connect+"):
		return connectStreamClientProtocol{}, nil
	case contentType == "application/grpc" || strings.HasPrefix(contentType, "application/grpc+"):
		return grpcClientProtocol{}, nil
	case contentType == "application/grpc-web" || strings.HasPrefix(contentType, "application/grpc-web+"):
		return grpcWebClientProtocol{}, nil
	case strings.HasPrefix(contentType, "application/"):
		connectVersion := req.Header["Connect-Protocol-Version"]
		if len(connectVersion) == 1 && connectVersion[0] == "1" {
			if req.Method == http.MethodGet {
				return connectUnaryGetClientProtocol{}, nil
			}
			return connectUnaryPostClientProtocol{}, nil
		}
		values = req.URL.Query()
		if values.Get("connect") == "v1" {
			if req.Method != http.MethodGet {
				return nil, nil
			}
			return connectUnaryGetClientProtocol{}, values
		}
		// REST usually uses application/json, but use of google.api.HttpBody means it could
		// also use *any* content-type.
		fallthrough
	default:
		return restClientProtocol{}, values
	}
}

// operation represents a single HTTP operation, which maps to an incoming HTTP request.
// It tracks properties needed to implement protocol transformation.
type operation struct {
	writer      http.ResponseWriter
	request     *http.Request
	cancel      context.CancelFunc
	bufferPool  *bufferPool
	compressors compressionMap

	queryVars       url.Values
	originalHeaders http.Header
	reqContentType  string      // original content-type in incoming request headers
	rspContentType  string      // original content-type in outgoing response headers
	contentLen      int64       // original content-length in incoming request headers or -1
	requestLine     requestLine // properties of the original incoming request line
	reqMeta         requestMeta
	deadline        time.Time
	methodConf      *methodConfig

	client clientProtocolDetails
	server serverProtocolDetails

	// only used when clientProtocolDetails.protocol == ProtocolREST
	restTarget *routeTarget
	restVars   []routeTargetVarMatch

	isValid bool

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

func (o *operation) validate(transcoder *Transcoder) error {
	// Identify the protocol.
	clientProtoHandler, queryVars := classifyRequest(o.request)
	if clientProtoHandler == nil {
		return newHTTPError(http.StatusUnsupportedMediaType, "could not classify protocol")
	}
	o.client.protocol = clientProtoHandler
	if queryVars != nil {
		// memoize this, so we don't have to parse query string again later
		o.queryVars = queryVars
	}
	o.originalHeaders = o.request.Header.Clone()
	o.reqContentType = o.originalHeaders.Get("Content-Type")
	o.contentLen = o.request.ContentLength
	o.request.ContentLength = -1 // transforming it will likely change it

	// Identify the method being invoked.
	err := o.resolveMethod(transcoder)
	if err != nil {
		return err
	}
	if !o.client.protocol.acceptsStreamType(o, o.methodConf.streamType) {
		return newHTTPError(http.StatusUnsupportedMediaType, "stream type %s not supported with %s protocol", o.methodConf.streamType, o.client.protocol)
	}
	if o.methodConf.streamType == connect.StreamTypeBidi && o.request.ProtoMajor < 2 {
		return newHTTPError(http.StatusHTTPVersionNotSupported, "bidi streams require HTTP/2")
	}
	if clientProtoHandler.protocol() == ProtocolGRPC && o.request.ProtoMajor != 2 {
		return newHTTPError(http.StatusHTTPVersionNotSupported, "gRPC requires HTTP/2")
	}

	// Identify the request encoding and compression.
	reqMeta, err := clientProtoHandler.extractProtocolRequestHeaders(o, o.request.Header)
	if err != nil {
		return newHTTPError(http.StatusBadRequest, err.Error())
	}
	// Remove other headers that might mess up the next leg
	if enc := o.request.Header.Get("Content-Encoding"); enc != "" && enc != CompressionIdentity {
		// If the protocol didn't remove the "Content-Encoding" header in above step,
		// that's because it models encoding in a different way. In that case, encoding
		// of the whole response with this header is not valid.
		return newHTTPError(http.StatusUnsupportedMediaType, "content-encoding %q not allowed for this protocol", enc)
	}
	o.request.Header.Del("Content-Encoding")
	o.request.Header.Del("Accept-Encoding")
	o.request.Header.Del("Content-Length")

	o.reqMeta = reqMeta
	if reqMeta.hasTimeout {
		o.deadline = time.Now().Add(reqMeta.timeout)
	}
	if reqMeta.compression == CompressionIdentity {
		reqMeta.compression = "" // normalize to empty string
	}
	if reqMeta.compression != "" {
		var ok bool
		o.client.reqCompression, ok = o.compressors[reqMeta.compression]
		if !ok {
			return newHTTPError(http.StatusUnsupportedMediaType, "%q compression not supported", reqMeta.compression)
		}
	}
	o.client.codec = transcoder.codecs.get(reqMeta.codec, o.methodConf.resolver)
	if o.client.codec == nil {
		return newHTTPError(http.StatusUnsupportedMediaType, "%q sub-format not supported", reqMeta.codec)
	}

	// Now we can determine the destination protocol details
	if _, supportsProtocol := o.methodConf.protocols[clientProtoHandler.protocol()]; supportsProtocol {
		o.server.protocol = clientProtoHandler.protocol().serverHandler(o)
	} else {
		for _, protocol := range allProtocols {
			if _, supportsProtocol := o.methodConf.protocols[protocol]; supportsProtocol {
				o.server.protocol = protocol.serverHandler(o)
				break
			}
		}
	}

	// Now that we've ruled out the use of bidi streaming above, it's safe to simulate HTTP/2
	// for the benefit of gRPC handlers, which require HTTP/2.
	if o.server.protocol.protocol() == ProtocolGRPC {
		o.request.Proto, o.request.ProtoMajor, o.request.ProtoMinor = "HTTP/2", 2, 0
	}

	if o.server.protocol.protocol() == ProtocolREST {
		// REST always defaults to JSON.
		// This is fine to set even if a custom content-type is used via
		// the use of google.api.HttpBody. The actual content-type and body
		// data will be written via serverBodyPreparer implementation.
		o.server.codec = transcoder.codecs.get(CodecJSON, o.methodConf.resolver)
	} else if _, supportsCodec := o.methodConf.codecNames[reqMeta.codec]; supportsCodec {
		o.server.codec = o.client.codec
	} else {
		o.server.codec = transcoder.codecs.get(o.methodConf.preferredCodec, o.methodConf.resolver)
	}

	if reqMeta.compression != "" {
		if _, supportsCompression := o.methodConf.compressorNames[reqMeta.compression]; supportsCompression {
			o.server.reqCompression = o.client.reqCompression
		}
		// If the server doesn't support the compression scheme, we'll just
		// decompress and not recompress.
	}

	o.isValid = true // Successfully validated!
	return nil
}

func (o *operation) queryValues() url.Values {
	if o.queryVars == nil && o.request.URL.RawQuery != "" {
		o.queryVars = o.request.URL.Query()
	}
	return o.queryVars
}

func (o *operation) handle() {
	o.clientEnveloper, _ = o.client.protocol.(envelopedProtocolHandler)
	o.clientPreparer, _ = o.client.protocol.(clientBodyPreparer)
	if o.clientPreparer != nil {
		o.clientReqNeedsPrep = o.clientPreparer.requestNeedsPrep(o)
	}
	o.serverEnveloper, _ = o.server.protocol.(serverEnvelopedProtocolHandler)
	o.serverPreparer, _ = o.server.protocol.(serverBodyPreparer)
	if o.serverPreparer != nil {
		o.serverReqNeedsPrep = o.serverPreparer.requestNeedsPrep(o)
	}

	serverRequestBuilder, _ := o.server.protocol.(requestLineBuilder)
	var requireMessageForRequestLine bool
	if serverRequestBuilder != nil {
		requireMessageForRequestLine = serverRequestBuilder.requiresMessageToProvideRequestLine(o)
	}

	sameRequestCompression := o.client.reqCompression.Name() == o.server.reqCompression.Name()
	sameCodec := o.client.codec.Name() == o.server.codec.Name()
	// even if body encoding uses same content type, we can't treat them as the same
	// (which means re-using encoded data) if either side needs to prep the data first
	sameRequestCodec := sameCodec && !o.clientReqNeedsPrep && !o.serverReqNeedsPrep
	mustDecodeRequest := !sameRequestCodec || requireMessageForRequestLine

	reqMsg := message{
		sameCompression: sameRequestCompression,
		sameCodec:       sameRequestCodec,
	}

	if mustDecodeRequest {
		// Need the message type to decode
		messageType, err := o.methodConf.resolver.FindMessageByName(o.methodConf.descriptor.Input().FullName())
		if err != nil {
			o.reportError(err)
			return
		}
		reqMsg.msg = messageType.New().Interface()
	}

	if requireMessageForRequestLine {
		// Go ahead and process first request message
		switch err := o.readRequestMessage(nil, o.request.Body, &reqMsg); {
		case errors.Is(err, io.EOF):
			// okay for the first message: means empty message data
			reqMsg.markReady()
		case err != nil:
			o.reportError(err)
			return
		}
		if err := reqMsg.advanceToStage(o, stageDecoded); err != nil {
			o.reportError(err)
			return
		}
	}

	var skipBody bool
	if serverRequestBuilder != nil {
		var hasBody bool
		var err error
		o.request.URL.Path, o.request.URL.RawQuery, o.request.Method, hasBody, err =
			serverRequestBuilder.requestLine(o, reqMsg.msg)
		if err != nil {
			o.reportError(err)
			return
		}
		skipBody = !hasBody
		// Recompute if the server needs to prep the request, now that we've modified
		// properties of op.request.
		if o.serverPreparer != nil {
			o.serverReqNeedsPrep = o.serverPreparer.requestNeedsPrep(o)
		}
	} else {
		// if no request line builder, use simple request layout
		o.request.URL.Path = o.methodConf.methodPath
		o.request.URL.RawQuery = ""
		o.request.Method = http.MethodPost
	}
	o.request.URL.ForceQuery = false
	serverReqMeta := o.reqMeta
	serverReqMeta.codec = o.server.codec.Name()
	serverReqMeta.compression = o.server.reqCompression.Name()
	serverReqMeta.acceptCompression = o.compressors.intersection(o.reqMeta.acceptCompression)
	o.server.protocol.addProtocolRequestHeaders(serverReqMeta, o.request.Header)

	// Now we can define the transformed response writer (which delays
	// much of its logic until it sees the response headers).
	flusher := asFlusher(o.writer)
	if flusher == nil {
		o.reportError(errors.New("http.ResponseWriter must implement http.Flusher"))
		return
	}
	rw := &responseWriter{op: o, delegate: o.writer, flusher: flusher}
	defer rw.close()
	o.writer = rw

	// And finally we can define the transformed request bodies.
	switch {
	case skipBody:
		// drain any contents of body so downstream handler sees empty
		o.drainBody(o.request.Body)
	case sameRequestCompression && sameRequestCodec && !mustDecodeRequest:
		// we do not need to decompress or decode; just transforming envelopes
		o.request.Body = &envelopingReader{rw: rw, r: o.request.Body}
	default:
		tw := &transformingReader{rw: rw, msg: &reqMsg, r: o.request.Body}
		o.request.Body = tw
		if reqMsg.stage != stageEmpty {
			if err := tw.prepareMessage(); err != nil {
				tw.err = err
			}
		}
	}

	o.methodConf.handler.ServeHTTP(o.writer, o.request)
}

func (o *operation) resolveMethod(transcoder *Transcoder) error {
	uriPath := o.request.URL.Path
	switch o.client.protocol.protocol() {
	case ProtocolREST:
		var methods routeMethods
		o.restTarget, o.restVars, methods = transcoder.restRoutes.match(uriPath, o.request.Method)
		if o.restTarget != nil {
			o.methodConf = o.restTarget.config
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
		methodConf := transcoder.methods[uriPath]
		if methodConf == nil {
			return errNotFound
		}
		o.restTarget = methodConf.httpRule
		if o.request.Method != http.MethodPost {
			mayAllowGet, ok := o.client.protocol.(clientProtocolAllowsGet)
			allowsGet := ok && mayAllowGet.allowsGetRequests(methodConf)
			if !allowsGet {
				return &httpError{
					code: http.StatusMethodNotAllowed,
					header: http.Header{
						"Allow": []string{http.MethodPost},
					},
				}
			}
			if allowsGet && o.request.Method != http.MethodGet {
				return &httpError{
					code: http.StatusMethodNotAllowed,
					header: http.Header{
						"Allow": []string{http.MethodGet + "," + http.MethodPost},
					},
				}
			}
		}
		o.methodConf = methodConf
		return nil
	}
}

// reportError handles an error that occurs while setting up the operation. It should not be used
// once the underlying server handler has been invoked. For those errors, responseWriter.reportError
// must be used instead.
func (o *operation) reportError(err error) {
	defer o.cancel()

	if !o.isValid {
		// We don't have enough operation details to render an RPC error,
		// so just send a simple HTTP error.
		asHTTPError(err).Encode(o.writer)
		return
	}

	rw, ok := o.writer.(*responseWriter)
	if ok {
		rw.reportError(err)
		return
	}
	// No responseWriter created yet, so we duplicate some of its behavior to write an error.
	httpErr := asHTTPError(err)
	httpErr.EncodeHeaders(o.writer.Header())
	connErr := asConnectError(err)
	end := &responseEnd{err: connErr, httpCode: httpErr.code}
	code := o.client.protocol.addProtocolResponseHeaders(responseMeta{end: end}, o.writer.Header())
	o.writer.WriteHeader(code)
	trailers := o.client.protocol.encodeEnd(o, end, o.writer, true)
	httpMergeTrailers(o.writer.Header(), trailers)
}

func (o *operation) readRequestMessage(rw *responseWriter, reader io.Reader, msg *message) error {
	msgLen := -1
	compressed := o.client.reqCompression != nil
	if o.clientEnveloper != nil {
		var envBuf envelopeBytes
		_, err := io.ReadFull(reader, envBuf[:])
		if err != nil {
			return err
		}
		msgLen, compressed, err = o.processRequestEnvelope(envBuf)
		if err != nil {
			if rw != nil {
				rw.reportError(err)
			}
			return err
		}
	}

	buffer := msg.reset(o.bufferPool, true, compressed)
	var err error
	if msgLen == -1 { //nolint:nestif
		limit, grow, makeError, limitErr := o.determineReadLimit()
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
	msg.markReady()
	return nil
}

func (o *operation) processRequestEnvelope(envBuf envelopeBytes) (msgLen int, compressed bool, err error) {
	env, err := o.clientEnveloper.decodeEnvelope(envBuf)
	if err != nil {
		return 0, false, malformedRequestError(err)
	}
	if env.trailer {
		return 0, false, malformedRequestError(errors.New("client stream cannot include status/trailer message"))
	}
	if limit := o.methodConf.maxMsgBufferBytes; env.length > limit {
		return 0, false, bufferLimitError(int64(limit))
	}
	return int(env.length), env.compressed, nil
}

func (o *operation) determineReadLimit() (limit int64, grow bool, makeError func(int64) error, err error) {
	limit = int64(o.methodConf.maxMsgBufferBytes)
	if o.contentLen == -1 {
		return limit, false, bufferLimitError, nil
	}
	if o.contentLen > limit {
		// content-length header tells us that entity is too large
		err := bufferLimitError(limit)
		return 0, false, nil, err
	}
	return o.contentLen, true, contentLengthError, nil
}

func (o *operation) drainBody(body io.ReadCloser) {
	if wt, ok := body.(io.WriterTo); ok {
		_, _ = wt.WriteTo(io.Discard)
		return
	}
	buf := o.bufferPool.Get()
	defer o.bufferPool.Put(buf)
	b := buf.Bytes()[0:buf.Cap()]
	_, _ = io.CopyBuffer(io.Discard, body, b)
}

// envelopingReader will translate between envelope styles as data is read.
// It does not do any decompressing or deserializing of data.
type envelopingReader struct {
	rw *responseWriter
	r  io.ReadCloser

	mu                 sync.Mutex
	err                error
	current            io.Reader
	mustReleaseCurrent bool
	env                envelopeBytes
	envRemain          int
}

func (r *envelopingReader) Read(data []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return 0, r.err
	}
	if r.current != nil {
		bytesRead, err := r.current.Read(data)
		isEOF := errors.Is(err, io.EOF)
		if bytesRead > 0 && (err == nil || isEOF) {
			return bytesRead, nil
		}
		if err != nil && !isEOF {
			r.err = err
			return bytesRead, err
		}
		// otherwise EOF, fall through
	}

	if err := r.prepareNext(); err != nil {
		r.err = err
		return 0, err
	}

	if len(data) < r.envRemain {
		copy(data, r.env[envelopeLen-r.envRemain:])
		r.envRemain -= len(data)
		return len(data), nil
	}
	var offset int
	if r.envRemain > 0 {
		copy(data, r.env[envelopeLen-r.envRemain:])
		offset = r.envRemain
		r.envRemain = 0
	}
	if len(data) > offset {
		n, err = r.current.Read(data[offset:])
	}
	return offset + n, err
}

func (r *envelopingReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mustReleaseCurrent {
		buf, ok := r.current.(*bytes.Buffer)
		if ok {
			r.rw.op.bufferPool.Put(buf)
		}
		r.current = nil
		r.mustReleaseCurrent = false
	}
	r.err = errors.New("body is closed")
	return r.r.Close()
}

func (r *envelopingReader) prepareNext() error {
	var env envelope
	switch {
	case r.rw.op.clientEnveloper == nil && r.rw.op.serverEnveloper == nil:
		// no envelopes to transform, just pass the body through w/ no change
		r.current = r.r
		r.envRemain = 0
		return nil
	case r.rw.op.clientEnveloper == nil:
		env.compressed = r.rw.op.client.reqCompression != nil
		if r.rw.op.contentLen != -1 {
			r.current = &hardLimitReader{r: r.r, rw: r.rw, limit: r.rw.op.contentLen, makeError: contentLengthError}
			env.length = uint32(r.rw.op.contentLen)
		} else {
			// Oof. We have to buffer entire request in order to measure it.
			limit := int64(r.rw.op.methodConf.maxMsgBufferBytes)
			buf := r.rw.op.bufferPool.Get()
			_, err := io.Copy(buf, &hardLimitReader{r: r.r, rw: r.rw, limit: limit})
			if err != nil {
				r.rw.op.bufferPool.Put(buf)
				r.err = err
				return err
			}
			r.current = buf
			r.mustReleaseCurrent = true
			env.length = uint32(buf.Len())
		}
	default: // clientEnveloper != nil
		var envBytes envelopeBytes
		_, err := io.ReadFull(r.r, envBytes[:])
		if err != nil {
			return err
		}
		env, err = r.rw.op.clientEnveloper.decodeEnvelope(envBytes)
		if err != nil {
			err = malformedRequestError(err)
			r.rw.reportError(err)
			return err
		}
		r.current = io.LimitReader(r.r, int64(env.length))
	}

	if r.rw.op.serverEnveloper == nil {
		r.envRemain = 0
	} else {
		r.envRemain = envelopeLen
		r.env = r.rw.op.serverEnveloper.encodeEnvelope(env)
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

	mu            sync.Mutex
	consumedFirst bool
	err           error
	buffer        *bytes.Buffer
	env           envelopeBytes
	envRemain     int
}

func (r *transformingReader) Read(data []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return 0, r.err
	}

	for {
		if len(data) < r.envRemain {
			copy(data, r.env[envelopeLen-r.envRemain:])
			r.envRemain -= len(data)
			return len(data), nil
		}
		var offset int
		if r.envRemain > 0 {
			copy(data, r.env[envelopeLen-r.envRemain:])
			offset = r.envRemain
			r.envRemain = 0
		}
		var err error
		if len(data) > offset && r.buffer != nil {
			n, err = r.buffer.Read(data[offset:])
		}
		if offset+n > 0 {
			return offset + n, err
		}

		// If we get here, there was nothing in tr.buffer to read, so
		// we need to prepare the next message and try again.

		if err := r.rw.op.readRequestMessage(r.rw, r.r, r.msg); err != nil {
			// If this is the first request message, the error is EOF, and there's a body
			// preparer, we'll allow it and let the preparer produce a message from zero
			// request bytes.
			if !r.consumedFirst && errors.Is(err, io.EOF) && r.rw.op.clientReqNeedsPrep {
				r.msg.markReady()
			} else {
				r.err = err
				return 0, err
			}
		}
		if err := r.prepareMessage(); err != nil {
			r.err = err
			return 0, err
		}
	}
}

func (r *transformingReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = errors.New("body is closed")
	r.msg.release(r.rw.op.bufferPool)
	return r.r.Close()
}

func (r *transformingReader) prepareMessage() error {
	r.consumedFirst = true
	if err := r.msg.advanceToStage(r.rw.op, stageSend); err != nil {
		return err
	}
	r.buffer = r.msg.sendBuffer()
	if r.rw.op.serverEnveloper == nil {
		r.envRemain = 0
		return nil
	}
	// Need to prefix the buffer with an envelope
	env := envelope{
		compressed: r.msg.wasCompressed && r.rw.op.server.reqCompression != nil,
		length:     uint32(r.buffer.Len()),
	}
	r.env = r.rw.op.serverEnveloper.encodeEnvelope(env)
	r.envRemain = envelopeLen
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

func (w *responseWriter) Header() http.Header {
	return w.delegate.Header()
}

func (w *responseWriter) Write(data []byte) (int, error) {
	if !w.headersWritten {
		w.WriteHeader(http.StatusOK)
	}
	if w.err != nil {
		return 0, w.err
	}
	return w.w.Write(data)
}

func (w *responseWriter) WriteHeader(statusCode int) {
	if w.headersWritten {
		return
	}
	w.headersWritten = true
	w.code = statusCode

	if w.endWritten {
		// Nothing to do: we already sent RPC error to client.
		return
	}

	var err error
	w.contentLen, err = httpExtractContentLength(w.Header())
	if err != nil {
		w.reportError(err)
		return
	}
	w.op.rspContentType = w.Header().Get("Content-Type")
	respMeta, processBody, err := w.op.server.protocol.extractProtocolResponseHeaders(statusCode, w.Header())
	if err != nil {
		w.reportError(err)
		return
	}
	// snapshot trailer keys
	trailerKeys := parseMultiHeader(w.Header().Values("Trailer"))
	if len(trailerKeys) > 0 {
		respMeta.pendingTrailerKeys = make(headerKeys, len(trailerKeys))
		for _, k := range trailerKeys {
			respMeta.pendingTrailerKeys.add(k)
		}
		w.Header().Del("Trailer")
	}

	// Remove other headers that might mess up the next leg
	w.Header().Del("Content-Encoding")
	w.Header().Del("Accept-Encoding")

	w.respMeta = &respMeta
	if respMeta.compression == CompressionIdentity {
		respMeta.compression = "" // normalize to empty string
	}
	if respMeta.compression != "" {
		respCompression, ok := w.op.compressors[respMeta.compression]
		if !ok {
			w.reportError(fmt.Errorf("response indicates unsupported compression encoding %q", respMeta.compression))
			return
		}
		w.op.client.respCompression = respCompression
		w.op.server.respCompression = respCompression
	}
	if respMeta.codec != "" && respMeta.codec != w.op.server.codec.Name() &&
		!restHTTPBodyResponse(w.op) {
		// unexpected content-type for reply
		w.reportError(fmt.Errorf("response uses incorrect codec: expecting %q but instead got %q", w.op.server.codec.Name(), respMeta.codec))
		return
	}

	if respMeta.end != nil {
		// RPC failed immediately.
		if processBody != nil {
			// We have to wait until we receive the body in order to process the error.
			w.w = &errorWriter{
				rw:          w,
				respMeta:    w.respMeta,
				processBody: processBody,
				buffer:      w.op.bufferPool.Get(),
			}
			return
		}
		// We can send back error response immediately.
		w.flushHeaders()
		w.w = noResponseBodyWriter{}
		return
	}

	if w.op.clientPreparer != nil {
		w.op.clientRespNeedsPrep = w.op.clientPreparer.responseNeedsPrep(w.op)
	}
	if w.op.serverPreparer != nil {
		w.op.serverRespNeedsPrep = w.op.serverPreparer.responseNeedsPrep(w.op)
	}

	sameCodec := w.op.client.codec.Name() == w.op.server.codec.Name()
	// even if body encoding uses same content type, we can't treat them as the same
	// (which means re-using encoded data) if either side needs to prep the data first
	sameResponseCodec := sameCodec && !w.op.clientRespNeedsPrep && !w.op.serverRespNeedsPrep
	mustDecodeResponse := !sameResponseCodec

	respMsg := message{sameCompression: true, sameCodec: sameResponseCodec}

	if mustDecodeResponse {
		// We will have to decode and re-encode, so we need the message type.
		messageType, err := w.op.methodConf.resolver.FindMessageByName(w.op.methodConf.descriptor.Output().FullName())
		if err != nil {
			w.reportError(err)
			return
		}
		respMsg.msg = messageType.New().Interface()
	}

	var endMustBeInHeaders bool
	if mustBe, ok := w.op.client.protocol.(clientProtocolEndMustBeInHeaders); ok {
		endMustBeInHeaders = mustBe.endMustBeInHeaders()
	}
	var delegate io.Writer
	if endMustBeInHeaders {
		// We must await the end before we can write headers, which means we have to
		// buffer the entire response.
		w.buf = w.op.bufferPool.Get()
		delegate = &limitWriter{buf: w.buf, limit: w.op.methodConf.maxMsgBufferBytes, rw: w}
	} else {
		// We can go ahead and flush headers now.
		w.flushHeaders()
		delegate = w.delegate
	}

	// Now we can define the transformed response body.
	if sameResponseCodec && !mustDecodeResponse {
		// we do not need to decompress or decode
		w.w = &envelopingWriter{rw: w, w: delegate}
	} else {
		w.w = &transformingWriter{rw: w, msg: &respMsg, w: delegate}
	}
}

// Unwrap provides access to the underlying response writer. This plays nicely
// with ResponseController functionality introduced in Go 1.21 without actually
// depending on Go 1.21.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.delegate
}

func (w *responseWriter) Flush() {
	// We expose this method so server can call it and won't panic
	// or blow-up when doing type conversion. But it's a no-op
	// since we automatically flush at message boundaries when
	// transforming the response body.
}

func (w *responseWriter) flushMessage() {
	if w.buf != nil {
		// we are buffering until we see trailers, so we don't
		// want to actually flush the underlying response writer yet
		return
	}
	w.flusher.Flush()
}

func (w *responseWriter) reportError(err error) {
	var end responseEnd
	if errors.As(err, &end.err) {
		end.httpCode = httpStatusCodeFromRPC(end.err.Code())
	} else {
		end.err = connect.NewError(connect.CodeUnknown, err)
		end.httpCode = http.StatusBadGateway
	}
	w.reportEnd(&end)
}

func (w *responseWriter) reportEnd(end *responseEnd) {
	if w.endWritten {
		// It's possible this could be called in the event of a cascading error,
		// where various receivers all call reportEnd. We will only respect the
		// first such call and ignore the others.
		return
	}
	if w.respMeta != nil && len(w.respMeta.pendingTrailers) > 0 && len(end.trailers) == 0 {
		// add any pending trailers to the end
		end.trailers = w.respMeta.pendingTrailers
	}
	switch {
	case w.headersFlushed:
		// write error to body or trailers
		w.writeEnd(end, false)
	case w.respMeta != nil:
		w.respMeta.end = end
		w.flushHeaders()
	default:
		w.respMeta = &responseMeta{end: end}
		w.flushHeaders()
	}
	w.flusher.Flush()
	// response is done
	w.op.cancel()
	w.err = context.Canceled
}

func (w *responseWriter) flushHeaders() {
	if w.headersFlushed {
		return // already flushed
	}
	cliRespMeta := *w.respMeta
	cliRespMeta.codec = w.op.client.codec.Name()
	cliRespMeta.compression = w.op.client.respCompression.Name()
	cliRespMeta.acceptCompression = w.op.compressors.intersection(w.respMeta.acceptCompression)
	statusCode := w.op.client.protocol.addProtocolResponseHeaders(cliRespMeta, w.Header())
	hasErr := w.respMeta.end != nil && w.respMeta.end.err != nil
	// We only buffer full response for unary operations, so if we have an error,
	// we ignore anything already written to the buffer.
	if w.buf != nil && !hasErr {
		w.Header().Set("Content-Length", strconv.Itoa(w.buf.Len()))
	}
	// TODO: At this point, if the server was gRPC but the client is not, we may have "Trailer"
	//       headers reserving the use of various metadata keys in trailers. It would be
	//       cleaner if they were culled and only remained present for sneding to gRPC clients.
	w.delegate.WriteHeader(statusCode)
	if w.buf != nil {
		if !hasErr {
			_, _ = w.buf.WriteTo(w.delegate)
		}
		w.op.bufferPool.Put(w.buf)
		w.buf = nil
	}
	if w.respMeta.end != nil {
		// response is done
		w.writeEnd(w.respMeta.end, true)
		w.err = context.Canceled
	}

	w.headersFlushed = true
}

func (w *responseWriter) close() {
	if !w.headersWritten {
		// treat as empty successful response
		w.WriteHeader(http.StatusOK)
	}
	if w.w != nil {
		_, _ = w.w.Write(nil) // trigger any final writes
		_ = w.w.Close()
	}
	if w.endWritten {
		return // all done
	}
	if w.respMeta.end != nil {
		// got end in headers
		w.reportEnd(w.respMeta.end)
		return
	}
	// try to get end from trailers
	trailer := httpExtractTrailers(w.Header(), w.respMeta.pendingTrailerKeys)
	end, err := w.op.server.protocol.extractEndFromTrailers(w.op, trailer)
	if err != nil {
		w.reportError(err)
		return
	}
	w.reportEnd(&end)
}

func (w *responseWriter) writeEnd(end *responseEnd, wasInHeaders bool) {
	trailers := w.op.client.protocol.encodeEnd(w.op, end, w.delegate, wasInHeaders)
	httpMergeTrailers(w.Header(), trailers)
	w.endWritten = true
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

func (w *envelopingWriter) Write(data []byte) (int, error) {
	w.maybeInit()
	if w.err != nil {
		return 0, w.err
	}
	if w.remainingBytes == -1 {
		n, err := w.current.Write(data)
		if err != nil {
			w.err = err
		}
		return n, err
	}

	var written int
	for {
		if w.err != nil {
			return written, w.err
		}
		if len(data) < w.remainingBytes {
			// not enough data to trigger next action; ingest data and return
			n, err := w.writeBytes(data)
			w.remainingBytes -= n
			written += n
			if err != nil {
				w.err = err
			}
			return written, err
		}
		// ingest remaining needed and trigger next action
		n, err := w.writeBytes(data[:w.remainingBytes])
		written += n
		data = data[w.remainingBytes:]
		w.remainingBytes -= n
		if err != nil {
			w.err = err
			return written, err
		}
		if w.writingEnvelope {
			if err := w.handleEnvelopeWritten(); err != nil {
				return written, err
			}
			continue
		}

		if w.currentIsTrailer {
			err := w.handleTrailer()
			if err != nil {
				return written, err
			}
		} else {
			// flush after each message and reset for next envelope
			w.rw.flushMessage()
			w.writingEnvelope = true
			w.remainingBytes = envelopeLen
		}
	}
}

func (w *envelopingWriter) writeBytes(data []byte) (int, error) {
	if w.writingEnvelope {
		copy(w.env[envelopeLen-w.remainingBytes:], data)
		return len(data), nil
	}
	return w.current.Write(data)
}

func (w *envelopingWriter) handleEnvelopeWritten() error {
	w.writingEnvelope = false
	env, err := w.rw.op.serverEnveloper.decodeEnvelope(w.env)
	if err != nil {
		err = malformedRequestError(err)
		w.rw.reportError(err)
		return err
	}
	if env.trailer {
		// buffer final message, so we can transform it to a responseEnd
		if limit := w.rw.op.methodConf.maxMsgBufferBytes; env.length > limit {
			err := bufferLimitError(int64(limit))
			w.rw.reportError(err)
			return err
		}
		buf := w.rw.op.bufferPool.Get()
		buf.Grow(int(env.length))
		w.current = buf
		w.mustReleaseCurrent = true
		w.currentIsTrailer = true
		w.trailerIsCompressed = env.compressed
		w.remainingBytes = int(env.length)
		return nil
	}
	if w.rw.op.clientEnveloper != nil {
		envBytes := w.rw.op.clientEnveloper.encodeEnvelope(env)
		_, err := w.w.Write(envBytes[:])
		if err != nil {
			w.err = err
			return err
		}
	}
	w.current = w.w
	w.remainingBytes = int(env.length)
	return nil
}

func (w *envelopingWriter) Close() error {
	var buf *bytes.Buffer
	if w.mustReleaseCurrent {
		var ok bool
		buf, ok = w.current.(*bytes.Buffer)
		if !ok {
			lw, ok := w.current.(*limitWriter)
			if ok {
				buf = lw.buf
			}
		}
		if buf == nil {
			return fmt.Errorf("current sink must be *limitWriter or *bytes.Buffer but instead is %T", w.current)
		}
		defer w.rw.op.bufferPool.Put(buf)
	}
	if w.remainingBytes == -1 && w.mustReleaseCurrent && w.err == nil {
		// We were buffering in order to measure size and create envelope,
		// so do that now.
		env := envelope{compressed: w.rw.op.client.respCompression != nil, length: uint32(buf.Len())}
		envBytes := w.rw.op.clientEnveloper.encodeEnvelope(env)
		_, err := w.w.Write(envBytes[:])
		if err != nil {
			w.err = err
			return err
		}
		_, err = buf.WriteTo(w.w)
		if err != nil {
			w.err = err
			return err
		}
	}
	var normalEOF bool
	if w.writingEnvelope && w.remainingBytes == envelopeLen {
		// We were looking for envelope of next message, but no next message in the stream
		normalEOF = true
	}
	if w.remainingBytes > 0 && !normalEOF {
		// Unfinished body!
		if w.writingEnvelope {
			w.rw.reportError(fmt.Errorf("handler only wrote %d out of %d bytes of message envelope", envelopeLen-w.remainingBytes, envelopeLen))
		} else {
			w.rw.reportError(fmt.Errorf("handler failed to write final %d bytes of message", w.remainingBytes))
		}
	}
	w.remainingBytes = 0
	w.current = nil
	w.err = errors.New("body is closed")
	return nil
}

func (w *envelopingWriter) maybeInit() {
	if w.initialized {
		return
	}
	w.initialized = true
	if w.rw.op.serverEnveloper != nil {
		w.writingEnvelope = true
		w.remainingBytes = envelopeLen
		return
	}
	if w.rw.op.clientEnveloper == nil {
		// just pass everything through
		w.remainingBytes = -1
		w.current = w.w
		return
	}
	if w.rw.contentLen == -1 {
		// Oof, we have to buffer everything to measure the request size
		// to construct an envelope.
		w.remainingBytes = -1
		buf := w.rw.op.bufferPool.Get()
		w.current = &limitWriter{buf: buf, limit: w.rw.op.methodConf.maxMsgBufferBytes, rw: w.rw}
		w.mustReleaseCurrent = true
		return
	}
	// synthesize envelope
	var env envelope
	env.compressed = w.rw.op.client.respCompression != nil
	env.length = uint32(w.rw.contentLen)
	envBytes := w.rw.op.clientEnveloper.encodeEnvelope(envelope{})
	_, err := w.w.Write(envBytes[:])
	if err != nil {
		w.err = err
		return
	}
	w.current = w.w
	w.remainingBytes = envelopeLen
}

func (w *envelopingWriter) handleTrailer() error {
	data, ok := w.current.(*bytes.Buffer)
	if !ok {
		// should not be possible
		return fmt.Errorf("trailer must be *limitWriter but instead is %T", w.current)
	}
	defer w.rw.op.bufferPool.Put(data)
	w.mustReleaseCurrent = false
	if w.trailerIsCompressed {
		uncompressed := w.rw.op.bufferPool.Get()
		defer w.rw.op.bufferPool.Put(uncompressed)
		if err := w.rw.op.server.respCompression.decompress(uncompressed, data); err != nil {
			return err
		}
		data = uncompressed
	}
	end, err := w.rw.op.serverEnveloper.decodeEndFromMessage(w.rw.op, data)
	if err != nil {
		w.rw.reportError(err)
		return err
	}
	end.wasCompressed = w.trailerIsCompressed
	w.rw.reportEnd(&end)
	w.err = errors.New("final data already written")
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

func (w *transformingWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.buffer == nil {
		w.reset()
	}
	if w.expectingBytes == -1 {
		if limit := int64(w.rw.op.methodConf.maxMsgBufferBytes); int64(len(data))+int64(w.buffer.Len()) > limit {
			err := bufferLimitError(limit)
			w.rw.reportError(err)
			return 0, err
		}
		return w.buffer.Write(data)
	}

	var written int
	// For enveloped protocols, it's possible that data contains
	// multiple messages, so we need to process in a loop.
	for {
		if w.err != nil {
			return written, w.err
		}
		remainingBytes := w.expectingBytes - w.buffer.Len()
		if len(data) < remainingBytes {
			// not enough data to trigger next action; ingest data and return
			w.buffer.Write(data)
			written += len(data)
			break
		}
		// ingest remaining needed and trigger next action
		w.buffer.Write(data[:remainingBytes])
		written += remainingBytes
		data = data[remainingBytes:]
		if w.writingEnvelope {
			var envBytes envelopeBytes
			_, _ = w.buffer.Read(envBytes[:])
			var err error
			w.latestEnvelope, err = w.rw.op.serverEnveloper.decodeEnvelope(envBytes)
			if err != nil {
				err = malformedRequestError(err)
				w.rw.reportError(err)
				return written, err
			}
			if limit := w.rw.op.methodConf.maxMsgBufferBytes; w.latestEnvelope.length > limit {
				err = bufferLimitError(int64(limit))
				w.rw.reportError(err)
				return written, err
			}
			w.buffer = w.msg.reset(w.rw.op.bufferPool, false, w.latestEnvelope.compressed)
			w.buffer.Grow(int(w.latestEnvelope.length))
			w.expectingBytes = int(w.latestEnvelope.length)
			w.writingEnvelope = false
		} else {
			if err := w.flushMessage(); err != nil {
				w.rw.reportError(err)
				return written, err
			}
			w.expectingBytes = envelopeLen
			w.writingEnvelope = true
		}
	}
	return written, nil
}

func (w *transformingWriter) Close() error {
	if w.expectingBytes == -1 {
		if err := w.flushMessage(); err != nil {
			w.rw.reportError(err)
		}
	} else if w.buffer != nil && w.buffer.Len() > 0 {
		// Unfinished body!
		if w.writingEnvelope {
			w.rw.reportError(fmt.Errorf("handler only wrote %d out of %d bytes of message envelope", w.buffer.Len(), envelopeLen))
		} else {
			w.rw.reportError(fmt.Errorf("handler only wrote %d out of %d bytes of message", w.buffer.Len(), w.expectingBytes))
		}
	}
	w.expectingBytes = 0
	w.msg.release(w.rw.op.bufferPool)
	w.buffer = nil
	w.err = errors.New("body is closed")
	return nil
}

func (w *transformingWriter) flushMessage() error {
	if w.latestEnvelope.trailer {
		data := w.buffer
		if w.latestEnvelope.compressed {
			data = w.rw.op.bufferPool.Get()
			defer w.rw.op.bufferPool.Put(data)
			if err := w.rw.op.server.respCompression.decompress(data, w.buffer); err != nil {
				return err
			}
		}
		end, err := w.rw.op.serverEnveloper.decodeEndFromMessage(w.rw.op, data)
		if err != nil {
			w.rw.reportError(err)
			return err
		}
		end.wasCompressed = w.latestEnvelope.compressed
		w.rw.reportEnd(&end)
		w.err = errors.New("final data already written")
		return nil
	}

	// We've finished reading the message, so we can manually set the stage
	w.msg.markReady()
	if err := w.msg.advanceToStage(w.rw.op, stageSend); err != nil {
		return err
	}
	buffer := w.msg.sendBuffer()
	if enveloper := w.rw.op.clientEnveloper; enveloper != nil {
		env := envelope{
			compressed: w.msg.wasCompressed && w.rw.op.client.respCompression != nil,
			length:     uint32(buffer.Len()),
		}
		envBytes := enveloper.encodeEnvelope(env)
		if _, err := w.w.Write(envBytes[:]); err != nil {
			w.err = err
			return err
		}
	}
	if _, err := buffer.WriteTo(w.w); err != nil {
		w.err = err
		return err
	}
	// flush after each message
	w.rw.flushMessage()

	w.reset()
	return nil
}

func (w *transformingWriter) reset() {
	if w.rw.op.serverEnveloper != nil {
		w.buffer = w.msg.reset(w.rw.op.bufferPool, false, false)
		w.expectingBytes = envelopeLen
		w.writingEnvelope = true
	} else {
		isCompressed := w.rw.respMeta.compression != ""
		w.buffer = w.msg.reset(w.rw.op.bufferPool, false, isCompressed)
		w.expectingBytes = -1
	}
}

type errorWriter struct {
	rw          *responseWriter
	respMeta    *responseMeta
	processBody responseEndUnmarshaller
	buffer      *bytes.Buffer
}

func (e *errorWriter) Write(data []byte) (int, error) {
	if e.buffer == nil {
		return 0, errors.New("writer already closed")
	}
	if limit := int64(e.rw.op.methodConf.maxMsgBufferBytes); int64(len(data))+int64(e.buffer.Len()) > limit {
		err := bufferLimitError(limit)
		e.rw.reportError(err)
		return 0, err
	}
	return e.buffer.Write(data)
}

func (e *errorWriter) Close() error {
	if e.respMeta.end == nil {
		e.respMeta.end = &responseEnd{}
	}
	bufferPool := e.rw.op.bufferPool
	defer bufferPool.Put(e.buffer)
	body := e.buffer
	if compressPool := e.rw.op.server.respCompression; compressPool != nil {
		uncompressed := bufferPool.Get()
		defer bufferPool.Put(uncompressed)
		if err := compressPool.decompress(uncompressed, body); err != nil {
			// can't really just return an error; we have to encode the
			// error into the RPC response, so we populate respMeta.end
			if e.respMeta.end.httpCode == 0 || e.respMeta.end.httpCode == http.StatusOK {
				e.respMeta.end.httpCode = http.StatusInternalServerError
			}
			e.respMeta.end.err = connect.NewError(connect.CodeInternal, fmt.Errorf("failed to decompress body: %w", err))
			body = nil
		} else {
			body = uncompressed
		}
	}
	if body != nil {
		e.processBody(e.rw.op.server.codec, body, e.respMeta.end)
	}
	e.rw.flushHeaders()
	e.buffer = nil
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
	// original size of the message on the wire, in bytes
	size int

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
	m.size = -1
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

func (m *message) markReady() {
	m.stage = stageRead
	if m.wasCompressed {
		m.size = m.compressed.Len()
	} else {
		m.size = m.data.Len()
	}
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
		pool = op.client.respCompression
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
		pool = op.server.respCompression
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

type requestLine struct {
	method, path, queryString, httpVersion string
}

func (l *requestLine) fromRequest(req *http.Request) {
	l.method = req.Method
	l.path = req.URL.Path
	l.queryString = req.URL.RawQuery
	l.httpVersion = req.Proto
}
