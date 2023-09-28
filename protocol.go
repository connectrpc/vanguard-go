// Copyright 2023 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vanguard

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

const envelopeLen = 5

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

// clientProtocolHandler handles the protocol used by the client.
// This allows the middleware to understand the incoming request
// and to send valid responses to the client.
type clientProtocolHandler interface {
	// Protocol returns the protocol used by the client.
	Protocol() Protocol
	// DecodeRequestHeader extracts request headers into the given requestMeta.
	DecodeRequestHeader(*requestMeta) error
	// PrepareRequestMessage prepares the given messageBuffer to receive the
	// message data from the reader.
	PrepareRequestMessage(*messageBuffer, *requestMeta) error
	// EncodeRequestHeader  given responseMeta as headers into the given target.
	EncodeRequestHeader(*responseMeta) error
	// PrepareResponseMessage prepares the given messageBuffer to receive the
	// message data from the writer.
	PrepareResponseMessage(*messageBuffer, *responseMeta) error
	// EncodeResponseTrailer encodes the given trailers into the given buffer and headers.
	EncodeResponseTrailer(*bytes.Buffer, *responseMeta) error
	// Encode the error into the given buffer.
	EncodeError(*bytes.Buffer, *responseMeta, error)
}

// serverProtocolHandler handles the protocol used by the server.
// This allows the middleware to send a valid request to the server
// and understand the responses it sends.
type serverProtocolHandler interface {
	// Protocol returns the protocol used by the server.
	Protocol() Protocol
	// EncodeRequestHeader encodes the given requestMeta as headers into the given target.
	EncodeRequestHeader(*requestMeta) error
	// PrepareRequestMessage prepares the given messageBuffer to receive the
	// message data from the reader.
	PrepareRequestMessage(*messageBuffer, *requestMeta) error
	// DecodeResponseHeader extracts response headers into the given responseMeta.
	DecodeRequestHeader(*responseMeta) error
	// PrepareResponseMessage prepares the given messageMeta to receive the
	// message data from the writer.
	PrepareResponseMessage(*messageBuffer, *responseMeta) error
	// DecodeResponseTrailer decodes the given trailers into the given buffer and headers.
	DecodeResponseTrailer(*bytes.Buffer, *responseMeta) error
}

// envelopeBytes is an array of bytes representing an encoded envelope.
type envelopeBytes [envelopeLen]byte

// encoding represents the encoding used for a request or response.
type encoding struct {
	Codec      Codec
	Compressor compressor
}

// requestMeta represents the metadata found in request headers that are
// protocol-specific.
type requestMeta struct {
	Body         io.ReadCloser
	Header       httpHeader
	URL          *url.URL
	Method       string
	ProtoMajor   int
	ProtoMinor   int
	RequiresBody bool // true if the header depends on the request body

	// Following fields are derived from the request headers.
	Timeout           time.Duration
	CodecName         string
	CompressionName   string
	AcceptCompression []string

	// State for the request encoding.
	Client encoding
	Server encoding
}

// responseMeta represents the metadata found in response headers that are
// protocol-specific.
type responseMeta struct {
	Header      httpHeader
	StatusCode  int
	HasTrailer  bool
	WroteStatus bool // true if the header has been written

	// Following fields are derived from the response headers.
	CodecName         string
	CompressionName   string
	AcceptCompression []string

	// State for the response encoding.
	Client encoding
	Server encoding
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
					result = append(result, strings.TrimSpace(val))
				}
				break
			}
			item := val[:pos]
			if item != "" {
				result = append(result, strings.TrimSpace(item))
			}
			val = val[pos+1:]
		}
	}
	return result
}

// type Stream interface {
// 	RecvHeader() http.Header
// 	RecvMessage(proto.Message) error
// 	SendHeader(http.Header) error
// 	SendMessage(proto.Message) error
// 	SendTrailer(http.Header) error
// }
//
// type StreamHandler func(Stream) error
// type StreamInterceptor func(info protoreflect.MethodDescriptor, stream Stream, handler StreamHandler) error
