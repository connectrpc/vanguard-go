// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"compress/gzip"
	"io"
	"sync"

	"connectrpc.com/connect"
)

// DefaultGzipCompressor is a factory for Compressor instances used by default
// for the "gzip" encoding type.
func DefaultGzipCompressor() connect.Compressor {
	return &gzip.Writer{}
}

// DefaultGzipDecompressor is a factory for Decompressor instances used by
// default for the "gzip" encoding type.
func DefaultGzipDecompressor() connect.Decompressor {
	return &gzip.Reader{}
}

type compressionPool struct {
	name          string
	decompressors sync.Pool
	compressors   sync.Pool
}

func newCompressionPool(
	name string,
	newCompressor func() connect.Compressor,
	newDecompressor func() connect.Decompressor,
) *compressionPool {
	return &compressionPool{
		name: name,
		compressors: sync.Pool{
			New: func() any { return newCompressor() },
		},
		decompressors: sync.Pool{
			New: func() any { return newDecompressor() },
		},
	}
}

func (p *compressionPool) Name() string {
	if p == nil {
		return ""
	}
	return p.name
}

func (p *compressionPool) compress(dst, src *bytes.Buffer) error {
	if p == nil {
		_, err := io.Copy(dst, src)
		return err
	}
	comp := p.compressors.Get().(connect.Compressor) //nolint:forcetypeassert,errcheck
	defer p.compressors.Put(comp)

	comp.Reset(dst)
	_, err := src.WriteTo(comp)
	if err != nil {
		return err
	}
	return comp.Close()
}

func (p *compressionPool) decompress(dst, src *bytes.Buffer) error {
	if p == nil {
		_, err := io.Copy(dst, src)
		return err
	}
	decomp := p.decompressors.Get().(connect.Decompressor) //nolint:forcetypeassert,errcheck
	defer p.decompressors.Put(decomp)

	if err := decomp.Reset(src); err != nil {
		return err
	}
	if _, err := dst.ReadFrom(decomp); err != nil {
		return err
	}
	return decomp.Close()
}
