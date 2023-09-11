# Pet Store

This is an example app that makes use of all of the protocol translation features
of Vanguard.

It is built on top of the [PetStore API][petstore] that is
available as an OpenAPI spec. This example includes a Protobuf definition that
matches the "pets" entry points of the PetStore. (The "store" and "users" entry
points are not included.)

This example includes two components:

1. `pets-fe`: The Pets _Frontend_ is a server that accepts requests in any a
   variety of protocols: REST, Connect, gRPC, or gRPC-Web. It uses the Vanguard
   middleware to wrap a reverse proxy, so it translates the requests to an RPC
   protocol and then forwards them to the `pets-be` backend service.

   This server listens on three ports:
   * **30301**: Requests to this port will be translated to the Connect protocol
     before being sent to the backend.
   * **30302**: Requests to this port will be translated to the gRPC protocol
     before being sent to the backend. It will also transcode requests that use
     the JSON format to the Protobuf binary format.
   * **30303**: Requests to this port will be translated to the gRPC-Web protocol
     before being sent to the backend. It will also transcode requests that use
     the JSON format to the Protobuf binary format.

   This server traces incoming requests to stdout.

2. `pets-be`: The Pets _Backend_ is an RPC server that accepts RPC requests from
   the frontend. It also uses the Vanguard middleware to wrap a reverse proxy, so
   it translates RPC requests into REST requests that are forwarded to the public
   example API server at [petstore.swagger.io][petstore].

   This server listens for requests on port 30304.

   This server traces both incoming (RPC) and outgoing (REST) requests.

You can try out these servers by running both and then issuing the following `curl`
command:

```shell
curl 'http://localhost:30302/pet/findByStatus?status=available' -v -X GET
```

The above sends a simple REST request to the frontend. This will get translated to
gRPC and sent to the backend. The backend will then turn it _back_ into a REST
request and send to petstore.swagger.io. And all of this activity can be seen in
the log output from the servers.

[petstore]: https://petstore.swagger.io/