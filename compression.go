// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"compress/gzip"
	"io"

	"connectrpc.com/connect"
)

// DefaultGzipCompressor is a factory for Compressor instances used by default
// for the "gzip" encoding type.
func DefaultGzipCompressor() connect.Compressor {
	return gzip.NewWriter(io.Discard)
}

// DefaultGzipDecompressor is a factory for Decompressor instances used by
// default for the "gzip" encoding type.
func DefaultGzipDecompressor() connect.Decompressor {
	return &gzip.Reader{}
}
