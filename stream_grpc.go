// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bufbuild/connect-go"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

type upstreamGRPC struct {
	*Mux

	codec   codec
	decComp compressor
	encComp compressor
	isWeb   bool
}

var _ upstream = (*upstreamGRPC)(nil)

func (s *upstreamGRPC) DecodeHeader(hdr requestHeader) error { return nil }
func (s *upstreamGRPC) DecodeMessage([]byte, proto.Message) error { // bytes -> msg
	return nil
}
func (s *upstreamGRPC) EncodeHeader(hdr responseHeader) error {
	return nil
}
func (s *upstreamGRPC) EncodeMessage([]byte, proto.Message) ([]byte, error) { // msg -> bytes
	return nil, nil
}
func (s *upstreamGRPC) EncodeTrailer(hdr header) error {
	return nil
}
func (s *upstreamGRPC) EncodeError(rsp io.Writer, hdr responseHeader, err error) {}

type downstreamGRPC struct {
	*Mux

	method *method

	codec   codec
	encComp compressor // recv
	decComp compressor // send
	isWeb   bool
}

var _ downstream = (*downstreamGRPC)(nil)

func (s *downstreamGRPC) EncodeHeader(hdr requestHeader) error {
	name := s.method.name
	u := hdr.URL()
	u.Path = name
	u.RawPath = name
	u.RawQuery = ""

	codecName := "proto"
	if contentType, ok := hdr.Get("Content-Type"); ok {
		if _, name, ok := strings.Cut(contentType, "+"); ok {
			codecName = name
		}
	}
	codec, err := s.getCodec(codecName)
	if err != nil {
		return err
	}
	s.codec = codec

	if encoding, ok := hdr.Get("Grpc-Encoding"); ok && encoding != "identity" {
		comp, err := s.getCompressor(encoding)
		if err != nil {
			return err
		}
		s.encComp = comp
	}
	return nil
}
func (d *downstreamGRPC) EncodeMessage(b []byte, msg proto.Message) ([]byte, error) { // msg -> bytes
	b = append(b, 0, 0, 0, 0, 0)
	b, err := d.codec.MarshalAppend(b, msg)
	if err != nil {
		return nil, err
	}
	// TODO: minCompressSize
	if comp := d.encComp; comp != nil && len(b) > 0 {
		c, err := d.compress(b[5:], comp)
		if err != nil {
			return nil, err
		}
		b[0] |= 0x01
		b = append(b[:5], c...)
	}
	binary.BigEndian.PutUint32(b[1:5], uint32(len(b)-5))
	return b, nil
}
func (s *downstreamGRPC) DecodeHeader(hdr responseHeader) error {
	if encoding, ok := hdr.Get("Grpc-Encoding"); ok && encoding != "identity" {
		comp, err := s.getCompressor(encoding)
		if err != nil {
			return err
		}
		s.encComp = comp
	}

	return nil
}
func (s *downstreamGRPC) DecodeMessage(b []byte, msg proto.Message) (err error) {
	// TODO: flags..
	isCompressed := b[0]&0x01 == 1
	if isCompressed {
		comp := s.decComp
		if comp == nil {
			return errDecompressorNotFound("Grpc-Encoding")
		}
		c, err := s.decompress(b[5:], comp)
		if err != nil {
			return err
		}
		b = c
	} else {
		b = b[5:] // TODO: check len
	}
	return s.codec.Unmarshal(b, msg)
}
func (d *downstreamGRPC) DecodeTrailer(hdr header) error {
	return nil
}

func grpcErrorWriter(_ io.Writer, hdr responseHeader, err error) {
	statusCode := http.StatusOK
	status := grpcStatusFromError(err)

	hdr.Del("Content-Encoding")
	hdr.Set("Grpc-Encoding", "identity")
	hdr.Set("Trailers", "Grpc-Status, Grpc-Message")
	hdr.WriteStatus(statusCode)

	bin, err := proto.Marshal(status)
	if err != nil {
		hdr.Set("Grpc-Status", strconv.Itoa(int(connect.CodeInternal)))
		hdr.Set("Grpc-Message", grpcPercentEncode("failed to marshal error: "+err.Error()))
		return
	}
	hdr.Set("Grpc-Status", strconv.Itoa(int(status.Code)))
	hdr.Set("Grpc-Message", grpcPercentEncode(status.Message))
	hdr.Set("Grpc-Status-Details-Bin", connect.EncodeBinaryHeader(bin))
}

func grpcStatusFromError(err error) *status.Status {
	cerr := asError(err)
	status := &status.Status{
		Code:    int32(cerr.Code()),
		Message: cerr.Message(),
	}
	if details := cerr.Details(); len(details) > 0 {
		// TODO: better way to do this?
		status.Details = make([]*anypb.Any, len(details))
		for i, detail := range details {
			status.Details[i] = &anypb.Any{
				TypeUrl: "type.googleapis.com/" + detail.Type(),
				Value:   detail.Bytes(),
			}
		}
	}

	return status
}

// grpcPercentEncode follows RFC 3986 Section 2.1 and the gRPC HTTP/2 spec.
// It's a variant of URL-encoding with fewer reserved characters. It's intended
// to take UTF-8 encoded text and escape non-ASCII bytes so that they're valid
// HTTP/1 headers, while still maximizing readability of the data on the wire.
//
// The grpc-message trailer (used for human-readable error messages) should be
// percent-encoded.
//
// References:
//
//	https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md#responses
//	https://datatracker.ietf.org/doc/html/rfc3986#section-2.1
func grpcPercentEncode(msg string) string {
	for i := 0; i < len(msg); i++ {
		// Characters that need to be escaped are defined in gRPC's HTTP/2 spec.
		// They're different from the generic set defined in RFC 3986.
		if c := msg[i]; c < ' ' || c > '~' || c == '%' {
			return grpcPercentEncodeSlow(msg, i)
		}
	}
	return msg
}

// msg needs some percent-escaping. Bytes before offset don't require
// percent-encoding, so they can be copied to the output as-is.
func grpcPercentEncodeSlow(msg string, offset int) string {
	var out strings.Builder
	out.Grow(2 * len(msg))
	out.WriteString(msg[:offset])
	for i := offset; i < len(msg); i++ {
		c := msg[i]
		if c < ' ' || c > '~' || c == '%' {
			fmt.Fprintf(&out, "%%%02X", c)
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

func grpcPercentDecode(encoded string) string {
	for i := 0; i < len(encoded); i++ {
		if c := encoded[i]; c == '%' && i+2 < len(encoded) {
			return grpcPercentDecodeSlow(encoded, i)
		}
	}
	return encoded
}

// Similar to percentEncodeSlow: encoded is percent-encoded, and needs to be
// decoded byte-by-byte starting at offset.
func grpcPercentDecodeSlow(encoded string, offset int) string {
	var out strings.Builder
	out.Grow(len(encoded))
	out.WriteString(encoded[:offset])
	for i := offset; i < len(encoded); i++ {
		c := encoded[i]
		if c != '%' || i+2 >= len(encoded) {
			out.WriteByte(c)
			continue
		}
		parsed, err := strconv.ParseUint(encoded[i+1:i+3], 16 /* hex */, 8 /* bitsize */)
		if err != nil {
			out.WriteRune(utf8.RuneError)
		} else {
			out.WriteByte(byte(parsed))
		}
		i += 2
	}
	return out.String()
}

// The gRPC wire protocol specifies that errors should be serialized using the
// binary Protobuf format, even if the messages in the request/response stream
// use a different codec. Consequently, this function needs a Protobuf codec to
// unmarshal error information in the headers.
func grpcErrorFromTrailer(tlr header) *connect.Error {
	codeHeader, ok := tlr.Get("Grpc-Status")
	if !ok || codeHeader == "" {
		return errorf(connect.CodeInternal,
			"gRPC protocol error: missing trailer header %q",
			"Grpc-Status")
	}
	if codeHeader == "0" {
		return nil
	}

	code, err := strconv.ParseUint(codeHeader, 10 /* base */, 32 /* bitsize */)
	if err != nil {
		return errorf(connect.CodeInternal,
			"gRPC protocol error: invalid error code %q: %w",
			codeHeader, err)
	}
	if code == 0 {
		return nil
	}

	detailsBinaryEncoded, _ := tlr.Get("Grpc-Status-Details-Bin")
	if len(detailsBinaryEncoded) == 0 {
		val, _ := tlr.Get("Grpc-Message")
		message := grpcPercentDecode(val)
		return connect.NewWireError(connect.Code(code), errors.New(message))
	}

	// Prefer the Protobuf-encoded data to the headers (grpc-go does this too).
	detailsBinary, err := connect.DecodeBinaryHeader(detailsBinaryEncoded)
	if err != nil {
		return errorf(connect.CodeInternal,
			"server returned invalid grpc-status-details-bin trailer: %w",
			err)
	}
	var status status.Status
	if err := proto.Unmarshal(detailsBinary, &status); err != nil {
		return errorf(connect.CodeInternal,
			"server returned invalid protobuf for error details: %w",
			err)
	}
	trailerErr := connect.NewWireError(connect.Code(status.Code), errors.New(status.Message))
	for _, msg := range status.Details {
		errDetail, _ := connect.NewErrorDetail(msg)
		trailerErr.AddDetail(errDetail)
	}
	return trailerErr
}

/*func getGRPCTrailer(hdr header) (http.Header, *connect.Error) {
	isTrailer := map[string]bool{
		"Grpc-Status":             true,
		"Grpc-Status-Details-Bin": true,
		"Grpc-Message":            true,
	}
	for _, key := range strings.Split(hdr.Get(headerTrailer), ",") {
		key = http.CanonicalHeaderKey(key)
		isTrailer[key] = true
	}

	trailer := make(http.Header)
	for key, vals := range header {
		if strings.HasPrefix(key, http.TrailerPrefix) {
			// Must remove trailer prefix before canonicalizing.
			key = http.CanonicalHeaderKey(key[len(http.TrailerPrefix):])
			trailer[key] = vals
		} else if key := http.CanonicalHeaderKey(key); isTrailer[key] {
			trailer[key] = vals
		}
	}

	trailerErr := grpcErrorFromTrailer(trailer)
	if trailerErr != nil {
		return trailer, trailerErr
	}

	// Remove all protocol trailer keys.
	for key := range trailer {
		if isProtocolHeader(key) {
			delete(trailer, key)
		}
	}
	return trailer, nil
}*/
