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
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	// TODO: Extract more constants for header names and values.
	contentTypeJSON = "application/json"

	connectFlagEnvelopeEndStream = 0b00000010
)

// connectUnaryClientProtocol implements the Connect protocol for
// processing unary RPCs received from the client that use POST as the
// HTTP method.
type connectUnaryClientProtocol struct {
	config *methodConfig
}

var _ clientProtocolHandler = connectUnaryClientProtocol{}

func (c connectUnaryClientProtocol) Protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryClientProtocol) acceptsStreamType(_ *operation, streamType connect.StreamType) bool {
	return streamType == connect.StreamTypeUnary
}

func (c connectUnaryClientProtocol) allowsGetRequests(conf *methodConfig) bool {
	methodOpts, ok := conf.descriptor.Options().(*descriptorpb.MethodOptions)
	return ok && methodOpts.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
}

func (c connectUnaryClientProtocol) decodeGetQuery(meta *requestMeta) error {
	if !c.allowsGetRequests(c.config) {
		return protocolError("GET requests not allowed for this method")
	}
	query := meta.URL.Query()
	meta.URL.RawQuery = ""
	meta.CodecName = query.Get("encoding")
	meta.CompressionName = query.Get("compression")
	meta.AcceptCompression = parseMultiHeader(meta.Header.Values("Accept-Encoding"))
	meta.Header.Del("Accept-Encoding")
	meta.Header.Del("Content-Type")
	meta.Header.Del("Connect-Protocol-Version")

	codec, err := c.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Client.Codec = codec
	if meta.CompressionName != "" {
		compressor, err := c.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = compressor
	}

	// Replace the request body with the bytes in the query string.
	base64Str := query.Get("base64")
	var base64enc bool
	switch base64Str {
	case "", "0":
	// not base64-encoded
	case "1":
		base64enc = true
	default:
		return protocolError("query string parameter base64 should be absent or have value 0 or 1; instead got %q", base64Str)
	}
	msgStr := query.Get("message")
	var msgData []byte
	if base64enc && msgStr != "" {
		var err error
		msgData, err = base64.RawURLEncoding.DecodeString(msgStr)
		if err != nil {
			// Padding characters are allowed.
			msgData, err = base64.URLEncoding.DecodeString(msgStr)
		}
		if err != nil {
			return fmt.Errorf("query string parameter message should be base64-encoded but could not decode: %w", err)
		}
	} else {
		msgData = ([]byte)(msgStr)
	}

	// Require blocking on request body to encode URL parameters.
	meta.RequiresBody = true
	meta.Body = io.NopCloser(bytes.NewBuffer(msgData))
	return nil
}

func (c connectUnaryClientProtocol) DecodeRequestHeader(meta *requestMeta) error {
	if c.config.streamType != connect.StreamTypeUnary {
		return protocolError("expected unary stream type")
	}
	if meta.Method == http.MethodGet {
		return c.decodeGetQuery(meta)
	}

	timeout, err := connectExtractTimeout(meta.Header)
	if err != nil {
		return err
	}
	meta.Timeout = timeout
	meta.CodecName = strings.TrimPrefix(meta.Header.Get("Content-Type"), "application/")
	if meta.CodecName == CodecJSON+"; charset=utf-8" {
		// TODO: should we support other text formats that may need charset check?
		meta.CodecName = CodecJSON
	}
	meta.Header.Del("Content-Type")
	meta.CompressionName = meta.Header.Get("Content-Encoding")
	meta.Header.Del("Content-Encoding")
	meta.AcceptCompression = parseMultiHeader(meta.Header.Values("Accept-Encoding"))
	meta.Header.Del("Accept-Encoding")
	meta.Header.Del("Connect-Protocol-Version")

	// Resolve Codecs
	codec, err := c.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Client.Codec = codec
	if meta.CompressionName != "" {
		compressor, err := c.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = compressor
	}
	return nil
}

func (c connectUnaryClientProtocol) PrepareRequestMessage(msg *messageBuffer, meta *requestMeta) error {
	msg.Src.IsCompressed = meta.Client.Compressor != nil
	msg.Src.ReadMode = readModeEOF
	return nil
}

func (c connectUnaryClientProtocol) EncodeRequestHeader(meta *responseMeta) error {
	// Resolve Codecs
	codec, err := c.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Client.Codec = codec
	meta.Header.Set("Content-Type", "application/"+meta.CodecName)
	if meta.CompressionName != "" {
		compressor, err := c.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = compressor
		meta.Header.Set("Content-Encoding", meta.CompressionName)
		meta.Header.Set("Accept-Encoding", meta.CompressionName)
	}
	return nil
}

func (c connectUnaryClientProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	msg.Dst.IsCompressed = meta.Client.Compressor != nil
	msg.Dst.WaitForTrailer = true
	return nil
}

func (c connectUnaryClientProtocol) EncodeResponseTrailer(_ *bytes.Buffer, meta *responseMeta) error {
	trailer := httpExtractTrailers(meta.Header)
	for key, values := range trailer {
		meta.Header.Del(key)
		meta.Header.Set("Trailer-"+key, strings.Join(values, ", "))
	}
	return nil
}
func (c connectUnaryClientProtocol) EncodeError(buf *bytes.Buffer, meta *responseMeta, err error) {
	// Encode the error as uncompressed JSON.
	meta.Header.Del("Content-Encoding")
	meta.Header.Set("Content-Type", contentTypeJSON)

	cerr := asConnectError(err)
	wireErr := connectErrorToWireError(cerr, c.config.resolver)
	if err := json.NewEncoder(buf).Encode(wireErr); err != nil {
		buf.WriteString(`{"code": "internal", "message": ` + strconv.Quote("failed to marshal end error: "+err.Error()) + `}`)
	}
	meta.StatusCode = httpStatusCodeFromRPC(cerr.Code())
}

// connectUnaryServerProtocol implements the Connect protocol for
// sending unary RPCs to the server handler.
type connectUnaryServerProtocol struct {
	config *methodConfig

	// State
	statusCode int
}

var _ serverProtocolHandler = (*connectUnaryServerProtocol)(nil)

func (c *connectUnaryServerProtocol) Protocol() Protocol {
	return ProtocolConnect
}

func (c *connectUnaryServerProtocol) encodeGetQuery(meta *requestMeta) error {
	meta.Method = http.MethodGet
	meta.Header.Del("Content-Type")

	codec, ok := meta.Server.Codec.(StableCodec)
	if !ok {
		return protocolError("cannot use GET with unstable codec")
	}
	meta.Server.Codec = connectGetRequestCodec{
		config:     c.config,
		codec:      codec,
		compressor: meta.Server.Compressor,
		meta:       meta,
	}
	// Handle compression in the codec.
	meta.Server.Compressor = nil
	return nil
}

func (c *connectUnaryServerProtocol) EncodeRequestHeader(meta *requestMeta) error {
	meta.RequiresBody = true // Require body for URL parameters.

	// Resolve codecs
	codec, err := c.config.GetServerCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Server.Codec = codec
	if meta.CompressionName != "" {
		compression, err := c.config.GetServerCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Server.Compressor = compression
	}
	if meta.Timeout > 0 {
		timeoutStr := connectEncodeTimeout(meta.Timeout)
		if timeoutStr != "" {
			meta.Header.Set("Connect-Timeout-Ms", timeoutStr)
		}
	}
	meta.Header.Set("Accept", "application/"+meta.CodecName)

	// Encode as GET request if possible.
	meta.URL.Path = c.config.methodPath
	if c.useGet(meta) {
		return c.encodeGetQuery(meta)
	}
	meta.Method = http.MethodPost
	meta.URL.RawQuery = ""

	meta.Header.Set("Content-Type", "application/"+meta.CodecName)
	if meta.CompressionName != "" {
		meta.Header.Set("Content-Encoding", meta.CompressionName)
	}
	if len(meta.AcceptCompression) > 0 {
		meta.Header.Set("Accept-Encoding", strings.Join(meta.AcceptCompression, ", "))
	}
	meta.Header.Set("Connect-Protocol-Version", "1")

	return nil
}

func (c *connectUnaryServerProtocol) PrepareRequestMessage(msg *messageBuffer, meta *requestMeta) error {
	msg.Dst.IsCompressed = meta.Server.Compressor != nil
	return nil
}

func (c *connectUnaryServerProtocol) DecodeRequestHeader(meta *responseMeta) error {
	contentType := meta.Header.Get("Content-Type")
	meta.Header.Del("Content-Type")
	meta.CompressionName = meta.Header.Get("Content-Encoding")
	meta.Header.Del("Content-Encoding")
	meta.AcceptCompression = parseMultiHeader(meta.Header.Values("Accept-Encoding"))
	meta.Header.Del("Accept-Encoding")
	c.statusCode = meta.StatusCode // Save for DecodeTrailer.

	switch {
	case strings.HasPrefix(contentType, "application/"):
		meta.CodecName = strings.TrimPrefix(contentType, "application/")
	default:
		// Invalid codec, try capture the error in DecodeMessage.
		return nil
	}

	// Encode connect trailers as HTTP trailers.
	trailers := connectExtractUnaryTrailers(meta.Header)
	for key, values := range trailers {
		meta.Header.Del(key)
		for _, value := range values {
			meta.Header.Add(http.TrailerPrefix+key, value)
		}
	}

	// Resolve codecs, only if the response is successful.
	// Otherwise we default to JSON using a connect compatible JSON codec.
	if c.statusCode == 200 {
		codec, err := c.config.GetServerCodec(meta.CodecName)
		if err != nil {
			return err
		}
		meta.Server.Codec = codec
	}
	if meta.CompressionName != "" {
		compression, err := c.config.GetServerCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Server.Compressor = compression
	}
	return nil
}

func (c *connectUnaryServerProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	msg.Src.ReadMode = readModeEOF // Read until EOF
	msg.Src.IsCompressed = meta.Server.Compressor != nil
	if meta.Server.Codec == nil || c.statusCode/100 != 2 {
		msg.Src.IsTrailer = true
	}
	return nil
}

// DecodeResponseTrailer decodes the error from the last message, if any.
// Trailers are set in the HTTP response headers.
func (c *connectUnaryServerProtocol) DecodeResponseTrailer(buf *bytes.Buffer, _ *responseMeta) error {
	if c.statusCode/100 == 2 {
		return nil
	}
	if buf.Len() == 0 {
		code := httpStatusCodeToRPC(c.statusCode)
		return connect.NewWireError(code, errors.New("no error details"))
	}

	var wireErr connectWireError
	if err := json.Unmarshal(buf.Bytes(), &wireErr); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to unmarshal error: %w", err))
	}
	return wireErr.toConnectError()
}

func (c *connectUnaryServerProtocol) useGet(meta *requestMeta) bool {
	methodOptions, _ := c.config.descriptor.Options().(*descriptorpb.MethodOptions)
	noSideEffects := methodOptions.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
	_, isStable := meta.Server.Codec.(StableCodec)
	return isStable && noSideEffects
}

type connectGetRequestCodec struct {
	config     *methodConfig
	codec      StableCodec
	compressor compressor
	meta       *requestMeta
}

func (c connectGetRequestCodec) Name() string {
	return "connect-get+" + c.codec.Name()
}
func (c connectGetRequestCodec) MarshalAppend(dst []byte, msg proto.Message) ([]byte, error) {
	vals := make(url.Values, 5)
	vals.Set("connect", "v1")
	vals.Set("encoding", c.codec.Name())

	dst, err := c.codec.MarshalAppendStable(dst, msg)
	if err != nil {
		return nil, err
	}

	msgRaw := dst

	// TODO: move the compression logic outside of the codec.
	if c.compressor != nil {
		var tmp bytes.Buffer
		src := bytes.NewBuffer(msgRaw)
		vals.Set("compression", c.compressor.Name())
		if err := c.compressor.compress(&tmp, src); err != nil {
			return nil, err
		}
		msgRaw = tmp.Bytes()
	}

	var msgStr string
	if c.codec.IsBinary() || c.compressor != nil {
		vals.Set("base64", "1")
		msgStr = base64.RawURLEncoding.EncodeToString(msgRaw)
	} else {
		msgStr = string(dst)
	}

	vals.Set("message", msgStr)
	c.meta.URL.RawQuery = vals.Encode()
	c.meta.Method = http.MethodGet
	size := len(c.config.methodPath) + len(c.meta.URL.RawQuery)
	if size >= int(c.config.maxGetURLBytes) {
		// URL is too big; fall back to POST
		// TODO: should we try to compress the message?
		c.meta.Header.Set("Content-Type", "application/"+c.codec.Name())
		c.meta.Header.Set("Connect-Protocol-Version", "1")
		c.meta.Header.Del("Content-Encoding")
		c.meta.Method = http.MethodPost
		c.meta.URL.RawQuery = ""
		return dst, nil
	}
	// Successfully encoded as GET request.
	return dst[:0], nil
}
func (c connectGetRequestCodec) Unmarshal(_ []byte, _ proto.Message) error {
	// This should never be called.
	return fmt.Errorf("unimplemented")
}

// connectStreamClientProtocol implements the Connect protocol for
// processing streaming RPCs received from the client.
type connectStreamClientProtocol struct {
	config *methodConfig
}

var _ clientProtocolHandler = connectStreamClientProtocol{}

func (c connectStreamClientProtocol) Protocol() Protocol {
	return ProtocolConnect
}

func (c connectStreamClientProtocol) acceptsStreamType(_ *operation, streamType connect.StreamType) bool {
	return streamType != connect.StreamTypeUnary
}

func (c connectStreamClientProtocol) DecodeRequestHeader(meta *requestMeta) error {
	if meta.Method != http.MethodPost {
		return protocolError("expected POST method")
	}
	timeout, err := connectExtractTimeout(meta.Header)
	if err != nil {
		return err
	}
	meta.Timeout = timeout

	meta.CodecName = strings.TrimPrefix(meta.Header.Get("Content-Type"), "application/connect+")
	meta.Header.Del("Content-Type")
	meta.CompressionName = meta.Header.Get("Connect-Content-Encoding")
	meta.Header.Del("Connect-Content-Encoding")
	meta.AcceptCompression = parseMultiHeader(meta.Header.Values("Connect-Accept-Encoding"))
	meta.Header.Del("Connect-Accept-Encoding")
	// Resolve codecs
	codec, err := c.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Client.Codec = codec
	if meta.CompressionName != "" {
		compressor, err := c.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = compressor
	}
	return nil
}

func (c connectStreamClientProtocol) PrepareRequestMessage(msg *messageBuffer, meta *requestMeta) error {
	if msg.Buf.Len() < 5 {
		return io.ErrShortBuffer // ask for more data
	}
	flags, size, err := readEnvelope(msg.Buf)
	if err != nil {
		return err
	}
	msg.Src.Size = size
	if flags&flagEnvelopeCompressed != 0 {
		msg.Src.IsCompressed = true
	}
	return nil
}

func (c connectStreamClientProtocol) EncodeRequestHeader(meta *responseMeta) error {
	meta.Header.Set("Content-Type", "application/connect+"+meta.CodecName)
	if meta.CompressionName != "" {
		meta.Header.Set("Connect-Content-Encoding", meta.CompressionName)
		meta.Header.Set("Connect-Accept-Encoding", meta.CompressionName)
	}
	// Resolve codecs
	codec, err := c.config.GetClientCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Client.Codec = codec
	if meta.CompressionName != "" {
		compressor, err := c.config.GetClientCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Client.Compressor = compressor
	}
	return nil
}

func (c connectStreamClientProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	msg.Dst.IsEnvelope = true
	msg.Dst.IsCompressed = meta.Client.Compressor != nil
	if msg.Dst.IsCompressed {
		msg.Dst.Flags |= flagEnvelopeCompressed
	}
	return nil
}
func (c connectStreamClientProtocol) EncodeResponseTrailer(buf *bytes.Buffer, meta *responseMeta) error {
	metadata := http.Header(httpExtractTrailers(meta.Header))
	streamEnd := &connectStreamEnd{
		Metadata: metadata,
	}
	connectEncodeStreamEnd(buf, streamEnd)
	return nil
}

func (c connectStreamClientProtocol) EncodeError(buf *bytes.Buffer, meta *responseMeta, err error) {
	// Encode the error as uncompressed JSON.
	cerr := asConnectError(err)
	wireErr := connectErrorToWireError(cerr, c.config.resolver)
	metadata := http.Header(httpExtractTrailers(meta.Header))
	streamEnd := &connectStreamEnd{
		Metadata: metadata,
		Error:    wireErr,
	}
	connectEncodeStreamEnd(buf, streamEnd)
}

// connectStreamServerProtocol implements the Connect protocol for
// sending streaming RPCs to the server handler.
type connectStreamServerProtocol struct {
	config *methodConfig
}

var _ serverProtocolHandler = connectStreamServerProtocol{}

func (c connectStreamServerProtocol) Protocol() Protocol {
	return ProtocolConnect
}

func (c connectStreamServerProtocol) EncodeRequestHeader(meta *requestMeta) error {
	meta.Header.Set("Content-Type", "application/connect+"+meta.CodecName)
	if meta.CompressionName != "" {
		meta.Header.Set("Connect-Content-Encoding", meta.CompressionName)
		meta.Header.Set("Connect-Accept-Encoding", meta.CompressionName)
	}
	if meta.Timeout > 0 {
		meta.Header.Set("Connect-Timeout-Ms", connectEncodeTimeout(meta.Timeout))
	}
	// Resovlve Codecs
	codec, err := c.config.GetServerCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Server.Codec = codec
	if meta.CompressionName != "" {
		compression, err := c.config.GetServerCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Server.Compressor = compression
	}
	meta.URL.Path = c.config.methodPath
	meta.Method = http.MethodPost
	return nil
}

func (c connectStreamServerProtocol) PrepareRequestMessage(msg *messageBuffer, meta *requestMeta) error {
	msg.Dst.IsEnvelope = true
	if meta.Server.Compressor != nil {
		msg.Dst.IsCompressed = true
		msg.Dst.Flags |= flagEnvelopeCompressed
	}
	return nil
}

func (c connectStreamServerProtocol) DecodeRequestHeader(meta *responseMeta) error {
	contentType := meta.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/connect+"):
		meta.CodecName = strings.TrimPrefix(contentType, "application/connect+")
	default:
		meta.CodecName = contentType + "?"
	}
	meta.Header.Del("Content-Type")
	meta.CompressionName = meta.Header.Get("Connect-Content-Encoding")
	meta.Header.Del("Connect-Content-Encoding")
	meta.AcceptCompression = parseMultiHeader(meta.Header.Values("Connect-Accept-Encoding"))
	meta.Header.Del("Connect-Accept-Encoding")
	if meta.StatusCode != http.StatusOK {
		return protocolError("expected HTTP status OK (200) but got %d", meta.StatusCode)
	}
	// Relove Codecs
	codec, err := c.config.GetServerCodec(meta.CodecName)
	if err != nil {
		return err
	}
	meta.Server.Codec = codec
	if meta.CompressionName != "" {
		compression, err := c.config.GetServerCompressor(meta.CompressionName)
		if err != nil {
			return err
		}
		meta.Server.Compressor = compression
	}
	return nil
}

func (c connectStreamServerProtocol) PrepareResponseMessage(msg *messageBuffer, meta *responseMeta) error {
	if msg.Buf.Len() < 5 {
		return io.ErrShortBuffer // ask for more data
	}
	flags, size, err := readEnvelope(msg.Buf)
	if err != nil {
		return err
	}
	msg.Src.Size = size
	if flags&flagEnvelopeCompressed != 0 {
		msg.Src.IsCompressed = true
	}
	if flags&connectFlagEnvelopeEndStream != 0 {
		msg.Src.IsTrailer = true
	}
	return nil
}

func (c connectStreamServerProtocol) DecodeResponseTrailer(buf *bytes.Buffer, meta *responseMeta) error {
	if buf.Len() == 0 {
		return protocolError("expected trailer")
	}

	var streamEnd connectStreamEnd
	if err := json.Unmarshal(buf.Bytes(), &streamEnd); err != nil {
		return protocolError("failed to unmarshal trailer: %w", err)
	}
	// Encode connect trailers as HTTP trailers.
	for key, values := range streamEnd.Metadata {
		meta.Header.Del(key)
		key = http.TrailerPrefix + key
		for _, value := range values {
			meta.Header.Add(key, value)
		}
	}
	if streamEnd.Error != nil {
		return streamEnd.Error.toConnectError()
	}
	return nil
}

func connectExtractUnaryTrailers(headers httpHeader) http.Header {
	var count int
	for k := range headers {
		if strings.HasPrefix(k, "Trailer-") {
			count++
		}
	}
	result := make(http.Header, count)
	for k, v := range headers {
		if strings.HasPrefix(k, "Trailer-") {
			result[strings.TrimPrefix(k, "Trailer-")] = v
			delete(headers, k)
		}
	}
	return result
}

func connectExtractTimeout(headers httpHeader) (time.Duration, error) {
	str := headers.Get("Connect-Timeout-Ms")
	headers.Del("Connect-Timeout-Ms")
	if str == "" {
		return 0, nil
	}
	timeoutInt, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return 0, err
	}
	if timeoutInt < 0 {
		return 0, fmt.Errorf("timeout header indicated invalid negative value: %d", timeoutInt)
	}
	timeout := time.Millisecond * time.Duration(timeoutInt)
	if timeout.Milliseconds() != timeoutInt {
		// overflow
		timeout = time.Duration(math.MaxInt64)
	}
	return timeout, nil
}

func connectEncodeTimeout(timeout time.Duration) string {
	str := strconv.FormatInt(timeout.Milliseconds(), 10)
	if len(str) > 10 {
		return "9999999999"
	}
	return str
}

type connectWireError struct {
	Code    connect.Code        `json:"code"`
	Message string              `json:"message,omitempty"`
	Details []connectWireDetail `json:"details,omitempty"`
}

func (e *connectWireError) toConnectError() *connect.Error {
	cerr := connect.NewError(e.Code, errors.New(e.Message))
	for _, detail := range e.Details {
		detailData, err := base64.RawStdEncoding.DecodeString(detail.Value)
		if err != nil {
			// seems a waste to fail or take other action here...
			// TODO: maybe we should instead *replace* this detail with a placeholder that
			//       indicates the original type and value and this error message?
			continue
		}
		errDetail, err := connect.NewErrorDetail(&anypb.Any{
			TypeUrl: "type.googleapis.com/" + detail.Type,
			Value:   detailData,
		})
		if err != nil {
			// shouldn't happen since we provided an Any that doesn't need to be marshalled
			continue
		}
		cerr.AddDetail(errDetail)
	}
	return cerr
}

type connectWireDetail struct {
	Type  string          `json:"type"`
	Value string          `json:"value"`
	Debug json.RawMessage `json:"debug,omitempty"`
}

func connectErrorToWireError(cerr *connect.Error, resolver TypeResolver) *connectWireError {
	result := &connectWireError{
		Code:    cerr.Code(),
		Message: cerr.Message(),
	}
	if details := cerr.Details(); len(details) > 0 {
		result.Details = make([]connectWireDetail, len(details))
		for i := range details {
			result.Details[i] = connectWireDetail{
				Type:  details[i].Type(),
				Value: base64.RawStdEncoding.EncodeToString(details[i].Bytes()),
			}
			// computing debug value is best effort; ignore errors
			msg, err := details[i].Value()
			if err == nil {
				data, err := protojson.MarshalOptions{Resolver: resolver}.Marshal(msg)
				if err == nil {
					result.Details[i].Debug = data
				}
			}
		}
	}
	return result
}

type connectStreamEnd struct {
	Error    *connectWireError `json:"error,omitempty"`
	Metadata http.Header       `json:"metadata,omitempty"`
}

func connectEncodeStreamEnd(dst *bytes.Buffer, streamEnd *connectStreamEnd) {
	dst.Write([]byte{0, 0, 0, 0, 0}) // empty message
	if err := json.NewEncoder(dst).Encode(streamEnd); err != nil {
		dst.WriteString(`{"error": {"code": "internal", "message": ` + strconv.Quote(err.Error()) + `}}`)
	}
	dst.Bytes()[0] |= connectFlagEnvelopeEndStream // set trailer flag
	size := uint32(dst.Len() - 5)
	binary.BigEndian.PutUint32(dst.Bytes()[1:], size)
}
