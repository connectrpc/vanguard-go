// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Codec is a message encoding format. It handles unmarshalling
// messages from bytes and back.
type Codec interface {
	Marshal(msg proto.Message) ([]byte, error)
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

func (p *protoCodec) Marshal(msg proto.Message) ([]byte, error) {
	return proto.Marshal(msg)
}

func (p *protoCodec) Unmarshal(bytes []byte, msg proto.Message) error {
	return (*proto.UnmarshalOptions)(p).Unmarshal(bytes, msg)
}

type jsonCodec struct {
	m protojson.MarshalOptions
	u protojson.UnmarshalOptions
}

func (p *jsonCodec) Marshal(msg proto.Message) ([]byte, error) {
	return p.m.Marshal(msg)
}

func (p *jsonCodec) Unmarshal(bytes []byte, msg proto.Message) error {
	return p.u.Unmarshal(bytes, msg)
}
