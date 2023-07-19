// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/protobuf/proto"
)

type protocol int

const (
	protocolUnknown       protocol = iota
	protocolGRPC                   // https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md
	protocolGRPCWeb                // https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-WEB.md
	protocolConnectUnary           // https://connect.build/docs/protocol/#unary-request-response-rpcs
	protocolConnectStream          // https://connect.build/docs/protocol/#streaming-rpcs
	protocolHTTPRule               // https://github.com/googleapis/googleapis/blob/master/google/api/http.proto
)

func (p protocol) String() string {
	switch p {
	case protocolGRPC:
		return "grpc"
	case protocolGRPCWeb:
		return "grpc-web"
	case protocolConnectUnary:
		return "connect-unary"
	case protocolConnectStream:
		return "connect-stream"
	case protocolHTTPRule:
		return "http-rule"
	default:
		return "unknown"
	}
}

func classifyProtocol(header header) protocol {
	contentType, _ := header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/grpc-web"):
		return protocolGRPCWeb
	case strings.HasPrefix(contentType, "application/grpc"):
		return protocolGRPC
	case strings.HasPrefix(contentType, "application/connect"):
		return protocolConnectStream
	default:
		if _, ok := header.Get("Connect-Protocol-Version"); ok {
			return protocolConnectUnary
		}
		return protocolHTTPRule
		//default:
		//	return protocolUnknown
	}
}

type header interface {
	Get(key string) (string, bool)
	Values(key string) []string
	Set(key, value string)
	Add(key, value string)
	Del(key string)
	Range(f func(key, value string) bool)
}

type headerMap map[string][]string

func (h headerMap) Get(key string) (string, bool) {
	if header := h[key]; len(header) > 0 {
		return header[0], true
	}
	return "", false
}
func (h headerMap) Values(key string) []string {
	return h[key]
}
func (h headerMap) Set(key, value string) {
	h[key] = []string{value}
}
func (h headerMap) Add(key, value string) {
	h[key] = append(h[key], value)
}
func (h headerMap) Del(key string) {
	delete(h, key)
}
func (h headerMap) Range(f func(key, value string) bool) {
	for key, values := range h {
		for _, value := range values {
			if !f(key, value) {
				return
			}
		}
	}
}

type statusError struct {
	code int
	err  error
}

func statusErrorf(code int, msg string, args ...any) statusError {
	return statusError{
		code: code,
		err:  fmt.Errorf(msg, args...),
	}
}

func (s statusError) Error() string {
	return s.err.Error()
}
func (s statusError) Code() int {
	return s.code
}
func asStatusError(err error) statusError {
	var statusError statusError
	if !errors.As(err, &statusError) {
		statusError.code = http.StatusInternalServerError
		statusError.err = err
	}
	return statusError
}
func errUnsupportedProtocol(p protocol) statusError {
	return statusError{
		code: http.StatusUnsupportedMediaType,
		err:  fmt.Errorf("unsupported protocol: %s", p),
	}
}
func errDecompressorNotFound() statusError {
	return statusError{
		code: http.StatusInternalServerError,
		err:  errors.New("missing decompressor"),
	}
}

func grpcStatusCodeToHTTP(c int) int {
	var codes = [...]int{
		http.StatusOK,                  // 0
		http.StatusRequestTimeout,      // 1
		http.StatusInternalServerError, // 2
		http.StatusBadRequest,          // 3
		http.StatusGatewayTimeout,      // 4
		http.StatusNotFound,            // 5
		http.StatusConflict,            // 6
		http.StatusForbidden,           // 7
		http.StatusTooManyRequests,     // 8
		http.StatusBadRequest,          // 9
		http.StatusConflict,            // 10
		http.StatusBadRequest,          // 11
		http.StatusNotImplemented,      // 12
		http.StatusInternalServerError, // 13
		http.StatusServiceUnavailable,  // 14
		http.StatusInternalServerError, // 15
		http.StatusUnauthorized,        // 16
	}
	if int(c) > len(codes) {
		return http.StatusInternalServerError
	}
	return codes[c]
}
func grpcErrorf(code int, msg string, args ...any) error {
	return statusErrorf(grpcStatusCodeToHTTP(code), msg, args...)
}

type converter struct {
	src protocol
	dst protocol
}

// src -> dst
func decodeHeader(src, dst protocol, header header) {
	x := converter{src: src, dst: dst}
	switch x {
	case converter{protocolGRPCWeb, protocolGRPC}:
		decodeHeaderGRPCWebToGRPC(header)
	case converter{protocolHTTPRule, protocolGRPC}:
		decodeHeaderHTTPRuleToGRPC(header)
	}
}

func decodeHeaderGRPCWebToGRPC(header header) {
	//
}

func decodeHeaderHTTPRuleToGRPC(header header) {
	header.Set("Content-Type", "application/grpc+proto")
	// TODO: decode header
}

// src <- dst
func encodeHeader(src, dst protocol, header header) {
	x := converter{src: src, dst: dst}
	switch x {
	case converter{protocolGRPCWeb, protocolGRPC}:
	case converter{protocolHTTPRule, protocolGRPC}:
	}
}

// src <- dst
func encodeTrailer(src, dst protocol, header header) {
	x := converter{src: src, dst: dst}
	switch x {
	case converter{protocolGRPCWeb, protocolGRPC}:
	case converter{protocolHTTPRule, protocolGRPC}:
	}
}

// A stream is one half of a full stream.
// Upstream decodes the recv message and encodes the send message.
// Downstream encodes the recv message and decodes the send message.
//
//	|  upstream | filter | downstream |
//	|-----------|--------|------------|
//	|    dec->  |  recv  |    enc->   |
//	|  <-enc    |  send  |  <-dec     |
type stream interface {
	DecodeHeader(header) error
	Decode([]byte, proto.Message) error // bytes -> msg
	DecodeTrailer(header) error
	EncodeHeader(header) error
	Encode([]byte, proto.Message) ([]byte, error) // msg -> bytes
	EncodeTrailer(header) error
	// EncodeStatus(buffer, error) error
}
type msgFunc func(proto.Message) error

type upstream interface {
	DecodeHeader(buffer buffer, header header) error
	DecodeMessage(buffer buffer, msg proto.Message) error
	EncodeHeader(buffer buffer, header header) error
	EncodeMessage(buffer buffer, msg proto.Message) error
	EncodeTrailer(buffer buffer, header header) error
	EncodeStatus(buffer buffer, header header, err error) error
}
type downstream interface {
	EncodeHeader(buffer buffer, header header) error
	EncodeMessage(buffer buffer, msg proto.Message) error
	DecodeHeader(buffer buffer, header header) error
	DecodeMessage(buffer buffer, msg proto.Message) error
	DecodeTrailer(buffer buffer, header header) error
}

func process(src, dst stream, msg proto.Message, onMsg msgFunc, b []byte) ([]byte, error) {
	if err := src.Decode(b, msg); err != nil {
		// TODO: errContinue?
		return nil, err
	}
	if onMsg != nil {
		if err := onMsg(msg); err != nil {
			return nil, err
		}
	}
	return dst.Encode(b[:0], msg)
}

type chunkstreamer struct {
	up    stream
	down  stream
	args  proto.Message
	reply proto.Message
	onMsg msgFunc
}

func (c *chunkstreamer) Decode(b []byte) ([]byte, error) {
	return process(c.up, c.down, c.args, c.onMsg, b)
}
func (c *chunkstreamer) Encode(b []byte) ([]byte, error) {
	return process(c.down, c.up, c.reply, c.onMsg, b)
}

type downstreamGRPC struct {
	*Mux

	codec   codec
	encComp compressor // recv
	decComp compressor // send
	isWeb   bool
}

func (d *downstreamGRPC) EncodeHeader(header header) error {
	contentType, ok := header.Get("Content-Type")
	if !ok {
		// Default to JSON
		contentType = "application/json"
		header.Set("Content-Type", contentType)
	}
	_, codecName, ok := strings.Cut(contentType, "+")
	if !ok {
		codecName = "proto"
	}
	codec, err := d.getCodec(codecName)
	if err != nil {
		return err
	}
	d.codec = codec

	header.Set("Grpc-Accept-Encoding", "identity")

	// TODO: encoding...
	return nil
}
func (d *downstreamGRPC) Encode(b []byte, msg proto.Message) ([]byte, error) { // msg -> bytes
	b = append(b, 0, 0, 0, 0, 0)
	b, err := d.codec.MarshalAppend(b, msg)
	if err != nil {
		return nil, err
	}
	// TODO minCompressSize
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
func (d *downstreamGRPC) DecodeHeader(header) error {

	return nil
}
func (d *downstreamGRPC) Decode(b []byte, msg proto.Message) (err error) {
	// TODO: flags..
	isCompressed := b[0]&0x01 == 1
	if isCompressed {
		comp := d.decComp
		if comp == nil {
			return errDecompressorNotFound()
		}
		b, err = d.decompress(b[5:], comp)
		if err != nil {
			return err
		}
	} else {
		b = b[5:]
	}
	return d.codec.Unmarshal(b, msg)
}
func (d *downstreamGRPC) EncodeTrailer(header) error {
	return nil
}
func (d *downstreamGRPC) DecodeTrailer(header) error {
	return nil
}

type upstreamGRPC struct {
	*Mux

	codec   codec
	decComp compressor
	encComp compressor
	isWeb   bool
}

func (u *upstreamGRPC) DecodeHeader(header) error { return nil }
func (u *upstreamGRPC) Decode([]byte, proto.Message) error { // bytes -> msg
	return nil
}
func (u *upstreamGRPC) DecodeTrailer(header) error {
	return nil
}
func (u *upstreamGRPC) EncodeHeader(header) error {
	return nil
}
func (u *upstreamGRPC) Encode([]byte, proto.Message) ([]byte, error) { // msg -> bytes
	return nil, nil
}
func (u *upstreamGRPC) EncodeTrailer(header) error {
	return nil
}

type upstreamHTTPRule struct {
	*Mux

	codec   codec
	decComp compressor
	encComp compressor
	params  params
	method  *method
	recv    int32
	send    int32
}

func (u *upstreamHTTPRule) DecodeHeader(header header) error {
	contentType, ok := header.Get("Content-Type")
	if !ok {
		// Default to JSON
		contentType = "application/json"
		header.Set("Content-Type", contentType)
	}
	codec, err := u.getCodec(strings.TrimPrefix(contentType, "application/"))
	if err != nil {
		return err
	}
	u.codec = codec

	encoding, ok := header.Get("Content-Encoding")
	if ok && encoding != "identity" {
		comp, err := u.getCompressor(encoding)
		if err != nil {
			return err
		}
		u.decComp = comp
	}
	return nil
}
func (u *upstreamHTTPRule) Decode(b []byte, msg proto.Message) (err error) {
	if comp := u.decComp; comp != nil {
		b, err = u.decompress(b, comp)
		if err != nil {
			return err
		}
	}
	if len(b) > 0 {
		if err := u.codec.Unmarshal(b, msg); err != nil {
			return err
		}
	}
	if u.recv == 0 {
		if err := u.params.set(msg); err != nil {
			return err
		}
	}
	u.recv++
	return nil
}
func (u *upstreamHTTPRule) EncodeHeader(header header) error {
	encoding, _ := header.Get("Content-Encoding")
	if len(encoding) > 0 && encoding != "identity" {
		comp, err := u.getCompressor(encoding)
		if err != nil {
			return err
		}
		u.encComp = comp
	}
	return nil
}
func (u *upstreamHTTPRule) Encode(b []byte, msg proto.Message) ([]byte, error) {
	b, err := u.codec.MarshalAppend(b, msg)
	if err != nil {
		return nil, err
	}
	if comp := u.encComp; comp != nil {
		b, err = u.compress(b, comp)
		if err != nil {
			return nil, err
		}
	}
	u.send++
	return b, nil
}
func (u *upstreamHTTPRule) EncodeTrailer(header) error {
	// TODO: clear map?
	return nil
}
func (u *upstreamHTTPRule) DecodeTrailer(header) error {
	// TODO: clear map?
	return nil
}
