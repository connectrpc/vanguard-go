# ⚔️ Vanguard

[![License](https://img.shields.io/github/license/connectrpc/vanguard-go?color=blue)][badges_license]
[![Slack](https://img.shields.io/badge/slack-buf-%23e01563)][badges_slack]
[![Build](https://github.com/connectrpc/vanguard-go/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/connectrpc/vanguard-go/actions/workflows/ci.yaml)
[![Report Card](https://goreportcard.com/badge/connectrpc.com/vanguard)](https://goreportcard.com/report/github.com/connectrpc/vanguard-go)
[![GoDoc](https://pkg.go.dev/badge/connectrpc.com/vanguard.svg)](https://pkg.go.dev/github.com/connectrpc/vanguard-go)

Vanguard is a powerful library for Go `net/http` servers that enables seamless
transcoding between REST and RPC protocols. Whether you need to bridge the gap
between gRPC, gRPC-Web, Connect, or REST, Vanguard has got you covered. With support for
Google's [HTTP transcoding options](https://github.com/googleapis/googleapis/blob/master/google/api/http.proto#L44),
it can effortlessly translate protocols using strongly typed Protobuf definitions.

[See an example in action!](internal/examples/fileserver/main.go)

## Why Vanguard?

Vanguard offers a range of compelling use cases that make it an invaluable addition
to your services:

1. **RESTful Transformation**: By leveraging HTTP transcoding annotations, you can effortlessly 
support REST clients. This feature is especially handy during the migration from a REST API 
to a schema-driven RPC API. With the right annotations, your existing REST clients can 
seamlessly access your API, even as you transition your server implementations to Protobuf 
and RPC.

2. **Efficiency and Code Generation**: Unlike traditional approaches like [gRPC-Gateway](https://github.com/grpc-ecosystem/grpc-gateway#readme), 
Vanguard operates efficiently within Go servers, compatible with various servers such as 
[Connect](https://github.com/connectrpc/connect-go) and [gRPC](https://github.com/grpc/grpc-go). 
It doesn't rely on extensive code generation, eliminating the need for additional code 
generation steps. This flexibility ensures that your code can adapt dynamically, loading 
service definitions from configuration, schema registries, or via 
[gRPC Server Reflection](https://github.com/grpc/grpc/blob/master/doc/server-reflection.md), 
making it a perfect fit for proxies without the hassle of recompilation and redeployment 
each time an RPC service schema changes.

3. **Legacy Compatibility**: The HTTP transcoding annotations also empower you to support 
legacy REST API servers when clients are accustomed to using Protobuf RPC. This lets 
you embrace RPC in specific teams, such as for web or mobile clients, without the 
prerequisite of migrating all backend API services.

4. **Seamless Protocol Bridging**: If your organization is transitioning from gRPC to Connect, 
Vanguard acts as a bridge between the protocols. This facilitates the use of your existing 
gRPC service handlers with Connect clients, allowing you to smoothly adapt to Connect's 
enhanced usability and inspectability with web browsers and mobile devices. No need to 
overhaul your server handler logic before migrating clients to Connect.

## Usage

Vanguard is straight-forward to configure and is used to wrap other HTTP handlers. In
that regard, a Vanguard transcoder acts kind of like HTTP middleware. The kinds of HTTP
handlers you'll typically be wrapping are:
1. gRPC handlers: After configuring a `*grpc.Server`, instead of calling its `Start`
   method, it can be mounted as a handler of an `http.Server` or `http.ServeMux`.
   This allows you to decorate the gRPC handler with the Vanguard middleware.
2. Connect handlers: With Connect, handlers already implement `http.Handler` and are
   thus trivial to wrap with Vanguard.
3. Proxy handlers: In some cases, your Go service may act as a proxy and forward
   requests to a different backend server. To support legacy REST API servers, the
   Go server can proxy requests to those legacy backends, and Vanguard can transcode
   incoming RPC requests to a form that the legacy backends can understand.

### Defining Services

A `Service` in Vanguard is the configuration for a single Protobuf RPC service. This
configuration includes a schema for the service, which empowers the format translations
and is also the typical source HTTP annotations, for mapping an RPC to REST-ful
conventions. It also includes a handler, which is an `http.Handler` that implements the
service. Finally, it can include options that configure the protocols and formats that
the handler can accept.

You create one by calling `vanguard.NewService`, supplying the service's fully-qualified
name (or URI path, which is the fully-qualified name with a leading and trailing slash)
and the corresponding handler.

If you supply no other options, a service's default configuration supports a Connect
handler. So if you use the `protoc-gen-connect-go` Protobuf plugin and use the
generated factory function for creating a handler, it will work perfectly with
Vanguard.
```go
// A Connect handler factory function returns the service path and the HTTP
// handler. So you can pass the result directly to NewService:
myService := vanguard.NewService(
    myservicev1connect.NewMyServiceHandler(&myServiceImpl{}),
)

// If not using a Connect handler, you can still use this function and
// directly refer to the service's name.
myService = vanguard.NewService(
    myservicev1connect.MyServiceName,
	someOtherHTTPHandler,
)
```

With the `vanguard.NewService` function, the service must be known to the program. That
means that Go code has been generated for the Protobuf file that defines the RPC service,
using the `protoc-gen-go` plugin, and then imported into the program. That generated Go
code registers the service's schema in a global registry named
[`protoregistry.GlobalFiles`](https://pkg.go.dev/google.golang.org/protobuf/reflect/protoregistry#Files).

If you are using Connect or gRPC handlers, there is nothing to worry about: the use of
generated Go code for Connect and gRPC stubs and handler interfaces ensures that the
services are known.

#### Dynamic Schemas

If you want to handle a service whose schema is _not_ known to the program at compile-time,
then you can use `vanguard.NewServiceWithSchema` and supply a
[`protoreflect.ServiceDescriptor`](https://pkg.go.dev/google.golang.org/protobuf/reflect/protoreflect#ServiceDescriptor)
that defines the RPC schema. This descriptor could have been loaded from a configuration file
or [file descriptor set](https://pkg.go.dev/google.golang.org/protobuf/reflect/protodesc#NewFiles),
downloaded from a server, such as a [Buf Schema Registry](https://github.com/bufbuild/reflect-proto),
or even [compiled from Protobuf sources](https://github.com/bufbuild/protocompile#readme).

#### Service Options

Both `vanguard.NewService` and `vanguard.NewServiceWithSchema` functions accept options
for configuring how a transcoder will process requests for that service. In particular,
you can configure the protocols, the codecs/message formats, and compression formats
that the service handler accepts.

As mentioned previously, the default configuration -- with no options -- is designed to
work out-of-the-box with Connect handlers: it assumes the handler can process Connect,
gRPC, and gRPC-Web protocols; that it can process "proto" and "json" message formats; and
that it can handle "gzip" compression.

You can change this, including adding more message formats or compression algorithms,
by supplying options:

```go
// In this example, we'll assume that proxyHandler functions as a reverse proxy
// to a gRPC backend that only supports the gRPC protocol and the "proto" message
// format.
otherService := vanguard.NewService(
	"some.other.Service",
	proxyHandler,
	WithTargetProtocols(vanguard.ProtocolGRPC),
	WithTargetCodecs(vanguard.CodecProto)
)
```

### Building the Transcoder

The thing that does all of the work in Vanguard is a `*vanguard.Transcoder`. When the
transcoder is instantiated, you provide the set of services. The transcoder implements
`http.Handler` and handles dispatching requests to the various configured service
handlers, and it also handles translating requests into a form that the handler
understands. It then also translates responses into the form that the client is
expecting.

```go
transcoder := vanguard.NewTranscoder([]*vanguard.Service{
    myService,
    otherService,
})
```

#### Transcoder Options

When creating a transcoder, you can supply options to customize it:
* You can provide a custom handler for unknown endpoints. Without this, requests
  for unrecognized URI paths will result in a simple "404 Not Found" response.
* You can provide separate HTTP annotations. This is to provide REST-ful mappings
  for RPC services that do not define the mapping in their Protobuf sources. It
  also allows you to _add_ mappings to a service that does already have annotations.
* You can add support for extra codecs, beyond "proto" and "json", and compression
  algorithms, beyond "gzip".

You can also supply service options that act as default service options that will
apply to every service (unless overridden via other options in a call to `NewService`).

```go
transcoder = vanguard.NewTranscoder(
	[]*vanguard.Service{
        myService,
        otherService,
    },
	WithUnkownHandler(custom404handler),
	WithCodec(myCustomMessageFormat{}),
)
```

The use of `WithCodec` and `WithCompression` can also be used to replace the default
implementations of the "proto" and "json" codec or the "gzip" compression algorithm.
Note that when replacing the "json" codec, you should use `*vanguard.JSONCodec` as the
implementation if you want to also support the REST protocol. REST-ful mappings can
require message formatting features that are implemented by `*vanguard.JSONCodec` but
not in the base `vanguard.Codec` interface.

#### gRPC Handlers

You can also wrap gRPC handlers with a `Transcoder`. This works a little differently
than wrapping Connect handlers. The generated Go code for gRPC does not include a handler
factory like Connect uses. It's generated functions instead register handler information
with a `*grpc.Server`.

So you would register the various service implementations with a `*grpc.Server`, which
implements `http.Handler` and can then be wrapped with a transcoder. But you can also
use the `vanguardgrpc` package to do everything in a single function call:

```go
transcoder = vanguardgrpc.NewTranscoder(grpcServer)
```
When you use `vanguardgrpc.NewTranscoder`, it automatically creates a `*vanguard.Service`
for each service registered with the gRPC service, and supplies service options
that tell it to transcode to the gRPC protocol and the "proto" codec. If you also
install a "json" codec for gRPC, it will configure the transcoder to also allow
sending the "json" codec to the gRPC server, which makes handling of JSON Connect
requests much more efficient.

### Wiring up to a server

Finally, you can register the transcoder with an `http.Server` or
`http.ServeMux`.

```go
// The Mux can be used as the sole handler for an HTTP server.
err := http.Serve(listener, transcoder)

// Or it can be used alongside other handlers, all registered with
// the same http.ServeMux.
mux := http.NewServeMux()
mux.Handle("/", transcoder)
err := http.Serve(listener, mux)
```
The above example registers the transcoder for the root path. This is useful
to support REST requests for the service, which could have very different
path URLs from those used for Connect and gRPC. Using a pattern this broad
means the transcoder can handle all paths that might correspond to a method,
without having to explicitly configure the `ServeMux` for the various paths
named in HTTP transcoding annotations.

For the same reason, it is best to use a single vanguard transcoder, even if
your service supports many exposed RPC services, so that the transcoder can
dispatch to the correct service based on the request. You can register many
services with the same transcoder. And if any need different configuration,
that can be handled by providing override options when the service is
created.


## Status: Alpha

Vanguard is undergoing initial development and is not yet stable.


## Legal

Offered under the [Apache 2 license][badges_license].


[badges_license]: https://github.com/connectrpc/vanguard-go/blob/main/LICENSE
[badges_slack]: https://buf.build/links/slack
