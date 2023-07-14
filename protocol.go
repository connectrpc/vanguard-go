// Copyright 2023 Buf Technologies, Inc.

package vanguard

import "strings"

type protocolType int

const (
	protocolTypeGRPC protocolType = iota
	protocolTypeGRPCWeb
	protocolTypeREST
)

func isGRPC(protocol string, contentType string) bool {
	return protocol == "HTTP/2" && strings.HasPrefix(contentType, "application/grpc")
}

func isGRPCWeb(contentType string) bool {
	return strings.HasPrefix(contentType, "application/grpc-web")
}

func isREST(contentType string) bool {
	return strings.HasPrefix(contentType, "application/json") ||
		strings.HasPrefix(contentType, "application/protobuf")
}
