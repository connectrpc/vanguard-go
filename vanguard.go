// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	CompressionGzip = "gzip"
	// TODO: Connect protocol spec also references "br" (Brotli) and "zstd". And gRPC
	//       protocol spec references "deflate" and "snappy". Should we also support
	//       those out of the box?

	CodecProto = "proto"
	CodecJSON  = "json"
	// TODO: Some grpc impls support "text" out of the box (but not JSON, ironically).
	//       such as the JS impl. Should we also support it out of the box?
)

// Middleware is the signature for HTTP middleware, that can wrap/decorate an
// existing HTTP handler.
type Middleware func(http.Handler) http.Handler

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

// Config controls the behavior of HTTP middleware.
//
// Config is not thread-safe so should only mutated (via Add* methods) by one
// thread during initialization. The middleware returned from the Middleware
// method is only thread-safe among concurrently executing HTTP requests. It
// is not safe to mutate the Config once Middleware is being used by server
// handlers.
type Config struct {
	// The protocols that are supported by the wrapped handler, by default.
	// This can be overridden on a per-service level via options when calling
	// AddService or AddServiceByName.
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
	// AddService or AddServiceByName.
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
	// calling AddService or AddServiceByName.
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
	// TypeResolver is the default TypeResolver. If no TypeResolver is specified
	// when a service is registered, this one is used. If nil, the default resolver
	// will be [protoregistry.GlobalTypes].
	TypeResolver TypeResolver

	init              sync.Once
	codecImpls        map[string]func(TypeResolver) Codec
	compressorImpls   map[string]func() connect.Compressor
	decompressorImpls map[string]func() connect.Decompressor
	methods           map[protoreflect.FullName]*methodConfig
	restRoutes        routeTrie
}

// AsMiddleware returns HTTP middleware that applies the given configuration
// to handlers.
//
// This should only be called after the configuration is finalized.
func (c *Config) AsMiddleware() (Middleware, error) {
	c.maybeInit()
	return c.apply, nil
}

// AddServiceByName registers the schema for the given service with the config.
// This queries the named service's schema from [protoregistry.GlobalFiles].
//
// If no other options are provided, it is assumed the downstream handler supports
// all three RPC protocols (Connect, gRPC-Web, gRPC), gzip compression, and proto
// encoding.
//
// Any methods that have `google.api.http` annotations will allow incoming
// requests to use REST+JSON conventions as specified by the annotations.
func (c *Config) AddServiceByName(serviceName protoreflect.FullName, opts ...ServiceOption) error {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(serviceName)
	if err != nil {
		return err
	}
	serviceDesc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return fmt.Errorf("descriptor %s is a %T; not a service", serviceName, desc)
	}
	return c.AddService(serviceDesc, opts...)
}

// AddService registers the given service schema with the config.
//
// If no other options are provided, it is assumed the downstream handler supports
// all three RPC protocols (Connect, gRPC-Web, gRPC), gzip compression, and proto
// encoding.
//
// Any methods that have `google.api.http` annotations will allow incoming
// requests to use REST+JSON conventions as specified by the annotations.
func (c *Config) AddService(serviceDesc protoreflect.ServiceDescriptor, opts ...ServiceOption) error {
	c.maybeInit()
	var svcOpts serviceOptions
	for _, opt := range opts {
		opt.apply(&svcOpts)
	}

	svcOpts.protocols = computeSet(svcOpts.protocols, c.Protocols, defaultProtocols, false)
	for protocol := range svcOpts.protocols {
		if protocol <= ProtocolUnknown || protocol > protocolMax {
			return fmt.Errorf("protocol %d is not a valid value", protocol)
		}
	}
	svcOpts.codecNames = computeSet(svcOpts.codecNames, c.Codecs, defaultCodecs, false)
	for codecName := range svcOpts.codecNames {
		if _, known := c.codecImpls[codecName]; !known {
			return fmt.Errorf("codec %d is not known; use config.AddCodec to add known codecs first", codecName)
		}
	}
	// empty is allowed here: non-nil but empty means do not send compressed data to handler
	svcOpts.codecNames = computeSet(svcOpts.compressorNames, c.Compressors, defaultCompressors, true)
	for compressorName := range svcOpts.compressorNames {
		if _, known := c.compressorImpls[compressorName]; !known {
			return fmt.Errorf("compression algorithm %d is not known; use config.AddCompression to add known algorithms first", compressorName)
		}
	}

	if svcOpts.resolver == nil {
		svcOpts.resolver = c.TypeResolver
		if svcOpts.resolver == nil {
			svcOpts.resolver = protoregistry.GlobalTypes
		}
	}

	methods := serviceDesc.Methods()
	for i, length := 0, methods.Len(); i < length; i++ {
		methodDesc := methods.Get(i)
		if err := c.addMethod(methodDesc, svcOpts); err != nil {
			return fmt.Errorf("failed to configure method %s: %w", methodDesc.FullName(), err)
		}
	}
	return nil
}

// AddCodec adds the given codec implementation.
//
// By default, the middleware already understands "proto", "json", and "text" codecs. The
// "json" and "text" codecs use default behavior (per MarshalOptions and UnmarshalOptions
// types in protojson and prototext packages) except that unmarshalling will ignore
// unrecognized fields.
//
// If this is called with an already-known name, the given codec factory replaces the
// already configured one. This can be used to override the default three codecs with
// different configuration.
func (c *Config) AddCodec(name string, newCodec func(TypeResolver) Codec) {
	c.maybeInit()
	c.codecImpls[name] = newCodec
}

// AddCompression adds the given compression algorithm implementation.
//
// By default, the middleware already understands "gzip" compression. For
// compression, this uses the default compression levels (which is closer
// to the "max speed" level than the "max compression" level).
//
// If this is called with an already-known name, the given implementation
// replaces any previously configured one. This can be used to override
// the default "gzip" compression algorithm with a different implementation
// or settings.
func (c *Config) AddCompression(name string, newCompressor func() connect.Compressor, newDecompressor func() connect.Decompressor) {
	c.maybeInit()
	c.compressorImpls[name] = newCompressor
	c.decompressorImpls[name] = newDecompressor
}

func (c *Config) addMethod(methodDesc protoreflect.MethodDescriptor, opts serviceOptions) error {
	if _, ok := c.methods[methodDesc.FullName()]; ok {
		return fmt.Errorf("duplicate registration: method %s has already been configured", methodDesc.FullName())
	}
	methodOpts, ok := methodDesc.Options().(*descriptorpb.MethodOptions)
	if !ok {
		return fmt.Errorf("method %s has unknown options type %T", methodDesc.FullName(), methodDesc.Options())
	}
	methodConf := &methodConfig{
		descriptor:      methodDesc,
		resolver:        opts.resolver,
		protocols:       opts.protocols,
		codecNames:      opts.codecNames,
		compressorNames: opts.compressorNames,
	}
	c.methods[methodDesc.FullName()] = methodConf
	if proto.HasExtension(methodOpts, annotations.E_Http) {
		httpRule, ok := proto.GetExtension(methodOpts, annotations.E_Http).(*annotations.HttpRule)
		if !ok {
			return fmt.Errorf("method %s has unexpected type for google.api.http annotation: %T", methodDesc.FullName(), proto.GetExtension(methodOpts, annotations.E_Http))
		}
		if err := c.restRoutes.addRoute(methodConf, httpRule); err != nil {
			return fmt.Errorf("failed to add REST route for method %s: %w", methodDesc.FullName(), err)
		}
		for i, rule := range httpRule.AdditionalBindings {
			if err := c.restRoutes.addRoute(methodConf, rule); err != nil {
				return fmt.Errorf("failed to add REST route (add'l binding #%d) for method %s: %w", i+1, methodDesc.FullName(), err)
			}
		}
	}
	return nil
}

func (c *Config) maybeInit() {
	c.init.Do(func() {
		// initialize default codecs and compressors
		c.codecImpls = map[string]func(res TypeResolver) Codec{
			CodecProto: DefaultProtoCodec,
			CodecJSON:  DefaultJSONCodec,
		}
		c.compressorImpls = map[string]func() connect.Compressor{
			CompressionGzip: DefaultGzipCompressor,
		}
		c.decompressorImpls = map[string]func() connect.Decompressor{
			CompressionGzip: DefaultGzipDecompressor,
		}
		c.methods = map[protoreflect.FullName]*methodConfig{}
	})
}

func (c *Config) apply(handler http.Handler) http.Handler {
	// TODO
	return handler
}

// ServiceOption is an option for configuring how the middleware will handle
// requests to a particular RPC service. See Config.AddService.
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
			opts.codecNames = nil
			return
		}
		if opts.codecNames == nil {
			opts.codecNames = map[string]struct{}{}
		}
		for _, n := range names {
			opts.codecNames[n] = struct{}{}
		}
	})
}

// WithNoCompression returns a service option indicating that the server handler does
// not support compression.
func WithNoCompression() ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		// a non-nil but empty set signals no compression
		opts.codecNames = map[string]struct{}{}
	})
}

// WithTypeResolver returns a service option to use the given resolver when serializing
// and de-serializing messages. If not specified, this defaults to Config.TypeResolver
// (which defaults to [protoregistry.GlobalTypes] if unset).
func WithTypeResolver(resolver TypeResolver) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		opts.resolver = resolver
	})
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

// Protocol represents an on-the-wire protocol for RPCs.
type Protocol int

const (
	// ProtocolUnknown is not a valid value. Since it is the zero value, this
	// requires that all Protocol values must be explicitly initialized.
	ProtocolUnknown = Protocol(iota)
	// ProtocolConnect indicates the Connect protocol. This protocol supports
	// unary and streaming endpoints. However, bidirectional streams are only
	// supported when combined with HTTP/2.
	ProtocolConnect
	// ProtocolGRPC indicates the gRPC protocol. This protocol can only be
	// used in combination with HTTP/2. It supports unary and all kinds of
	// streaming endpoints.
	ProtocolGRPC
	// ProtocolGRPCWeb indicates the gRPC-Web protocol. This is a tweak of the
	// gRPC protocol to support HTTP 1.1. This protocol supports unary and
	// streaming endpoints. However, bidirectional streams are only supported
	// when combined with HTTP/2.
	ProtocolGRPCWeb
	// ProtocolREST indicates the REST+JSON protocol. This protocol often
	// requires non-trivial transformations between HTTP requests and responses
	// and Protobuf request and response messages.
	//
	// Only methods that have the google.api.http annotation can be invoked
	// with this protocol. The annotation defines the "shape" of the HTTP
	// request and response, such as the URI path, HTTP method, and how URI
	// path components, query string parameters, and an optional request
	// body are mapped to the Protobuf request message.
	//
	// This protocol only supports unary and server-stream endpoints.
	ProtocolREST

	// protocolMax is the maximum valid value for a Protocol.
	protocolMax = ProtocolREST
)

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
}

type methodConfig struct {
	descriptor                  protoreflect.MethodDescriptor
	resolver                    TypeResolver
	protocols                   map[Protocol]struct{}
	codecNames, compressorNames map[string]struct{}
}
