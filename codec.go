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
	"encoding/json"
	"fmt"
	"strconv"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Codec is a message encoding format. It handles unmarshalling
// messages from bytes and back.
type Codec interface {
	Name() string
	MarshalAppend(b []byte, msg proto.Message) ([]byte, error)
	Unmarshal(data []byte, msg proto.Message) error
}

// StableCodec is an encoding format that can produce stable, deterministic
// output when marshalling data. This stable form is the result of the
// MarshalAppendStable method. So the codec's MarshalAppend method is
// free to produce unstable/non-deterministic output, if useful for
// improved performance. The performance penalty of stable output will
// only be taken when necessary.
//
// This is used to encode messages that end up in the URL query string,
// for the Connect protocol when unary methods use the HTTP GET method.
// If the codec in use does not implement StableCodec then HTTP GET
// methods will not be used; a Mux will send all unary RPCs that use the
// Connect protocol and that codec as POST requests.
type StableCodec interface {
	Codec
	MarshalAppendStable(b []byte, msg proto.Message) ([]byte, error)
	// IsBinary returns true for non-text formats. This is used to decide
	// whether the message query string parameter should be base64-encoded.
	IsBinary() bool
}

// RESTCodec is a Codec with additional methods for marshalling and unmarshalling
// individual fields of a message. This is necessary to support query string
// variables and request and response bodies whose value is a specific field, not
// an entire message. These methods are only used by the REST protocol.
type RESTCodec interface {
	Codec
	MarshalAppendField(b []byte, msg proto.Message, field protoreflect.FieldDescriptor) ([]byte, error)
	UnmarshalField(data []byte, msg proto.Message, field protoreflect.FieldDescriptor) error
}

// DefaultProtoCodec is the default codec factory used for
// the codec name "proto". The given resolver is used to
// unmarshal extensions.
func DefaultProtoCodec(res TypeResolver) Codec {
	return &protoCodec{Resolver: res}
}

// DefaultJSONCodec is the default codec factory used for
// the codec name "json". The given resolve is used to
// unmarshal extensions and also to marshal and unmarshal
// instances of google.protobuf.Any.
func DefaultJSONCodec(res TypeResolver) Codec {
	return &jsonCodec{
		m: protojson.MarshalOptions{Resolver: res, EmitUnpopulated: true},
		u: protojson.UnmarshalOptions{Resolver: res, DiscardUnknown: true},
	}
}

type protoCodec proto.UnmarshalOptions

var _ StableCodec = (*protoCodec)(nil)

func (p *protoCodec) Name() string {
	return CodecProto
}

func (p *protoCodec) IsBinary() bool {
	return true
}

func (p *protoCodec) MarshalAppend(base []byte, msg proto.Message) ([]byte, error) {
	return proto.MarshalOptions{}.MarshalAppend(base, msg)
}

func (p *protoCodec) MarshalAppendStable(base []byte, msg proto.Message) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.MarshalAppend(base, msg)
}

func (p *protoCodec) Unmarshal(bytes []byte, msg proto.Message) error {
	return (*proto.UnmarshalOptions)(p).Unmarshal(bytes, msg)
}

type jsonCodec struct {
	m protojson.MarshalOptions
	u protojson.UnmarshalOptions
}

var _ StableCodec = (*jsonCodec)(nil)
var _ RESTCodec = (*jsonCodec)(nil)

func (p *jsonCodec) Name() string {
	return CodecJSON
}

func (p *jsonCodec) IsBinary() bool {
	return false
}

func (p *jsonCodec) MarshalAppend(base []byte, msg proto.Message) ([]byte, error) {
	return p.m.MarshalAppend(base, msg)
}

func (p *jsonCodec) MarshalAppendStable(base []byte, msg proto.Message) ([]byte, error) {
	data, err := p.m.MarshalAppend(base, msg)
	if err != nil {
		return nil, err
	}
	return jsonStabilize(data)
}

func (p *jsonCodec) MarshalAppendField(base []byte, msg proto.Message, field protoreflect.FieldDescriptor) ([]byte, error) {
	opts := p.m                // copy marshal options
	p.m.EmitUnpopulated = true // so we can make sure that default/zero values get marshalled
	msgReflect := msg.ProtoReflect()
	if !msgReflect.Has(field) && field.ContainingOneof() != nil {
		// If the field is part of a oneof and absent, it will not be
		// emitted, even with EmitUnpopulated set to true. So we set
		// it to its default to force serialization.
		// NB: At this point in a request flow, we should have already used the message
		//     to populate the URI path and query string, so it should be safe to mutate
		//     it. In the response flow, nothing looks at the message except the
		//     marshalling step. So, again, mutation should be okay.
		msgReflect.Set(field, msgReflect.Get(field))
	}
	wholeMessage, err := opts.MarshalAppend(base, msg)
	if err != nil {
		return nil, err
	}

	// NB: Do we need another overload, like MarshalAppendFieldStable?? For now, we'll
	//     just always use stable marshalling with fields. We want to do this in tests
	//     for determinism. And we want to do this for query string params, for
	//     stable cache keys in the event that responses are cacheable.
	wholeMessage, err = jsonStabilize(wholeMessage)
	if err != nil {
		return nil, err
	}

	// We have to dig a repeated field out of the message we just marshalled.
	var val map[string]json.RawMessage
	if err := json.Unmarshal(wholeMessage, &val); err != nil {
		return nil, err
	}
	result, ok := val[p.fieldName(field)]
	if ok {
		return result, nil
	}
	// Nothing in the serialized form. Unclear under what conditions this may happen
	// (maybe it can't happen?). In case it does, we'll emit a zero/empty value.
	switch {
	case field.IsList():
		return append(base, '[', ']'), nil
	case field.IsMap():
		return append(base, '{', '}'), nil
	default:
		switch field.Kind() {
		case protoreflect.MessageKind, protoreflect.GroupKind:
			return append(base, 'n', 'u', 'l', 'l'), nil
		case protoreflect.BoolKind:
			return append(base, 'f', 'a', 'l', 's', 'e'), nil
		case protoreflect.StringKind, protoreflect.BytesKind:
			return append(base, '"', '"'), nil
		case protoreflect.Int64Kind, protoreflect.Uint64Kind, protoreflect.Sint64Kind,
			protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
			return append(base, '"', '0', '"'), nil
		case protoreflect.Int32Kind, protoreflect.Uint32Kind, protoreflect.Sint32Kind,
			protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind,
			protoreflect.FloatKind, protoreflect.DoubleKind:
			return append(base, '0'), nil
		case protoreflect.EnumKind:
			defaultEnumVal := field.Enum().Values().Get(0)
			if opts.UseEnumNumbers {
				return strconv.AppendInt(base, int64(defaultEnumVal.Number()), 10), nil
			}
			return strconv.AppendQuote(base, string(defaultEnumVal.Name())), nil
		default:
			return nil, fmt.Errorf("unknown kind %v", field.Kind())
		}
	}
}

func (p *jsonCodec) UnmarshalField(data []byte, msg proto.Message, field protoreflect.FieldDescriptor) error {
	// It would be nice if we could weave a bufferPool to here...
	fieldName := p.fieldName(field)
	buf := bytes.NewBuffer(make([]byte, 0, len(fieldName)+len(data)+3))
	buf.WriteByte('{')
	if err := json.NewEncoder(buf).Encode(fieldName); err != nil {
		return err
	}
	buf.WriteByte(':')
	buf.Write(data)
	buf.WriteByte('}')
	return p.Unmarshal(buf.Bytes(), msg)
}

func (p *jsonCodec) Unmarshal(bytes []byte, msg proto.Message) error {
	return p.u.Unmarshal(bytes, msg)
}

func (p *jsonCodec) fieldName(field protoreflect.FieldDescriptor) string {
	if !p.m.UseProtoNames {
		return field.JSONName()
	}
	if field.IsExtension() {
		// unlikely...
		return "[" + string(field.FullName()) + "]"
	}
	return string(field.Name())
}

func jsonStabilize(data []byte) ([]byte, error) {
	// NB: Because json.Compact only removes whitespace, never elongating data, it is
	//     safe to use the same backing slice as source and destination. This is safe
	//     for the same reason that copy is safe even when the two slices overlap.
	buf := bytes.NewBuffer(data[:0])
	if err := json.Compact(buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
