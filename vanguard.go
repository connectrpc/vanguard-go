// Copyright 2023-2026 Buf Technologies, Inc.
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

// Package vanguard provides REST support for services built on
// connectrpc.com/connect/v2.
//
// When a Protobuf method is annotated with a google.api.http rule, it declares
// the HTTP method, URL path, and body mapping for that RPC. Vanguard implements
// these rules in both directions:
//
//   - [Mount] exposes a connect.Server over REST, allowing standard HTTP
//     clients to reach handlers written against generated RPC interfaces.
//   - [NewTransport] returns a connect.Transport that speaks REST, allowing
//     generated RPC clients to call a REST server that knows nothing about Connect.
//
// Vanguard acts as a peer to connect-go's connecthttp package. While
// connecthttp serves the Connect, gRPC, and gRPC-Web protocols, Vanguard
// handles REST. Because [Mount] takes the same multiplexer interface as
// connecthttp.Mount, and [NewTransport] takes the same HTTP client, REST can
// be seamlessly added without disturbing existing protocols.
//
// Request and response bodies may be compressed, negotiated through the standard
// Content-Encoding and Accept-Encoding headers. gzip is supported by default.
// See [WithCompressors].
//
// Streaming RPCs are carried over REST when the streamed side is a
// google.api.HttpBody: a client stream's request body is the whole HTTP request
// body, and a server stream writes one chunk per message to the HTTP response
// body. Bidirectional streams have no REST mapping.
//
// Server side:
//
//	server := connect.NewServer()
//	pingv1connect.RegisterPingServiceHandler(server, &pingServer{})
//
//	mux := http.NewServeMux()
//	connecthttp.Mount(mux, server) // Connect, gRPC, gRPC-Web
//	vanguard.Mount(mux, server)    // google.api.http (REST)
//
// Client side:
//
//	transport, _ := vanguard.NewTransport(http.DefaultClient, "https://api.example.com")
//	client := pingv1connect.NewPingServiceClient(connect.NewClient(transport))
//	resp, err := client.Ping(ctx, &pingv1.PingRequest{Number: 42})
//
// Proxy, with no generated code for the service:
//
//	upstream := connect.NewClient(connecthttp.NewTransport(http.DefaultClient, backendURL))
//	server.Register(vanguard.ForwardService(upstream, serviceDescriptor)...)
//	vanguard.Mount(mux, server)
package vanguard

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectgzip"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/api/annotations"
)

// Option configures the REST handler installed by Mount or NewTransport.
type Option interface {
	applyVanguard(*options)
}

type options struct {
	newCodec                  func(connectproto.TypeResolver) RESTCodec
	resolver                  connectproto.TypeResolver
	rules                     []*annotations.HttpRule
	maxReadBytes              int64
	discardUnknownQueryParams bool
	compressors               []connect.Compressor
	sendCompression           string
}

// defaultOptions returns the baseline configuration. The codec is
// constructed lazily in effectiveCodec so that the factory sees the
// resolver chosen by WithTypeResolver (or the default).
func defaultOptions() options {
	return options{
		maxReadBytes: 4 * 1024 * 1024,
		compressors:  []connect.Compressor{connectgzip.New()},
	}
}

// effectiveCodec returns a codec constructed with the configured
// resolver. The factory is either the user-supplied one (WithCodec)
// or the default JSON factory.
func (o *options) effectiveCodec() RESTCodec {
	factory := o.newCodec
	if factory == nil {
		factory = func(r connectproto.TypeResolver) RESTCodec { return NewJSONCodec(r) }
	}
	return factory(o.resolver)
}

type optionFunc func(*options)

func (f optionFunc) applyVanguard(o *options) { f(o) }

// WithCodec overrides the RESTCodec factory used to encode and decode
// bodies. The factory is invoked once per Mount or NewTransport call,
// after option processing, with the resolver chosen by WithTypeResolver (or
// protoregistry.GlobalTypes if none).
//
//	// Use the default JSON codec but configure it.
//	vanguard.Mount(mux, server,
//	    vanguard.WithCodec(func(r connectproto.TypeResolver) vanguard.RESTCodec {
//	        c := vanguard.NewJSONCodec(r)
//	        c.MarshalOptions.UseProtoNames = true
//	        return c
//	    }),
//	)
func WithCodec(newCodec func(connectproto.TypeResolver) RESTCodec) Option {
	return optionFunc(func(o *options) {
		o.newCodec = newCodec
	})
}

// WithTypeResolver overrides the proto type resolver used when
// instantiating request and response messages. Defaults to
// protoregistry.GlobalTypes.
func WithTypeResolver(resolver connectproto.TypeResolver) Option {
	return optionFunc(func(o *options) {
		o.resolver = resolver
	})
}

// WithRules attaches HTTP rules that are not embedded directly as google.api.http
// annotations on the method descriptors. Each rule's selector must match a
// registered procedure (the fully-qualified method name).
func WithRules(rules ...*annotations.HttpRule) Option {
	return optionFunc(func(o *options) {
		o.rules = append(o.rules, rules...)
	})
}

// WithMaxReadBytes caps the size of a body read off the wire: the request
// body for [Mount], the response body for [NewTransport], measured after any
// decompression. Bodies exceeding this limit fail with CodeResourceExhausted.
// The default is 4 MiB. Zero allows any size.
func WithMaxReadBytes(n int64) Option {
	return optionFunc(func(o *options) {
		o.maxReadBytes = n
	})
}

// WithCompressors configures the compression algorithms available for request
// and response bodies, keyed by their Content-Encoding token. A handler
// decompresses requests sent with a known encoding and compresses responses
// using the request's encoding, or else the first Accept-Encoding token it
// supports. A transport advertises the names in Accept-Encoding and
// decompresses responses.
//
// Calling WithCompressors with no arguments disables compression entirely. If
// multiple compressors share the same name, only the first is kept.
//
// The default is gzip.
func WithCompressors(compressors ...connect.Compressor) Option {
	return optionFunc(func(o *options) {
		o.compressors = slices.Clone(compressors)
	})
}

// WithSendCompression configures a transport to compress request bodies using
// the named algorithm, which must be registered via [WithCompressors] (or be
// the default gzip compressor).
//
// By default, transports send uncompressed requests. [Mount] ignores this option.
func WithSendCompression(name string) Option {
	return optionFunc(func(o *options) {
		o.sendCompression = name
	})
}

// WithDiscardUnknownQueryParams controls whether unknown query parameters are
// ignored when decoding a REST request. By default (false), unknown parameters
// return an error.
func WithDiscardUnknownQueryParams(discard bool) Option {
	return optionFunc(func(o *options) {
		o.discardUnknownQueryParams = discard
	})
}

// Mount installs Vanguard's REST router onto mux. Methods registered on the
// server that carry a google.api.http annotation (or one supplied via WithRules)
// become REST endpoints. Methods without a rule are silently skipped. A rule
// on a streaming method whose streamed side is not a google.api.HttpBody is an
// error.
//
// The mux interface is the same [connecthttp.ServeMux] used by
// [connecthttp.Mount]. This allows a single *http.ServeMux to host Connect,
// gRPC, and REST routes simultaneously.
//
// Vanguard installs a single catch-all "/" handler rather than per-pattern
// routes, because google.api.http templates support syntax (like `**`,
// single-segment `*`, and `:verb` suffixes) that net/http's pattern matcher
// does not. To scope this catch-all, mount Vanguard on a sub-mux (e.g.,
// `mux.Handle("/api/", subMux)`).
//
// Dispatch flows through [connect.Server.Call], ensuring any interceptors
// registered on the server fire as expected. HTTP has no trailers in this
// binding, so metadata set on the CallInfo's ResponseTrailer is discarded.
func Mount(mux connecthttp.ServeMux, server *connect.Server, opts ...Option) error {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt.applyVanguard(&cfg)
	}
	methods, routes, err := resolveMethods(server, cfg)
	if err != nil {
		return err
	}
	mux.Handle("/", &restHandler{
		server:      server,
		options:     cfg,
		codec:       cfg.effectiveCodec(),
		compressors: newCompressors(cfg.compressors),
		methods:     methods,
		routes:      routes,
	})
	return nil
}

type restHandler struct {
	server      *connect.Server
	options     options
	codec       RESTCodec
	compressors *compressors
	methods     map[string]*method
	routes      *routeTrie
}

func (h *restHandler) ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	target, vars, allowedMethods := h.routes.match(request.URL.Path, request.Method)
	if target == nil {
		if len(allowedMethods) > 0 {
			responseWriter.Header().Set("Allow", strings.Join(slices.Sorted(maps.Keys(allowedMethods)), ", "))
			httpWriteStatus(responseWriter, http.StatusMethodNotAllowed,
				connect.Errorf(connect.CodeUnimplemented, "HTTP method %s not allowed", request.Method))
			return
		}
		httpWriteError(responseWriter, connect.Errorf(connect.CodeNotFound, "no REST route for %s", request.URL.Path))
		return
	}
	method := target.method
	ctx := request.Context()

	requestCompressor, responseCompressor, err := h.compressors.negotiate(
		request.Header.Get("Content-Encoding"), request.Header.Get("Accept-Encoding"))
	if err != nil {
		responseWriter.Header().Set("Accept-Encoding", h.compressors.names)
		httpWriteError(responseWriter, err)
		return
	}

	// Build a CallInfo from the request. Server.Call attaches it to ctx
	// so handlers and interceptors can read it via
	// connect.CallInfoForServerContext.
	info := &connect.CallInfo{
		Spec:             method.spec,
		PeerAddr:         request.RemoteAddr,
		Protocol:         "rest",
		Codec:            h.codec.Name(),
		RequestEncoding:  encodingName(requestCompressor),
		ResponseEncoding: encodingName(responseCompressor),
	}
	for key, vals := range request.Header {
		info.RequestHeader().SetValues(key, vals)
	}

	stream := newServerStream(method, target, vars, &h.options, h.codec, info, responseWriter, request)
	stream.requestCompressor = requestCompressor
	stream.responseCompressor = responseCompressor
	err = h.server.Call(ctx, method.spec.Procedure, info, stream)
	if err == nil && !stream.sent && method.spec.StreamType&connect.StreamTypeServer == 0 {
		err = connect.NewError(connect.CodeInternal, "handler sent no response message")
	}
	// If the stream has already committed response headers (the handler
	// called Send before returning the error), the HTTP status is fixed
	// and we can't insert a JSON error body; drop the error. Otherwise
	// write the google.rpc.Status body with the mapped HTTP status,
	// merging response metadata the handler set on info.
	if err == nil || stream.committed() {
		_ = stream.close()
		return
	}
	setResponseHeaders(responseWriter.Header(), info.ResponseHeader())
	httpWriteError(responseWriter, err)
}

// NewTransport returns a [connect.Transport] that issues REST requests for
// procedures with a google.api.http annotation (or one supplied via WithRules).
//
// The httpClient takes the same interface as [connecthttp.NewTransport]
// (typically an *http.Client). If nil, http.DefaultClient is used.
//
// Each call to NewClientStream resolves the procedure's HTTP rule from the
// provided Spec. Calling an unknown procedure, or one without an HTTP rule,
// returns connect.CodeUnimplemented.
func NewTransport(httpClient connecthttp.HTTPClient, baseURL string, opts ...Option) (connect.Transport, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cfg := defaultOptions()
	for _, opt := range opts {
		opt.applyVanguard(&cfg)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse baseURL: %w", err)
	}
	compressors := newCompressors(cfg.compressors)
	var sendCompressor connect.Compressor
	if name := cfg.sendCompression; name != "" && name != connect.CompressionNameIdentity {
		if sendCompressor = compressors.get(name); sendCompressor == nil {
			return nil, fmt.Errorf("unknown compression %q: supported encodings are %v", name, compressors.names)
		}
	}
	return &transport{
		httpClient:     httpClient,
		baseURL:        parsed,
		options:        cfg,
		codec:          cfg.effectiveCodec(),
		compressors:    compressors,
		sendCompressor: sendCompressor,
	}, nil
}

type transport struct {
	httpClient     connecthttp.HTTPClient
	baseURL        *url.URL
	options        options
	codec          RESTCodec
	compressors    *compressors
	sendCompressor connect.Compressor // nil sends identity

	// methods caches per-procedure rule resolution; the cache key is
	// the procedure name.
	methods sync.Map // procedure string -> *method
}

func (t *transport) NewClientStream(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
	method, err := t.resolveMethod(spec)
	if err != nil {
		return nil, err
	}
	if info, ok := connect.CallInfoForClientContext(ctx); ok {
		info.Spec = spec
		info.PeerAddr = t.baseURL.Host
		info.Protocol = "rest"
		info.Codec = t.codec.Name()
		info.RequestEncoding = encodingName(t.sendCompressor)
	}
	return newClientStream(ctx, t, method, spec), nil
}

func (t *transport) resolveMethod(spec connect.Spec) (*method, error) {
	if v, ok := t.methods.Load(spec.Procedure); ok {
		if cached, ok := v.(*method); ok {
			return cached, nil
		}
	}
	method := methodFromSpec(spec, t.options.resolver)
	if method == nil {
		// Spec.Schema is not a proto MethodDescriptor, so vanguard
		// can't route this call. (See the doc on methodFromSpec.)
		return nil, connect.Errorf(connect.CodeUnimplemented,
			"vanguard: Spec.Schema for %s is not a protoreflect.MethodDescriptor",
			spec.Procedure)
	}
	rule, ok := getHTTPRuleExtension(method.descriptor)
	if !ok {
		// Allow rule lookup via WithRules selectors.
		for _, external := range t.options.rules {
			if procedureFromSelector(external.GetSelector()) == spec.Procedure {
				rule = external
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil, connect.Errorf(connect.CodeUnimplemented,
			"no google.api.http rule for %s", spec.Procedure)
	}
	// Build a one-method routeTrie so makeTarget runs and validates the
	// rule. Only the primary binding is used on the client side; the
	// trie is for path-matching, which clients don't need.
	trie := &routeTrie{}
	target, err := trie.addRoute(method, rule)
	if err != nil {
		return nil, fmt.Errorf("attach rule for %s: %w", spec.Procedure, err)
	}
	method.httpRule = target

	// Concurrent calls may race to resolve the same procedure; the
	// results are equivalent, so whichever is stored first wins.
	t.methods.LoadOrStore(spec.Procedure, method)
	return method, nil
}

var _ connect.Transport = (*transport)(nil)
