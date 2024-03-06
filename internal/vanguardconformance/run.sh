#!/bin/bash
connectconformance --mode server --conf=config.yaml -vv \
	--run="Basic/HTTPVersion:1/Protocol:PROTOCOL_GRPC_WEB/Codec:CODEC_PROTO/Compression:COMPRESSION_IDENTITY/TLS:true/unary/success" \
	-- go run . 1>&2
	#--conf=config.yaml \
	#--run="Basic/HTTPVersion:1/Protocol:PROTOCOL_GRPC_WEB/Codec:CODEC_PROTO/Compression:COMPRESSION_IDENTITY/TLS:true/unary/success" \
