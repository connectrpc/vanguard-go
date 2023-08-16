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

type restClientProtocol struct{}

var _ clientProtocolHandler = restClientProtocol{}
var _ clientProtocolAllowsGet = restClientProtocol{}
var _ clientBodyPreparer = restClientProtocol{}

// restClientProtocol implements the REST protocol for
// processing RPCs received from the client.
func (r restClientProtocol) protocol() Protocol {
	return ProtocolREST
}

func (r restClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	// TODO: support connect.StreamTypeServer, too
	return streamType == connect.StreamTypeUnary
}

func (r restClientProtocol) allowsGetRequests() {}

func (r restClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header, allowedCompression []string) {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) encodeEnd(codec Codec, end *responseEnd, writer io.Writer) http.Header {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) requestNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) prepareUnmarshalledRequest(op *operation, src []byte, target proto.Message) error {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) responseNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) prepareMarshalledResponse(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error) {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) String() string {
	return protocolNameREST
}

// restServerProtocol implements the REST protocol for
// sending RPCs to the server handler.
type restServerProtocol struct{}

var _ serverProtocolHandler = restServerProtocol{}
var _ requestLineBuilder = restServerProtocol{}
var _ serverBodyPreparer = restServerProtocol{}
var _ serverProtocolEndMustBeInHeaders = restServerProtocol{}

func (r restServerProtocol) protocol() Protocol {
	return ProtocolREST
}

func (r restServerProtocol) endMustBeInHeaders() bool {
	// TODO: when we support server streams over REST, this
	//       should return false when streaming
	return true
}

func (r restServerProtocol) addProtocolRequestHeaders(meta requestMeta, header http.Header, allowedCompression []string) {
	//TODO implement me
	panic("implement me")
}

func (r restServerProtocol) extractProtocolResponseHeaders(i int, header http.Header) (responseMeta, func(io.Reader, *responseEnd), error) {
	//TODO implement me
	panic("implement me")
}

func (r restServerProtocol) extractEndFromTrailers(o *operation, header http.Header) (responseEnd, error) {
	//TODO implement me
	panic("implement me")
}

func (r restServerProtocol) requestNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (r restServerProtocol) prepareMarshalledRequest(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error) {
	//TODO implement me
	panic("implement me")
}

func (r restServerProtocol) responseNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (r restServerProtocol) prepareUnmarshalledResponse(op *operation, src []byte, target proto.Message) error {
	//TODO implement me
	panic("implement me")
}

func (r restServerProtocol) requiresMessageToProvideRequestLine(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (r restServerProtocol) requestLine(op *operation, req proto.Message) (urlPath, queryParams, method string, includeBody bool, err error) {
	//TODO implement me
	panic("implement me")
}

func (r restServerProtocol) String() string {
	return protocolNameREST
}
