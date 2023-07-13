// Copyright 2023 Buf Technologies, Inc.

package vanguard

// https://pkg.go.dev/github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api

import (
	"fmt"
	"strconv"

	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api"
)

var UpdateUpstreamBody = "upstream response body updated by vanguard plugin"

type filter struct {
	//api.PassThroughStreamFilter

	callbacks api.FilterCallbackHandler
	path      string
	config    *Config
}

func (f *filter) sendLocalReplyInternal() api.StatusType {
	headers := make(map[string]string)
	body := fmt.Sprintf("%s, path: %s\r\n", f.config.echoBody, f.path)
	f.callbacks.SendLocalReply(200, body, headers, -1, "test-from-go")
	return api.LocalReply
}

// Callbacks which are called in request path
func (f *filter) DecodeHeaders(header api.RequestHeaderMap, endStream bool) api.StatusType {
	f.path, _ = header.Get(":path")
	if f.path == "/localreply_by_config" {
		return f.sendLocalReplyInternal()
	}
	return api.Continue
}

//The callbacks can be implemented on demand

func (f *filter) DecodeData(buffer api.BufferInstance, endStream bool) api.StatusType {

	return api.Continue
}

func (f *filter) DecodeTrailers(trailers api.RequestTrailerMap) api.StatusType {
	return api.Continue
}

func (f *filter) EncodeHeaders(header api.ResponseHeaderMap, endStream bool) api.StatusType {
	if f.path == "/update_upstream_response" {
		header.Set("Content-Length", strconv.Itoa(len(UpdateUpstreamBody)))
	}
	header.Set("Rsp-Header-From-Go", "bar-test")
	return api.Continue
}

// Callbacks which are called in response path
func (f *filter) EncodeData(buffer api.BufferInstance, endStream bool) api.StatusType {
	if f.path == "/update_upstream_response" {
		if endStream {
			buffer.SetString(UpdateUpstreamBody)
		} else {
			// TODO implement buffer->Drain, buffer.SetString means buffer->Drain(buffer.Len())
			buffer.SetString("")
		}
	}
	return api.Continue
}

func (f *filter) EncodeTrailers(trailers api.ResponseTrailerMap) api.StatusType {
	return api.Continue
}

func (f *filter) OnDestroy(reason api.DestroyReason) {
}
