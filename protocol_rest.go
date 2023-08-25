// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

//nolint:revive // this is temporary, will be removed when implementation is complete
package vanguard

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type restClientProtocol struct{}

var _ clientProtocolHandler = restClientProtocol{}
var _ clientBodyPreparer = restClientProtocol{}
var _ clientProtocolEndMustBeInHeaders = restClientProtocol{}

// restClientProtocol implements the REST protocol for
// processing RPCs received from the client.
func (r restClientProtocol) protocol() Protocol {
	return ProtocolREST
}

func (r restClientProtocol) acceptsStreamType(op *operation, streamType connect.StreamType) bool {
	switch streamType {
	case connect.StreamTypeUnary:
		return true
	case connect.StreamTypeClient:
		return restHTTPBodyRequest(op)
	case connect.StreamTypeServer:
		// TODO: support server streams even when body is not google.api.HttpBody
		return restHTTPBodyResponse(op)
	default:
		return false
	}
}

func (r restClientProtocol) endMustBeInHeaders() bool {
	// TODO: when we support server streams over REST, this should return false when streaming
	return true
}

func (r restClientProtocol) extractProtocolRequestHeaders(op *operation, headers http.Header) (requestMeta, error) {
	var reqMeta requestMeta
	reqMeta.compression = headers.Get("Content-Encoding")
	headers.Del("Content-Encoding")
	// TODO: A REST client could use "q" weights in the `Accept-Encoding` header, which
	//       would currently cause the middleware to not recognize the compression.
	//       We may want to address this. We'd need to sort the values by their weight
	//       since other protocols don't allow weights with acceptable encodings.
	reqMeta.acceptCompression = parseMultiHeader(headers.Values("Accept-Encoding"))
	headers.Del("Accept-Encoding")

	reqMeta.codec = CodecJSON // if actually a custom content-type, handled by body preparer methods
	contentType := headers.Get("Content-Type")
	if contentType != "" &&
		contentType != "application/json" &&
		contentType != "application/json; charset=utf-8" &&
		!restHTTPBodyRequest(op) {
		// invalid content-type
		reqMeta.codec = contentType + "?"
	}
	headers.Del("Content-Type")

	if timeoutStr := headers.Get("X-Server-Timeout"); timeoutStr != "" {
		timeout, err := strconv.ParseFloat(timeoutStr, 64)
		if err != nil {
			return requestMeta{}, err
		}
		reqMeta.timeout = time.Duration(timeout * float64(time.Second))
	}
	return reqMeta, nil
}

func (r restClientProtocol) addProtocolResponseHeaders(meta responseMeta, headers http.Header) int {
	isErr := meta.end != nil && meta.end.err != nil
	// TODO: this formulation might only be valid when meta.codec is JSON; support other codecs.
	// Headers are only set if they are not already set, specially to allow
	// for google.api.HttpBody payloads.
	if headers["Content-Type"] == nil {
		headers["Content-Type"] = []string{"application/" + meta.codec}
	}
	// TODO: Content-Encoding to compress error, too?
	if !isErr && meta.compression != "" {
		headers["Content-Encoding"] = []string{meta.compression}
	}
	if len(meta.acceptCompression) != 0 {
		headers["Accept-Encoding"] = []string{strings.Join(meta.acceptCompression, ", ")}
	}
	if isErr {
		return httpStatusCodeFromRPC(meta.end.err.Code())
	}
	return http.StatusOK
}

func (r restClientProtocol) encodeEnd(op *operation, end *responseEnd, writer io.Writer, wasInHeaders bool) http.Header {
	cerr := end.err
	if cerr != nil && !wasInHeaders {
		// TODO: Uh oh. We already flushed headers and started writing body. What can we do?
		//       Should this log? If we are using http/2, is there some way we could send
		//       a "goaway" frame to the client, to indicate abnormal end of stream?
		return nil
	}
	if cerr == nil {
		return nil
	}
	stat := grpcStatusFromError(cerr)
	bin, err := op.client.codec.MarshalAppend(nil, stat)
	if err != nil {
		// TODO: This is always uses JSON whereas above we use the given codec.
		//       If/when we support codecs for REST other than JSON, what should
		//       we do here?
		bin = []byte(`{"code": 13, "message": ` + strconv.Quote(err.Error()) + `}`)
	}
	// TODO: compress?
	_, _ = writer.Write(bin)
	return nil
}

func (r restClientProtocol) requestNeedsPrep(op *operation) bool {
	return len(op.restTarget.vars) != 0 ||
		len(op.request.URL.Query()) != 0 ||
		op.restTarget.requestBodyFields != nil ||
		restHTTPBodyRequest(op)
}

func (r restClientProtocol) prepareUnmarshalledRequest(op *operation, src []byte, target proto.Message) error {
	msg := target.ProtoReflect()
	for _, field := range op.restTarget.requestBodyFields {
		msg = msg.Mutable(field).Message()
	}
	if restIsHTTPBody(msg.Descriptor(), nil) {
		fields := msg.Descriptor().Fields()
		contentType := op.reqContentType
		msg.Set(fields.ByName("content_type"), protoreflect.ValueOfString(contentType))
		msg.Set(fields.ByName("data"), protoreflect.ValueOfBytes(src))
	} else if len(src) > 0 {
		if err := op.client.codec.Unmarshal(src, msg.Interface()); err != nil {
			return err
		}
	}
	msg = target.ProtoReflect()
	for i := len(op.restVars) - 1; i >= 0; i-- {
		variable := op.restVars[i]
		if err := setParameter(msg, variable.fields, variable.value); err != nil {
			return err
		}
	}
	for fieldPath, values := range op.queryValues() {
		fields, err := resolvePathToDescriptors(msg.Descriptor(), fieldPath)
		if err != nil {
			return err
		}
		for _, value := range values {
			if err := setParameter(msg, fields, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r restClientProtocol) responseNeedsPrep(op *operation) bool {
	return len(op.restTarget.responseBodyFields) != 0 ||
		restHTTPBodyResponse(op)
}

func (r restClientProtocol) prepareMarshalledResponse(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error) {
	if restHTTPBodyResponse(op) {
		msg := src.ProtoReflect()
		for _, field := range op.restTarget.responseBodyFields {
			msg = msg.Get(field).Message()
		}
		if !msg.IsValid() {
			return base, nil
		}
		desc := msg.Descriptor()
		dataField := desc.Fields().ByName("data")
		contentField := desc.Fields().ByName("content_type")
		contentType := msg.Get(contentField).String()
		bytes := msg.Get(dataField).Bytes()
		if contentType != "" {
			headers.Set("Content-Type", contentType)
		}
		return bytes, nil
	}

	msg := src.ProtoReflect()
	for _, field := range op.restTarget.responseBodyFields {
		msg = msg.Get(field).Message()
	}
	return op.client.codec.MarshalAppend(base, msg.Interface())
}

func (r restClientProtocol) String() string {
	return protocolNameREST
}

// restServerProtocol implements the REST protocol for
// sending RPCs to the server handler.
type restServerProtocol struct{}

var _ serverProtocolHandler = restServerProtocol{}
var _ requestLineBuilder = restServerProtocol{}
var _ serverBodyPreparer = restServerProtocol{}

func (r restServerProtocol) protocol() Protocol {
	return ProtocolREST
}

func (r restServerProtocol) addProtocolRequestHeaders(meta requestMeta, headers http.Header) {
	// TODO: don't set content-type on no body requests.
	headers["Content-Type"] = []string{"application/" + meta.codec}
	if meta.compression != "" {
		headers["Content-Encoding"] = []string{meta.compression}
	}
	if len(meta.acceptCompression) != 0 {
		headers["Accept-Encoding"] = []string{strings.Join(meta.acceptCompression, ", ")}
	}
	if meta.timeout != 0 {
		// Encode timeout as a float in seconds.
		value := strconv.FormatFloat(meta.timeout.Seconds(), 'E', -1, 64)
		headers["X-Server-Timeout"] = []string{value}
	}
}

func (r restServerProtocol) extractProtocolResponseHeaders(statusCode int, headers http.Header) (responseMeta, responseEndUnmarshaler, error) {
	if statusCode/100 != 2 {
		return responseMeta{
				end: &responseEnd{httpCode: statusCode},
			}, func(_ Codec, src io.Reader, end *responseEnd) {
				if err := httpErrorFromResponse(src); err != nil {
					end.err = err
					end.httpCode = httpStatusCodeFromRPC(err.Code())
				}
			}, nil
	}
	var meta responseMeta
	contentType := headers.Get("Content-Type")
	switch {
	case contentType == "application/json":
		meta.codec = CodecJSON
	case strings.HasPrefix(contentType, "application/"):
		meta.codec = strings.TrimPrefix(contentType, "application/")
		if n := strings.Index(meta.codec, ";"); n != -1 {
			meta.codec = meta.codec[:n]
		}
	default:
		meta.codec = ""
	}
	headers.Del("Content-Type")

	meta.compression = headers.Get("Content-Encoding")
	headers.Del("Content-Encoding")

	meta.acceptCompression = parseMultiHeader(headers.Values("Accept-Encoding"))
	headers.Del("Accept-Encoding")
	return meta, nil, nil
}

func (r restServerProtocol) extractEndFromTrailers(o *operation, headers http.Header) (responseEnd, error) {
	return responseEnd{}, nil
}

func (r restServerProtocol) requestNeedsPrep(op *operation) bool {
	if op.restTarget == nil {
		return false // no REST bindings
	}
	return len(op.restTarget.vars) != 0 ||
		len(op.request.URL.Query()) != 0 ||
		op.restTarget.requestBodyFields != nil
}

func (r restServerProtocol) prepareMarshalledRequest(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error) {
	if op.restTarget.requestBodyFields == nil {
		return base, nil
	}
	msg := src.ProtoReflect()
	for _, field := range op.restTarget.requestBodyFields {
		msg = msg.Get(field).Message()
	}
	if restHTTPBodyRequest(op) {
		fields := msg.Descriptor().Fields()
		contentType := msg.Get(fields.ByName("content_type")).String()
		bytes := msg.Get(fields.ByName("data")).Bytes()
		headers.Set("Content-Type", contentType)
		return bytes, nil
	}
	return op.server.codec.MarshalAppend(base, msg.Interface())
}

func (r restServerProtocol) responseNeedsPrep(op *operation) bool {
	return len(op.restTarget.responseBodyFieldPath) != 0 ||
		restHTTPBodyResponse(op)
}

func (r restServerProtocol) prepareUnmarshalledResponse(op *operation, src []byte, target proto.Message) error {
	msg := target.ProtoReflect()
	for _, field := range op.restTarget.responseBodyFields {
		msg = msg.Mutable(field).Message()
	}
	if restHTTPBodyResponse(op) {
		fields := msg.Descriptor().Fields()
		contentType := op.rspContentType
		msg.Set(fields.ByName("content_type"), protoreflect.ValueOfString(contentType))
		msg.Set(fields.ByName("data"), protoreflect.ValueOfBytes(src))
		return nil
	}
	return op.server.codec.Unmarshal(src, msg.Interface())
}

func (r restServerProtocol) requiresMessageToProvideRequestLine(o *operation) bool {
	return true
}

func (r restServerProtocol) requestLine(op *operation, req proto.Message) (urlPath, queryParams, method string, includeBody bool, err error) {
	path, query, err := httpEncodePathValues(req.ProtoReflect(), op.restTarget)
	if err != nil {
		return "", "", "", false, err
	}
	urlPath = path
	queryParams = query.Encode()
	includeBody = op.restTarget.requestBodyFields != nil // can be len(0) if body is '*'
	return urlPath, queryParams, op.restTarget.method, includeBody, nil
}

func (r restServerProtocol) String() string {
	return protocolNameREST
}

func restHTTPBodyRequest(op *operation) bool {
	return restIsHTTPBody(op.methodConf.descriptor.Input(), op.restTarget.requestBodyFields)
}

func restHTTPBodyResponse(op *operation) bool {
	return restIsHTTPBody(op.methodConf.descriptor.Output(), op.restTarget.responseBodyFields)
}

func restIsHTTPBody(msg protoreflect.MessageDescriptor, bodyPath []protoreflect.FieldDescriptor) bool {
	if len(bodyPath) > 0 {
		field := bodyPath[len(bodyPath)-1]
		msg = field.Message()
	}
	return msg != nil && msg.FullName() == "google.api.HttpBody"
}
