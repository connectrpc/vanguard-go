// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

//nolint:unused // temporary
package vanguard

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

// Protocol represents an on-the-wire protocol for RPCs.
type Protocol int

const (
	// NB: The ordinal value of the protocol (other than the zero value) reflects
	//     the preference order. So Connect is the highest preferred protocol,
	//     then gRPC, etc.

	// ProtocolUnknown is not a valid value. Since it is the zero value, this
	// requires that all Protocol values must be explicitly initialized.
	ProtocolUnknown = Protocol(iota)
	// ProtocolConnect indicates the Connect protocol. This protocol supports
	// unary and streaming endpoints. However, bidirectional streams are only
	// supported when combined with HTTP/2.
	ProtocolConnect
	// ProtocolGRPC indicates the gRPC protocol. This protocol can only be
	// used in combination with HTTP/2. It supports unary and all kinds of
	// streaming endpoints.
	ProtocolGRPC
	// ProtocolGRPCWeb indicates the gRPC-Web protocol. This is a tweak of the
	// gRPC protocol to support HTTP 1.1. This protocol supports unary and
	// streaming endpoints. However, bidirectional streams are only supported
	// when combined with HTTP/2.
	ProtocolGRPCWeb
	// ProtocolREST indicates the REST+JSON protocol. This protocol often
	// requires non-trivial transformations between HTTP requests and responses
	// and Protobuf request and response messages.
	//
	// Only methods that have the google.api.http annotation can be invoked
	// with this protocol. The annotation defines the "shape" of the HTTP
	// request and response, such as the URI path, HTTP method, and how URI
	// path components, query string parameters, and an optional request
	// body are mapped to the Protobuf request message.
	//
	// This protocol only supports unary and server-stream endpoints.
	ProtocolREST

	// protocolMin is the minimum valid value for a Protocol.
	protocolMin = ProtocolConnect
	// protocolMax is the maximum valid value for a Protocol.
	protocolMax = ProtocolREST

	protocolNameConnect = "Connect"
	protocolNameGRPC    = "gRPC"
	protocolNameGRPCWeb = "gRPC-Web"
	protocolNameREST    = "REST"
)

func (p Protocol) String() string {
	switch p {
	case ProtocolConnect:
		return protocolNameConnect
	case ProtocolGRPC:
		return protocolNameGRPC
	case ProtocolGRPCWeb:
		return protocolNameGRPCWeb
	case ProtocolREST:
		return protocolNameREST
	default:
		return fmt.Sprintf("unknown protocol (%d)", p)
	}
}

func (p Protocol) serverHandler(op *operation) serverProtocolHandler {
	switch p {
	case ProtocolConnect:
		if op.streamType == connect.StreamTypeUnary {
			return connectUnaryServerProtocol{}
		}
		return connectStreamServerProtocol{}
	case ProtocolGRPC:
		return grpcServerProtocol{}
	case ProtocolGRPCWeb:
		return grpcWebServerProtocol{}
	case ProtocolREST:
		return restServerProtocol{}
	default:
		return nil
	}
}

// clientProtocolHandler handles the protocol used by the client.
// This allows the middleware to understand the incoming request
// and to send valid responses to the client.
type clientProtocolHandler interface {
	protocol() Protocol
	acceptsStreamType(connect.StreamType) bool
	allowsGetRequests() bool

	extractProtocolRequestHeaders(http.Header) (requestMeta, error)
	addProtocolResponseHeaders(meta responseMeta, target http.Header, allowedCompression []string)
	encodeEnd(responseEnd, io.Writer) (http.Header, error)

	String() string
}

// backendProtocolHandler handles the protocol used by the server.
// This allows the middleware to send a valid request to the server
// and understand the responses it sends.
type serverProtocolHandler interface {
	protocol() Protocol

	addProtocolRequestHeaders(meta requestMeta, target http.Header, allowedCompression []string)
	// returns the response metadata from the headers; if the second
	// arg is non-nil, the caller should supply the body to it along
	// with the responseMeta.end to finish processing the end of the
	// response.
	extractProtocolResponseHeaders(int, http.Header) (responseMeta, func(io.Reader, *responseEnd), error)

	// Called at end of RPC if responseEnd has not been returned by
	// extractProtocolResponseHeaders or from an enveloped message
	// in the response body whose trailer bit is set.
	extractEndFromTrailers(*operation, http.Header) (responseEnd, error)

	String() string
}

// envelopedProtocolHandler is an optional interface implemented
// by clientProtocolHandler and serverProtocolHandler instances
// whose protocol uses an envelope around messages.
type envelopedProtocolHandler interface {
	readEnvelope([5]byte) (envelope, error)
	writeEnvelope(envelope) [5]byte

	decodeEndFromMessage(*operation, io.Reader) (responseEnd, error)
}

// requestLineBuilder is an optional interface implemented by
// serverProtocolHandler instances whose HTTP request line
// needs to be computed in a custom manner. By default (for
// protocols that do not implement this), the request line is
// "POST /<service>/<method>".
type requestLineBuilder interface {
	requiresMessageToProvideRequestLine(*operation) bool
	requestLine(op *operation, req proto.Message) (urlPath, queryParams, method string, includeBody bool, err error)
}

// clientBodyPreparer is an optional interface implemented by
// clientProtocolHandler instances whose request messages may
// need to be assembled from sources other than just decoding
// the request or response body.
type clientBodyPreparer interface {
	requestNeedsPrep(*operation) bool
	prepareRequest(*operation, []byte) (proto.Message, error)
	responseNeedsPrep(*operation) bool
	prepareResponse(*operation, proto.Message) ([]byte, error)
}

// serverBodyPreparer is an optional interface implemented by
// serverProtocolHandler instances whose request messages may
// need to be assembled from sources other than just decoding
// the request or response body.
type serverBodyPreparer interface {
	// requestNeedsPrep returns true if the actual HTTP request body is not the request message.
	// So if the request must be composed using the request body and/or other sources, this
	// returns true.
	requestNeedsPrep(*operation) bool
	// prepareRequest receives the request body as well as the current decoders so it can
	// process it and assemble a request message.
	prepareRequest(*operation, proto.Message) ([]byte, error)
	responseNeedsPrep(*operation) bool
	prepareResponse(*operation, []byte) (proto.Message, error)
}

type envelope struct {
	trailer    bool
	compressed bool
	length     uint32
}

type requestMeta struct {
	timeout           time.Duration
	codec             string
	compression       string
	acceptCompression []string
}

type responseMeta struct {
	end               *responseEnd
	codec             string
	compression       string
	acceptCompression []string
}

type responseEnd struct {
	httpCode int
	err      *connect.Error
	trailers http.Header
}
