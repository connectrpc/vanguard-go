module connectrpc.com/vanguard

go 1.21
toolchain go1.23.7

require (
	buf.build/gen/go/connectrpc/eliza/connectrpc/go v1.11.1-20230822171018-8b8b971d6fde.1
	buf.build/gen/go/connectrpc/eliza/grpc/go v1.3.0-20230822171018-8b8b971d6fde.1
	buf.build/gen/go/connectrpc/eliza/protocolbuffers/go v1.31.0-20230822171018-8b8b971d6fde.1
	connectrpc.com/connect v1.18.1
	connectrpc.com/grpcreflect v1.2.0
	github.com/google/go-cmp v0.6.0
	github.com/stretchr/testify v1.8.4
	golang.org/x/net v0.36.0
	golang.org/x/sync v0.11.0
	google.golang.org/genproto/googleapis/api v0.0.0-20241230172942-26aa7a208def
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241223144023-3abc09e42ca8
	google.golang.org/grpc v1.67.3
	google.golang.org/protobuf v1.36.3
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/text v0.22.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
