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
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	contentRestPrefix = "application/"
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
		return restHTTPBodyResponse(op)
	default:
		return false
	}
}

func (r restClientProtocol) endMustBeInHeaders() bool {
	// TODO: when we support server streams over REST, this should return
	// false when streaming
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
		timeout, err := restDecodeTimeout(timeoutStr)
		if err != nil {
			return requestMeta{}, err
		}
		reqMeta.timeout = timeout
		reqMeta.hasTimeout = true
	}
	return reqMeta, nil
}

func (r restClientProtocol) addProtocolResponseHeaders(meta responseMeta, headers http.Header) int {
	isErr := meta.end != nil && meta.end.err != nil
	// Only JSON is supported for now unless using google.api.HttpBody
	// payloads which override the content-type.
	if headers["Content-Type"] == nil {
		headers["Content-Type"] = []string{contentRestPrefix + meta.codec}
	}
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
		// Hardcode the error to be a JSON-encoded gRPC status.
		bin = []byte(`{"code":13,"message":"failed to marshal end error"}`)
	}
	_, _ = writer.Write(bin)
	return nil
}

func (r restClientProtocol) requestNeedsPrep(op *operation) bool {
	return len(op.restTarget.vars) != 0 ||
		len(op.request.URL.Query()) != 0 ||
		op.restTarget.requestBodyFields != nil ||
		restHTTPBodyRequest(op) ||
		restHTTPBodyRequestIsEmpty(op)
}

func restHTTPBodyRequestIsEmpty(op *operation) bool {
	return op.request.ContentLength < 0
}

func (r restClientProtocol) prepareUnmarshalledRequest(op *operation, src []byte, target proto.Message) error {
	if err := r.prepareUnmarshalledRequestFromBody(op, src, target); err != nil {
		return err
	}
	// Now pull in the fields from the URI path:
	msg := target.ProtoReflect()
	for i := len(op.restVars) - 1; i >= 0; i-- {
		variable := op.restVars[i]
		if err := setParameter(msg, variable.fields, variable.value); err != nil {
			return err
		}
	}
	// And finally from the query string:
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

func (r restClientProtocol) prepareUnmarshalledRequestFromBody(op *operation, src []byte, target proto.Message) error {
	if op.restTarget.requestBodyFields == nil {
		if len(src) > 0 {
			return fmt.Errorf("request should have no body; instead got %d bytes", len(src))
		}
		return nil
	}

	msg, leafField, err := getBodyField(op.restTarget.requestBodyFields, target.ProtoReflect(), protoreflect.Message.Mutable)
	if err != nil {
		return err
	}

	if leafField == nil && restIsHTTPBody(msg.Descriptor(), nil) {
		fields := msg.Descriptor().Fields()
		contentType := op.reqContentType
		msg.Set(fields.ByName("content_type"), protoreflect.ValueOfString(contentType))
		msg.Set(fields.ByName("data"), protoreflect.ValueOfBytes(src))
		return nil
	}

	if len(src) == 0 {
		// No data to unmarshal.
		return nil
	}
	if leafField == nil {
		return op.client.codec.Unmarshal(src, msg.Interface())
	}
	restCodec, ok := op.client.codec.(RESTCodec)
	if !ok {
		return fmt.Errorf("codec %q (%T) does not implement RESTCodec, so non-message request body cannot be unmarshalled",
			op.client.codec.Name(), op.client.codec)
	}
	return restCodec.UnmarshalField(src, msg.Interface(), leafField)
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

	msg, leafField, err := getBodyField(op.restTarget.responseBodyFields, src.ProtoReflect(), protoreflect.Message.Get)
	if err != nil {
		return nil, err
	}
	if leafField == nil {
		return op.client.codec.MarshalAppend(base, msg.Interface())
	}
	restCodec, ok := op.client.codec.(RESTCodec)
	if !ok {
		return nil,
			fmt.Errorf("codec %q (%T) does not implement RESTCodec, so non-message response body cannot be marshalled",
				op.client.codec.Name(), op.client.codec)
	}
	return restCodec.MarshalAppendField(base, msg.Interface(), leafField)
}

func (r restClientProtocol) String() string {
	return r.protocol().String()
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
	headers["Content-Type"] = []string{contentRestPrefix + meta.codec}
	if meta.compression != "" {
		headers["Content-Encoding"] = []string{meta.compression}
	}
	if len(meta.acceptCompression) != 0 {
		headers["Accept-Encoding"] = []string{strings.Join(meta.acceptCompression, ", ")}
	}
	if meta.hasTimeout {
		value := restEncodeTimeout(meta.timeout)
		headers["X-Server-Timeout"] = []string{value}
	}
}

func (r restServerProtocol) extractProtocolResponseHeaders(statusCode int, headers http.Header) (responseMeta, responseEndUnmarshaller, error) {
	contentType := headers.Get("Content-Type")
	if statusCode/100 != 2 {
		return responseMeta{
				end: &responseEnd{httpCode: statusCode},
			}, func(_ Codec, buf *bytes.Buffer, end *responseEnd) {
				if err := httpErrorFromResponse(statusCode, contentType, buf); err != nil {
					end.err = err
					end.httpCode = httpStatusCodeFromRPC(err.Code())
				}
			}, nil
	}
	var meta responseMeta
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

func (r restServerProtocol) extractEndFromTrailers(_ *operation, _ http.Header) (responseEnd, error) {
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
	msg, leafField, err := getBodyField(op.restTarget.requestBodyFields, src.ProtoReflect(), protoreflect.Message.Get)
	if err != nil {
		return nil, err
	}
	if restHTTPBodyRequest(op) {
		fields := msg.Descriptor().Fields()
		contentType := msg.Get(fields.ByName("content_type")).String()
		bytes := msg.Get(fields.ByName("data")).Bytes()
		headers.Set("Content-Type", contentType)
		return bytes, nil
	}
	if leafField == nil {
		return op.server.codec.MarshalAppend(base, msg.Interface())
	}
	restCodec, ok := op.server.codec.(RESTCodec)
	if !ok {
		return nil,
			fmt.Errorf("codec %q (%T) does not implement RESTCodec, so non-message request body cannot be marshalled",
				op.server.codec.Name(), op.server.codec)
	}
	return restCodec.MarshalAppendField(base, msg.Interface(), leafField)
}

func (r restServerProtocol) responseNeedsPrep(op *operation) bool {
	return len(op.restTarget.responseBodyFieldPath) != 0 ||
		restHTTPBodyResponse(op)
}

func (r restServerProtocol) prepareUnmarshalledResponse(op *operation, src []byte, target proto.Message) error {
	msg, leafField, err := getBodyField(op.restTarget.responseBodyFields, target.ProtoReflect(), protoreflect.Message.Mutable)
	if err != nil {
		return err
	}
	if restHTTPBodyResponse(op) {
		fields := msg.Descriptor().Fields()
		contentType := op.rspContentType
		msg.Set(fields.ByName("content_type"), protoreflect.ValueOfString(contentType))
		msg.Set(fields.ByName("data"), protoreflect.ValueOfBytes(src))
		return nil
	}
	if leafField == nil {
		return op.server.codec.Unmarshal(src, msg.Interface())
	}
	restCodec, ok := op.server.codec.(RESTCodec)
	if !ok {
		return fmt.Errorf("codec %q (%T) does not implement RESTCodec, so non-message response body cannot be unmarshalled",
			op.server.codec.Name(), op.server.codec)
	}
	return restCodec.UnmarshalField(src, msg.Interface(), leafField)
}

func (r restServerProtocol) requiresMessageToProvideRequestLine(_ *operation) bool {
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
	return r.protocol().String()
}

// Decode timeout as a float in seconds from X-Server-Timeout header.
func restDecodeTimeout(timeout string) (time.Duration, error) {
	if timeout == "" {
		return 0, nil
	}
	val, err := strconv.ParseFloat(timeout, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", timeout, err)
	}
	return time.Duration(val * float64(time.Second)), nil
}

// Encode timeout as a float in seconds for X-Server-Timeout header.
func restEncodeTimeout(timeout time.Duration) string {
	if timeout == 0 {
		return ""
	}
	return strconv.FormatFloat(timeout.Seconds(), 'f', -1, 64)
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
		if field.IsList() || field.IsMap() {
			return false
		}
		msg = field.Message()
	}
	return msg != nil && msg.FullName() == "google.api.HttpBody"
}

type accessor func(protoreflect.Message, protoreflect.FieldDescriptor) protoreflect.Value

func getBodyField(fields []protoreflect.FieldDescriptor, root protoreflect.Message, acc accessor) (protoreflect.Message, protoreflect.FieldDescriptor, error) {
	msg := root
	var leafField protoreflect.FieldDescriptor
	for i, field := range fields {
		if field.Message() != nil && field.Cardinality() != protoreflect.Repeated {
			msg = acc(msg, field).Message()
			continue
		}
		if i != len(fields)-1 {
			// This should not be possible since we validate the field types when
			// we build the fields path, when methods are configured with the mux.
			// This logic to construct an error is "just in case", like if we
			// introduce a bug such that the validation fails to catch this.
			var actual string
			switch {
			case field.IsList():
				actual = "list"
			case field.IsMap():
				actual = "map"
			default:
				actual = field.Kind().String()
			}
			return nil, nil, fmt.Errorf("field %s of %s has invalid type: need message, but got %s", field.Name(), msg.Descriptor().FullName(), actual)
		}
		leafField = field
	}
	return msg, leafField, nil
}
