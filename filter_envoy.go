// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

// https://pkg.go.dev/github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api

import (
	"github.com/envoyproxy/envoy/contrib/golang/filters/http/source/go/pkg/api"
)

type filterEnvoy struct {
	*mux

	callbacks api.FilterCallbackHandler
}

// Callbacks which are called in request path.
func (f *filterEnvoy) DecodeHeaders(header api.RequestHeaderMap, endStream bool) api.StatusType { //nolint:revive
	return api.Continue
}

func (f *filterEnvoy) DecodeData(buffer api.BufferInstance, endStream bool) api.StatusType { //nolint:revive
	return api.Continue
}

func (f *filterEnvoy) DecodeTrailers(trailers api.RequestTrailerMap) api.StatusType { //nolint:revive
	return api.Continue
}

func (f *filterEnvoy) EncodeHeaders(header api.ResponseHeaderMap, endStream bool) api.StatusType { //nolint:revive
	header.Set("Rsp-Header-From-Go", "bar-test")
	return api.Continue
}

// Callbacks which are called in response path.
func (f *filterEnvoy) EncodeData(buffer api.BufferInstance, endStream bool) api.StatusType { //nolint:revive
	return api.Continue
}

func (f *filterEnvoy) EncodeTrailers(trailers api.ResponseTrailerMap) api.StatusType { //nolint:revive
	return api.Continue
}

func (f *filterEnvoy) OnDestroy(reason api.DestroyReason) {} //nolint:revive
