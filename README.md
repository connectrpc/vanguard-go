# ⚔️ Vanguard

[![Build](https://github.com/connectrpc/vanguard-go/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/connectrpc/vanguard-go/actions/workflows/ci.yaml)
[![GoDoc](https://pkg.go.dev/badge/connectrpc.com/vanguard.svg)](https://pkg.go.dev/github.com/connectrpc/vanguard-go)
[![Slack](https://img.shields.io/badge/slack-buf-%23e01563)][badges_slack]

Vanguard adds REST to [connect-go](https://github.com/connectrpc/connect-go)
services. Using Google's [HTTP transcoding options](https://github.com/googleapis/googleapis/blob/master/google/api/http.proto#L44),
it maps each RPC to an HTTP method, URL path, and JSON body from its strongly
typed Protobuf definition. connect-go's `connecthttp` package serves the
Connect, gRPC, and gRPC-Web protocols from the same `*connect.Server`, so one
set of handlers is reachable from all four.

[See an example in action!](internal/examples/fileserver/main.go)

## Why Vanguard?

1. **REST for RPC services**: Add `google.api.http` annotations and your RPC
handlers also serve REST clients. This is especially handy during a migration
from a REST API to a schema-driven RPC API: existing REST clients keep working
while your server implementations move to Protobuf and RPC.

2. **No extra code generation**: Unlike [gRPC-Gateway](https://github.com/grpc-ecosystem/grpc-gateway#readme),
Vanguard runs inside your Go server and needs no generated gateway code. It
works from Protobuf descriptors at runtime, so service definitions can come
from configuration, schema registries, or
[gRPC Server Reflection](https://github.com/grpc/grpc/blob/master/doc/server-reflection.md).
That makes it a good fit for proxies, which don't need to be recompiled and
redeployed each time an RPC service schema changes.

3. **RPC clients for REST servers**: The same annotations let generated
Connect clients call REST APIs. Teams can adopt RPC, for example in web or
mobile clients, before every backend service has migrated.

4. **Existing gRPC services**: `vanguardgrpc` registers
[grpc-go](https://github.com/grpc/grpc-go) service implementations on a
`*connect.Server`. `connecthttp` then serves them to Connect and gRPC-Web
clients and Vanguard serves them over REST, without rewriting your handlers.

## Usage

Vanguard builds on [connect-go v2](https://github.com/connectrpc/connect-go).
Services register with a `*connect.Server`, and transports expose the server
on the wire. The `connecthttp` package (part of connect-go) serves the Connect,
gRPC, and gRPC-Web protocols. vanguard adds the REST half, driven by the
`google.api.http` annotations on your service's Protobuf schema.

### Serving REST

Register your services with a `*connect.Server`, then mount vanguard on an
`http.ServeMux` alongside `connecthttp`:

```go
server := connect.NewServer()
pingv1connect.RegisterPingServiceHandler(server, &pingServer{})

mux := http.NewServeMux()
connecthttp.Mount(mux, server) // Connect, gRPC, gRPC-Web
if err := vanguard.Mount(mux, server); err != nil { // google.api.http
	log.Fatal(err)
}
log.Fatal(http.ListenAndServe(":8080", mux))
```

Methods whose descriptors carry a `google.api.http` annotation become REST
endpoints. Methods without one are only reachable via the RPC protocols. For
services that don't define the mapping in their Protobuf sources, supply rules
externally with `vanguard.WithRules`.

vanguard installs one catch-all `"/"` handler rather than per-pattern routes:
`google.api.http` templates support `**`, single-segment `*`, and `:verb`
suffixes that `net/http`'s pattern matcher does not. Mount vanguard on a
sub-mux (e.g. `mux.Handle("/api/", subMux)`) to scope its catch-all.

Dispatch flows through `connect.Server.Call`, so interceptors registered on
the server fire for REST requests just like for RPC requests.

### Calling REST servers

`vanguard.NewTransport` returns a `connect.Transport` that translates each
RPC into the REST request described by the method's `google.api.http`
annotation. Generated Connect clients work unchanged:

```go
transport, err := vanguard.NewTransport(http.DefaultClient, "https://api.example.com")
if err != nil {
	log.Fatal(err)
}
client := pingv1connect.NewPingServiceClient(connect.NewClient(transport))
resp, err := client.Ping(ctx, &pingv1.PingRequest{Number: 42})
```

### Proxying

`vanguard.ForwardService` registers methods that relay each call to an
upstream `*connect.Client`, so you can put REST in front of a backend without
its generated code. Only the service descriptor is needed:

```go
upstream := connect.NewClient(connecthttp.NewTransport(http.DefaultClient, backendURL))
server := connect.NewServer()
server.Register(vanguard.ForwardService(upstream, serviceDescriptor)...)

mux := http.NewServeMux()
if err := vanguard.Mount(mux, server); err != nil {
	log.Fatal(err)
}
```

[See the proxy example.](internal/examples/proxy/main.go)

### gRPC Handlers

The generated Go code for gRPC does not include a handler factory like Connect
uses. Its generated functions instead register handler information with a
`grpc.ServiceRegistrar`. The `vanguardgrpc` package provides a registrar that
adapts those stubs onto a `*connect.Server`:

```go
server := connect.NewServer()
registrar := vanguardgrpc.NewServiceRegistrar(server)

// Existing protoc-gen-go-grpc-generated code:
pingv1grpc.RegisterPingServiceServer(registrar, &pingServer{})

mux := http.NewServeMux()
connecthttp.Mount(mux, server) // Connect, gRPC, gRPC-Web
if err := vanguard.Mount(mux, server); err != nil { // google.api.http
	log.Fatal(err)
}
```

## Status: Alpha

Vanguard is undergoing initial development and is not yet stable.


## Legal

Offered under the [Apache 2 license][badges_license].


[badges_license]: https://github.com/connectrpc/vanguard-go/blob/main/LICENSE
[badges_slack]: https://buf.build/links/slack
