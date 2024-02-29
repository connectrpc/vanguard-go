// Copyright 2023-2024 Buf Technologies, Inc.
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
	contentTypeJSON            = "application/json"
	contentConnectStreamPrefix = "application/connect+"
	contentConnectUnaryPrefix  = "application/"
)

// connectUnaryGetClientProtocol implements the Connect protocol for
// processing unary RPCs received from the client that use GET as the
// HTTP method.
type connectUnaryGetClientProtocol struct{}

var _ clientProtocolHandler = connectUnaryGetClientProtocol{}
var _ clientProtocolAllowsGet = connectUnaryGetClientProtocol{}
var _ clientProtocolEndMustBeInHeaders = connectUnaryGetClientProtocol{}
var _ clientBodyPreparer = connectUnaryGetClientProtocol{}

func (c connectUnaryGetClientProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryGetClientProtocol) acceptsStreamType(_ *operation, streamType connect.StreamType) bool {
	return streamType == connect.StreamTypeUnary
}

func (c connectUnaryGetClientProtocol) allowsGetRequests(conf *methodConfig) bool {
	methodOpts, ok := conf.descriptor.Options().(*descriptorpb.MethodOptions)
	return ok && methodOpts.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
}

func (c connectUnaryGetClientProtocol) endMustBeInHeaders() bool {
	return true
}

func (c connectUnaryGetClientProtocol) extractProtocolRequestHeaders(op *operation, headers http.Header) (requestMeta, error) {
	var reqMeta requestMeta
	if err := connectExtractTimeout(headers, &reqMeta); err != nil {
		return reqMeta, err
	}
	query := op.queryValues()
	reqMeta.codec = query.Get("encoding")
	reqMeta.compression = query.Get("compression")
	reqMeta.acceptCompression = parseMultiHeader(headers.Values("Accept-Encoding"))
	headers.Del("Accept-Encoding")
	headers.Del("Content-Type")
	headers.Del("Connect-Protocol-Version")
	return reqMeta, nil
}

func (c connectUnaryGetClientProtocol) addProtocolResponseHeaders(meta responseMeta, headers http.Header) int {
	// Response format is the same as unary POST; only requests differ
	return connectUnaryPostClientProtocol{}.addProtocolResponseHeaders(meta, headers)
}

func (c connectUnaryGetClientProtocol) encodeEnd(op *operation, end *responseEnd, writer io.Writer, wasInHeaders bool) http.Header {
	// Response format is the same as unary POST; only requests differ
	return connectUnaryPostClientProtocol{}.encodeEnd(op, end, writer, wasInHeaders)
}

func (c connectUnaryGetClientProtocol) requestNeedsPrep(_ *operation) bool {
	return true
}

func (c connectUnaryGetClientProtocol) prepareUnmarshalledRequest(op *operation, src []byte, target proto.Message) error {
	if len(src) > 0 {
		return fmt.Errorf("connect unary protocol using GET HTTP method should have no body; instead got %d bytes", len(src))
	}
	// TODO: ideally we could *replace* the request body with the bytes in the query string and then
	//       otherwise use message and its re-encoding/re-compressing capability.
	vals := op.queryValues()
	base64Str := vals.Get("base64")
	var base64enc bool
	switch base64Str {
	case "", "0":
	// not base64-encoded
	case "1":
		base64enc = true
	default:
		return fmt.Errorf("query string parameter base64 should be absent or have value 0 or 1; instead got %q", base64Str)
	}
	msgStr := vals.Get("message")
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
	if op.client.reqCompression != nil {
		dst := op.bufferPool.Get()
		defer op.bufferPool.Put(dst)
		if err := op.client.reqCompression.decompress(dst, bytes.NewBuffer(msgData)); err != nil {
			return err
		}
		msgData = dst.Bytes()
	}
	return op.client.codec.Unmarshal(msgData, target)
}

func (c connectUnaryGetClientProtocol) responseNeedsPrep(_ *operation) bool {
	return false
}

func (c connectUnaryGetClientProtocol) prepareMarshalledResponse(_ *operation, _ []byte, _ proto.Message, _ http.Header) ([]byte, error) {
	return nil, errors.New("response does not need preparation")
}

func (c connectUnaryGetClientProtocol) String() string {
	return c.protocol().String() + " unary (GET)"
}

// connectUnaryPostClientProtocol implements the Connect protocol for
// processing unary RPCs received from the client that use POST as the
// HTTP method.
type connectUnaryPostClientProtocol struct{}

var _ clientProtocolHandler = connectUnaryPostClientProtocol{}
var _ clientProtocolEndMustBeInHeaders = connectUnaryPostClientProtocol{}

func (c connectUnaryPostClientProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryPostClientProtocol) acceptsStreamType(_ *operation, streamType connect.StreamType) bool {
	return streamType == connect.StreamTypeUnary
}

func (c connectUnaryPostClientProtocol) endMustBeInHeaders() bool {
	return true
}

func (c connectUnaryPostClientProtocol) extractProtocolRequestHeaders(_ *operation, headers http.Header) (requestMeta, error) {
	var reqMeta requestMeta
	if err := connectExtractTimeout(headers, &reqMeta); err != nil {
		return reqMeta, err
	}
	reqMeta.codec = strings.TrimPrefix(headers.Get("Content-Type"), contentConnectUnaryPrefix)
	if reqMeta.codec == CodecJSON+"; charset=utf-8" {
		// TODO: should we support other text formats that may need charset check?
		reqMeta.codec = CodecJSON
	}
	headers.Del("Content-Type")
	reqMeta.compression = headers.Get("Content-Encoding")
	headers.Del("Content-Encoding")
	reqMeta.acceptCompression = parseMultiHeader(headers.Values("Accept-Encoding"))
	headers.Del("Accept-Encoding")
	headers.Del("Connect-Protocol-Version")
	return reqMeta, nil
}

func (c connectUnaryPostClientProtocol) addProtocolResponseHeaders(meta responseMeta, headers http.Header) int {
	status := http.StatusOK
	if meta.end != nil && meta.end.err != nil {
		status = httpStatusCodeFromRPC(meta.end.err.Code())
		headers.Set("Content-Type", contentTypeJSON) // error bodies are always in JSON
		// TODO: Content-Encoding to compress error?
	} else {
		headers.Set("Content-Type", contentConnectUnaryPrefix+meta.codec)
		if meta.compression != "" {
			headers.Set("Content-Encoding", meta.compression)
		}
	}
	if meta.end != nil {
		for k, v := range meta.end.trailers {
			headers["Trailer-"+k] = v
		}
	}
	if len(meta.acceptCompression) > 0 {
		headers.Set("Accept-Encoding", strings.Join(meta.acceptCompression, ", "))
	}
	return status
}

func (c connectUnaryPostClientProtocol) encodeEnd(op *operation, end *responseEnd, writer io.Writer, wasInHeaders bool) http.Header {
	if end.err != nil && !wasInHeaders {
		// TODO: Uh oh. We already flushed headers and started writing body. What can we do?
		//       Should this log? If we are using http/2, is there some way we could send
		//       a "goaway" frame to the client, to indicate abnormal end of stream?
		return nil
	}
	if end.err == nil {
		return nil
	}
	wireErr := connectErrorToWireError(end.err, op.methodConf.resolver)
	data, err := json.Marshal(wireErr)
	if err != nil {
		data = ([]byte)(`{"code": "internal", "message": ` + strconv.Quote("failed to marshal end error: "+err.Error()) + `}`)
	}
	_, _ = writer.Write(data)
	return nil
}

func (c connectUnaryPostClientProtocol) String() string {
	return c.protocol().String() + " unary (POST)"
}

// connectUnaryServerProtocol implements the Connect protocol for
// sending unary RPCs to the server handler.
type connectUnaryServerProtocol struct{}

// the latter two interfaces must be implemented to handle GET requests.
var _ serverProtocolHandler = connectUnaryServerProtocol{}
var _ requestLineBuilder = connectUnaryServerProtocol{}
var _ serverBodyPreparer = connectUnaryServerProtocol{}

func (c connectUnaryServerProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryServerProtocol) addProtocolRequestHeaders(meta requestMeta, headers http.Header) {
	headers.Set("Content-Type", contentConnectUnaryPrefix+meta.codec)
	if meta.compression != "" {
		headers.Set("Content-Encoding", meta.compression)
	}
	if len(meta.acceptCompression) > 0 {
		headers.Set("Accept-Encoding", strings.Join(meta.acceptCompression, ", "))
	}
	headers.Set("Connect-Protocol-Version", "1")
	if meta.hasTimeout {
		timeoutStr := connectEncodeTimeout(meta.timeout)
		if timeoutStr != "" {
			headers.Set("Connect-Timeout-Ms", timeoutStr)
		}
	}
}

func (c connectUnaryServerProtocol) extractProtocolResponseHeaders(statusCode int, headers http.Header) (responseMeta, responseEndUnmarshaller, error) {
	var respMeta responseMeta
	contentType := headers.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, contentConnectUnaryPrefix):
		respMeta.codec = strings.TrimPrefix(contentType, contentConnectUnaryPrefix)
	default:
		respMeta.codec = contentType + "?"
	}
	headers.Del("Content-Type")
	respMeta.compression = headers.Get("Content-Encoding")
	headers.Del("Content-Encoding")
	respMeta.acceptCompression = parseMultiHeader(headers.Values("Accept-Encoding"))
	headers.Del("Accept-Encoding")
	trailers := connectExtractUnaryTrailers(headers)

	var endUnmarshaller responseEndUnmarshaller
	if statusCode == http.StatusOK {
		respMeta.pendingTrailers = trailers
	} else {
		// Content-Type must be application/json for errors or else it's invalid
		if contentType != contentTypeJSON {
			respMeta.codec = contentType + "?"
		} else {
			respMeta.codec = ""
		}
		respMeta.end = &responseEnd{
			wasCompressed: respMeta.compression != "",
			trailers:      trailers,
		}
		endUnmarshaller = func(_ Codec, buf *bytes.Buffer, end *responseEnd) {
			var wireErr connectWireError
			if err := json.Unmarshal(buf.Bytes(), &wireErr); err != nil {
				end.err = connect.NewError(connect.CodeInternal, err)
				return
			}
			end.err = wireErr.toConnectError()
		}
	}
	return respMeta, endUnmarshaller, nil
}

func (c connectUnaryServerProtocol) extractEndFromTrailers(_ *operation, _ http.Header) (responseEnd, error) {
	return responseEnd{}, nil
}

func (c connectUnaryServerProtocol) requestNeedsPrep(op *operation) bool {
	return c.useGet(op)
}

func (c connectUnaryServerProtocol) useGet(op *operation) bool {
	methodOptions, _ := op.methodConf.descriptor.Options().(*descriptorpb.MethodOptions)
	_, isStable := op.server.codec.(StableCodec)
	return op.request.Method == http.MethodGet && isStable &&
		methodOptions.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
}

func (c connectUnaryServerProtocol) prepareMarshalledRequest(_ *operation, _ []byte, _ proto.Message, _ http.Header) ([]byte, error) {
	// This would be called when requestNeedsPrep returns true, for GET requests.
	// In that case, there is no request body, so we can return a nil result.
	// The request data will actually be put into the URL in that case.
	// See the requestLine method below.
	return nil, nil
}

func (c connectUnaryServerProtocol) responseNeedsPrep(_ *operation) bool {
	return false
}

func (c connectUnaryServerProtocol) prepareUnmarshalledResponse(_ *operation, _ []byte, _ proto.Message) error {
	return errors.New("response does not need preparation")
}

func (c connectUnaryServerProtocol) requiresMessageToProvideRequestLine(op *operation) bool {
	return c.useGet(op)
}

func (c connectUnaryServerProtocol) requestLine(op *operation, msg proto.Message) (urlPath, queryParams, method string, includeBody bool, err error) {
	if !c.useGet(op) {
		return op.methodConf.methodPath, "", http.MethodPost, true, nil
	}
	vals := make(url.Values, 5)
	vals.Set("connect", "v1")

	vals.Set("encoding", op.server.codec.Name())
	buf := op.bufferPool.Get()
	stableMarshaler, _ := op.server.codec.(StableCodec) // c.useGet called above already checked this
	data, err := stableMarshaler.MarshalAppendStable(buf.Bytes(), msg)
	if err != nil {
		op.bufferPool.Put(buf)
		return "", "", "", false, err
	}
	buf = op.bufferPool.Wrap(data, buf)
	defer op.bufferPool.Put(buf)

	encoded := op.bufferPool.Get()
	defer op.bufferPool.Put(encoded)
	if op.server.reqCompression != nil {
		vals.Set("compression", op.server.reqCompression.Name())
		if err := op.server.reqCompression.compress(encoded, buf); err != nil {
			return "", "", "", false, err
		}
		// for the next step, we want encoded empty and data to be the message source
		buf, encoded = encoded, buf // swap so writing to encoded doesn't mutate data
		encoded.Reset()
		data = buf.Bytes()
	}

	var msgStr string
	if stableMarshaler.IsBinary() || op.server.reqCompression != nil {
		b64encodedLen := base64.RawURLEncoding.EncodedLen(len(data))
		vals.Set("base64", "1")
		encoded.Grow(b64encodedLen)
		encodedBytes := encoded.Bytes()[:b64encodedLen]
		base64.RawURLEncoding.Encode(encodedBytes, data)
		msgStr = string(encodedBytes)
	} else {
		msgStr = string(data)
	}

	vals.Set("message", msgStr)
	queryString := vals.Encode()
	if uint32(len(op.methodConf.methodPath)+len(queryString)+1) > op.methodConf.maxGetURLBytes {
		// URL is too big; fall back to POST
		return op.methodConf.methodPath, "", http.MethodPost, true, nil
	}
	return op.methodConf.methodPath, vals.Encode(), http.MethodGet, false, nil
}

func (c connectUnaryServerProtocol) String() string {
	return c.protocol().String() + " unary"
}

// connectStreamClientProtocol implements the Connect protocol for
// processing streaming RPCs received from the client.
type connectStreamClientProtocol struct{}

var _ clientProtocolHandler = connectStreamClientProtocol{}
var _ envelopedProtocolHandler = connectStreamClientProtocol{}

func (c connectStreamClientProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectStreamClientProtocol) acceptsStreamType(_ *operation, streamType connect.StreamType) bool {
	return streamType != connect.StreamTypeUnary
}

func (c connectStreamClientProtocol) extractProtocolRequestHeaders(_ *operation, headers http.Header) (requestMeta, error) {
	var reqMeta requestMeta
	if err := connectExtractTimeout(headers, &reqMeta); err != nil {
		return reqMeta, err
	}
	reqMeta.codec = strings.TrimPrefix(headers.Get("Content-Type"), contentConnectStreamPrefix)
	headers.Del("Content-Type")
	reqMeta.compression = headers.Get("Connect-Content-Encoding")
	headers.Del("Connect-Content-Encoding")
	reqMeta.acceptCompression = parseMultiHeader(headers.Values("Connect-Accept-Encoding"))
	headers.Del("Connect-Accept-Encoding")
	return reqMeta, nil
}

func (c connectStreamClientProtocol) addProtocolResponseHeaders(meta responseMeta, headers http.Header) int {
	headers.Set("Content-Type", contentConnectStreamPrefix+meta.codec)
	if meta.compression != "" {
		headers.Set("Connect-Content-Encoding", meta.compression)
	}
	if len(meta.acceptCompression) > 0 {
		headers.Set("Connect-Accept-Encoding", strings.Join(meta.acceptCompression, ", "))
	}
	return http.StatusOK
}

func (c connectStreamClientProtocol) encodeEnd(op *operation, end *responseEnd, writer io.Writer, _ bool) http.Header {
	streamEnd := &connectStreamEnd{Metadata: end.trailers}
	if end.err != nil {
		streamEnd.Error = connectErrorToWireError(end.err, op.methodConf.resolver)
	}
	buffer := op.bufferPool.Get()
	defer op.bufferPool.Put(buffer)
	enc := json.NewEncoder(buffer)
	if err := enc.Encode(streamEnd); err != nil {
		buffer.WriteString(`{"error": {"code": "internal", "message": ` + strconv.Quote(err.Error()) + `}}`)
	}
	// TODO: compress?
	env := envelope{trailer: true, length: uint32(buffer.Len())}
	envBytes := c.encodeEnvelope(env)
	_, _ = writer.Write(envBytes[:])
	_, _ = buffer.WriteTo(writer)
	return nil
}

func (c connectStreamClientProtocol) decodeEnvelope(envBytes envelopeBytes) (envelope, error) {
	flags := envBytes[0]
	if flags != 0 && flags != 1 {
		return envelope{}, fmt.Errorf("invalid compression flag: must be 0 or 1; instead got %d", flags)
	}
	return envelope{
		compressed: flags == 1,
		length:     binary.BigEndian.Uint32(envBytes[1:]),
	}, nil
}

func (c connectStreamClientProtocol) encodeEnvelope(env envelope) envelopeBytes {
	var envBytes envelopeBytes
	if env.compressed {
		envBytes[0] = 1
	}
	if env.trailer {
		envBytes[0] |= 2
	}
	binary.BigEndian.PutUint32(envBytes[1:], env.length)
	return envBytes
}

func (c connectStreamClientProtocol) String() string {
	return c.protocol().String() + " stream"
}

// connectStreamServerProtocol implements the Connect protocol for
// sending streaming RPCs to the server handler.
type connectStreamServerProtocol struct{}

var _ serverProtocolHandler = connectStreamServerProtocol{}
var _ serverEnvelopedProtocolHandler = connectStreamServerProtocol{}

func (c connectStreamServerProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectStreamServerProtocol) addProtocolRequestHeaders(meta requestMeta, headers http.Header) {
	headers.Set("Content-Type", contentConnectStreamPrefix+meta.codec)
	if meta.compression != "" {
		headers.Set("Connect-Content-Encoding", meta.compression)
	}
	if len(meta.acceptCompression) > 0 {
		headers.Set("Connect-Accept-Encoding", strings.Join(meta.acceptCompression, ", "))
	}
	if meta.hasTimeout {
		headers.Set("Connect-Timeout-Ms", connectEncodeTimeout(meta.timeout))
	}
}

func (c connectStreamServerProtocol) extractProtocolResponseHeaders(statusCode int, headers http.Header) (responseMeta, responseEndUnmarshaller, error) {
	var respMeta responseMeta
	contentType := headers.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, contentConnectStreamPrefix):
		respMeta.codec = strings.TrimPrefix(contentType, contentConnectStreamPrefix)
	default:
		respMeta.codec = contentType + "?"
	}
	headers.Del("Content-Type")
	respMeta.compression = headers.Get("Connect-Content-Encoding")
	headers.Del("Connect-Content-Encoding")
	respMeta.acceptCompression = parseMultiHeader(headers.Values("Connect-Accept-Encoding"))
	headers.Del("Connect-Accept-Encoding")

	// See if RPC is already over (unexpected HTTP error or trailers-only response)
	if statusCode != http.StatusOK {
		if respMeta.end == nil {
			respMeta.end = &responseEnd{}
		}
		if respMeta.end.err == nil {
			code := httpStatusCodeToRPC(statusCode)
			respMeta.end.err = connect.NewError(code, fmt.Errorf("unexpected HTTP error: %d %s", statusCode, http.StatusText(statusCode)))
		}
	}
	return respMeta, nil, nil
}

func (c connectStreamServerProtocol) extractEndFromTrailers(_ *operation, _ http.Header) (responseEnd, error) {
	return responseEnd{}, errors.New("connect streaming protocol does not use HTTP trailers")
}

func (c connectStreamServerProtocol) decodeEnvelope(envBytes envelopeBytes) (envelope, error) {
	flags := envBytes[0]
	if flags&0b1111_1100 != 0 {
		// invalid bits are set
		return envelope{}, fmt.Errorf("invalid frame flags: only lowest two bits may be set; instead got %d", flags)
	}
	return envelope{
		compressed: flags&1 != 0,
		trailer:    flags&2 != 0,
		length:     binary.BigEndian.Uint32(envBytes[1:]),
	}, nil
}

func (c connectStreamServerProtocol) encodeEnvelope(env envelope) envelopeBytes {
	var envBytes envelopeBytes
	if env.compressed {
		envBytes[0] = 1
	}
	binary.BigEndian.PutUint32(envBytes[1:], env.length)
	return envBytes
}

func (c connectStreamServerProtocol) decodeEndFromMessage(_ *operation, buffer *bytes.Buffer) (responseEnd, error) {
	var streamEnd connectStreamEnd
	if err := json.Unmarshal(buffer.Bytes(), &streamEnd); err != nil {
		return responseEnd{}, err
	}
	var cerr *connect.Error
	if streamEnd.Error != nil {
		cerr = streamEnd.Error.toConnectError()
	}
	return responseEnd{
		err:      cerr,
		trailers: streamEnd.Metadata,
	}, nil
}

func (c connectStreamServerProtocol) String() string {
	return c.protocol().String() + " stream"
}

func connectExtractUnaryTrailers(headers http.Header) http.Header {
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

func connectExtractTimeout(headers http.Header, meta *requestMeta) error {
	str := headers.Get("Connect-Timeout-Ms")
	headers.Del("Connect-Timeout-Ms")
	if str == "" {
		return nil
	}
	timeoutInt, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return err
	}
	if timeoutInt < 0 {
		return fmt.Errorf("timeout header indicated invalid negative value: %d", timeoutInt)
	}
	timeout := time.Millisecond * time.Duration(timeoutInt)
	if timeout.Milliseconds() != timeoutInt {
		// overflow
		timeout = time.Duration(math.MaxInt64)
	}
	meta.timeout = timeout
	meta.hasTimeout = true
	return nil
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
