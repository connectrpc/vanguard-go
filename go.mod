module connectrpc.com/vanguard

go 1.26.0

require (
	connectrpc.com/connect/v2 v2.0.0
	github.com/google/go-cmp v0.7.0
	github.com/stretchr/testify v1.12.1
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.11
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.6.1 // indirect
)

tool google.golang.org/grpc/cmd/protoc-gen-go-grpc
