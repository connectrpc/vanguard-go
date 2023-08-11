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

	// Extracts relevant request metadata from the given headers to
	// determine the codec (aka sub-format), compression (aka encoding),
	// timeout, etc. The relevant headers are interpreted into the
	// returned requestMeta and also *removed* from the given headers.
	extractProtocolRequestHeaders(http.Header) (requestMeta, error)
	// Encodes the given responseMeta as headers into the given target
	// headers. If provided, allowedCompression should be used instead
	// of meta.allowedCompression when adding "accept-encoding" headers.
	addProtocolResponseHeaders(meta responseMeta, target http.Header, allowedCompression []string)
	// Encodes the given final disposition of the RPC to the given
	// writer. It can also return any trailers to add to the response.
	// Some protocols may ignore the writer, some will return no
	// trailers.
	encodeEnd(responseEnd, io.Writer) (http.Header, error)

	// String returns a human-readable name/description of protocol.
	String() string
}

// serverProtocolHandler handles the protocol used by the server.
// This allows the middleware to send a valid request to the server
// and understand the responses it sends.
type serverProtocolHandler interface {
	protocol() Protocol

	// Encodes the given requestMeta has headers into the given target
	// headers. If provided, allowedCompression should be used instead
	// of meta.allowedCompression when adding "accept-encoding" headers.
	addProtocolRequestHeaders(meta requestMeta, target http.Header, allowedCompression []string)
	// Returns the response metadata from the headers; if the second
	// arg is non-nil, the caller should supply the body to it along
	// with the responseMeta.end to finish processing the end of the
	// response.
	extractProtocolResponseHeaders(int, http.Header) (responseMeta, func(io.Reader, *responseEnd), error)
	// Called at end of RPC if responseEnd has not been returned by
	// extractProtocolResponseHeaders or from an enveloped message
	// in the response body whose trailer bit is set.
	extractEndFromTrailers(*operation, http.Header) (responseEnd, error)

	// String returns a human-readable name/description of protocol.
	String() string
}

// envelopedProtocolHandler is an optional interface implemented
// by clientProtocolHandler and serverProtocolHandler instances
// whose protocol uses an envelope around messages.
type envelopedProtocolHandler interface {
	decodeEnvelope([5]byte) (envelope, error)
	encodeEnvelope(envelope) [5]byte

	// If a stream includes an envelope with the trailer bit
	// set, this is called to parse the message contents. The
	// given reader will be decompressed (even if the envelope
	// had its compressed bit set).
	decodeEndFromMessage(*operation, io.Reader) (responseEnd, error)
}

// requestLineBuilder is an optional interface implemented by
// serverProtocolHandler instances whose HTTP request line
// needs to be computed in a custom manner. By default (for
// protocols that do not implement this), the request line is
// "POST /<service>/<method>".
//
// This is necessary for REST and Connect GET requests, which
// can encode parts of the request data into the URI path
// or query string parameters.
type requestLineBuilder interface {
	// Returns true if the request message must be known in order
	// to compute the request line.
	requiresMessageToProvideRequestLine(*operation) bool
	// Computes the components of the request line and also
	// indicates if the request will include a body or not. The
	// body can be omitted for requests where *all* request
	// information is supplied in the request line.
	requestLine(op *operation, req proto.Message) (urlPath, queryParams, method string, includeBody bool, err error)
}

// clientBodyPreparer is an optional interface implemented by
// clientProtocolHandler instances whose request messages may
// need to be assembled from sources other than just decoding
// the request or response body.
type clientBodyPreparer interface {
	// Returns true if the request message needs to be prepared.
	// If it can simply be read and decoded from the request body
	// then it does not need to be prepared. But if the message
	// data must be merged with parts of the request path or
	// query param (etc), it must return true.
	requestNeedsPrep(*operation) bool
	// Combines the given request body data with other info to
	// produce a request message. The given bytes represent the
	// uncompressed request body. The given message should be
	// populated if/when the method returns nil.
	prepareUnmarshalledRequest(op *operation, src []byte, target proto.Message) error
	// Returns true if the response message needs to be prepared.
	// If it can simply be encoded into the response body then it
	// does not need to be prepared. But if the message data must
	// be wrapped or some parts discarded, the method must return
	// true.
	responseNeedsPrep(*operation) bool
	// Produces the request body for the given message. The data
	// should be appended to the given slice (which will be empty
	// but have capacity to accept data) to reduce allocations.
	prepareMarshalledResponse(op *operation, base []byte, src proto.Message) ([]byte, error)
}

// serverBodyPreparer is an optional interface implemented by
// serverProtocolHandler instances whose request messages may
// need to be assembled from sources other than just decoding
// the request or response body.
type serverBodyPreparer interface {
	// These methods are reversed from clientBodyPreparer: for the
	// server side, we have a request message and must produce a
	// body; and we have a response body and must extract from that
	// a message.
	requestNeedsPrep(*operation) bool
	prepareMarshalledRequest(op *operation, base []byte, src proto.Message) ([]byte, error)
	responseNeedsPrep(*operation) bool
	prepareUnmarshalledResponse(op *operation, src []byte, target proto.Message) error
}

// envelope is an exploded representation of the 5-byte preamble that appears
// on the wire for enveloped protocols. This form is protocol-agnostic.
type envelope struct {
	trailer    bool
	compressed bool
	length     uint32
}

// requestMeta represents the metadata found in request headers that are
// protocol-specific.
type requestMeta struct {
	timeout           time.Duration
	codec             string
	compression       string
	acceptCompression []string
}

// responseMeta represents the metadata found in response headers that are
// protocol-specific.
type responseMeta struct {
	end               *responseEnd
	codec             string
	compression       string
	acceptCompression []string
}

// responseEnd is a protocol-agnostic representation of the disposition
// of an RPC.
type responseEnd struct {
	err      *connect.Error
	trailers http.Header

	// httpCode is only populated when the responseEnd source contained
	// such a code. This happens when the responseEnd comes from the
	// response headers, which include the status line. It can also
	// occur for REST streaming responses, where the final message may
	// include both gRPC and HTTP codes.
	httpCode int
}
