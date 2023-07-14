// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type codec interface {
	MarshalAppend([]byte, proto.Message) ([]byte, error)
	Unmarshal([]byte, proto.Message) error
	Name() string
}

// codecProto is a Codec implementation with protobuf binary format.
type codecProto struct {
	proto.MarshalOptions
}

func (c codecProto) MarshalAppend(b []byte, m proto.Message) ([]byte, error) {
	return c.MarshalOptions.MarshalAppend(b, m)
}

func (codecProto) Unmarshal(data []byte, m proto.Message) error {
	return proto.Unmarshal(data, m)
}

func (codecProto) Name() string { return "proto" }

// codecJSON is a Codec implementation with protobuf json format.
type codecJSON struct {
	protojson.MarshalOptions
	protojson.UnmarshalOptions
}

func (c codecJSON) MarshalAppend(b []byte, m proto.Message) ([]byte, error) {
	return c.MarshalOptions.MarshalAppend(b, m)
}

func (c codecJSON) Unmarshal(data []byte, m proto.Message) error {
	return c.UnmarshalOptions.Unmarshal(data, m)
}

func (codecJSON) Name() string { return "json" }
