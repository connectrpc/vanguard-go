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

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Codec is a message encoding format. It handles unmarshalling
// messages from bytes and back.
type Codec interface {
	// Name returns the name of this codec. This is used in content-type
	// strings to indicate this codec in the various RPC protocols.
	Name() string
	// MarshalAppend marshals the given message to bytes, appended to the
	// given base byte slice. The given slice may be empty, but its
	// capacity should be used when marshalling to bytes to reduce
	// additional allocations.
	MarshalAppend(base []byte, msg proto.Message) ([]byte, error)
	// Unmarshal unmarshals the given data into the given target message.
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
	// MarshalAppendStable is the same as MarshalAppend except that the
	// bytes produced must be deterministic and stable. Ideally, the
	// produced bytes represent a *canonical* encoding. But this is not
	// required as many codecs (including binary Protobuf and JSON) do
	// not have a well-defined canonical encoding format.
	MarshalAppendStable(b []byte, msg proto.Message) ([]byte, error)
	// IsBinary returns true for non-text formats. This is used to decide
	// whether the message query string parameter should be base64-encoded.
	IsBinary() bool
}

// RESTCodec is a Codec with additional methods for marshalling and unmarshalling
// individual fields of a message. This is necessary to support query string
// variables and request and response bodies whose value is a specific field, not
// an entire message. The extra methods are only used by the REST protocol.
type RESTCodec interface {
	Codec
	// MarshalAppendField marshals just the given field of the given message to
	// bytes, and appends it to the given base byte slice.
	MarshalAppendField(base []byte, msg proto.Message, field protoreflect.FieldDescriptor) ([]byte, error)
	// UnmarshalField unmarshals the given data into the given field of the given
	// message.
	UnmarshalField(data []byte, msg proto.Message, field protoreflect.FieldDescriptor) error
}

// DefaultProtoCodec is the default codec factory used for
// the codec name "proto". The given resolver is used to
// unmarshal extensions.
//
// The returned codec implements StableCodec, in addition to
// Codec.
func DefaultProtoCodec(res TypeResolver) Codec {
	return &protoCodec{Resolver: res}
}

// DefaultJSONCodec is the default codec factory used for the codec named
// "json". The given resolver is used to unmarshal extensions and also to
// marshal and unmarshal instances of google.protobuf.Any.
//
// The returned codec implements StableCodec and RESTCodec, in addition to
// Codec.
func DefaultJSONCodec(res TypeResolver) Codec {
	return NewJSONCodec(
		protojson.MarshalOptions{Resolver: res, EmitUnpopulated: true},
		protojson.UnmarshalOptions{Resolver: res, DiscardUnknown: true},
	)
}

// NewJSONCodec creates a new Codec for the JSON format that uses the given
// marshal and unmarshal options. This can be used to create JSON codecs
// with behavior that differs from the DefaultJSONCodec.
//
// The returned codec implements StableCodec and RESTCodec, in addition to
// Codec.
func NewJSONCodec(marshalOpts protojson.MarshalOptions, unmarshalOpts protojson.UnmarshalOptions) Codec {
	return &jsonCodec{m: marshalOpts, u: unmarshalOpts}
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
	if field.Message() != nil && field.Cardinality() != protoreflect.Repeated {
		return p.MarshalAppend(base, msg.ProtoReflect().Get(field).Message().Interface())
	}
	opts := p.m // copy marshal options, so we might modify them
	msgReflect := msg.ProtoReflect()
	if !msgReflect.Has(field) {
		if field.HasPresence() {
			// NB: At this point in a request flow, we should have already used the message
			//     to populate the URI path and query string, so it should be safe to mutate
			//     it. In the response flow, nothing looks at the message except the
			//     marshalling step. So, again, mutation should be okay.
			msgReflect.Set(field, msgReflect.Get(field))
		} else {
			// Setting the field (like above) won't help due to implicit presence.
			// So instead, force the default value to be marshalled.
			opts.EmitUnpopulated = true
		}
	}

	// NB: We could possibly manually perform the marshaling, but that is
	//     a decent bit of protojson to reproduce (lot of new code to test
	//     and to maintain) and risks inadvertently diverging from protojson.
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
	fieldName := p.fieldName(field)
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

func (p *jsonCodec) UnmarshalField(data []byte, msg proto.Message, field protoreflect.FieldDescriptor) error {
	if field.Message() != nil && field.Cardinality() != protoreflect.Repeated {
		return p.Unmarshal(data, msg.ProtoReflect().Mutable(field).Message().Interface())
	}
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
	// NB: We could possibly manually perform the unmarshaling, but that is
	//     a decent bit of protojson to reproduce (lot of new code to test
	//     and to maintain) and risks inadvertently diverging from protojson.
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
