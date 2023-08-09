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

const (
	protocolNameConnectUnary     = protocolNameConnect + " unary"
	protocolNameConnectUnaryGet  = protocolNameConnectUnary + " (GET)"
	protocolNameConnectUnaryPost = protocolNameConnectUnary + " (POST)"
	protocolNameConnectStream    = protocolNameConnect + " stream"
)

// connectUnaryGetClientProtocol implements the Connect protocol for
// processing unary RPCs received from the client that use GET as the
// HTTP method.
type connectUnaryGetClientProtocol struct{}

func (c connectUnaryGetClientProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryGetClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	return streamType == connect.StreamTypeUnary
}

func (c connectUnaryGetClientProtocol) allowsGetRequests() bool {
	return true
}

func (c connectUnaryGetClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) encodeEnd(end responseEnd, writer io.Writer) (http.Header, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) String() string {
	return protocolNameConnectUnaryGet
}

// connectUnaryPostClientProtocol implements the Connect protocol for
// processing unary RPCs received from the client that use POST as the
// HTTP method.
type connectUnaryPostClientProtocol struct{}

func (c connectUnaryPostClientProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryPostClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	return streamType == connect.StreamTypeUnary
}

func (c connectUnaryPostClientProtocol) allowsGetRequests() bool {
	return false
}

func (c connectUnaryPostClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryPostClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryPostClientProtocol) encodeEnd(end responseEnd, writer io.Writer) (http.Header, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryPostClientProtocol) String() string {
	return protocolNameConnectUnaryPost
}

// connectUnaryBackendProtocol implements the Connect protocol for
// sending unary RPCs to the handler.
type connectUnaryServerProtocol struct{}

func (c connectUnaryServerProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryServerProtocol) addProtocolRequestHeaders(meta requestMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryServerProtocol) extractProtocolResponseHeaders(i int, header http.Header) (responseMeta, func(io.Reader, *responseEnd), error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryServerProtocol) extractEndFromTrailers(o *operation, header http.Header) (responseEnd, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryServerProtocol) String() string {
	return protocolNameConnectUnary
}

type connectStreamClientProtocol struct{}

func (c connectStreamClientProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectStreamClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	return streamType != connect.StreamTypeUnary
}

func (c connectStreamClientProtocol) allowsGetRequests() bool {
	return false
}

func (c connectStreamClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) encodeEnd(end responseEnd, writer io.Writer) (http.Header, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) String() string {
	return protocolNameConnectStream
}

type connectStreamServerProtocol struct{}

func (c connectStreamServerProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectStreamServerProtocol) addProtocolRequestHeaders(meta requestMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) extractProtocolResponseHeaders(i int, header http.Header) (responseMeta, func(io.Reader, *responseEnd), error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) extractEndFromTrailers(o *operation, header http.Header) (responseEnd, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) String() string {
	return protocolNameConnectStream
}
