// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"io"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type convertKey struct {
	src protocol
	dst protocol
}

type converter interface {
	Upstream() protocol
	Downstream() protocol
	DecodeHeader(requestHeader) error
	EncodeHeader(io.Writer, responseHeader) error
	EncodeTrailer(io.Writer, responseHeader) error
	//EncodeError(io.Writer, responseHeader, error) error
}

func convert(mux *Mux, src, dst protocol) (converter, error) {
	x := convertKey{src: src, dst: dst}
	switch x {
	//case convertKey{protocolGRPCWeb, protocolGRPC}:
	//	return convertGRPCWebToGRPC{}, nil
	case convertKey{protocolHTTPRule, protocolGRPC}:
		return convertHTTPToGRPC{Mux: mux}, nil
	}
	return nil, errUnsupportedProtocolConversion(src, dst)
}

type convertGRPCWebToGRPC struct{}

func (c convertGRPCWebToGRPC) DecodeHeader(header header) {
	codecName := "proto"
	if contentType, ok := header.Get("Content-Type"); ok {
		_, part, ok := strings.Cut(contentType, "+")
		if ok {
			codecName = part
		}
	}
	header.Set("Content-Type", "application/grpc+"+codecName)

}
func (c convertGRPCWebToGRPC) EncodeHeader(header header) {
	codecName := "proto"
	if contentType, ok := header.Get("Content-Type"); ok {
		_, part, ok := strings.Cut(contentType, "+")
		if ok {
			codecName = part
		}
	}
	header.Set("Content-Type", "application/grpc+"+codecName)
}
func (c convertGRPCWebToGRPC) EncodeTrailer(header header) {
	// TODO: delete trailers...
}

// Upstream decodes the recv message and encodes the send message.
// Downstream encodes the recv message and decodes the send message.
//
//	|  upstream | filter | downstream |
//	|-----------|--------|------------|
//	|    dec->  |  recv  |    enc->   |
//	|  <-enc    |  send  |  <-dec     |

type msgFunc func(proto.Message) error

type upstream interface {
	Protocol() protocol
	DecodeHeader(requestHeader) error
	DecodeMessage([]byte, proto.Message) error
	EncodeHeader(responseHeader) error
	EncodeMessage([]byte, proto.Message) ([]byte, error)
	EncodeTrailer(header) error
	EncodeError(io.Writer, responseHeader, error)
}
type downstream interface {
	Protocol() protocol
	EncodeHeader(requestHeader) error
	EncodeMessage([]byte, proto.Message) ([]byte, error)
	DecodeHeader(responseHeader) error
	DecodeMessage([]byte, proto.Message) error
	DecodeTrailer(header) error
}

type chunkstreamer struct {
	up    upstream
	down  downstream
	desc  protoreflect.MethodDescriptor
	args  proto.Message // lazy
	reply proto.Message // lazy
	onMsg msgFunc       // optional
}

func (c *chunkstreamer) getArgs() proto.Message {
	if c.args == nil {
		argsDesc := c.desc.Input()
		c.args = dynamicpb.NewMessage(argsDesc)
	}
	return c.args
}
func (c *chunkstreamer) getReply() proto.Message {
	if c.reply == nil {
		replyDesc := c.desc.Output()
		c.reply = dynamicpb.NewMessage(replyDesc)
	}
	return c.reply
}

func (c *chunkstreamer) Decode(b []byte) ([]byte, error) {
	msg := c.getArgs()
	defer proto.Reset(msg) // ?

	if err := c.up.DecodeMessage(b, msg); err != nil {
		return nil, err
	}
	if c.onMsg != nil {
		if err := c.onMsg(msg); err != nil {
			return nil, err
		}
	}
	return c.down.EncodeMessage(b[:0], msg)
}
func (c *chunkstreamer) Encode(b []byte) ([]byte, error) {
	msg := c.getReply()
	defer proto.Reset(msg) // needed?

	if err := c.down.DecodeMessage(b, msg); err != nil {
		return nil, err
	}
	if c.onMsg != nil {
		if err := c.onMsg(msg); err != nil {
			return nil, err
		}
	}
	return c.up.EncodeMessage(b[:0], msg)
}
