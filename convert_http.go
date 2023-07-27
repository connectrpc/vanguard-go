// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"io"
	"net/http"
)

type convertHTTPToGRPC struct {
	*Mux
}

var _ converter = convertHTTPToGRPC{}

func (c convertHTTPToGRPC) DecodeHeader(hdr requestHeader) error {
	hdr.Set("Content-Type", "application/grpc+proto")
	if val, ok := hdr.Get("Encoding"); ok {
		hdr.Del("Encoding")
		hdr.Set("Grpc-Encoding", val)
	}
	if val, ok := hdr.Get("Accept-Encoding"); ok {
		hdr.Del("Accept-Encoding")
		hdr.Set("Grpc-Accept-Encoding", val) // TODO: translate?
	}

	hdr.SetProto("HTTP/2", 2, 0)
	hdr.SetMethod(http.MethodPost)
	return nil
}
func (c convertHTTPToGRPC) EncodeHeader(_ io.Writer, hdr responseHeader) error {
	if val, ok := hdr.Get("Grpc-Encoding"); ok {
		hdr.Del("Grpc-Encoding")
		hdr.Set("Content-Encoding", val)
	}
	hdr.Del("Grpc-Message-Type")

	if val, ok := hdr.Get("Grpc-Status"); ok && val != "0" {
		return grpcErrorFromTrailer(hdr)
	}
	// Content-Type set by filter.
	hdr.Del("Content-Type")
	return nil
}
func (c convertHTTPToGRPC) EncodeTrailer(_ io.Writer, hdr responseHeader) error {
	cerr := grpcErrorFromTrailer(hdr)
	hdr.Del("Grpc-Status")
	hdr.Del("Grpc-Message")
	hdr.Del("Grpc-Status-Details-Bin")
	if cerr != nil {
		return cerr
	}
	return nil
}
