# ⚔️ Vanguard

[![Build](https://github.com/connectrpc/vanguard-go/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/connectrpc/vanguard-go/actions/workflows/ci.yaml)
[![GoDoc](https://pkg.go.dev/badge/connectrpc.com/vanguard.svg)](https://pkg.go.dev/github.com/connectrpc/vanguard-go)
[![Slack](https://img.shields.io/badge/slack-buf-%23e01563)][badges_slack]

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
vanguard.Mount(mux, server)    // google.api.http
```

## Status: Alpha

Vanguard is undergoing initial development and is not yet stable.


## Legal

Offered under the [Apache 2 license][badges_license].


[badges_license]: https://github.com/connectrpc/vanguard-go/blob/main/LICENSE
[badges_slack]: https://buf.build/links/slack
