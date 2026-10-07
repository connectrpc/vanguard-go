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
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// RESTCodec extends [connect.Codec] with methods for marshaling and
// unmarshaling individual message fields. This supports query string variables
// and body mappings that target specific fields rather than the entire message.
// These extra methods are only used by the REST protocol.
type RESTCodec interface {
	connect.Codec

	// MarshalAppendField marshals just the given field of the given message to
	// bytes, and appends it to the given base byte slice.
	MarshalAppendField(ctx context.Context, base []byte, msg proto.Message, field protoreflect.FieldDescriptor) ([]byte, error)
	// UnmarshalField unmarshals the given data into the given field of the given
	// message.
	UnmarshalField(ctx context.Context, data []byte, msg proto.Message, field protoreflect.FieldDescriptor) error
}

// JSONCodec implements [connect.Codec], [connect.StableCodec], and
// [RESTCodec] for the JSON format. It embeds [connectproto.JSONCodec] for its
// implementation.
type JSONCodec struct {
	connectproto.JSONCodec
}

var _ connect.StableCodec = (*JSONCodec)(nil)
var _ RESTCodec = (*JSONCodec)(nil)

// NewJSONCodec is the default codec factory for the "json" codec. The provided
// resolver is used to unmarshal extensions and to marshal/unmarshal instances
// of google.protobuf.Any.
//
// By default, the returned codec emits unpopulated fields when marshaling and
// discards unknown fields when unmarshaling.
func NewJSONCodec(res connectproto.TypeResolver) *JSONCodec {
	codec := connectproto.NewJSONCodec(connectproto.WithTypeResolver(res))
	codec.MarshalOptions.EmitUnpopulated = true
	return &JSONCodec{JSONCodec: *codec}
}

// MarshalAppendField implements [RESTCodec].
func (j *JSONCodec) MarshalAppendField(_ context.Context, base []byte, msg proto.Message, field protoreflect.FieldDescriptor) ([]byte, error) {
	if field.Message() != nil && field.Cardinality() != protoreflect.Repeated {
		return j.MarshalOptions.MarshalAppend(base, msg.ProtoReflect().Get(field).Message().Interface())
	}
	opts := j.MarshalOptions // copy marshal options, so we might modify them
	msgReflect := msg.ProtoReflect()
	if !msgReflect.Has(field) {
		if field.HasPresence() {
			// At this point in a request flow, we should have already used the message
			// to populate the URI path and query string, so it should be safe to mutate
			// it. In the response flow, nothing looks at the message except the
			// marshalling step. So, again, mutation should be okay.
			msgReflect.Set(field, msgReflect.Get(field))
		} else {
			// Setting the field (like above) won't help due to implicit presence.
			// So instead, force the default value to be marshalled.
			opts.EmitUnpopulated = true
		}
	}

	// We could possibly manually perform the marshaling, but that is
	// a decent bit of protojson to reproduce (lot of new code to test
	// and to maintain) and risks inadvertently diverging from protojson.
	wholeMessage, err := opts.MarshalAppend(base, msg)
	if err != nil {
		return nil, err
	}

	// We have to dig a repeated field out of the message we just marshalled.
	dec := json.NewDecoder(bytes.NewReader(wholeMessage))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok != json.Delim('{') {
		return nil, fmt.Errorf("JSON should be an object and begin with '{'; instead got %v", tok)
	}
	fieldName := j.fieldName(field)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("JSON object key should be a string; instead got %T", keyTok)
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		if key == fieldName {
			return val, nil
		}
	}
	return nil, fmt.Errorf("JSON does not contain key %s", fieldName)
}

// UnmarshalField implements [RESTCodec].
func (j *JSONCodec) UnmarshalField(_ context.Context, data []byte, msg proto.Message, field protoreflect.FieldDescriptor) error {
	if field.Message() != nil && field.Cardinality() != protoreflect.Repeated {
		return j.UnmarshalOptions.Unmarshal(data, msg.ProtoReflect().Mutable(field).Message().Interface())
	}
	// It would be nice if we could weave a bufferPool to here...
	fieldName := j.fieldName(field)
	buf := bytes.NewBuffer(make([]byte, 0, len(fieldName)+len(data)+3))
	buf.WriteByte('{')
	if err := json.NewEncoder(buf).Encode(fieldName); err != nil {
		return err
	}
	buf.WriteByte(':')
	buf.Write(data)
	buf.WriteByte('}')
	// We could possibly manually perform the unmarshaling, but that is
	// a decent bit of protojson to reproduce (lot of new code to test
	// and to maintain) and risks inadvertently diverging from protojson.
	return j.UnmarshalOptions.Unmarshal(buf.Bytes(), msg)
}

func (j *JSONCodec) fieldName(field protoreflect.FieldDescriptor) string {
	if !j.MarshalOptions.UseProtoNames {
		return field.JSONName()
	}
	if field.IsExtension() {
		// unlikely...
		return "[" + string(field.FullName()) + "]"
	}
	return string(field.Name())
}
