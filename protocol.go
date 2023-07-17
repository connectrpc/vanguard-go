// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type protocol int

const (
	protocolUnknown protocol = iota
	protocolGRPC
	protocolGRPCWeb
	protocolConnectUnary
	protocolConnectStream
	protocolHTTPRule
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
	contentType, ok := header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/grpc-web"):
		return protocolGRPCWeb
	case strings.HasPrefix(contentType, "application/grpc"):
		return protocolGRPC
	case strings.HasPrefix(contentType, "application/connect"):
		return protocolConnectStream
	case ok:
		if _, ok := header.Get("Connect-Protocol-Version"); ok {
			return protocolConnectUnary
		}
		return protocolHTTPRule
	default:
		return protocolUnknown
	}
}

func (p protocol) getCodecName(header header) string {
	contentType, _ := header.Get("Content-Type")
	switch p {
	case protocolGRPC, protocolGRPCWeb, protocolConnectStream:
		_, name, ok := strings.Cut(contentType, "+")
		if !ok {
			return "proto"
		}
		return name
	case protocolConnectUnary, protocolHTTPRule:
		return strings.TrimPrefix(contentType, "application/")
	default:
		return ""
	}
}
func (p protocol) getCompressorName(header header) string {
	switch p {
	case protocolGRPC, protocolGRPCWeb:
		encoding, _ := header.Get("Grpc-Encoding")
		return encoding
	case protocolConnectStream:
		encoding, _ := header.Get("Connect-Content-Encoding")
		return encoding
	case protocolConnectUnary, protocolHTTPRule:
		encoding, _ := header.Get("Content-Encoding")
		return encoding
	default:
		return ""
	}
}

type streamType struct {
	codec      codec
	compressor compressor
	protocol   protocol
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
	//
}
