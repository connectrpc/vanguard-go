// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

//nolint:forbidigo,revive,gocritic // this is temporary, will be removed when implementation is complete
package vanguard

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
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
	protocolNameConnectUnary     = protocolNameConnect + " unary"
	protocolNameConnectUnaryGet  = protocolNameConnectUnary + " (GET)"
	protocolNameConnectUnaryPost = protocolNameConnectUnary + " (POST)"
	protocolNameConnectStream    = protocolNameConnect + " stream"
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
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) encodeEnd(op *operation, end *responseEnd, writer io.Writer, wasInHeaders bool) http.Header {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) requestNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) prepareUnmarshalledRequest(op *operation, src []byte, target proto.Message) error {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) responseNeedsPrep(o *operation) bool {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) prepareMarshalledResponse(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectUnaryGetClientProtocol) String() string {
	return protocolNameConnectUnaryGet
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
	reqMeta.codec = strings.TrimPrefix(headers.Get("Content-Type"), "application/")
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
		headers.Set("Content-Type", "application/json") // error bodies are always in JSON
		// TODO: Content-Encoding to compress error?
	} else {
		headers.Set("Content-Type", "application/"+meta.codec)
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
	wireErr := connectErrorToWireError(end.err, op.resolver)
	data, err := json.Marshal(wireErr)
	if err != nil {
		data = ([]byte)(`{"code": "internal", "message": ` + strconv.Quote(err.Error()) + `}`)
	}
	_, _ = writer.Write(data)
	return nil
}

func (c connectUnaryPostClientProtocol) String() string {
	return protocolNameConnectUnaryPost
}

// connectUnaryServerProtocol implements the Connect protocol for
// sending unary RPCs to the server handler.
type connectUnaryServerProtocol struct{}

// NB: the latter two interfaces must be implemented to handle GET requests.
var _ serverProtocolHandler = connectUnaryServerProtocol{}
var _ requestLineBuilder = connectUnaryServerProtocol{}
var _ serverBodyPreparer = connectUnaryServerProtocol{}

func (c connectUnaryServerProtocol) protocol() Protocol {
	return ProtocolConnect
}

func (c connectUnaryServerProtocol) addProtocolRequestHeaders(meta requestMeta, headers http.Header) {
	headers.Set("Content-Type", "application/"+meta.codec)
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

func (c connectUnaryServerProtocol) extractProtocolResponseHeaders(statusCode int, headers http.Header) (responseMeta, responseEndUnmarshaler, error) {
	var respMeta responseMeta
	contentType := headers.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/"):
		respMeta.codec = strings.TrimPrefix(contentType, "application/")
	default:
		respMeta.codec = contentType + "?"
	}
	headers.Del("Content-Type")
	respMeta.compression = headers.Get("Content-Encoding")
	headers.Del("Content-Encoding")
	respMeta.acceptCompression = parseMultiHeader(headers.Values("Accept-Encoding"))
	headers.Del("Accept-Encoding")
	trailers := connectExtractUnaryTrailers(headers)

	var endUnmarshaler responseEndUnmarshaler
	if statusCode == http.StatusOK {
		respMeta.pendingTrailers = trailers
	} else {
		// Content-Type must be application/json for errors or else it's invalid
		if contentType != "application/json" {
			respMeta.codec = contentType + "?"
		} else {
			respMeta.codec = ""
		}
		respMeta.end = &responseEnd{
			wasCompressed: respMeta.compression != "",
			trailers:      trailers,
		}
		endUnmarshaler = func(_ Codec, r io.Reader, end *responseEnd) {
			// TODO: buffer size limit; use op.bufferPool
			data, err := io.ReadAll(r)
			if err != nil {
				end.err = connect.NewError(connect.CodeInternal, err)
				return
			}
			var wireErr connectWireError
			if err := json.Unmarshal(data, &wireErr); err != nil {
				end.err = connect.NewError(connect.CodeInternal, err)
				return
			}
			end.err = wireErr.toConnectError()
		}
	}
	return respMeta, endUnmarshaler, nil
}

func (c connectUnaryServerProtocol) extractEndFromTrailers(_ *operation, _ http.Header) (responseEnd, error) {
	return responseEnd{}, nil
}

func (c connectUnaryServerProtocol) requestNeedsPrep(op *operation) bool {
	// TODO: must return true if using GET
	return false
}

func (c connectUnaryServerProtocol) prepareMarshalledRequest(op *operation, base []byte, src proto.Message, headers http.Header) ([]byte, error) {
	// NB: This would be called when requestNeedsPrep returns true, for GET requests.
	//     In that case, there is no request body, so we can nil result.
	return nil, nil
}

func (c connectUnaryServerProtocol) responseNeedsPrep(op *operation) bool {
	return false
}

func (c connectUnaryServerProtocol) prepareUnmarshalledResponse(_ *operation, _ []byte, _ proto.Message) error {
	return errors.New("response does not need preparation")
}

func (c connectUnaryServerProtocol) requiresMessageToProvideRequestLine(o *operation) bool {
	// TODO: must return true if using GET
	return false
}

func (c connectUnaryServerProtocol) requestLine(op *operation, _ proto.Message) (urlPath, queryParams, method string, includeBody bool, err error) {
	// TODO: support GET requests, too
	return op.methodPath, "", http.MethodPost, true, nil
}

func (c connectUnaryServerProtocol) String() string {
	return protocolNameConnectUnary
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
	reqMeta.codec = strings.TrimPrefix(headers.Get("Content-Type"), "application/connect+")
	headers.Del("Content-Type")
	reqMeta.compression = headers.Get("Connect-Content-Encoding")
	headers.Del("Connect-Content-Encoding")
	reqMeta.acceptCompression = parseMultiHeader(headers.Values("Connect-Accept-Encoding"))
	headers.Del("Connect-Accept-Encoding")
	return reqMeta, nil
}

func (c connectStreamClientProtocol) addProtocolResponseHeaders(meta responseMeta, headers http.Header) int {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) encodeEnd(op *operation, end *responseEnd, writer io.Writer, wasInHeaders bool) http.Header {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) decodeEnvelope(bytes envelopeBytes) (envelope, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) encodeEnvelope(e envelope) envelopeBytes {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamClientProtocol) String() string {
	return protocolNameConnectStream
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
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) extractProtocolResponseHeaders(statusCode int, headers http.Header) (responseMeta, responseEndUnmarshaler, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) extractEndFromTrailers(o *operation, headers http.Header) (responseEnd, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) decodeEnvelope(bytes envelopeBytes) (envelope, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) encodeEnvelope(e envelope) envelopeBytes {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) decodeEndFromMessage(op *operation, reader io.Reader) (responseEnd, error) {
	//TODO implement me
	panic("implement me")
}

func (c connectStreamServerProtocol) String() string {
	return protocolNameConnectStream
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

func (err *connectWireError) toConnectError() *connect.Error {
	cerr := connect.NewError(err.Code, errors.New(err.Message))
	for _, detail := range err.Details {
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
