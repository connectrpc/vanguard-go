// Copyright 2023 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
	return gzip.NewWriter(io.Discard)
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
	if src.Len() == 0 {
		return nil
	}
	comp, _ := p.compressors.Get().(connect.Compressor)
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
	if src.Len() == 0 {
		return nil
	}
	decomp, _ := p.decompressors.Get().(connect.Decompressor)
	defer p.decompressors.Put(decomp)

	if err := decomp.Reset(src); err != nil {
		return err
	}
	if _, err := dst.ReadFrom(decomp); err != nil {
		return err
	}
	return decomp.Close()
}
