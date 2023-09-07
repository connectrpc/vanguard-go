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
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

const (
	CompressionGzip     = "gzip"
	CompressionIdentity = "identity"
	// TODO: Connect protocol spec also references "br" (Brotli) and "zstd". And gRPC
	//       protocol spec references "deflate" and "snappy". Should we also support
	//       those out of the box?

	CodecProto = "proto"
	CodecJSON  = "json"
	// TODO: Some grpc impls support "text" out of the box (but not JSON, ironically).
	//       such as the JS impl. Should we also support it out of the box?

	DefaultMaxMessageBufferBytes = math.MaxUint32
	DefaultMaxGetURLBytes        = 8 * 1024
)

// Mux is a registry of RPC handlers that can handle transforming requests
// between RPC protocols (such as Connect and gRPC) or even between REST
// and RPC (using annotations on the service that define its mapping to
// REST).
//
// All services should be registered (via Register* methods) from a single
// thread during initialization. The handler returned from the AsHandler
// method is only thread-safe among concurrently executing HTTP requests. It
// is not safe to mutate the Mux once its handler is being used by a server.
type Mux struct {
	// The protocols that are supported by the wrapped handler, by default.
	// This can be overridden on a per-service level via options when calling
	// RegisterService or RegisterServiceByName.
	//
	// If left empty, the default is to assume the handler can handle all
	// three of ProtocolConnect, ProtocolGRPC, and ProtocolGRPCWeb.
	//
	// If the wrapped handler is a Connect handler, it can handle all three.
	// If the wrapped handler is a gRPC handler, it can only handle ProtocolGRPC.
	// If the wrapped handler is a reverse proxy, this should be configured based
	// on what protocols the destination server(s) support.
	Protocols []Protocol
	// The codec names that are supported by the wrapped handler, by default.
	// This can be overridden on a per-service level via options when calling
	// RegisterService or RegisterServiceByName.
	//
	// If this includes any non-default codec names, you must also call AddCodec
	// to register the codec implementation.
	//
	// If left empty, the default is to support both CodecProto and CodecJSON. Both
	// Connect and gRPC handlers can support custom codecs. Without customization,
	// Connect handlers support both CodecProto and CodecJSON while gRPC handlers
	// support only CodecProto.
	//
	// If the wrapped handler is a reverse proxy, this should be configured based
	// on what codecs (aka "sub-formats") that the destination server(s) support.
	Codecs []string
	// The names of compression algorithms that are supported by the wrapped handler,
	// by default. This can be overridden on a per-service level via options when
	// calling RegisterService or RegisterServiceByName.
	//
	// If this includes any non-default compression names, you must also call
	// AddCompression to register the implementation.
	//
	// If left nil, the default is to support CompressionGzip. Both Connect and
	// gRPC handlers can support custom compression algorithms. Without customization,
	// both support CompressionGzip.
	//
	// If the wrapped handler is a reverse proxy, this should be configured based
	// on what codecs (aka "sub-formats") that the destination server(s) support.
	//
	// If set to an explicit empty but *non-nil* slice, the wrapped handler will
	// only see uncompressed payloads. If any requests arrive that use a known
	// compression algorithm, the data will be decompressed.
	Compressors []string
	// MaxMessageBufferBytes is the maximum size of a received message when buffering
	// is necessary. Buffering a message is sometimes necessary when translating
	// from one protocol to another or one encoding to another. If buffering is
	// necessary to translate a given request and a message exceeds the configured
	// buffer size, the RPC will fail with a "resource exhausted" error. This applies
	// to on-the-wire sizes. The actual memory usage for a buffered memory could be
	// much higher than this due to decompression and/or decoding.
	//
	// If no value is configured, or it is set to a non-positive value, the limit is
	// 4 GB. This is also a technical limitation of Connect and gRPC, whose envelopes
	// do not allow payloads whose size in bytes overflows 32 bits. This should
	// generally be set to a value that is less than or equal to similar limits
	// configured in the server handler (or remote server, if the server handler will
	// proxy the request elsewhere).
	MaxMessageBufferBytes uint32
	// MaxGetURLBytes is the maximum size of a GET URL that can be used to send an
	// RPC using the Connect unary protocol with GET as the HTTP method. If a
	// GET request would exceed this limit, the RPC will be sent using POST as the
	// HTTP method instead.
	//
	// If no value is configured, or it is set to a non-positive value, a default
	// limit of 8 KB (8192 bytes) will be used.
	MaxGetURLBytes uint32
	// TypeResolver is the default TypeResolver. If no TypeResolver is specified
	// when a service is registered, this one is used. If nil, the default resolver
	// will be [protoregistry.GlobalTypes].
	TypeResolver TypeResolver
	// UnknownHandler is the handler to use when a request is received for a method
	// that has not been registered. If nil, the default is to return a 404 Not Found
	// error.
	UnknownHandler http.Handler

	init             sync.Once
	codecImpls       map[string]func(TypeResolver) Codec
	compressionPools map[string]*compressionPool
	methods          map[string]*methodConfig
	restRoutes       routeTrie
}

// AsHandler returns HTTP middleware that applies the given configuration
// to handlers.
//
// This should only be called after the configuration is finalized.
func (m *Mux) AsHandler() http.Handler {
	m.maybeInit()
	canDecompress := make([]string, 0, len(m.compressionPools))
	for compression := range m.compressionPools {
		canDecompress = append(canDecompress, compression)
	}
	sort.Strings(canDecompress)
	return &handler{
		mux:           m,
		bufferPool:    newBufferPool(),
		codecs:        newCodecMap(m.methods, m.codecImpls),
		canDecompress: canDecompress,
	}
}

// RegisterServiceByName registers the given handler for the named service.
// This queries the given service's schema from [protoregistry.GlobalFiles].
//
// If no other options are provided, it is assumed the given handler supports
// all three RPC protocols (Connect, gRPC-Web, gRPC), gzip compression, and proto
// encoding.
//
// Any methods that have `google.api.http` annotations will allow incoming
// requests to use REST+JSON conventions as specified by the annotations.
func (m *Mux) RegisterServiceByName(handler http.Handler, serviceName protoreflect.FullName, opts ...ServiceOption) error {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(serviceName)
	if err != nil {
		return err
	}
	serviceDesc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return fmt.Errorf("descriptor %s is a %T; not a service", serviceName, desc)
	}
	return m.RegisterService(handler, serviceDesc, opts...)
}

// RegisterService registers the given handler for the service schema.
//
// If no other options are provided, it is assumed the given handler supports
// all three RPC protocols (Connect, gRPC-Web, gRPC), gzip compression, and proto
// encoding.
//
// Any methods that have `google.api.http` annotations will allow incoming
// requests to use REST+JSON conventions as specified by the annotations.
func (m *Mux) RegisterService(handler http.Handler, serviceDesc protoreflect.ServiceDescriptor, opts ...ServiceOption) error {
	m.maybeInit()
	var svcOpts serviceOptions
	for _, opt := range opts {
		opt.apply(&svcOpts)
	}

	svcOpts.protocols = computeSet(svcOpts.protocols, m.Protocols, defaultProtocols, false)
	for protocol := range svcOpts.protocols {
		if protocol <= ProtocolUnknown || protocol > protocolMax {
			return fmt.Errorf("protocol %d is not a valid value", protocol)
		}
	}
	svcOpts.codecNames = computeSet(svcOpts.codecNames, m.Codecs, defaultCodecs, false)
	for codecName := range svcOpts.codecNames {
		if _, known := m.codecImpls[codecName]; !known {
			return fmt.Errorf("codec %s is not known; use mux.AddCodec to add known codecs first", codecName)
		}
	}
	if svcOpts.preferredCodec == "" {
		if len(m.Codecs) > 0 {
			svcOpts.preferredCodec = m.Codecs[0]
		} else {
			svcOpts.preferredCodec = CodecProto
		}
	}
	// empty is allowed here: non-nil but empty means do not send compressed data to handler
	svcOpts.compressorNames = computeSet(svcOpts.compressorNames, m.Compressors, defaultCompressors, true)
	for compressorName := range svcOpts.compressorNames {
		if _, known := m.compressionPools[compressorName]; !known {
			return fmt.Errorf("compression algorithm %s is not known; use mux.AddCompression to add known algorithms first", compressorName)
		}
	}

	switch {
	case svcOpts.maxGetURLBytes <= 0 && m.MaxGetURLBytes > 0:
		svcOpts.maxGetURLBytes = m.MaxGetURLBytes
	case svcOpts.maxGetURLBytes <= 0:
		svcOpts.maxGetURLBytes = DefaultMaxGetURLBytes
	}
	switch {
	case svcOpts.maxMsgBufferBytes <= 0 && m.MaxMessageBufferBytes > 0:
		svcOpts.maxMsgBufferBytes = m.MaxMessageBufferBytes
	case svcOpts.maxMsgBufferBytes <= 0:
		svcOpts.maxMsgBufferBytes = DefaultMaxMessageBufferBytes
	}

	if svcOpts.resolver == nil {
		svcOpts.resolver = m.TypeResolver
		if svcOpts.resolver == nil {
			svcOpts.resolver = protoregistry.GlobalTypes
		}
	}

	methods := serviceDesc.Methods()
	for i, length := 0, methods.Len(); i < length; i++ {
		methodDesc := methods.Get(i)
		if err := m.registerMethod(handler, methodDesc, svcOpts); err != nil {
			return fmt.Errorf("failed to configure method %s: %w", methodDesc.FullName(), err)
		}
	}
	return nil
}

// AddCodec adds the given codec implementation.
//
// By default, the mux already understands "proto", "json", and "text" codecs. The
// "json" and "text" codecs use default behavior (per MarshalOptions and UnmarshalOptions
// types in protojson and prototext packages) except that unmarshalling will ignore
// unrecognized fields.
//
// If this is called with an already-known name, the given codec factory replaces the
// already configured one. This can be used to override the default three codecs with
// different configuration.
func (m *Mux) AddCodec(name string, newCodec func(TypeResolver) Codec) {
	m.maybeInit()
	m.codecImpls[name] = newCodec
}

// AddCompression adds the given compression algorithm implementation.
//
// By default, the mux already understands "gzip" compression and uses the default
// compression level.
//
// If this is called with an already-known name, the given implementation
// replaces any previously configured one. This can be used to override
// the default "gzip" compression algorithm with a different implementation
// or compression level.
func (m *Mux) AddCompression(name string, newCompressor func() connect.Compressor, newDecompressor func() connect.Decompressor) {
	m.maybeInit()
	m.compressionPools[name] = newCompressionPool(name, newCompressor, newDecompressor)
}

func (m *Mux) registerMethod(handler http.Handler, methodDesc protoreflect.MethodDescriptor, opts serviceOptions) error {
	methodPath := "/" + string(methodDesc.Parent().FullName()) + "/" + string(methodDesc.Name())
	if _, ok := m.methods[methodPath]; ok {
		return fmt.Errorf("duplicate registration: method %s has already been configured", methodDesc.FullName())
	}
	methodConf := &methodConfig{
		descriptor:        methodDesc,
		methodPath:        methodPath,
		handler:           handler,
		resolver:          opts.resolver,
		protocols:         opts.protocols,
		codecNames:        opts.codecNames,
		preferredCodec:    opts.preferredCodec,
		compressorNames:   opts.compressorNames,
		maxMsgBufferBytes: opts.maxMsgBufferBytes,
		maxGetURLBytes:    opts.maxGetURLBytes,
	}
	m.methods[methodPath] = methodConf

	switch {
	case methodDesc.IsStreamingClient() && methodDesc.IsStreamingServer():
		methodConf.streamType = connect.StreamTypeBidi
	case methodDesc.IsStreamingClient():
		methodConf.streamType = connect.StreamTypeClient
	case methodDesc.IsStreamingServer():
		methodConf.streamType = connect.StreamTypeServer
	default:
		methodConf.streamType = connect.StreamTypeUnary
	}

	if httpRule, ok := getHTTPRuleExtension(methodDesc); ok {
		firstTarget, err := m.restRoutes.addRoute(methodConf, httpRule)
		if err != nil {
			return fmt.Errorf("failed to add REST route for method %s: %w", methodPath, err)
		}
		methodConf.httpRule = firstTarget
		for i, rule := range httpRule.AdditionalBindings {
			if len(rule.AdditionalBindings) > 0 {
				return fmt.Errorf("nested additional bindings are not supported (method %s)", methodPath)
			}
			if _, err := m.restRoutes.addRoute(methodConf, rule); err != nil {
				return fmt.Errorf("failed to add REST route (add'l binding #%d) for method %s: %w", i+1, methodPath, err)
			}
		}
	}
	return nil
}

func (m *Mux) maybeInit() {
	m.init.Do(func() {
		// initialize default codecs and compressors
		m.codecImpls = map[string]func(res TypeResolver) Codec{
			CodecProto: DefaultProtoCodec,
			CodecJSON: func(res TypeResolver) Codec {
				return DefaultJSONCodec(res)
			},
		}
		m.compressionPools = map[string]*compressionPool{
			CompressionGzip: newCompressionPool(CompressionGzip, DefaultGzipCompressor, DefaultGzipDecompressor),
		}
		m.methods = map[string]*methodConfig{}
	})
}

// ServiceOption is an option for configuring how the middleware will handle
// requests to a particular RPC service. See Mux.RegisterService.
type ServiceOption interface {
	apply(*serviceOptions)
}

// WithProtocols returns a service option indicating that the service handler
// supports protocols. By default, the handler is assumed to support all but the
// REST protocol, which is true if the handler is a Connect handler (created
// using generated code from the protoc-gen-connect-go plugin or an explicit
// call to one of the New*Handler functions in the "connectrpc.com/connect"
// package).
//
// If called with an empty set of protocols, the supported protocols are set
// back to defaults. (The call is a no-op if no protocols have otherwise been
// set.)
func WithProtocols(protocols ...Protocol) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		if opts.protocols == nil {
			opts.protocols = map[Protocol]struct{}{}
		}
		for _, p := range protocols {
			opts.protocols[p] = struct{}{}
		}
	})
}

// WithCodecs returns a service option indicating that the service handler supports
// the given codecs. By default, the handler is assumed only to support the "proto"
// codec.
//
// If called with an empty set of codec names, the supported codecs are set
// back to the default. (The call is a no-op if no codecs have otherwise been
// set.)
func WithCodecs(names ...string) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		if opts.codecNames == nil {
			opts.codecNames = map[string]struct{}{}
		}
		for _, n := range names {
			opts.codecNames[n] = struct{}{}
		}
		if len(names) > 0 {
			opts.preferredCodec = names[0]
		} else {
			opts.preferredCodec = ""
		}
	})
}

// WithCompression returns a service option indicating that the service handler supports
// the given compression algorithms. By default, the handler is assumed only to support
// the "gzip" compression algorithm.
//
// If called with an empty set of names, the supported codecs are set back to the
// default. (The call is a no-op if no compression algorithms have otherwise been set.)
// In order to disable all compression support, see WithNoCompression.
func WithCompression(names ...string) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		if len(names) == 0 {
			// nil signals to use defaults
			opts.compressorNames = nil
			return
		}
		if opts.compressorNames == nil {
			opts.compressorNames = map[string]struct{}{}
		}
		for _, n := range names {
			opts.compressorNames[n] = struct{}{}
		}
	})
}

// WithNoCompression returns a service option indicating that the server handler does
// not support compression.
func WithNoCompression() ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		// a non-nil but empty set signals no compression
		opts.compressorNames = map[string]struct{}{}
	})
}

// WithTypeResolver returns a service option to use the given resolver when serializing
// and de-serializing messages. If not specified, this defaults to Mux.TypeResolver
// (which defaults to [protoregistry.GlobalTypes] if unset).
func WithTypeResolver(resolver TypeResolver) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		opts.resolver = resolver
	})
}

// WithMaxMessageBufferBytes returns a service option that limits buffering of data
// when handling the service to the given limit. If any payload in a request or
// response exceeds this, the RPC will fail with a "resource exhausted" error.
//
// If set to zero or a negative value, a limit of 4 GB will be used.
func WithMaxMessageBufferBytes(limit uint32) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		opts.maxMsgBufferBytes = limit
	})
}

// WithMaxGetURLBytes returns a service option that limits the size of URLs with
// the Connect unary protocol using the GET HTTP method. If a URL's length would
// exceed this limit, the POST HTTP method will be used instead (and the request
// contents moved from the URL to the body).
//
// If set to zero or a negative value, a limit of 8 KB will be used.
func WithMaxGetURLBytes(limit uint32) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		opts.maxGetURLBytes = limit
	})
}

// TypeResolver can resolve message and extension types and is used to instantiate
// messages as needed for the middleware to serialize/de-serialize request and
// response payloads.
//
// Implementations of this interface should be comparable, so they can be used as
// map keys. Typical implementations are pointers to structs, which are suitable.
type TypeResolver interface {
	protoregistry.MessageTypeResolver
	protoregistry.ExtensionTypeResolver
}

//nolint:gochecknoglobals
var (
	defaultProtocols = map[Protocol]struct{}{
		ProtocolConnect: {},
		ProtocolGRPC:    {},
		ProtocolGRPCWeb: {},
	}
	defaultCodecs      = map[string]struct{}{CodecProto: {}, CodecJSON: {}}
	defaultCompressors = map[string]struct{}{CompressionGzip: {}}
)

type serviceOptionFunc func(*serviceOptions)

func (f serviceOptionFunc) apply(opts *serviceOptions) {
	f(opts)
}

type serviceOptions struct {
	resolver                    TypeResolver
	protocols                   map[Protocol]struct{}
	codecNames, compressorNames map[string]struct{}
	preferredCodec              string
	maxMsgBufferBytes           uint32
	maxGetURLBytes              uint32
}

type methodConfig struct {
	descriptor                  protoreflect.MethodDescriptor
	methodPath                  string
	streamType                  connect.StreamType
	handler                     http.Handler
	resolver                    TypeResolver
	protocols                   map[Protocol]struct{}
	codecNames, compressorNames map[string]struct{}
	preferredCodec              string
	httpRule                    *routeTarget // First HTTP rule, if any.
	maxMsgBufferBytes           uint32
	maxGetURLBytes              uint32
}

// computeSet returns a resolved set of values of type T, preferring the given values if
// valid, then the given defaults if valid, and finally the given backupDefaults.
//
// An empty or nil set is invalid (so empty or nil values means fallback to defaults;
// similarly, an empty or nil defaults means fallback to backupDefaults), unless
// allowEmpty is true, in which case an empty set is okay but nil is invalid.
func computeSet[T comparable](values map[T]struct{}, defaults []T, backupDefaults map[T]struct{}, allowEmpty bool) map[T]struct{} {
	if len(values) > 0 {
		// non-empty is always okay
		return values
	}
	if allowEmpty && values != nil {
		// empty but nil is okay
		return values
	}
	if (allowEmpty && defaults == nil) || (!allowEmpty && len(defaults) == 0) {
		// defaults is not valid either
		return backupDefaults
	}
	result := make(map[T]struct{}, len(defaults))
	for _, t := range defaults {
		result[t] = struct{}{}
	}
	return result
}
