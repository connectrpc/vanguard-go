// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

//nolint:unused // temporary
package vanguard

import (
	"fmt"
	"io"
	"net/http"
	"strings"
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
	acceptsStreamType(*operation, connect.StreamType) bool

	// Extracts relevant request metadata from the given headers to
	// determine the codec (aka sub-format), compression (aka encoding),
	// timeout, etc. The relevant headers are interpreted into the
	// returned requestMeta and also *removed* from the given headers.
	extractProtocolRequestHeaders(*operation, http.Header) (requestMeta, error)
	// Encodes the given responseMeta as headers into the given target
	// headers. If provided, allowedCompression should be used instead
	// of meta.allowedCompression when adding "accept-encoding" headers.
	//
	// The return value is the status code that should be sent to the
	// client. If the status code written was anything other than
	// 200 OK, the given meta will include a responseEnd that has that
	// original code.
	//
	// Note that this method's responsibility is to decide the status
	// code and set headers. When meta.end is non-nil, encodeEnd will
	// also be called, which is where a response body and trailers
	// can be written.
	addProtocolResponseHeaders(meta responseMeta, target http.Header, allowedCompression []string) int
	// Encodes the given final disposition of the RPC to the given
	// writer. It can also return any trailers to add to the response.
	// Some protocols may ignore the writer; some will return no
	// trailers.
	//
	// The given codec represents the sub-format that the client used
	// (which could be used, for example, to encode the error).
	//
	// The wasInHeaders flag indicates that end was signalled in the
	// response headers. For some protocols, like gRPC and gRPC-Web,
	// this is the difference between a trailers-only response and a
	// normal response (where the end is signalled in the response
	// body or trailers, not headers). When this is true, the end was
	// also already provided to addProtocolResponseHeaders.
	encodeEnd(codec Codec, end *responseEnd, writer io.Writer, wasInHeaders bool) http.Header

	// String returns a human-readable name/description of protocol.
	String() string
}

// clientProtocolSupportsGet is an optional interface implemented by
// clientProtocolHandler instances that can support the GET HTTP method.
type clientProtocolAllowsGet interface {
	allowsGetRequests(*methodConfig) bool
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
	// Returns the response metadata from the headers. If the response
	// meta's end field is set (i.e. headers indicate RPC is over), but
	// the protocol needs to read the response body to populate it, it
	// should return a non-nil function as the second returned value.
	// This function will receive the server's codec (optionally used
	// to encode other messages and could be used to decode the error
	// body), the body, and a pointer to the responseEnd which should
	// be populated with the details. If the response body was compressed,
	// it will be decompressed before it is provided to the given function.
	extractProtocolResponseHeaders(int, http.Header) (responseMeta, responseEndUnmarshaler, error)
	// Called at end of RPC if responseEnd has not been returned by
	// extractProtocolResponseHeaders or from an enveloped message
	// in the response body whose trailer bit is set.
	extractEndFromTrailers(*operation, http.Header) (responseEnd, error)

	// String returns a human-readable name/description of protocol.
	String() string
}

// responseEndUnmarshaler populates the given responseEnd by unmarshalling
// information from the given reader. If unmarshalling needs to know the
// server's codec, it also provided as the first argument.
type responseEndUnmarshaler func(Codec, io.Reader, *responseEnd)

// serverProtocolEndMustBeInHeaders is an optional interface implemented
// by serverProtocolHandler instances to indicate if the end of an RPC
// must be indicated in response headers (not trailers or in the body).
// If a protocol handler does not implement this, it is assumed to be
// false.
type serverProtocolEndMustBeInHeaders interface {
	endMustBeInHeaders() bool
}

// envelopedProtocolHandler is an optional interface implemented
// by clientProtocolHandler and serverProtocolHandler instances
// whose protocol uses an envelope around messages.
type envelopedProtocolHandler interface {
	decodeEnvelope([5]byte) (envelope, error)
	encodeEnvelope(envelope) [5]byte
}

// serverEnvelopedProtocolHandler is an optional interface implemented
// by serverProtocolHandler instances whose protocol uses an envelope
// around messages.
type serverEnvelopedProtocolHandler interface {
	envelopedProtocolHandler
	// If a stream includes an envelope with the trailer bit
	// set, this is called to parse the message contents. The
	// given reader will be decompressed (even if the envelope
	// had its compressed bit set).
	//
	// The given codec represents the sub-format used to send
	// the request to the server (which may be used to decode
	// the error).
	decodeEndFromMessage(Codec, io.Reader) (responseEnd, error)
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
	// The given headers may be updated, like if a message has
	// content that must go into headers (such as recording a
	// custom content-type for uses of google.api.HttpBody).
	prepareMarshalledResponse(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error)
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
	prepareMarshalledRequest(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error)
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
	hasTimeout        bool
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

	// For enveloping protocols where the end is in a special stream
	// payload, this will be true if that special payload was compressed.
	// This can be used by a protocol handler that also encodes the end
	// in a stream payload to decide whether to compress the final frame.
	wasCompressed bool
}

// parseMultiHeader parses headers that allow multiple values. It
// supports the values being supplied in a single header separated
// by commas, multiple headers, or a combination thereof.
func parseMultiHeader(vals []string) []string {
	if len(vals) == 0 {
		return nil
	}
	var count int
	for _, val := range vals {
		count += strings.Count(val, ",") + 1
	}
	result := make([]string, 0, count)
	for _, val := range vals {
		for {
			pos := strings.IndexByte(val, ',')
			if pos == -1 {
				if val != "" {
					result = append(result, val)
				}
				break
			}
			item := val[:pos]
			if item != "" {
				result = append(result, item)
			}
			val = val[pos+1:]
		}
	}
	return result
}
