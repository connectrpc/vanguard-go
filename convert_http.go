// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"io"
)

type convertHTTPToGRPC struct {
	*Mux
}

func (c convertHTTPToGRPC) Upstream() protocol {
	return protocolHTTPRule
}
func (c convertHTTPToGRPC) Downstream() protocol {
	return protocolGRPC
}

func (c convertHTTPToGRPC) DecodeHeader(hdr requestHeader) error {
	// TODO: decode header
	hdr.Set("Content-Type", "application/grpc+proto")

	return nil
}
func (c convertHTTPToGRPC) EncodeHeader(rsp io.Writer, hdr responseHeader) error {
	fmt.Println("encode header")
	return nil
}
func (c convertHTTPToGRPC) EncodeTrailer(rsp io.Writer, hdr responseHeader) error {
	return nil
}
