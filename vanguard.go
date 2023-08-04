// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Middleware is the signature for HTTP middleware, that can wrap/decorate an
// existing HTTP handler.
type Middleware func(http.Handler) http.Handler

type TypeResolver interface {
	protoregistry.MessageTypeResolver
	protoregistry.ExtensionTypeResolver
}

// Config controls the behavior of HTTP middleware.
type Config struct {
	// TypeResolver is the default TypeResolver. If no TypeResolver is specified
	// when a service is registered, this one is used. If nil, the default resolver
	// will be [protoregistry.GlobalTypes].
	TypeResolver TypeResolver

	codecs        map[string]connect.Codec
	compressors   map[string]func() connect.Compressor
	decompressors map[string]func() connect.Decompressor
	methods       map[protoreflect.FullName]*methodConfig
	restRoutes    routeTrie
}

// AsMiddleware returns HTTP middleware that applies the given configuration
// to handlers.
func (c *Config) AsMiddleware() (Middleware, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c.apply, nil
}

// Protocol represents an on-the-wire protocol for RPCs.
type Protocol int

const (
	ProtocolUnknown = Protocol(iota)
	ProtocolConnect
	ProtocolGRPC
	ProtocolGRPCWeb
	ProtocolREST
)

func (c *Config) validate() error {
	// TODO
	return nil
}

func (c *Config) apply(handler http.Handler) http.Handler {
	// TODO
	return handler
}

type methodConfig struct {
	descriptor                protoreflect.MethodDescriptor
	protocol                  Protocol
	codecName, compressorName string
}

type operation struct {
	method                            protoreflect.MethodDescriptor
	sourceProtocol, destProtocol      Protocol
	srcCodec, destCodec               connect.Codec
	srcCompressor, destCompressor     connect.Compressor
	srcDecompressor, destDecompressor connect.Decompressor
}
