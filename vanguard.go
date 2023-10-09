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
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/api/annotations"
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

// NewTranscoder creates a new transcoder that handles the given services, with the
// configuration described by the given options. A non-nil error is returned if
// there is an issue with this configuration.
//
// The returned handler does the routing and dispatch to the RPC handlers
// associated with each provided service. Routing supports more than just the
// service path provided to NewService since HTTP transcoding annotations are
// used to also support REST-ful URI paths for each method.
//
// The returned handler also acts like a middleware, transparently "upgrading"
// the RPC handlers to support incoming request protocols they wouldn't otherwise
// support. This can be used to upgrade Connect handlers to support REST requests
// (based on HTTP transcoding configuration) or gRPC handlers to support Connect,
// gRPC-Web, or REST. This can even be used with a reverse proxy handler, to
// translate all incoming requests to a single protocol that another backend server
// supports.
//
// If any options given implement ServiceOption, they are treated as default service
// options and apply to all configured services, unless overridden by a particular
// service.
func NewTranscoder(services []*Service, opts ...TranscoderOption) (*Transcoder, error) {
	for _, svc := range services {
		if svc.err != nil {
			return nil, svc.err
		}
	}

	transcoderOpts := transcoderOptions{
		codecs: codecMap{
			CodecProto: defaultProtoCodec,
			CodecJSON: func(res TypeResolver) Codec {
				return defaultJSONCodec(res)
			},
		},
		compressors: compressionMap{
			CompressionGzip: newCompressionPool(CompressionGzip, defaultGzipCompressor, defaultGzipDecompressor),
		},
		defaultServiceOptions: serviceOptions{
			maxMsgBufferBytes: DefaultMaxMessageBufferBytes,
			maxGetURLBytes:    DefaultMaxGetURLBytes,
			resolver:          protoregistry.GlobalTypes,
			preferredCodec:    CodecProto,
			codecNames:        map[string]struct{}{CodecProto: {}, CodecJSON: {}},
			compressorNames:   map[string]struct{}{CompressionGzip: {}},
			protocols:         map[Protocol]struct{}{ProtocolConnect: {}, ProtocolGRPC: {}, ProtocolGRPCWeb: {}},
		},
	}
	for _, opt := range opts {
		opt.applyToTranscoder(&transcoderOpts)
	}

	transcoder := &Transcoder{
		codecs:         transcoderOpts.codecs,
		compressors:    transcoderOpts.compressors,
		unknownHandler: transcoderOpts.unknownHandler,
		methods:        map[string]*methodConfig{},
	}
	for _, svc := range services {
		if err := transcoder.registerService(svc, transcoderOpts.defaultServiceOptions); err != nil {
			return nil, err
		}
	}
	if err := transcoder.registerRules(transcoderOpts.rules); err != nil {
		return nil, err
	}
	return transcoder, nil
}

// TranscoderOption is an option used to configure a Transcoder. See NewTranscoder.
type TranscoderOption interface {
	applyToTranscoder(*transcoderOptions)
}

// WithRules returns an option that adds HTTP transcoding configuration to the set of
// configured services. The given rules must have a selector defined, and the selector
// must match at least one configured method. Otherwise, NewTranscoder will report a
// configuration error.
func WithRules(rules ...*annotations.HttpRule) TranscoderOption {
	return transcoderOptionFunc(func(opts *transcoderOptions) {
		opts.rules = append(opts.rules, rules...)
	})
}

// WithCodec returns an option that instructs the transcoder to use the given
// function for instantiating codec implementations. The function is immediately
// invoked in order to determine the name of the codec. The name reported by codecs
// created with the function should all return the same name. (Otherwise, behavior
// is undefined.)
//
// By default, "proto" and "json" codecs are supported using default options. This
// option can be used to support additional codecs or to override the default
// implementations (such as to change serialization or de-serialization options).
func WithCodec(newCodec func(TypeResolver) Codec) TranscoderOption {
	codecName := newCodec(protoregistry.GlobalTypes).Name()
	return transcoderOptionFunc(func(opts *transcoderOptions) {
		if opts.codecs == nil {
			opts.codecs = codecMap{}
		}
		opts.codecs[codecName] = newCodec
	})
}

// WithCompression returns an option that instructs the transcoder to use the given
// functions to instantiate compressors and decompressors for the given compression
// algorithm name.
//
// By default, "gzip" compression is supported using default options. This option can be
// used to support additional compression algorithms or to override the default "gzip"
// implementation (such as to change the compression level).
func WithCompression(name string, newCompressor func() connect.Compressor, newDecompressor func() connect.Decompressor) TranscoderOption {
	return transcoderOptionFunc(func(opts *transcoderOptions) {
		if opts.codecs == nil {
			opts.compressors = compressionMap{}
		}
		opts.compressors[name] = newCompressionPool(name, newCompressor, newDecompressor)
	})
}

// WithUnknownHandler returns an option that instructs the transcoder to delegate to
// the given handler when a request arrives for an unknown endpoint. If no such option
// is used, the transcoder will reply with a simple "404 Not Found" error.
func WithUnknownHandler(unknownHandler http.Handler) TranscoderOption {
	return transcoderOptionFunc(func(opts *transcoderOptions) {
		opts.unknownHandler = unknownHandler
	})
}

// Service represents the configuration for a single RPC service.
type Service struct {
	err     error
	schema  protoreflect.ServiceDescriptor
	handler http.Handler
	opts    []ServiceOption
}

// NewService creates a new service definition for the given service path and handler.
// The service path must be the service's fully-qualified name, with an optional leading
// and trailing slash. This means you can provide generated constants for service names
// or you can provide the path returned by a New*Transcoder function generated by the
// [Protobuf plugin for Connect Go]. In fact, if you do not need to specify any
// service-specific options, you can wrap the call to New*Transcoder with NewService
// like so:
//
//	vanguard.NewService(elizav1connect.NewElizaServiceHandler(elizaImpl))
//
// If the given service path does not reflect a known service (one whose schema is
// registered with the Protobuf runtime, usually from generated code), NewTranscoder
// will return an error. For these cases, where the corresponding service schema may
// be dynamically retrieved, use NewServiceWithSchema instead.
//
// [Protobuf plugin for Connect Go]: https://pkg.go.dev/connectrpc.com/connect@v1.11.1/cmd/protoc-gen-connect-go
func NewService(servicePath string, handler http.Handler, opts ...ServiceOption) *Service {
	serviceName := strings.TrimSuffix(strings.TrimPrefix(servicePath, "/"), "/")
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return &Service{err: fmt.Errorf("could not resolve schema for service at path %q: %w", servicePath, err)}
	}
	svcDesc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return &Service{
			err: fmt.Errorf("could not resolve schema for service at path %q: resolved descriptor is %s, not a service", servicePath, descKind(desc)),
		}
	}
	return NewServiceWithSchema(svcDesc, handler, opts...)
}

// NewServiceWithSchema creates a new service using the given schema and handler.
func NewServiceWithSchema(schema protoreflect.ServiceDescriptor, handler http.Handler, opts ...ServiceOption) *Service {
	return &Service{
		schema:  schema,
		handler: handler,
		opts:    opts,
	}
}

// ServiceOption is an option for configuring how the middleware will handle
// requests to a particular RPC service. See NewService and NewServiceWithSchema.
//
// A ServiceOption may also be passed to NewTranscoder, as a TranscoderOption. When
// used this way, the option represents a default service option that will apply
// to all services. Such default options may be overridden for a particular
// service via an option passed to NewService or NewServiceWithSchema. Note that
// any ServiceOption passed directly to NewTranscoder is considered a default,
// regardless of the order. In other words, when it is passed as an option after
// a WithServices option, it still applies as a default to those prior services.
type ServiceOption interface {
	applyToService(*serviceOptions)
	applyToTranscoder(*transcoderOptions)
}

var _ TranscoderOption = ServiceOption(nil)

// WithTargetProtocols returns a service option indicating that the service handler
// supports protocols. By default, the handler is assumed to support all but the
// REST protocol, which is true if the handler is a Connect handler (created
// using generated code from the protoc-gen-connect-go plugin or an explicit
// call to one of the New*Transcoder functions in the "connectrpc.com/connect"
// package).
func WithTargetProtocols(protocols ...Protocol) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		opts.protocols = make(map[Protocol]struct{}, len(protocols))
		for _, p := range protocols {
			opts.protocols[p] = struct{}{}
		}
	})
}

// WithTargetCodecs returns a service option indicating that the service handler supports
// the given codecs. By default, the handler is assumed only to support the "proto"
// codec.
func WithTargetCodecs(names ...string) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		opts.codecNames = make(map[string]struct{}, len(names))
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

// WithTargetCompression returns a service option indicating that the service handler supports
// the given compression algorithms. By default, the handler is assumed only to support
// the "gzip" compression algorithm.
//
// To configure the handler to not use any compression, one could use this option and supply
// no names. However, to make this scenario more readable, prefer WithNoTargetCompression instead.
func WithTargetCompression(names ...string) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		opts.compressorNames = make(map[string]struct{}, len(names))
		for _, n := range names {
			opts.compressorNames[n] = struct{}{}
		}
	})
}

// WithNoTargetCompression returns a service option indicating that the server handler does
// not support compression.
func WithNoTargetCompression() ServiceOption {
	return WithTargetCompression()
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

type transcoderOptions struct {
	defaultServiceOptions serviceOptions
	rules                 []*annotations.HttpRule
	unknownHandler        http.Handler
	codecs                codecMap
	compressors           compressionMap
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

type transcoderOptionFunc func(*transcoderOptions)

func (f transcoderOptionFunc) applyToTranscoder(opts *transcoderOptions) {
	f(opts)
}

type serviceOptionFunc func(*serviceOptions)

func (f serviceOptionFunc) applyToService(opts *serviceOptions) {
	f(opts)
}

func (f serviceOptionFunc) applyToTranscoder(opts *transcoderOptions) {
	f(&opts.defaultServiceOptions)
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
	*serviceOptions
	descriptor protoreflect.MethodDescriptor
	methodPath string
	streamType connect.StreamType
	handler    http.Handler
	httpRule   *routeTarget // First HTTP rule, if any.
}

func descKind(desc protoreflect.Descriptor) string {
	switch desc := desc.(type) {
	case protoreflect.FileDescriptor:
		return "a file"
	case protoreflect.MessageDescriptor:
		return "a message"
	case protoreflect.FieldDescriptor:
		if desc.IsExtension() {
			return "an extension"
		}
		return "a field"
	case protoreflect.OneofDescriptor:
		return "a oneof"
	case protoreflect.EnumDescriptor:
		return "an enum"
	case protoreflect.EnumValueDescriptor:
		return "an enum value"
	case protoreflect.ServiceDescriptor:
		return "a service"
	case protoreflect.MethodDescriptor:
		return "a method"
	default:
		return fmt.Sprintf("%T", desc)
	}
}
