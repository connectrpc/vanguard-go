// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Protocol string

const (
	ProtocolGRPC    Protocol = "grpc"
	ProtocolGRPCWeb Protocol = "grpc-web"
	ProtocolHTTP    Protocol = "http"
	ProtocolConnect Protocol = "connect"
)

type protocol int

const (
	protocolUnknown       protocol = iota
	protocolGRPC                   // https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md
	protocolGRPCWeb                // https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-WEB.md
	protocolConnectUnary           // https://connect.build/docs/protocol/#unary-request-response-rpcs
	protocolConnectStream          // https://connect.build/docs/protocol/#streaming-rpcs
	protocolHTTP                   // https://github.com/googleapis/googleapis/blob/master/google/api/http.proto
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
	case protocolHTTP:
		return "http-rule"
	default:
		return "unknown"
	}
}

func todoErrorWriter(w io.Writer, hdr responseHeader, err error) {
	panic(err)
}

func classifyProtocol(header header) (protocol, errorWriter) {
	contentType, _ := header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/grpc-web"):
		return protocolGRPCWeb, todoErrorWriter
	case strings.HasPrefix(contentType, "application/grpc"):
		return protocolGRPC, grpcErrorWriter
	case strings.HasPrefix(contentType, "application/connect"):
		return protocolConnectStream, todoErrorWriter
	default:
		if _, ok := header.Get("Connect-Protocol-Version"); ok {
			return protocolConnectUnary, todoErrorWriter
		}
		return protocolHTTP, newHTTPErrorWriter(contentType)
	}
}

type protocaller interface {
	Protocol() protocol
	EncodeError(io.Writer, responseHeader, error)
}

type header interface {
	Get(key string) (string, bool)
	Values(key string) []string
	Set(key, value string)
	Add(key, value string)
	Del(key string)
	Range(f func(key, value string) bool)
}
type requestHeader interface {
	header
	Method() string
	SetMethod(method string)
	URL() *url.URL
	Proto() (proto string, major, minor int)
	SetProto(proto string, major, minor int)
}
type responseHeader interface {
	header
	WriteStatus(statusCode int)
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

type trailerMap map[string][]string

func (h trailerMap) isDeclaredTrailer(key string) bool {
	for _, v := range h["Trailer"] {
		if v == key { // strings.EqualFold(v, key) {
			return true
		}
	}
	return false
}
func (h trailerMap) toKey(key string) string {
	if strings.HasPrefix(key, http.TrailerPrefix) {
		return key
	}
	if h.isDeclaredTrailer(key) {
		return key
	}
	return http.TrailerPrefix + key
}

func (h trailerMap) Get(key string) (string, bool) {
	key = h.toKey(key)
	if header := h[key]; len(header) > 0 {
		return header[0], true
	}
	return "", false
}
func (h trailerMap) Values(key string) []string {
	key = h.toKey(key)
	return h[key]
}
func (h trailerMap) Set(key, value string) {
	key = h.toKey(key)
	h[key] = []string{value}
}
func (h trailerMap) Add(key, value string) {
	key = h.toKey(key)
	h[key] = append(h[key], value)
}
func (h trailerMap) Del(key string) {
	key = h.toKey(key)
	delete(h, key)
}
func (h trailerMap) Range(f func(key, value string) bool) {
	declaredTrailers := make(map[string]bool, len(h["Trailer"]))
	for _, key := range h["Trailer"] {
		declaredTrailers[key] = true
	}
	for key, values := range h {
		if strings.HasPrefix(key, http.TrailerPrefix) {
			key = key[len(http.TrailerPrefix):]
		} else if !declaredTrailers[key] {
			continue
		}
		for _, value := range values {
			if !f(key, value) {
				return
			}
		}
	}
}

type requestHeaderHTTP struct {
	header

	req *http.Request
}

func makeRequestHeaderHTTP(req *http.Request) requestHeaderHTTP {
	return requestHeaderHTTP{
		header: headerMap(req.Header),
		req:    req,
	}
}

func (h requestHeaderHTTP) Method() string          { return h.req.Method }
func (h requestHeaderHTTP) SetMethod(method string) { h.req.Method = method }
func (h requestHeaderHTTP) URL() *url.URL           { return h.req.URL }
func (h requestHeaderHTTP) Proto() (string, int, int) {
	return h.req.Proto, h.req.ProtoMajor, h.req.ProtoMinor
}
func (h requestHeaderHTTP) SetProto(proto string, major, minor int) {
	h.req.Proto, h.req.ProtoMajor, h.req.ProtoMinor = proto, major, minor
}

type responseHeaderHTTP struct {
	header

	rsp http.ResponseWriter
}

func makeResponseHeaderHTTP(rsp http.ResponseWriter) responseHeaderHTTP {
	return responseHeaderHTTP{
		header: headerMap(rsp.Header()),
		rsp:    rsp,
	}
}

func (h responseHeaderHTTP) WriteStatus(statusCode int) {
	h.rsp.WriteHeader(statusCode)
}
