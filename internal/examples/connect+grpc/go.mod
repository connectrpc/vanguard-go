module connectrpc.com/vanguard/internal/examples/connect+grpc

go 1.20

// once the repo is public, we can remove this
replace connectrpc.com/vanguard => ../../../

require (
	buf.build/gen/go/connectrpc/eliza/connectrpc/go v1.11.1-20230822171018-8b8b971d6fde.1
	buf.build/gen/go/connectrpc/eliza/grpc/go v1.3.0-20230822171018-8b8b971d6fde.1
	buf.build/gen/go/connectrpc/eliza/protocolbuffers/go v1.31.0-20230822171018-8b8b971d6fde.1
	connectrpc.com/connect v1.11.1
	connectrpc.com/vanguard v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.58.0
)

require (
	github.com/golang/protobuf v1.5.3 // indirect
	golang.org/x/net v0.12.0 // indirect
	golang.org/x/sys v0.10.0 // indirect
	golang.org/x/text v0.11.0 // indirect
	google.golang.org/genproto v0.0.0-20230807174057-1744710a1577 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20230803162519-f966b187b2e5 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230807174057-1744710a1577 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
)
