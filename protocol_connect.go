// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

//nolint:forbidigo,revive,gocritic // this is temporary, will be removed when implementation is complete
package vanguard

import (
	"io"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
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

var _ clientProtocolHandler = connectUnaryGetClientProtocol{}
var _ clientProtocolAllowsGet = connectUnaryGetClientProtocol{}
var _ clientBodyPreparer = connectUnaryGetClientProtocol{}

func (c connectUnaryGetClientProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryGetClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	return streamType == connect.StreamTypeUnary
}

func (c connectUnaryGetClientProtocol) allowsGetRequests() {}

func (c connectUnaryGetClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header, allowedCompression []string) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) encodeEnd(codec Codec, end *responseEnd, writer io.Writer) http.Header {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) requestNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) prepareUnmarshalledRequest(op *operation, src []byte, target proto.Message) error {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) responseNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) prepareMarshalledResponse(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error) {
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

var _ clientProtocolHandler = connectUnaryPostClientProtocol{}

func (c connectUnaryPostClientProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryPostClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	return streamType == connect.StreamTypeUnary
}

func (c connectUnaryPostClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryPostClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header, allowedCompression []string) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryPostClientProtocol) encodeEnd(codec Codec, end *responseEnd, writer io.Writer) http.Header {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryPostClientProtocol) String() string {
	return protocolNameConnectUnaryPost
}

// connectUnaryServerProtocol implements the Connect protocol for
// sending unary RPCs to the server handler.
type connectUnaryServerProtocol struct{}

// NB: the latter two interfaces must be implemented to handle GET requests.
var _ serverProtocolHandler = connectUnaryServerProtocol{}
var _ requestLineBuilder = connectUnaryServerProtocol{}
var _ serverBodyPreparer = connectUnaryServerProtocol{}
var _ serverProtocolEndMustBeInHeaders = connectUnaryServerProtocol{}

func (c connectUnaryServerProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryServerProtocol) endMustBeInHeaders() bool {
	return true
}

func (c connectUnaryServerProtocol) addProtocolRequestHeaders(meta requestMeta, header http.Header, allowedCompression []string) {
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

func (c connectUnaryServerProtocol) requestNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryServerProtocol) prepareMarshalledRequest(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryServerProtocol) responseNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryServerProtocol) prepareUnmarshalledResponse(op *operation, src []byte, target proto.Message) error {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryServerProtocol) requiresMessageToProvideRequestLine(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryServerProtocol) requestLine(op *operation, req proto.Message) (urlPath, queryParams, method string, includeBody bool, err error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryServerProtocol) String() string {
	return protocolNameConnectUnary
}

// connectStreamClientProtocol implements the Connect protocol for
// processing streaming RPCs received from the client.
type connectStreamClientProtocol struct{}

var _ clientProtocolHandler = connectStreamClientProtocol{}
var _ envelopedProtocolHandler = connectStreamClientProtocol{}

func (c connectStreamClientProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectStreamClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	return streamType != connect.StreamTypeUnary
}

func (c connectStreamClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header, allowedCompression []string) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) encodeEnd(codec Codec, end *responseEnd, writer io.Writer) http.Header {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) decodeEnvelope(bytes [5]byte) (envelope, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) encodeEnvelope(e envelope) [5]byte {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) String() string {
	return protocolNameConnectStream
}

// connectStreamServerProtocol implements the Connect protocol for
// sending streaming RPCs to the server handler.
type connectStreamServerProtocol struct{}

var _ serverProtocolHandler = connectStreamServerProtocol{}
var _ serverEnvelopedProtocolHandler = connectStreamServerProtocol{}

func (c connectStreamServerProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectStreamServerProtocol) addProtocolRequestHeaders(meta requestMeta, header http.Header, allowedCompression []string) {
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

func (c connectStreamServerProtocol) decodeEnvelope(bytes [5]byte) (envelope, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) encodeEnvelope(e envelope) [5]byte {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) decodeEndFromMessage(codec Codec, reader io.Reader) (responseEnd, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) String() string {
	return protocolNameConnectStream
}
