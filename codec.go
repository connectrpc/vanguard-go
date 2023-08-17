// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"

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

func (p *jsonCodec) Unmarshal(bytes []byte, msg proto.Message) error {
	return p.u.Unmarshal(bytes, msg)
}

func marshal(dst *bytes.Buffer, msg proto.Message, codec Codec) error {
	raw, err := codec.MarshalAppend(dst.Bytes(), msg)
	if err != nil {
		return err
	}
	if cap(raw) > dst.Cap() {
		// Dst buffer was too small, so MarshalAppend grew the slice.
		// Replace the buffer with the larger, newly-allocated slice.
		*dst = *bytes.NewBuffer(raw)
	} else {
		// The buffer from the pool was large enough, MarshalAppend didn't allocate.
		// Copy to the same byte slice is a nop.
		dst.Write(raw[dst.Len():])
	}
	return nil
}
