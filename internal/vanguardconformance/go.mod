module connectrpc.com/vanguard/internal/vanguardconformance

go 1.23.3

require (
	buf.build/gen/go/connectrpc/conformance/connectrpc/go v1.18.1-20241008212309-5939a22621c8.1
	buf.build/gen/go/connectrpc/conformance/protocolbuffers/go v1.36.5-20241008212309-5939a22621c8.1
	connectrpc.com/vanguard v0.3.0
	google.golang.org/protobuf v1.36.5
)

require (
	connectrpc.com/conformance v1.0.5-0.20250313224606-0f9690cfc68d // indirect
	connectrpc.com/connect v1.18.1 // indirect
	github.com/andybalholm/brotli v1.1.1 // indirect
	github.com/go-task/slim-sprig v0.0.0-20230315185526-52ccab3ef572 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/pprof v0.0.0-20210407192527-94a9f03dee38 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/onsi/ginkgo/v2 v2.9.5 // indirect
	github.com/quic-go/qpack v0.5.1 // indirect
	github.com/quic-go/quic-go v0.50.0 // indirect
	github.com/rs/cors v1.11.1 // indirect
	go.uber.org/mock v0.5.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/mod v0.18.0 // indirect
	golang.org/x/net v0.37.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/tools v0.22.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250313205543-e70fdf4c4cb4 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250313205543-e70fdf4c4cb4 // indirect
	google.golang.org/grpc v1.70.0 // indirect
)

replace connectrpc.com/vanguard => ../../

tool connectrpc.com/conformance/cmd/referenceserver
