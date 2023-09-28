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
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type restClientProtocol struct {
	target *routeTarget
	vars   []routeTargetVarMatch
}

var _ clientProtocolHandler = restClientProtocol{}

// restClientProtocol implements the REST protocol for
// processing RPCs received from the client.
func (r restClientProtocol) Protocol() Protocol {
	return ProtocolREST
}

func (r restClientProtocol) DecodeRequestHeader(meta *requestMeta) error {
	if !r.acceptsStreamType() {
		return protocolError("REST client protocol does not support %s stream", r.target.config.streamType)
	}
	meta.CompressionName = meta.Header.Get("Content-Encoding")
	meta.Header.Del("Content-Encoding")
	// TODO: A REST client could use "q" weights in the `Accept-Encoding` header, which
	//       would currently cause the middleware to not recognize the compression.
	//       We may want to address this. We'd need to sort the values by their weight
	//       since other protocols don't allow weights with acceptable encodings.
	meta.AcceptCompression = parseMultiHeader(meta.Header.Values("Accept-Encoding"))
	meta.Header.Del("Accept-Encoding")

	meta.CodecName = CodecJSON // if actually a custom content-type, handled by body preparer methods
	contentType := meta.Header.Get("Content-Type")
	if contentType != "" &&
		contentType != "application/json" &&
		contentType != "application/json; charset=utf-8" &&
		!restIsHTTPBody(r.target.config.descriptor.Input(), r.target.requestBodyFields) {
		meta.CodecName = contentType + "?" // invalid
	}
	meta.Header.Del("Content-Type")

	if timeoutStr := meta.Header.Get("X-Server-Timeout"); timeoutStr != "" {
		timeout, err := strconv.ParseFloat(timeoutStr, 64)
		if err != nil {
			return err
		}
		meta.Timeout = time.Duration(timeout * float64(time.Second))
	}

	// Resolve codecs
	codec, err := r.target.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	urlClone := *meta.URL
	meta.Client.Codec = &restRequestCodec{
		target:      r.target,
		vars:        r.vars,
		url:         &urlClone,
		codec:       codec,
		contentType: contentType,
	}
	if meta.CompressionName != "" {
		comp, err := r.target.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = comp
	}
	return nil
}

func (r restClientProtocol) acceptsStreamType() bool {
	switch r.target.config.streamType {
	case connect.StreamTypeUnary:
		return true
	case connect.StreamTypeClient:
		return restIsHTTPBody(r.target.config.descriptor.Input(), r.target.requestBodyFields)
	case connect.StreamTypeServer:
		return restIsHTTPBody(r.target.config.descriptor.Output(), r.target.responseBodyFields)
	case connect.StreamTypeBidi:
		return false
	}
	return false
}

func (r restClientProtocol) PrepareRequestMessage(msg *messageBuffer, meta *requestMeta) error {
	msg.Src.IsCompressed = meta.Client.Compressor != nil
	msg.Src.IsTrailer = false
	msg.Src.ReadMode = readModeEOF
	if r.target.config.streamType&connect.StreamTypeClient != 0 {
		msg.Src.ReadMode = readModeChunk
	}
	return nil
}

func (r restClientProtocol) EncodeRequestHeader(meta *responseMeta) error {
	var baseCodec Codec
	if meta.CodecName == CodecJSON {
		codec, err := r.target.config.GetClientCodec(CodecJSON)
		if err != nil {
			return err
		}
		baseCodec = codec
		meta.Header.Set("Content-Type", "application/json")
	}
	meta.Client.Codec = &restResponseCodec{
		target: r.target,
		meta:   meta, // Encode URL values.
		codec:  baseCodec,
	}
	if meta.CompressionName != "" {
		comp, err := r.target.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = comp
		meta.Header.Set("Content-Encoding", meta.CompressionName)
		meta.Header.Set("Accept-Encoding", meta.CompressionName)
	}
	return nil
}

func (r restClientProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	msg.Dst.Flags = 0
	msg.Dst.IsEnvelope = false
	msg.Dst.IsCompressed = meta.Client.Compressor != nil

	// TODO: support httpBody compression
	isHTTPBody := restIsHTTPBody(r.target.config.descriptor.Input(), r.target.requestBodyFields)
	if isHTTPBody {
		// TODO: support full stream compression
		msg.Dst.IsCompressed = false
	}
	return nil
}

func (r restClientProtocol) EncodeResponseTrailer(_ *bytes.Buffer, _ *responseMeta) error {
	return nil
}

func (r restClientProtocol) EncodeError(buf *bytes.Buffer, meta *responseMeta, err error) {
	// Encode the error as uncompressed JSON.
	meta.Header.Del("Content-Encoding")
	meta.Header.Set("Content-Type", "application/json")

	cerr := asConnectError(err)
	stat := grpcStatusFromError(cerr)
	meta.StatusCode = httpStatusCodeFromRPC(cerr.Code())

	codec, err := r.target.config.GetClientCodec(CodecJSON)
	if err != nil {
		codec = DefaultJSONCodec(r.target.config.resolver)
	}
	if err = marshal(buf, stat, codec); err != nil {
		bin := []byte(`{"code": 13, "message": ` + strconv.Quote("failed to marshal end error: "+err.Error()) + `}`)
		_, _ = buf.Write(bin)
	}
}

// restServerProtocol implements the REST protocol for
// sending RPCs to the server handler.
type restServerProtocol struct {
	target *routeTarget

	// For error encoding.
	statusCode  int
	contentType string
}

var _ serverProtocolHandler = (*restServerProtocol)(nil)

func (r *restServerProtocol) Protocol() Protocol {
	return ProtocolREST
}

func (r *restServerProtocol) EncodeRequestHeader(meta *requestMeta) error {
	codec, err := r.target.config.GetServerCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Server.Codec = &restRequestCodec{
		target: r.target,
		url:    meta.URL,
		header: meta.Header,
		codec:  codec,
	}
	meta.Method = r.target.method

	// Encode header values for the request body.
	if r.target.requestBodyFieldPath != "" {
		// Content-Type may be overridden by the body preparer.
		meta.Header.Set("Content-Type", "application/"+meta.CodecName)

		if meta.CompressionName != "" {
			compressor, err := r.target.config.GetServerCompressor(meta.CompressionName)
			if err != nil {
				return err
			}
			meta.Server.Compressor = compressor
			meta.Header.Set("Content-Encoding", meta.CompressionName)
			meta.Header.Set("Accept-Encoding", meta.CompressionName)
		}
	}
	if meta.Timeout != 0 {
		// Encode timeout as a float in seconds.
		value := strconv.FormatFloat(meta.Timeout.Seconds(), 'E', -1, 64)
		meta.Header.Set("X-Server-Timeout", value)
	}
	// Require request body to encode URL values.
	meta.RequiresBody = true
	return nil
}

func (r *restServerProtocol) PrepareRequestMessage(msg *messageBuffer, meta *requestMeta) error {
	msg.Dst.IsCompressed = meta.Server.Compressor != nil
	return nil
}

func (r *restServerProtocol) DecodeRequestHeader(meta *responseMeta) error {
	r.statusCode = meta.StatusCode
	r.contentType = meta.Header.Get("Content-Type")
	meta.Header.Del("Content-Type")
	switch {
	case r.contentType == "application/json":
		meta.CodecName = CodecJSON
	case strings.HasPrefix(r.contentType, "application/"):
		meta.CodecName = strings.TrimPrefix(r.contentType, "application/")
		if n := strings.Index(meta.CodecName, ";"); n != -1 {
			meta.CodecName = meta.CodecName[:n]
		}
	}
	codec, err := r.target.config.GetServerCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Server.Codec = &restResponseCodec{
		target:      r.target,
		codec:       codec,
		meta:        meta,
		contentType: r.contentType,
	}

	meta.CompressionName = meta.Header.Get("Content-Encoding")
	meta.Header.Del("Content-Encoding")
	if meta.CompressionName != "" {
		compressor, err := r.target.config.GetServerCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Server.Compressor = compressor
	}

	meta.AcceptCompression = parseMultiHeader(meta.Header.Values("Accept-Encoding"))
	meta.Header.Del("Accept-Encoding")

	return nil
}

func (r *restServerProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	msg.Src.IsCompressed = meta.Server.Compressor != nil
	msg.Src.ReadMode = readModeEOF
	if r.target.config.streamType&connect.StreamTypeServer != 0 {
		msg.Src.ReadMode = readModeChunk
	}
	if r.statusCode/100 != 2 {
		msg.Src.IsTrailer = true
		msg.Src.ReadMode = readModeEOF
	}
	return nil
}

func (r *restServerProtocol) DecodeResponseTrailer(buf *bytes.Buffer, _ *responseMeta) error {
	if r.statusCode/100 == 2 {
		return nil
	}
	if cerr := httpErrorFromResponse(r.statusCode, r.contentType, buf); cerr != nil {
		return cerr
	}
	return nil
}

type restRequestCodec struct {
	target      *routeTarget
	vars        []routeTargetVarMatch
	codec       Codec
	url         *url.URL   // Clone or original URL.
	header      httpHeader // Empty or original header.
	contentType string
	count       int
}

func (c *restRequestCodec) Name() string {
	if c.codec == nil {
		return "rest"
	}
	return "rest-" + c.codec.Name()
}
func (c *restRequestCodec) MarshalAppend(dst []byte, msg proto.Message) ([]byte, error) {
	defer func() { c.count++ }()
	dst, err := c.marshalBody(dst, msg)
	if err != nil {
		return nil, err
	}
	if c.count > 0 {
		return dst, nil
	}
	path, query, err := httpEncodePathValues(msg.ProtoReflect(), c.target)
	if err != nil {
		return nil, err
	}
	c.url.Path = path
	c.url.RawQuery = query.Encode()
	return dst, nil
}
func (c *restRequestCodec) Unmarshal(src []byte, dst proto.Message) error {
	defer func() { c.count++ }()
	if err := c.unmarshalBody(src, dst); err != nil {
		return err
	}
	if c.count > 0 {
		return nil
	}
	// Now pull in the fields from the URI path:
	msg := dst.ProtoReflect()
	for i := len(c.vars) - 1; i >= 0; i-- {
		variable := c.vars[i]
		if err := setParameter(msg, variable.fields, variable.value); err != nil {
			return err
		}
	}
	// And finally from the query string:
	for fieldPath, values := range c.url.Query() {
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

func (c *restRequestCodec) marshalBody(dst []byte, src proto.Message) ([]byte, error) {
	if c.target.requestBodyFields == nil {
		return dst, nil
	}
	msg, leafField, err := getBodyField(c.target.requestBodyFields, src.ProtoReflect(), protoreflect.Message.Get)
	if err != nil {
		return nil, err
	}
	if leafField == nil && restIsHTTPBody(msg.Descriptor(), nil) {
		fields := msg.Descriptor().Fields()
		contentType := msg.Get(fields.ByName("content_type")).String()
		bytes := msg.Get(fields.ByName("data")).Bytes()
		if c.count == 0 && contentType != "" {
			c.header.Set("Content-Type", contentType)
		}
		return bytes, nil
	}
	if leafField == nil {
		return c.codec.MarshalAppend(dst, msg.Interface())
	}
	restCodec, err := asRESTCodec(c.codec)
	if err != nil {
		return nil, err
	}
	return restCodec.MarshalAppendField(dst, msg.Interface(), leafField)
}

func (c *restRequestCodec) unmarshalBody(src []byte, dst proto.Message) error {
	if c.target.requestBodyFields == nil {
		if len(src) > 0 {
			return fmt.Errorf("request should have no body; instead got %d bytes", len(src))
		}
		return nil
	}
	// Reset the message to clear any existing values, since we may only
	// be partially encoding it.
	proto.Reset(dst)

	msg, leafField, err := getBodyField(c.target.requestBodyFields, dst.ProtoReflect(), protoreflect.Message.Mutable)
	if err != nil {
		return err
	}

	if leafField == nil && restIsHTTPBody(msg.Descriptor(), nil) {
		fields := msg.Descriptor().Fields()
		if c.count == 0 {
			msg.Set(fields.ByName("content_type"), protoreflect.ValueOfString(c.contentType))
		}
		// Take ownership of the bytes.
		data := make([]byte, len(src))
		copy(data, src)
		msg.Set(fields.ByName("data"), protoreflect.ValueOfBytes(data))
		return nil
	}
	if leafField == nil {
		return c.codec.Unmarshal(src, msg.Interface())
	}
	restCodec, err := asRESTCodec(c.codec)
	if err != nil {
		return err
	}
	return restCodec.UnmarshalField(src, msg.Interface(), leafField)
}

type restResponseCodec struct {
	target      *routeTarget
	codec       Codec
	meta        *responseMeta
	contentType string
	count       int
}

func (c *restResponseCodec) Name() string {
	if c.codec == nil {
		return "rest"
	}
	return "rest-" + c.codec.Name()
}

func (c *restResponseCodec) MarshalAppend(dst []byte, src proto.Message) ([]byte, error) {
	defer func() { c.count++ }()
	msg, leafField, err := getBodyField(c.target.responseBodyFields, src.ProtoReflect(), protoreflect.Message.Get)
	if err != nil {
		return nil, err
	}
	if leafField == nil && restIsHTTPBody(msg.Descriptor(), nil) {
		if !msg.IsValid() {
			return dst, nil
		}
		fields := msg.Descriptor().Fields()
		dataField := fields.ByName("data")
		contentField := fields.ByName("content_type")
		contentType := msg.Get(contentField).String()
		bytes := msg.Get(dataField).Bytes()
		if c.count == 0 && contentType != "" {
			c.meta.Header.Set("Content-Type", contentType)
		}
		return bytes, nil
	}
	if leafField == nil {
		return c.codec.MarshalAppend(dst, msg.Interface())
	}
	restCodec, err := asRESTCodec(c.codec)
	if err != nil {
		return nil, err
	}
	return restCodec.MarshalAppendField(dst, msg.Interface(), leafField)
}
func (c *restResponseCodec) Unmarshal(src []byte, dst proto.Message) error {
	defer func() { c.count++ }()
	msg, leafField, err := getBodyField(c.target.responseBodyFields, dst.ProtoReflect(), protoreflect.Message.Mutable)
	if err != nil {
		return err
	}
	if leafField == nil && restIsHTTPBody(msg.Descriptor(), nil) {
		if len(src) == 0 {
			return nil
		}
		fields := msg.Descriptor().Fields()
		dataField := fields.ByName("data")
		contentField := fields.ByName("content_type")
		if c.count == 0 {
			msg.Set(contentField, protoreflect.ValueOfString(c.contentType))
		}
		// Take ownership of the bytes.
		data := make([]byte, len(src))
		copy(data, src)
		msg.Set(dataField, protoreflect.ValueOfBytes(data))
		return nil
	}
	if leafField == nil {
		return c.codec.Unmarshal(src, msg.Interface())
	}
	restCodec, err := asRESTCodec(c.codec)
	if err != nil {
		return err
	}
	return restCodec.UnmarshalField(src, msg.Interface(), leafField)
}

func asRESTCodec(codec Codec) (RESTCodec, error) {
	restCodec, ok := codec.(RESTCodec)
	if !ok {
		return nil, fmt.Errorf("codec %q (%T) does not implement RESTCodec", codec.Name(), codec)
	}
	return restCodec, nil
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
			// NB: This should not be possible since we validate the field types when
			//     we build the fields path, when methods are configured with the mux.
			//     This logic to construct an error is "just in case", like if we
			//     introduce a bug such that the validation fails to catch this.
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
