// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	grpcTimeoutMaxHours = math.MaxInt64 / int64(time.Hour)
	grpcMaxTimeoutChars = 8
)

var (
	//nolint:gochecknoglobals
	grpcTimeoutUnits = []struct {
		size time.Duration
		char byte
	}{
		{time.Nanosecond, 'n'},
		{time.Microsecond, 'u'},
		{time.Millisecond, 'm'},
		{time.Second, 'S'},
		{time.Minute, 'M'},
		{time.Hour, 'H'},
	}
	//nolint:gochecknoglobals
	grpcTimeoutUnitLookup = func() map[byte]time.Duration {
		m := make(map[byte]time.Duration)
		for _, pair := range grpcTimeoutUnits {
			m[pair.char] = pair.size
		}
		return m
	}()
)

func grpcWriteError(rsp http.ResponseWriter, err error) {
	statusCode := http.StatusOK
	status := grpcStatusFromError(err)

	hdr := rsp.Header()
	hdr.Del("Content-Encoding")
	hdr.Set("Grpc-Encoding", "identity")
	hdr.Set("Trailers", "Grpc-Status, Grpc-Message")
	rsp.WriteHeader(statusCode)

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
func grpcErrorFromTrailer(tlr http.Header) *connect.Error {
	codeHeader := tlr.Get("Grpc-Status")
	if codeHeader == "" {
		return connect.NewError(
			connect.CodeInternal,
			errProtocol("missing trailer header %q"),
		)
	}
	if codeHeader == "0" {
		return nil
	}

	code, err := strconv.ParseUint(codeHeader, 10 /* base */, 32 /* bitsize */)
	if err != nil {
		return connect.NewError(
			connect.CodeInternal,
			errProtocol("invalid error code %q: %w", codeHeader, err),
		)
	}
	if code == 0 {
		return nil
	}

	detailsBinaryEncoded := tlr.Get("Grpc-Status-Details-Bin")
	if len(detailsBinaryEncoded) == 0 {
		val := tlr.Get("Grpc-Message")
		message := grpcPercentDecode(val)
		return connect.NewWireError(connect.Code(code), errors.New(message))
	}

	// Prefer the Protobuf-encoded data to the headers (grpc-go does this too).
	detailsBinary, err := connect.DecodeBinaryHeader(detailsBinaryEncoded)
	if err != nil {
		return connect.NewError(
			connect.CodeInternal,
			errProtocol("invalid grpc-status-details-bin trailer: %w", err),
		)
	}
	var status status.Status
	if err := proto.Unmarshal(detailsBinary, &status); err != nil {
		return connect.NewError(
			connect.CodeInternal,
			errProtocol("invalid protobuf for error details: %w", err),
		)
	}
	trailerErr := connect.NewWireError(connect.Code(status.Code), errors.New(status.Message))
	for _, msg := range status.Details {
		errDetail, _ := connect.NewErrorDetail(msg)
		trailerErr.AddDetail(errDetail)
	}
	return trailerErr
}

func grpcDecodeTimeout(timeout string) (time.Duration, error) {
	if timeout == "" {
		return 0, errNoTimeout
	}
	unit, ok := grpcTimeoutUnitLookup[timeout[len(timeout)-1]]
	if !ok {
		return 0, errProtocol("timeout %q has invalid unit", timeout)
	}
	num, err := strconv.ParseInt(timeout[:len(timeout)-1], 10 /* base */, 64 /* bitsize */)
	if err != nil || num < 0 {
		return 0, errProtocol("invalid timeout %q", timeout)
	}
	if num > 99999999 { // timeout must be ASCII string of at most 8 digits
		return 0, errProtocol("timeout %q is too long", timeout)
	}
	if unit == time.Hour && num > grpcTimeoutMaxHours {
		// Timeout is effectively unbounded, so ignore it. The grpc-go
		// implementation does the same thing.
		return 0, errNoTimeout
	}
	return time.Duration(num) * unit, nil
}

func grpcEncodeTimeout(timeout time.Duration) (string, error) {
	if timeout <= 0 {
		return "0n", nil
	}
	for _, pair := range grpcTimeoutUnits {
		digits := strconv.FormatInt(int64(timeout/pair.size), 10 /* base */)
		if len(digits) < grpcMaxTimeoutChars {
			return digits + string(pair.char), nil
		}
	}
	// The max time.Duration is smaller than the maximum expressible gRPC
	// timeout, so we can't reach this case.
	return "", errNoTimeout
}
