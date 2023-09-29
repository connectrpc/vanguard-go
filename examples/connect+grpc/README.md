# Supporting Connect Clients from gRPC Server

This example shows how Vanguard can be used to support a migration from gRPC to Connect.
In such a migration, an organization can start using Connect clients first, typically from
web browser clients and/or mobile app clients. In order to ease the migration, instead of
having to first update servers, to implement both gRPC and Connect handler interfaces, one
can use Vanguard to translate Connect client requests to gRPC, so that existing gRPC handler
implementations can continue to be used.

The example is an implementation of the demo service in [buf.build/connectrpc/eliza][eliza].
This is an _extremely_ simple implementation, since the actual service and any logic for it
are not the purpose of this example.

This example has two components:
1. `server`: The server command is a gRPC server. It uses the `google.golang.org/grpc` package
   as the server runtime and uses generated code from `protoc-gen-grpc-go`. But it wraps the
   server handler in Vanguard middleware so that it transparently can support the Connect and
   gRPC-Web protocols as well.
2. `client`: The client command is a simple command-line interface. It uses the Connect protocol
   and the `connectrpc.com/connect` runtime package. It prompts the user to interact with the
   Eliza service and simply echoes back the service responses.
