#!/bin/bash

pushd ./internal/vanguardconformance
connectconformance --mode server --conf=config.yaml -vv \
	--run="Basic/HTTPVersion:1/Protocol:PROTOCOL_GRPC_WEB/Codec:CODEC_PROTO/Compression:COMPRESSION_IDENTITY/TLS:true/unary/success" \
	-- go run . 1>&2
popd
#"Basic/HTTPVersion:1/Protocol:PROTOCOL_CONNECT/Codec:CODEC_JSON/Compression:COMPRESSION_IDENTITY/TLS:false/client-stream/success"
#--run="Basic/HTTPVersion:1/**" \
