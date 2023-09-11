module connectrpc.com/vanguard/internal/examples/pets

go 1.20

// once the repo is public, we can remove this
replace connectrpc.com/vanguard => ../../../

require (
	connectrpc.com/connect v1.11.0
	connectrpc.com/grpcreflect v1.2.0
	connectrpc.com/vanguard v0.0.0-00010101000000-000000000000
	golang.org/x/net v0.10.0
	golang.org/x/sync v0.3.0
	google.golang.org/genproto/googleapis/api v0.0.0-20230803162519-f966b187b2e5
	google.golang.org/protobuf v1.31.0
)

require (
	golang.org/x/text v0.9.0 // indirect
	google.golang.org/genproto v0.0.0-20230807174057-1744710a1577 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230807174057-1744710a1577 // indirect
)
