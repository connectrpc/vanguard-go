# File Server

Example file server app using RPC to connect. This uses the support in Vanguard
for streaming uploads/downloads via the use of
[`google.api.HttpBody`](https://github.com/googleapis/googleapis/blob/ecb1cf0a0021267dd452289fc71c75674ae29fe3/google/api/httpbody.proto#L28).

```sh
go run ./internal/examples/fileserver -d /tmp -p 8100

# Index a directory or fetch a file.
curl http://localhost:8100/

# Upload a file (client-streaming RPC).
curl -X POST --data-binary @hello.txt -H "Content-Type: text/plain" \
  http://localhost:8100/hello.txt:upload

# Download a file (server-streaming RPC).
curl http://localhost:8100/hello.txt:download -o hello.txt
```
