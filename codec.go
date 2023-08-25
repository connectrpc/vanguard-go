// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Codec is a message encoding format. It handles unmarshalling
// messages from bytes and back.
type Codec interface {
	Name() string
	MarshalAppend(b []byte, msg proto.Message) ([]byte, error)
	Unmarshal(bytes []byte, msg proto.Message) error
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

func (p *protoCodec) Name() string {
	return CodecProto
}

func (p *protoCodec) MarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	return proto.MarshalOptions{}.MarshalAppend(b, msg)
}

func (p *protoCodec) StableMarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.MarshalAppend(b, msg)
}

func (p *protoCodec) Unmarshal(bytes []byte, msg proto.Message) error {
	return (*proto.UnmarshalOptions)(p).Unmarshal(bytes, msg)
}

type jsonCodec struct {
	m protojson.MarshalOptions
	u protojson.UnmarshalOptions
}

func (p *jsonCodec) Name() string {
	return CodecJSON
}

func (p *jsonCodec) MarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	return p.m.MarshalAppend(b, msg)
}

func (p *jsonCodec) StableMarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	data, err := p.m.MarshalAppend(b, msg)
	if err != nil {
		return nil, err
	}
	// NB: Because json.Compact only removes whitespace, never elongating data, it is
	//     safe to use the same backing slice as source and destination. This is safe
	//     for the same reason that copy is safe even when the two slices overlap.
	// TODO: How to better verify the above? Should we instead inject a bufferPool
	//       and just allocate a whole new buffer, for more assured safety?
	buf := bytes.NewBuffer(data[:0])
	if err := json.Compact(buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (p *jsonCodec) Unmarshal(bytes []byte, msg proto.Message) error {
	return p.u.Unmarshal(bytes, msg)
}
