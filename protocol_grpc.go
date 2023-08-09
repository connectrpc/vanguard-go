// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

//nolint:forbidigo,revive,gocritic // this is temporary, will be removed when implementation is complete
package vanguard

import (
	"io"
	"net/http"

	"connectrpc.com/connect"
)

type grpcClientProtocol struct{}

func (g grpcClientProtocol) protocol() Protocol {
	return ProtocolGRPC
}

func (g grpcClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	return true
}

func (g grpcClientProtocol) allowsGetRequests() bool {
	return false
}

func (g grpcClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (g grpcClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (g grpcClientProtocol) encodeEnd(end responseEnd, writer io.Writer) (http.Header, error) {
	//TODO implement me
	panic("implement me")
}

func (g grpcClientProtocol) String() string {
	return protocolNameGRPC
}

type grpcServerProtocol struct{}

func (g grpcServerProtocol) protocol() Protocol {
	return ProtocolGRPC
}

func (g grpcServerProtocol) addProtocolRequestHeaders(meta requestMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (g grpcServerProtocol) extractProtocolResponseHeaders(i int, header http.Header) (responseMeta, func(io.Reader, *responseEnd), error) {
	//TODO implement me
	panic("implement me")
}

func (g grpcServerProtocol) extractEndFromTrailers(o *operation, header http.Header) (responseEnd, error) {
	//TODO implement me
	panic("implement me")
}

func (g grpcServerProtocol) String() string {
	return protocolNameGRPC
}

type grpcWebClientProtocol struct{}

func (g grpcWebClientProtocol) protocol() Protocol {
	return ProtocolGRPCWeb
}

func (g grpcWebClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	return true
}

func (g grpcWebClientProtocol) allowsGetRequests() bool {
	return false
}

func (g grpcWebClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (g grpcWebClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (g grpcWebClientProtocol) encodeEnd(end responseEnd, writer io.Writer) (http.Header, error) {
	//TODO implement me
	panic("implement me")
}

func (g grpcWebClientProtocol) String() string {
	return protocolNameGRPCWeb
}

type grpcWebServerProtocol struct{}

func (g grpcWebServerProtocol) protocol() Protocol {
	return ProtocolGRPCWeb
}

func (g grpcWebServerProtocol) addProtocolRequestHeaders(meta requestMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (g grpcWebServerProtocol) extractProtocolResponseHeaders(i int, header http.Header) (responseMeta, func(io.Reader, *responseEnd), error) {
	//TODO implement me
	panic("implement me")
}

func (g grpcWebServerProtocol) extractEndFromTrailers(o *operation, header http.Header) (responseEnd, error) {
	//TODO implement me
	panic("implement me")
}

func (g grpcWebServerProtocol) String() string {
	return protocolNameGRPCWeb
}
