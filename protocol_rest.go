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

type restClientProtocol struct{}

func (r restClientProtocol) protocol() Protocol {
	return ProtocolREST
}

func (r restClientProtocol) acceptsStreamType(streamType connect.StreamType) bool {
	return streamType == connect.StreamTypeUnary || streamType == connect.StreamTypeServer
}

func (r restClientProtocol) allowsGetRequests() bool {
	return true
}

func (r restClientProtocol) extractProtocolRequestHeaders(header http.Header) (requestMeta, error) {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) addProtocolResponseHeaders(meta responseMeta, header http.Header) {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) encodeEnd(end responseEnd, writer io.Writer) (http.Header, error) {
	//TODO implement me
	panic("implement me")
}

func (r restClientProtocol) String() string {
	return protocolNameREST
}

type restServerProtocol struct{}

func (r restServerProtocol) protocol() Protocol {
	return ProtocolREST
}

func (r restServerProtocol) addProtocolRequestHeaders(meta requestMeta, header http.Header) {
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

func (r restServerProtocol) String() string {
	return protocolNameREST
}
