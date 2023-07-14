// Copyright 2023 Buf Technologies, Inc.

package vanguard

import "google.golang.org/protobuf/proto"

type filter interface {
	RecvHeaders(Header)
	RecvMsg(proto.Message) error
	SendHeaders(Header)
	SendMsg(proto.Message) error
	SendTrailers(Header)
}

type Header interface {
	Get(string) string
	Set(string, string)
	Del(string)
}
