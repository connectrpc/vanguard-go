# Proxy

Example REST front end for an RPC backend the proxy has no Go types for. One
forwarding method is built per RPC from the service descriptor alone, so the
same code proxies any service whose schema it can load.

Both halves run in one process over HTTP/1.1. Speaking gRPC to the backend
instead is possible but requires giving the client an h2c transport.
