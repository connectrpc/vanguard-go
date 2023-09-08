# ⚔️ Vanguard

[![License](https://img.shields.io/github/license/connectrpc/vanguard-go?color=blue)][badges_license]
[![Slack](https://img.shields.io/badge/slack-buf-%23e01563)][badges_slack]
[![Build](https://github.com/connectrpc/vanguard-go/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/connectrpc/vanguard-go/actions/workflows/ci.yaml)
[![Report Card](https://goreportcard.com/badge/connectrpc.com/vanguard)](https://goreportcard.com/report/github.com/connectrpc/vanguard-go)
[![GoDoc](https://pkg.go.dev/badge/connectrpc.com/vanguard.svg)](https://pkg.go.dev/github.com/connectrpc/vanguard-go)

This repo provides middleware for Go `net/http` servers that can translate between various
RPC protocols. It can seamlessly translate between gRPC, gRPC-Web, and Connect protocols,
and it can also translate to/from REST if your Protobuf services are annotated with Google's
[HTTP transcoding options](https://github.com/googleapis/googleapis/blob/master/google/api/http.proto#L44).

[See an example in action!](internal/examples/fileserver/main.go)

There are a handful of key use cases for such middleware:
1. Using the above mentioned HTTP transcoding annotations allows you to support REST
   clients. This can be particularly useful when migrating from a REST API to a schema-driven
   RPC API. With the right annotations, existing REST clients can continue to access
   your API, even after the server implementations have been migrated to Protobuf and RPC.

   This middleware can effectively replace the use of [gRPC-Gateway](https://github.com/grpc-ecosystem/grpc-gateway#readme)
   when it's used in-process in a Go server. In particular, this middleware is compatible
   with _all_ kinds of handlers -- Connect RPC, gRPC, etc. gRPC-Gateway, on the other hand,
   requires the use of gRPC handlers and both the [gRPC Protobuf plugin](https://pkg.go.dev/google.golang.org/grpc/cmd/protoc-gen-go-grpc)
   as well as a bespoke [gRPC-Gateway Protobuf plugin](https://pkg.go.dev/github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway).
   Unlike gRPC-Gateway, this middleware does not require any additional code generation.

   Since it does not rely on code generation, Vanguard can be used in dynamic situations,
   where service definitions are loaded from configuration, a schema registry, or via
   [gRPC Server Reflection](https://github.com/grpc/grpc/blob/master/doc/server-reflection.md).
   This allows it to be used in proxies without needing to recompile and redeploy the proxy
   whenever an RPC service schema is changed.

2. The HTTP transcoding annotations also allow you to support legacy REST API servers
   from clients that are use Protobuf RPC. This allows you to adopt RPC in some teams,
   like for web or mobile clients, without having to first migrate all of the backend
   API services.

3. If your organization is in the process of migrating from gRPC to Connect, you can use
   this middleware to bridge the protocols. This allows you to use your existing gRPC
   service handlers with Connect clients. (The reverse works without any middleware:
   Connect servers can natively handle gRPC clients.)

   This is particularly valuable since a big draw of the Connect protocol over gRPC is its
   usability and inspectability with web browser and mobile device clients, and this
   middleware allows you to migrate clients to Connect without having to first migrate
   your server handler logic.


## Usage

The middleware is simple to configure and is used to wrap other HTTP handlers. The
kinds of HTTP handlers you'll typically be wrapping are:
1. gRPC handlers: After configuring a `*grpc.Server`, instead of calling its `Start`
   method, it can be mounted as a handler of an `http.Server` or `http.ServeMux`.
   This allows you to decorate the gRPC handler with HTTP middleware.
2. Connect handlers: With Connect, handlers already implement `http.Handler` and are
   thus trivial to wrap with the HTTP middleware.
3. Proxy handlers: In some cases, your Go service may act as a proxy and forward
   requests to a different backend server. To support legacy REST API servers, the
   Go server can proxy requests to those legacy backends, and the HTTP middleware can
   translate incoming RPC requests to a form that the legacy backends can understand.

### Configuring the middleware

The middleware takes the form of a `Mux`. All handlers to be decorated with the
middleware are registered with the `Mux`. When the `Mux` is instantiated, you can
provide configuration by setting exported fields on the `Mux`. These fields control
how requests are received by the wrapped handler. The `Mux` itself can receive
requests in a wide variety of flavors (Connect, gRPC, gRPC-Web, REST), and it then
transforms the requests to be compatible with the handler.
```go
vanguardMux := &vanguard.Mux{
	// The wrapped handler expects the gRPC protocol.
	Protocols: []vanguard.Protocol{vanguard.ProtocolGRPC},
	// The wrapped handler supports Protobuf binary encoding.
	Codecs: []string{vanguard.CodecProto},
	// The wrapped handler supports gzip compression for content encoding.
	Compressors: []string{vanguard.CompressionGzip},
	// When buffering is required for protocol translation, do not buffer
	// more than 8 megabytes.
	MaxMessageBufferSize: 8*1024*1024,
}
```
The above example can be used to wrap a gRPC handler, so that the handler can
now also support Connect, gRPC-Web, and REST clients.

The above options can also be configured on a _per service_ basis. So the options
defined on the `vanguard.Mux` will serve as defaults, which can be overridden when
registering a particular service.

If no options are provided, the resulting middleware is useful for wrapping a
Connect handler: it assumes the server handler can support Connect, gRPC, and gRPC-Web
protocols; that it supports both Protobuf and JSON encoding; and that it supports
Gzip compression. There is no message buffer limit by default, so with no options the
middleware can translate any request, regardless of how much data it may require
buffering. (But configuring a limit is highly recommended to reduce chances of abuse
and high resource utilization caused by pathological requests or misbehaving clients.)

### Registering services

In order to apply the middleware to a handler, it is necessary to indicate
which Protobuf service(s) the handler implements. When wrapping Connect handlers,
this can be easily accomplished by referencing the generated service name constants:

```go
// Here's an example using a Connect handler.
_, svcHandler := myservicev1connect.NewMyServiceHandler(&myServiceImpl{})
err := vanguardMux.RegisterServiceByName(
	svcHandler,
	myservicev1.MyServiceName,
	// Overrides for configuration can be supplied here
	vanguard.WithMaxMessageBufferSize(16*1024*1024),
)
if err != nil {
	panic(err)
}
```
If not using Connect code generation, these constants may not be available and
the service names may instead need to be hard-coded.
```go
// And here's an example using a gRPC handler.
svr := grpc.NewServer()
myservicev1.RegisterMyServiceServer(svr, &myServiceImpl{})
err := vanguardMux.RegisterServiceByName(
	svr,
	"foo.bar.myservice.v1.MyService",
	// Overrides for configuration can be supplied here
	vanguard.WithMaxMessageBufferSize(16*1024*1024),
)
if err != nil {
	panic(err)
}

// As an alternative to above: If all services registered with the
// gRPC server use the same configuration, you can use a loop to
// register all of them without needing hard-coded service names:
for name := range svr.GetServiceInfo() {
	err := vanguardMux.RegisterServiceByName(
		svr,
		name,
		vanguard.WithMaxMessageBufferSize(16*1024*1024),
	)
	if err != nil {
		panic(err)
	}
}
```

If a single handler supports multiple services, you can register it repeatedly
with the vanguard Mux, specifying a different service name each time.

When specifying a service name, the actual schema for the service is loaded from
the Protobuf runtime library. The runtime library contains the schemas for all
Protobuf services for which generated code has been linked into your program.

For more dynamic use cases, you can instead use the `RegisterService` method and
supply a `protoreflect.ServiceDescriptor`, which is a full description of the
service's schema. (Such a descriptor could be loaded from a configuration file
or downloaded from a remote server or registry.)

### Wiring up to a server

Finally, you can register the middleware handler with an `http.Server` or
`http.ServeMux`.

```go
// The Mux can be used as the sole handler for an HTTP server.
err := http.Serve(listener, vanguardMux.AsHandler())

// Or it can be used alongside other handlers, all registered with
// the same http.ServeMux.
mux := http.NewServeMux()
mux.Handle("/", vanguardMux.AsHandler())
err := http.Serve(listener, mux)
```
The above example registers the handler for the root path. This is useful
to support REST requests for the service, which could have very different
path URLs from those used for Connect and gRPC. Using a pattern this broad
means the vanguard Mux can handle all paths that might correspond to a method,
without having to explicitly configure it for the various paths named in HTTP
transcoding annotations.

For the same reason, it is best to use a single vanguard Mux, even if your
service supports many exposed RPC services, so that the Mux can handle
dispatch to the correct service based on the request. You can register many
services with the same vanguard Mux. And if any need different configuration,
that can be handled by providing override options when the service is
registered.


## Status: Alpha

Vanguard is undergoing initial development and is not yet stable.


## Legal

Offered under the [Apache 2 license][badges_license].


[badges_license]: https://github.com/connectrpc/vanguard-go/blob/main/LICENSE
[badges_slack]: https://buf.build/links/slack
