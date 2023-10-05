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
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
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

// NewHandler creates a new Vanguard handler for the given services, with the
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
func NewHandler(services []*Service, opts ...HandlerOption) (*Handler, error) {
	for _, svc := range services {
		if svc.err != nil {
			return nil, svc.err
		}
	}

	handlerOpts := handlerOptions{
		codecs: codecMap{
			CodecProto: DefaultProtoCodec,
			CodecJSON: func(res TypeResolver) Codec {
				return DefaultJSONCodec(res)
			},
		},
		compressors: compressionMap{
			CompressionGzip: newCompressionPool(CompressionGzip, DefaultGzipCompressor, DefaultGzipDecompressor),
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
		opt.applyToHandler(&handlerOpts)
	}

	handler := &Handler{
		codecs:         handlerOpts.codecs,
		compressors:    handlerOpts.compressors,
		unknownHandler: handlerOpts.unknownHandler,
		defaultHooks:   handlerOpts.defaultServiceOptions.hooks,
		methods:        map[string]*methodConfig{},
	}
	for _, svc := range services {
		if err := registerService(svc, handler, handlerOpts.defaultServiceOptions); err != nil {
			return nil, err
		}
	}
	if err := registerRules(handlerOpts.rules, handler); err != nil {
		return nil, err
	}
	return handler, nil
}

// HandlerOption is an option used to configure a Vanguard handler. See NewHandler.
type HandlerOption interface {
	applyToHandler(*handlerOptions)
}

// WithRules returns an option that adds HTTP transcoding configuration to the set of
// configured services. The given rules must have a selector defined, and the selector
// must match at least one configured method. Otherwise, NewHandler will report a
// configuration error.
func WithRules(rules ...*annotations.HttpRule) HandlerOption {
	return handlerOptionFunc(func(opts *handlerOptions) {
		opts.rules = append(opts.rules, rules...)
	})
}

// WithCodec returns an option that instructs the Vanguard handler to use the given
// function for instantiating codec implementations. The function is immediately
// invoked in order to determine the name of the codec. The name reported by codecs
// created with the function should all return the same name. (Otherwise, behavior
// is undefined.)
//
// By default, "proto" and "json" codecs are supported using default options. This
// option can be used to support additional codecs or to override the default
// implementations (such as to change serialization or de-serialization options).
func WithCodec(newCodec func(TypeResolver) Codec) HandlerOption {
	codecName := newCodec(protoregistry.GlobalTypes).Name()
	return handlerOptionFunc(func(opts *handlerOptions) {
		if opts.codecs == nil {
			opts.codecs = codecMap{}
		}
		opts.codecs[codecName] = newCodec
	})
}

// WithCompression returns an option that instructs the Vanguard handler to use the
// given functions to instantiate compressors and decompressors for the given compression
// algorithm name.
//
// By default, "gzip" compression is supported using default options. This option can be
// used to support additional compression algorithms or to override the default "gzip"
// implementation (such as to change the compression level).
func WithCompression(name string, newCompressor func() connect.Compressor, newDecompressor func() connect.Decompressor) HandlerOption {
	return handlerOptionFunc(func(opts *handlerOptions) {
		if opts.codecs == nil {
			opts.compressors = compressionMap{}
		}
		opts.compressors[name] = newCompressionPool(name, newCompressor, newDecompressor)
	})
}

// WithUnknownHandler returns an option that instructs the Vanguard handler to delegate
// to the given handler when a request arrives for an unknown endpoint. If no such option
// is used, the Vanguard handler will reply with a simple "404 Not Found" error.
func WithUnknownHandler(unknownHandler http.Handler) HandlerOption {
	return handlerOptionFunc(func(opts *handlerOptions) {
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
// or you can provide the path returned by a New*Handler function generated by the
// [Protobuf plugin for Connect Go]. In fact, if you do not need to specify any
// service-specific options, you can wrap the call to New*Handler with NewService
// like so:
//
//	vanguard.NewService(elizav1connect.NewElizaServiceHandler(elizaImpl))
//
// If the given service path does not reflect a known service (one whose schema is
// registered with the Protobuf runtime, usually from generated code), NewHandler
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
// A ServiceOption may also be passed to NewHandler, as a HandlerOption. When
// used this way, the option represents a default service option that will apply
// to all services. Such default options may be overridden for a particular
// service via an option passed to NewService or NewServiceWithSchema. Note that
// any ServiceOption passed directly to NewHandler is considered a default,
// regardless of the order. In other words, when it is passed as an option after
// a WithServices option, it still applies as a default to those prior services.
type ServiceOption interface {
	applyToService(*serviceOptions)
	applyToHandler(*handlerOptions)
}

var _ HandlerOption = ServiceOption(nil)

// WithTargetProtocols returns a service option indicating that the service handler
// supports protocols. By default, the handler is assumed to support all but the
// REST protocol, which is true if the handler is a Connect handler (created
// using generated code from the protoc-gen-connect-go plugin or an explicit
// call to one of the New*Handler functions in the "connectrpc.com/connect"
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

// WithHooks sets the given callbacks for hooking into the RPC flow.
// This overrides any callback defined on the Mux.
//
// For invalid requests (where Operation.IsValid returns false), only
// a single hook (Hooks.OnClientRequestHeaders) is invoked and nothing
// further (not even Hooks.OnOperationFail). If the operation is not
// valid because the intended endpoint cannot be determined or is not
// known, a per-service option cannot be applied. So in this case, only
// the Hooks configured as a default service option (where WithHooks was
// passed directly to NewHandler) will be used.
//
// Otherwise, for valid operations, the Hooks.OnClientRequestHeaders and
// a completion hook (either Hooks.OnOperationFinish or Hooks.OnOperationFail)
// will always be invoked, even when a hook aborts the operation with an
// error.
func WithHooks(hooks Hooks) ServiceOption {
	return serviceOptionFunc(func(opts *serviceOptions) {
		opts.hooks = &hooks
	})
}

type handlerOptions struct {
	defaultServiceOptions serviceOptions
	rules                 []*annotations.HttpRule
	unknownHandler        http.Handler
	codecs                codecMap
	compressors           compressionMap
}

func registerService(svc *Service, h *Handler, svcOpts serviceOptions) error {
	for _, opt := range svc.opts {
		opt.applyToService(&svcOpts)
	}

	if len(svcOpts.protocols) == 0 {
		return fmt.Errorf("service %s was configured with no target protocols", svc.schema.FullName())
	}
	for protocol := range svcOpts.protocols {
		if protocol <= ProtocolUnknown || protocol > protocolMax {
			return fmt.Errorf("protocol %d is not a valid value", protocol)
		}
	}

	if len(svcOpts.codecNames) == 0 {
		return fmt.Errorf("service %s was configured with no target codecs", svc.schema.FullName())
	}
	for codecName := range svcOpts.codecNames {
		if _, known := h.codecs[codecName]; !known {
			return fmt.Errorf("codec %s is not known; use WithCodec to configure known codecs", codecName)
		}
	}

	// empty svcOpts.compressorNames is okay
	for compressorName := range svcOpts.compressorNames {
		if _, known := h.compressors[compressorName]; !known {
			return fmt.Errorf("compression algorithm %s is not known; use WithCompression to configure known algorithms", compressorName)
		}
	}

	if svcOpts.maxMsgBufferBytes <= 0 {
		return fmt.Errorf("service %s is configured with an invalid max message buffer size: %d bytes", svc.schema.FullName(), svcOpts.maxMsgBufferBytes)
	}
	if svcOpts.maxGetURLBytes <= 0 {
		return fmt.Errorf("service %s is configured with an invalid max GET URL length: %d bytes", svc.schema.FullName(), svcOpts.maxGetURLBytes)
	}

	if svcOpts.resolver == nil {
		return fmt.Errorf("service %s is configured with a nil type resolver", svc.schema.FullName())
	}

	methods := svc.schema.Methods()
	for i, length := 0, methods.Len(); i < length; i++ {
		methodDesc := methods.Get(i)
		if err := registerMethod(svc.handler, methodDesc, h, &svcOpts); err != nil {
			return fmt.Errorf("failed to configure method %s: %w", methodDesc.FullName(), err)
		}
	}
	return nil
}

func registerRules(rules []*annotations.HttpRule, h *Handler) error {
	if len(rules) == 0 {
		return nil
	}
	methodRules := make(map[*methodConfig][]*annotations.HttpRule)
	for _, rule := range rules {
		var applied bool
		selector := rule.GetSelector()
		if selector == "" {
			return fmt.Errorf("rule missing selector")
		}
		if i := strings.Index(selector, "*"); i >= 0 {
			if i != len(selector)-1 {
				return fmt.Errorf("wildcard selector %q must be at the end", rule.GetSelector())
			}
			selector = selector[:len(selector)-1]
			if len(selector) > 0 && !strings.HasSuffix(selector, ".") {
				return fmt.Errorf("wildcard selector %q must be whole component", rule.GetSelector())
			}
		}
		for _, methodConf := range h.methods {
			methodName := string(methodConf.descriptor.FullName())
			if !strings.HasPrefix(methodName, selector) {
				continue
			}
			methodRules[methodConf] = append(methodRules[methodConf], rule)
			applied = true
		}
		if !applied {
			return fmt.Errorf("rule %q does not match any methods", rule.GetSelector())
		}
	}
	for methodConf, rules := range methodRules {
		for _, rule := range rules {
			if err := addRule(rule, methodConf, h); err != nil {
				// TODO: use the multi-error type errors.Join()
				return err
			}
		}
	}
	return nil
}

func registerMethod(rpcHandler http.Handler, methodDesc protoreflect.MethodDescriptor, h *Handler, opts *serviceOptions) error {
	methodPath := "/" + string(methodDesc.Parent().FullName()) + "/" + string(methodDesc.Name())
	if _, ok := h.methods[methodPath]; ok {
		return fmt.Errorf("duplicate registration: method %s has already been configured", methodDesc.FullName())
	}
	methodConf := &methodConfig{
		serviceOptions: opts,
		descriptor:     methodDesc,
		methodPath:     methodPath,
		handler:        rpcHandler,
	}
	if h.methods == nil {
		h.methods = make(map[string]*methodConfig, 1)
	}
	h.methods[methodPath] = methodConf

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
		if err := addRule(httpRule, methodConf, h); err != nil {
			return err
		}
	}
	return nil
}

func addRule(httpRule *annotations.HttpRule, methodConf *methodConfig, h *Handler) error {
	methodPath := methodConf.methodPath
	firstTarget, err := h.restRoutes.addRoute(methodConf, httpRule)
	if err != nil {
		return fmt.Errorf("failed to add REST route for method %s: %w", methodPath, err)
	}
	methodConf.httpRule = firstTarget
	for i, rule := range httpRule.AdditionalBindings {
		if len(rule.AdditionalBindings) > 0 {
			return fmt.Errorf("nested additional bindings are not supported (method %s)", methodPath)
		}
		if _, err := h.restRoutes.addRoute(methodConf, rule); err != nil {
			return fmt.Errorf("failed to add REST route (add'l binding #%d) for method %s: %w", i+1, methodPath, err)
		}
	}
	return nil
}

// Operation represents an in-progress RPC operation. This is supplied to
// hook callback methods.
//
// Operation is not thread-safe. These methods should only be invoked from
// the goroutine that runs the Hooks callback method.
type Operation interface {
	// IsValid returns true if the operation is valid. If it returns
	// false, then there was an error in the incoming request that
	// prevents some of hte operation details from being ascertained.
	//
	// When this returns false, Method may return nil and ClientInfo
	// and HandlerInfo may report invalid attributes (like unknown
	// protocol or blank codec name).
	IsValid() bool

	// HTTPRequestLine provides access to properties of the client's
	// HTTP request line. These are available, even if IsValid returns
	// false.
	HTTPRequestLine() (method, path, queryString, httpVersion string)

	// Method returns the descriptor of the RPC method being invoked.
	Method() protoreflect.MethodDescriptor
	// Deadline returns the time at which this operation must complete,
	// per a deadline sent by the client. It returns a zero time and false
	// when the client did not include a deadline.
	Deadline() (time.Time, bool)
	// ClientInfo returns information about the client side of the operation.
	// This includes the incoming protocol, codec, and compression.
	ClientInfo() PeerInfo
	// HandlerInfo returns information about the server handler side of the
	// operation. These properties will vary from those returned by ClientInfo
	// when the middleware is transcoding the protocol, re-encoding messages,
	// or using a different compression algorithm.
	HandlerInfo() PeerInfo

	doNotImplement() // allows us to add methods to this interface in the future
}

// PeerInfo describes operation attributes for one of peers. Every operation has
// two peers: the client that initiated the request, and the server handler that
// will act on the request.
//
// PeerInfo is not thread-safe. These methods should only be invoked from the
// goroutine that runs the Hooks callback method.
type PeerInfo interface {
	// Protocol indicates the protocol this peer uses.
	Protocol() Protocol
	// Codec indicates the name of the codec that this peer uses.
	Codec() string
	// RequestCompression indicates the name of the compression algorithm that
	// this peer uses. If no compression is used, this returns the empty string.
	RequestCompression() string
	// ResponseCompression will not be known until after response headers have
	// been received. If this method is invoked before then, it will return
	// the empty string, even if compression ultimately is used in the response.
	ResponseCompression() string

	doNotImplement() // allows us to add methods to this interface in the future
}

// Hooks represents a set of observability/validation hooks that are invoked
// as an RPC operation is executed.
//
// When any of them is non-nil, that function will be invoked when the relevant
// occurs when processing an RPC.
type Hooks struct {
	// OnClientRequestHeaders is invoked immediately at the start of an
	// operation, providing access to the request headers. If it returns
	// an error, the operation is immediately aborted, and the server
	// handler is never invoked.
	//
	// The headers can vary from the actual headers sent by the client as
	// protocol-specific headers (like those indicating the protocol, the
	// message encoding format, compression algorithm, and timeout) will
	// have already been removed. The relevant information is instead
	// available via attributes of the Operation.
	//
	// If Operation.IsValid returns false for the given operation, no
	// subsequent hooks will be invoked, not even OnOperationFail.
	// Any configured unknown handler may be invoked if the operation
	// is invalid because the intended endpoint could not be determined
	// or is unknown to the handler.
	OnClientRequestHeaders func(context.Context, Operation, http.Header) error
	// OnClientRequestMessage is invoked for each request message received
	// from the client. For unary RPCs, this will always be exactly one per
	// operation. For client and bidirectional streaming RPCs, this can be
	// called zero or more times.
	//
	// If an error is returned, the operation is immediately aborted, and
	// the message is not sent to the server. The client will observe the
	// returned error (as translated to an RPC error), but the server will
	// observe a cancelled error.
	//
	// The provided compressed flag and wireSize are the details of the
	// message sent by the client. The wire size of the data sent to the
	// server handler may differ if the middleware is transcoding the
	// protocol, re-encoding messages to a different format, or applying
	// a different compression algorithm.
	//
	// The reported wireSize is based on the data read from the request
	// body. If the request was constructed from components of the URL
	// path or the query string, those bytes are not reported, and the
	// size could be reported as zero even if the request is not empty.
	// If the client protocol uses envelopes to delimit messages, the
	// envelope size is NOT included in the reported wireSize.
	OnClientRequestMessage func(ctx context.Context, op Operation, req proto.Message, compressed bool, wireSize int) error
	// OnServerResponseHeaders is invoked when response headers are received
	// from the server.  If it returns an error, the operation is immediately
	// aborted. The client will observe the returned error (as translated to
	// an RPC error), but the server will observe a cancelled error.
	OnServerResponseHeaders func(ctx context.Context, op Operation, statusCode int, headers http.Header) error
	// OnServerResponseMessage is invoked for each response message received
	// from the server. For unary RPCs, this will always be either zero (on
	// failure) or one (on success) per operation. For server and
	// bidirectional streaming RPCs, this can be called zero or more times.
	//
	// If an error is returned, the operation is immediately aborted, and the
	// message is not sent to the client. The client and server will both
	// observe the returned error (as translated to an RPC error for the
	// client). The server will receive the error from the corresponding call
	// to [http.ResponseWriter.Write]. Subsequent attempts to write to the
	// response will observe a cancelled error.
	//
	// The provided compressed flag and wireSize are the details of the
	// message sent by the server. The wire size of the data sent to the
	// client may differ if the middleware is transcoding the protocol,
	// re-encoding messages to a different format, or applying a different
	// compression algorithm.
	OnServerResponseMessage func(ctx context.Context, op Operation, rsp proto.Message, compressed bool, wireSize int) error
	// OnOperationFinish is called if and when the operation completes
	// successfully. If the operation does not complete successfully, then
	// OnOperationFail will be called instead.
	//
	// This is the only callback that does not return an error. At this point
	// the RPC has completed successfully, so it is too late to inject an error.
	OnOperationFinish func(ctx context.Context, op Operation, trailers http.Header)
	// OnOperationFail is called when the operation completes unsuccessfully.
	// If a non-nil error is returned, it will replace the given err when the
	// error is sent to the client.
	OnOperationFail func(ctx context.Context, op Operation, trailers http.Header, err error) error
}

func (h Hooks) isEmpty() bool {
	return h.OnClientRequestHeaders == nil &&
		h.OnClientRequestMessage == nil &&
		h.OnServerResponseHeaders == nil &&
		h.OnServerResponseMessage == nil &&
		h.OnOperationFinish == nil &&
		h.OnOperationFail == nil
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

type handlerOptionFunc func(*handlerOptions)

func (f handlerOptionFunc) applyToHandler(opts *handlerOptions) {
	f(opts)
}

type serviceOptionFunc func(*serviceOptions)

func (f serviceOptionFunc) applyToService(opts *serviceOptions) {
	f(opts)
}

func (f serviceOptionFunc) applyToHandler(opts *handlerOptions) {
	f(&opts.defaultServiceOptions)
}

type serviceOptions struct {
	resolver                    TypeResolver
	protocols                   map[Protocol]struct{}
	codecNames, compressorNames map[string]struct{}
	preferredCodec              string
	maxMsgBufferBytes           uint32
	maxGetURLBytes              uint32
	hooks                       *Hooks
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
