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
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// httpBodyTypeName is the fully-qualified name of google.api.HttpBody.
// Methods whose request or response field resolves to HttpBody bypass
// codec encoding and pass body bytes through with the recorded
// Content-Type.
const httpBodyTypeName = "google.api.HttpBody"

// bodyChunkSize caps each google.api.HttpBody message carved from a
// streamed HTTP body.
const bodyChunkSize = 32 * 1024

// decodeRequestURL merges path variables and query parameters into msg.
func decodeRequestURL(
	request *http.Request,
	vars []routeTargetVarMatch,
	opts *options,
	msg proto.Message,
) error {
	// Path variables (in reverse so earlier matches stay authoritative
	// after later ones are set — mirrors the legacy ordering).
	mreflect := msg.ProtoReflect()
	for _, v := range slices.Backward(vars) {
		if err := setParameter(mreflect, v.fields, v.value); err != nil {
			return fmt.Errorf("path variable: %w", err)
		}
	}
	// Query parameters.
	for fieldPath, values := range request.URL.Query() {
		fields, err := resolvePathToFieldDescriptors(mreflect.Descriptor(), fieldPath, true)
		if err != nil {
			if opts.discardUnknownQueryParams && errors.Is(err, errUnknownField) {
				continue
			}
			return fmt.Errorf("query parameter %q: %w", fieldPath, err)
		}
		for _, value := range values {
			if err := setParameter(mreflect, fields, value); err != nil {
				return fmt.Errorf("query parameter %q: %w", fieldPath, err)
			}
		}
	}
	return nil
}

// readChunk returns the next slice of body, or io.EOF once it is exhausted.
// A fresh buffer per chunk, since the message retains it.
func readChunk(body io.Reader) ([]byte, error) {
	buf := make([]byte, bodyChunkSize)
	size, err := io.ReadFull(body, buf)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return buf[:size], nil
}

// setBodyHTTPBody stores data and contentType in the google.api.HttpBody
// that fields select on msg.
func setBodyHTTPBody(fields []protoreflect.FieldDescriptor, msg proto.Message, contentType string, data []byte) error {
	host, _, err := walkBodyFields(fields, msg.ProtoReflect(), protoreflect.Message.Mutable)
	if err != nil {
		return err
	}
	setHTTPBody(host, contentType, data)
	return nil
}

// decodeBody reads a REST body into msg along fields (nil for the whole
// message). The codec decides what an empty body means.
func decodeBody(
	ctx context.Context,
	body io.Reader,
	contentType string,
	fields []protoreflect.FieldDescriptor,
	codec RESTCodec,
	msg proto.Message,
) error {
	host, leaf, err := walkBodyFields(fields, msg.ProtoReflect(), protoreflect.Message.Mutable)
	if err != nil {
		return err
	}
	if leaf == nil && isHTTPBody(host.Descriptor()) {
		data, err := io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}
		setHTTPBody(host, contentType, data)
		return nil
	}
	if leaf == nil {
		return codec.UnmarshalRead(ctx, body, host.Interface())
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	return codec.UnmarshalField(ctx, data, host.Interface(), leaf)
}

// bodyContentType returns the Content-Type for the part of msg selected by
// fields: a google.api.HttpBody's own content_type, otherwise the codec's.
func bodyContentType(fields []protoreflect.FieldDescriptor, msg proto.Message, codec RESTCodec) string {
	host, leaf, err := walkBodyFields(fields, msg.ProtoReflect(), protoreflect.Message.Get)
	if err == nil && leaf == nil && isHTTPBody(host.Descriptor()) {
		return host.Get(host.Descriptor().Fields().ByName("content_type")).String()
	}
	return "application/" + codec.Name()
}

// encodeBody writes the part of msg selected by fields (nil for the whole
// message) to writer. A google.api.HttpBody's data is written verbatim.
func encodeBody(
	ctx context.Context,
	writer io.Writer,
	fields []protoreflect.FieldDescriptor,
	msg proto.Message,
	codec RESTCodec,
) error {
	host, leaf, err := walkBodyFields(fields, msg.ProtoReflect(), protoreflect.Message.Get)
	if err != nil {
		return err
	}
	if leaf == nil && isHTTPBody(host.Descriptor()) {
		_, err := writer.Write(host.Get(host.Descriptor().Fields().ByName("data")).Bytes())
		return err
	}
	if leaf == nil {
		return codec.MarshalWrite(ctx, writer, host.Interface())
	}
	data, err := codec.MarshalAppendField(ctx, nil, host.Interface(), leaf)
	if err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

// isHTTPBodyRequest reports whether the resolved request body field is
// a google.api.HttpBody.
func isHTTPBodyRequest(target *routeTarget, msgDesc protoreflect.MessageDescriptor) bool {
	return target.requestBodyFields != nil && isHTTPBodyPath(target.requestBodyFields, msgDesc)
}

// isHTTPBodyResponse reports whether the resolved response body field
// is a google.api.HttpBody.
func isHTTPBodyResponse(target *routeTarget, msgDesc protoreflect.MessageDescriptor) bool {
	return isHTTPBodyPath(target.responseBodyFields, msgDesc)
}

// isHTTPBodyPath reports whether fields (empty for the whole message)
// resolves to a google.api.HttpBody.
func isHTTPBodyPath(fields []protoreflect.FieldDescriptor, msgDesc protoreflect.MessageDescriptor) bool {
	if len(fields) == 0 {
		return msgDesc.FullName() == httpBodyTypeName
	}
	last := fields[len(fields)-1]
	if last.IsList() || last.IsMap() {
		return false
	}
	if m := last.Message(); m != nil {
		return m.FullName() == httpBodyTypeName
	}
	return false
}

func isHTTPBody(msgDesc protoreflect.MessageDescriptor) bool {
	return msgDesc != nil && msgDesc.FullName() == httpBodyTypeName
}

func setHTTPBody(msg protoreflect.Message, contentType string, data []byte) {
	desc := msg.Descriptor()
	msg.Set(desc.Fields().ByName("content_type"), protoreflect.ValueOfString(contentType))
	msg.Set(desc.Fields().ByName("data"), protoreflect.ValueOfBytes(data))
}

// walkBodyFields descends fields (e.g. the body path "a.b.c"), using
// acc to access intermediate fields (Mutable for decode, Get for
// encode). The terminal field — if it's a scalar or repeated message —
// is returned as leaf; otherwise leaf is nil and host is the leaf
// message itself.
func walkBodyFields(
	fields []protoreflect.FieldDescriptor,
	root protoreflect.Message,
	acc func(protoreflect.Message, protoreflect.FieldDescriptor) protoreflect.Value,
) (host protoreflect.Message, leaf protoreflect.FieldDescriptor, err error) {
	host = root
	for i, field := range fields {
		if field.Message() != nil && field.Cardinality() != protoreflect.Repeated {
			host = acc(host, field).Message()
			continue
		}
		if i != len(fields)-1 {
			return nil, nil, fmt.Errorf("field %s of %s is not a singular message", field.Name(), host.Descriptor().FullName())
		}
		leaf = field
	}
	return host, leaf, nil
}
