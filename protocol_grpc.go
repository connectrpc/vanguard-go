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
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	// flagEnvelopeCompressed indicates that the data is compressed. It has the
	// same meaning in the gRPC-Web, gRPC-HTTP2, and Connect protocols.
	flagEnvelopeCompressed = 0b00000001

	grpcWebFlagEnvelopeTrailer = 0b10000000
)

// grpcClientProtocol implements the gRPC protocol for
// processing RPCs received from the client.
type grpcClientProtocol struct {
	config *methodConfig
}

var _ clientProtocolHandler = grpcClientProtocol{}

func (g grpcClientProtocol) Protocol() Protocol {
	return ProtocolGRPC
}

func (g grpcClientProtocol) DecodeRequestHeader(meta *requestMeta) error {
	if meta.Method != http.MethodPost {
		return protocolError("invalid method %q", meta.Method)
	}
	if meta.ProtoMajor != 2 && meta.ProtoMinor != 0 {
		return protocolError("invalid HTTP version %q.%q", meta.ProtoMajor, meta.ProtoMinor)
	}
	meta.Header.Del("Te") // no need to propagate "te: trailers" to requests in different protocols

	if err := grpcExtractRequestMeta("application/grpc", "application/grpc+", meta); err != nil {
		return err
	}
	// Resolve Codecs
	codec, err := g.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Client.Codec = codec
	if meta.CompressionName != "" {
		comp, err := g.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = comp
	}
	return nil
}

func (g grpcClientProtocol) PrepareRequestMessage(msg *messageBuffer, _ *requestMeta) error {
	if msg.Buf.Len() < 5 {
		return io.ErrShortBuffer // ask for more data
	}
	flags, size, err := readEnvelope(msg.Buf)
	if err != nil {
		return err
	}
	msg.Src.Size = size
	if flags&1 != 0 {
		msg.Src.IsCompressed = true
	}
	return nil
}

func (g grpcClientProtocol) EncodeRequestHeader(meta *responseMeta) error {
	grpcEncodeResponseHeader("application/grpc+", meta)
	meta.Header.Set("Trailer", "Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin")

	codec, err := g.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Client.Codec = codec
	if meta.CompressionName != "" {
		comp, err := g.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = comp
	}
	return nil
}

func (g grpcClientProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	msg.Dst.IsEnvelope = true
	msg.Dst.IsCompressed = meta.Client.Compressor != nil
	if msg.Dst.IsCompressed {
		msg.Dst.IsCompressed = true
		msg.Dst.Flags |= flagEnvelopeCompressed
	}
	return nil
}

func (g grpcClientProtocol) EncodeResponseTrailer(_ *bytes.Buffer, meta *responseMeta) error {
	meta.Header.Set("Grpc-Status", "0")
	meta.Header.Set("Grpc-Message", "")
	return nil
}

func (g grpcClientProtocol) EncodeError(_ *bytes.Buffer, meta *responseMeta, err error) {
	cerr := asConnectError(err)
	grpcEncodeError(cerr, meta.Header)
	meta.StatusCode = http.StatusOK // gRPC errors are always HTTP 200
}

// grpcServerProtocol implements the gRPC protocol for
// sending RPCs to the server handler.
type grpcServerProtocol struct {
	config *methodConfig
}

var _ serverProtocolHandler = grpcServerProtocol{}

func (g grpcServerProtocol) Protocol() Protocol {
	return ProtocolGRPC
}

func (g grpcServerProtocol) EncodeRequestHeader(meta *requestMeta) error {
	codec, err := g.config.GetServerCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Server.Codec = codec
	if meta.CompressionName != "" {
		compressor, err := g.config.GetServerCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Server.Compressor = compressor
	}
	grpcEncodeRequestHeader("application/grpc+", meta)
	meta.URL.Path = g.config.methodPath
	meta.Header.Set("Te", "trailers")
	meta.Method = http.MethodPost
	meta.ProtoMajor = 2
	meta.ProtoMinor = 0
	return nil
}

func (g grpcServerProtocol) PrepareRequestMessage(msg *messageBuffer, meta *requestMeta) error {
	msg.Dst.IsEnvelope = true
	if meta.Server.Compressor != nil {
		msg.Dst.IsCompressed = true
		msg.Dst.Flags |= 1
	}
	return nil
}

func (g grpcServerProtocol) DecodeRequestHeader(meta *responseMeta) error {
	if err := grpcExtractResponseMeta("application/grpc", "application/grpc+", meta); err != nil {
		return err
	}
	codec, err := g.config.GetServerCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Server.Codec = codec
	if meta.CompressionName != "" {
		compressor, err := g.config.GetServerCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Server.Compressor = compressor
	}
	return nil
}

func (g grpcServerProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	if msg.Buf.Len() < 5 {
		return io.ErrShortBuffer // ask for more data
	}
	flags, size, err := readEnvelope(msg.Buf)
	if err != nil {
		return err
	}
	msg.Src.Size = size
	isCompressed := flags&1 != 0
	if isCompressed {
		if meta.Server.Compressor == nil {
			return protocolError("server sent compressed message but client did not request compression")
		}
		msg.Src.IsCompressed = true
	}
	return nil
}

func (g grpcServerProtocol) DecodeResponseTrailer(_ *bytes.Buffer, meta *responseMeta) error {
	if err := grpcExtractErrorFromTrailer(meta.Header); err != nil {
		return err // Connect error
	}
	return nil
}

// grpcClientProtocol implements the gRPC protocol for
// processing RPCs received from the client.
type grpcWebClientProtocol struct {
	config *methodConfig
}

var _ clientProtocolHandler = grpcWebClientProtocol{}

func (g grpcWebClientProtocol) Protocol() Protocol {
	return ProtocolGRPCWeb
}

func (g grpcWebClientProtocol) DecodeRequestHeader(meta *requestMeta) error {
	if meta.Method != http.MethodPost {
		return protocolError("invalid method %q", meta.Method)
	}
	if err := grpcExtractRequestMeta("application/grpc-web", "application/grpc-web+", meta); err != nil {
		return err
	}
	// Resolve Codecs
	codec, err := g.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Client.Codec = codec
	if meta.CompressionName != "" {
		compressor, err := g.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = compressor
	}
	return nil
}

func (g grpcWebClientProtocol) PrepareRequestMessage(msg *messageBuffer, meta *requestMeta) error {
	if msg.Buf.Len() < 5 {
		return io.ErrShortBuffer // ask for more data
	}
	flags, size, err := readEnvelope(msg.Buf)
	if err != nil {
		return err
	}
	msg.Src.Size = size
	if flags&1 != 0 {
		msg.Src.IsCompressed = true
		if meta.Client.Compressor == nil {
			return protocolError("server sent compressed message but client did not request compression")
		}
	}
	return nil
}

func (g grpcWebClientProtocol) EncodeRequestHeader(meta *responseMeta) error {
	grpcEncodeResponseHeader("application/grpc-web+", meta)

	codec, err := g.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Client.Codec = codec
	if meta.CompressionName != "" {
		comp, err := g.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = comp
	}
	return nil
}

func (g grpcWebClientProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	msg.Dst.IsEnvelope = true
	if meta.Client.Compressor != nil {
		msg.Dst.Flags |= 1
		msg.Dst.IsCompressed = true
	}
	return nil
}

func (g grpcWebClientProtocol) EncodeResponseTrailer(buf *bytes.Buffer, meta *responseMeta) error {
	trailer := httpExtractTrailers(meta.Header)
	trailer["grpc-status"] = []string{"0"}
	trailer["grpc-message"] = []string{""}
	grpcWebEncodeTrailers(buf, trailer)
	return nil
}

func (g grpcWebClientProtocol) EncodeError(buf *bytes.Buffer, meta *responseMeta, err error) {
	cerr := asConnectError(err)
	if !meta.WroteStatus {
		grpcEncodeError(cerr, meta.Header)
		meta.StatusCode = http.StatusOK // gRPC errors are always HTTP 200
	} else {
		trailer := httpExtractTrailers(meta.Header)
		grpcEncodeError(cerr, trailer)
		grpcWebEncodeTrailers(buf, trailer)
	}
}

// grpcServerProtocol implements the gRPC-Web protocol for
// sending RPCs to the server handler.
type grpcWebServerProtocol struct {
	config *methodConfig
}

var _ serverProtocolHandler = grpcWebServerProtocol{}

func (g grpcWebServerProtocol) Protocol() Protocol {
	return ProtocolGRPCWeb
}

func (g grpcWebServerProtocol) EncodeRequestHeader(meta *requestMeta) error {
	codec, err := g.config.GetServerCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Server.Codec = codec
	if meta.CompressionName != "" {
		comp, err := g.config.GetServerCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Server.Compressor = comp
	}
	grpcEncodeRequestHeader("application/grpc-web+", meta)
	meta.URL.Path = g.config.methodPath
	meta.Method = http.MethodPost
	return nil
}

func (g grpcWebServerProtocol) PrepareRequestMessage(msg *messageBuffer, meta *requestMeta) error {
	msg.Dst.IsEnvelope = true
	if meta.Server.Compressor != nil {
		msg.Dst.Flags |= 1
		msg.Dst.IsCompressed = true
	}
	return nil
}

func (g grpcWebServerProtocol) DecodeRequestHeader(meta *responseMeta) error {
	if err := grpcExtractResponseMeta("application/grpc-web", "application/grpc-web+", meta); err != nil {
		return err
	}
	codec, err := g.config.GetServerCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Server.Codec = codec
	if meta.CompressionName != "" {
		compressor, err := g.config.GetServerCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Server.Compressor = compressor
	}
	return err
}

func (g grpcWebServerProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	if msg.Buf.Len() < 5 {
		return io.ErrShortBuffer // ask for more data
	}
	flags, size, err := readEnvelope(msg.Buf)
	if err != nil {
		return err
	}
	msg.Src.Size = size
	if flags&1 != 0 {
		msg.Src.IsCompressed = true
		if meta.Server.Compressor == nil {
			return protocolError("server sent compressed message but client did not request compression")
		}
	}
	if flags&0b0111_1110 != 0 {
		return protocolError("invalid frame flags: only highest and lowest bits may be set; instead got %d", flags)
	}
	msg.Src.IsTrailer = flags&grpcWebFlagEnvelopeTrailer != 0
	return nil
}

func (g grpcWebServerProtocol) DecodeResponseTrailer(buf *bytes.Buffer, meta *responseMeta) error {
	// Check for grpc-Web trailers in headers, otherwise look for trailers in
	// the message.
	trailer := meta.Header
	if meta.Header.Get("Grpc-Status") == "" {
		// Per the gRPC-Web specification, trailers should be encoded as an HTTP/1
		// headers block _without_ the terminating newline. To make the headers
		// parseable by net/textproto, we need to add the newline.
		buf.WriteByte('\n')
		bufferedReader := bufio.NewReader(buf)
		mimeReader := textproto.NewReader(bufferedReader)
		mimeHeader, mimeErr := mimeReader.ReadMIMEHeader()
		if mimeErr != nil {
			return protocolError("invalid trailer: %w", mimeErr)
		}
		mimeHeader.Del("Content-Type")
		for key, vals := range mimeHeader {
			if !strings.HasPrefix(key, "Grpc-") {
				key = http.TrailerPrefix + key
			}
			trailer[key] = vals
		}
	}
	if err := grpcExtractErrorFromTrailer(trailer); err != nil {
		return err
	}
	return nil
}

func grpcExtractRequestMeta(contentTypeShort, contentTypePrefix string, meta *requestMeta) error {
	if err := grpcExtractTimeoutFromHeaders(meta.Header, meta); err != nil {
		return err
	}
	contentType := meta.Header.Get("Content-Type")
	if contentType == contentTypeShort {
		meta.CodecName = CodecProto
	} else {
		meta.CodecName = strings.TrimPrefix(contentType, contentTypePrefix)
	}
	meta.Header.Del("Content-Type")
	meta.CompressionName = meta.Header.Get("Grpc-Encoding")
	meta.Header.Del("Grpc-Encoding")
	meta.AcceptCompression = parseMultiHeader(meta.Header.Values("Grpc-Accept-Encoding"))
	meta.Header.Del("Grpc-Accept-Encoding")
	return nil
}

func grpcExtractResponseMeta(contentTypeShort, contentTypePrefix string, meta *responseMeta) error {
	contentType := meta.Header.Get("Content-Type")
	switch {
	case contentType == contentTypeShort:
		meta.CodecName = CodecProto
	case strings.HasPrefix(contentType, contentTypePrefix):
		meta.CodecName = strings.TrimPrefix(contentType, contentTypePrefix)
	default:
		meta.CodecName = contentType + "?"
	}
	meta.Header.Del("Content-Type")
	meta.CompressionName = meta.Header.Get("Grpc-Encoding")
	meta.Header.Del("Grpc-Encoding")
	meta.AcceptCompression = parseMultiHeader(meta.Header.Values("Grpc-Accept-Encoding"))
	meta.Header.Del("Grpc-Accept-Encoding")

	// See if RPC is already over (unexpected HTTP error or trailers-only response)
	if len(meta.Header.Values("Grpc-Status")) > 0 {
		if err := grpcExtractErrorFromTrailer(meta.Header); err != nil {
			return err
		}
	}
	if meta.StatusCode != http.StatusOK {
		return connect.NewError(connect.CodeUnknown,
			fmt.Errorf("unexpected HTTP error: %d %s",
				meta.StatusCode, http.StatusText(meta.StatusCode)))
	}
	return nil
}

func grpcEncodeRequestHeader(contentTypePrefix string, meta *requestMeta) {
	meta.Header.Set("Content-Type", contentTypePrefix+meta.CodecName)
	if meta.Server.Compressor != nil {
		meta.Header.Set("Grpc-Encoding", meta.CompressionName)
		meta.Header.Set("Grpc-Accept-Encoding", meta.CompressionName)
	}
	if meta.Timeout > 0 {
		timeoutStr := grpcEncodeTimeout(meta.Timeout)
		meta.Header.Set("Grpc-Timeout", timeoutStr)
	}
}

func grpcEncodeResponseHeader(contentTypePrefix string, meta *responseMeta) {
	meta.Header.Set("Content-Type", contentTypePrefix+meta.CodecName)
	if meta.CompressionName != "" {
		meta.Header.Set("Grpc-Encoding", meta.CompressionName)
		meta.Header.Set("Grpc-Accept-Encoding", meta.CompressionName)
	}
}

func grpcEncodeError(cerr *connect.Error, trailers httpHeader) {
	trailers.Set("Grpc-Status", strconv.Itoa(int(cerr.Code())))
	trailers.Set("Grpc-Message", grpcPercentEncode(cerr.Message()))
	if len(cerr.Details()) == 0 {
		return
	}
	stat := grpcStatusFromError(cerr)
	bin, err := proto.Marshal(stat)
	if err == nil {
		trailers.Set("Grpc-Status-Details-Bin", connect.EncodeBinaryHeader(bin))
	}
}

func grpcStatusFromError(err *connect.Error) *status.Status {
	stat := &status.Status{
		Code:    int32(err.Code()),
		Message: err.Message(),
	}
	if details := err.Details(); len(details) > 0 {
		stat.Details = make([]*anypb.Any, len(details))
		for i, detail := range details {
			stat.Details[i] = &anypb.Any{
				TypeUrl: "type.googleapis.com/" + detail.Type(),
				Value:   detail.Bytes(),
			}
		}
	}
	return stat
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
	var hexCount int
	for i := 0; i < len(msg); i++ {
		if grpcShouldEscape(msg[i]) {
			hexCount++
		}
	}
	if hexCount == 0 {
		return msg
	}
	// We need to escape some characters, so we'll need to allocate a new string.
	var out strings.Builder
	out.Grow(len(msg) + 2*hexCount)
	for i := 0; i < len(msg); i++ {
		switch char := msg[i]; {
		case grpcShouldEscape(char):
			out.WriteByte('%')
			out.WriteByte(upperhex[char>>4])
			out.WriteByte(upperhex[char&15])
		default:
			out.WriteByte(char)
		}
	}
	return out.String()
}

func grpcPercentDecode(input string) (string, error) {
	percentCount := 0
	for i := 0; i < len(input); {
		switch input[i] {
		case '%':
			percentCount++
			if err := validateHex(input[i:]); err != nil {
				return "", err
			}
			i += 3
		default:
			i++
		}
	}
	if percentCount == 0 {
		return input, nil
	}
	// We need to unescape some characters, so we'll need to allocate a new string.
	var out strings.Builder
	out.Grow(len(input) - 2*percentCount)
	for i := 0; i < len(input); i++ {
		switch input[i] {
		case '%':
			out.WriteByte(unhex(input[i+1])<<4 | unhex(input[i+2]))
			i += 2
		default:
			out.WriteByte(input[i])
		}
	}
	return out.String(), nil
}

// Characters that need to be escaped are defined in gRPC's HTTP/2 spec.
// They're different from the generic set defined in RFC 3986.
func grpcShouldEscape(char byte) bool {
	return char < ' ' || char > '~' || char == '%'
}

// The gRPC wire protocol specifies that errors should be serialized using the
// binary Protobuf format, even if the messages in the request/response stream
// use a different codec. Consequently, this function needs a Protobuf codec to
// unmarshal error information in the headers.
func grpcExtractErrorFromTrailer(trailers httpHeader) *connect.Error {
	grpcStatus := trailers.Get("Grpc-Status")
	grpcMsg := trailers.Get("Grpc-Message")
	grpcDetails := trailers.Get("Grpc-Status-Details-Bin")
	trailers.Del("Grpc-Status")
	trailers.Del("Grpc-Message")
	trailers.Del("Grpc-Status-Details-Bin")

	codeHeader := grpcStatus
	if codeHeader == "" {
		return connect.NewError(
			connect.CodeInternal,
			protocolError("missing trailer header %q", "Grpc-Status"),
		)
	}
	if codeHeader == "0" {
		return nil
	}

	code, err := strconv.ParseUint(codeHeader, 10 /* base */, 32 /* bitsize */)
	if err != nil {
		return connect.NewError(
			connect.CodeInternal,
			protocolError("invalid error code %q: %w", codeHeader, err),
		)
	}
	if code == 0 {
		return nil
	}

	if len(grpcDetails) == 0 {
		message, err := grpcPercentDecode(grpcMsg)
		if err != nil {
			return connect.NewError(
				connect.CodeInternal,
				protocolError("invalid grpc-message trailer: %w", err),
			)
		}
		return connect.NewWireError(connect.Code(code), errors.New(message))
	}

	// Prefer the Protobuf-encoded data to the headers (grpc-go does this too).
	detailsBinary, err := connect.DecodeBinaryHeader(grpcDetails)
	if err != nil {
		return connect.NewError(
			connect.CodeInternal,
			protocolError("invalid grpc-status-details-bin trailer: %w", err),
		)
	}
	var stat status.Status
	if err := proto.Unmarshal(detailsBinary, &stat); err != nil {
		return connect.NewError(
			connect.CodeInternal,
			protocolError("invalid protobuf for error details: %w", err),
		)
	}
	trailerErr := connect.NewWireError(connect.Code(stat.Code), errors.New(stat.Message))
	for _, msg := range stat.Details {
		errDetail, err := connect.NewErrorDetail(msg)
		if err != nil {
			// shouldn't happen since msg is an Any and doesn't need to be marshalled
			continue
		}
		trailerErr.AddDetail(errDetail)
	}
	return trailerErr
}

func grpcExtractTimeoutFromHeaders(headers httpHeader, meta *requestMeta) error {
	timeoutStr := headers.Get("Grpc-Timeout")
	headers.Del("Grpc-Timeout")
	if timeoutStr == "" {
		return nil
	}
	timeout, err := grpcDecodeTimeout(timeoutStr)
	if err != nil {
		return err
	}
	meta.Timeout = timeout
	return nil
}

func grpcDecodeTimeout(timeout string) (time.Duration, error) {
	if timeout == "" {
		return 0, errNoTimeout
	}
	unit := grpcTimeoutUnitLookup(timeout[len(timeout)-1])
	if unit == 0 {
		return 0, protocolError("timeout %q has invalid unit", timeout)
	}
	num, err := strconv.ParseInt(timeout[:len(timeout)-1], 10 /* base */, 64 /* bitsize */)
	if err != nil || num < 0 {
		return 0, protocolError("invalid timeout %q", timeout)
	}
	if num > 99999999 { // timeout must be ASCII string of at most 8 digits
		return 0, protocolError("timeout %q is too long", timeout)
	}
	const grpcTimeoutMaxHours = 8
	if unit == time.Hour && num > grpcTimeoutMaxHours {
		// Timeout is effectively unbounded, so ignore it. The grpc-go
		// implementation does the same thing.
		return 0, errNoTimeout
	}
	return time.Duration(num) * unit, nil
}

func grpcEncodeTimeout(timeout time.Duration) string {
	if timeout <= 0 {
		return "0n"
	}
	const grpcTimeoutMaxValue = 1e8
	var (
		size time.Duration
		unit byte
	)
	switch {
	case timeout < time.Nanosecond*grpcTimeoutMaxValue:
		size, unit = time.Nanosecond, 'n'
	case timeout < time.Microsecond*grpcTimeoutMaxValue:
		size, unit = time.Microsecond, 'u'
	case timeout < time.Millisecond*grpcTimeoutMaxValue:
		size, unit = time.Millisecond, 'm'
	case timeout < time.Second*grpcTimeoutMaxValue:
		size, unit = time.Second, 'S'
	case timeout < time.Minute*grpcTimeoutMaxValue:
		size, unit = time.Minute, 'M'
	default:
		size, unit = time.Hour, 'H'
	}
	value := timeout / size
	return strconv.FormatInt(int64(value), 10 /* base */) + string(unit)
}

func grpcTimeoutUnitLookup(unit byte) time.Duration {
	switch unit {
	case 'n':
		return time.Nanosecond
	case 'u':
		return time.Microsecond
	case 'm':
		return time.Millisecond
	case 'S':
		return time.Second
	case 'M':
		return time.Minute
	case 'H':
		return time.Hour
	default:
		return 0
	}
}

func grpcWebEncodeTrailers(dst *bytes.Buffer, trailer httpHeader) {
	for key, values := range trailer {
		lowerKey := strings.ToLower(key)
		if lowerKey == key {
			continue
		}
		delete(trailer, key)
		trailer[lowerKey] = values
	}
	dst.Write([]byte{0, 0, 0, 0, 0}) // empty message
	_ = http.Header(trailer).Write(dst)
	dst.Bytes()[0] |= grpcWebFlagEnvelopeTrailer // set trailer flag
	size := uint32(dst.Len() - 5)
	binary.BigEndian.PutUint32(dst.Bytes()[1:], size)
}
