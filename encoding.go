// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"compress/gzip"
	"io"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type codec interface {
	MarshalAppend([]byte, proto.Message) ([]byte, error)
	Unmarshal([]byte, proto.Message) error
	Name() string
}

// codecProto is a Codec implementation with protobuf binary format.
type codecProto struct {
	proto.MarshalOptions
}

func (c codecProto) MarshalAppend(b []byte, m proto.Message) ([]byte, error) {
	return c.MarshalOptions.MarshalAppend(b, m)
}

func (codecProto) Unmarshal(data []byte, m proto.Message) error {
	return proto.Unmarshal(data, m)
}

func (codecProto) Name() string { return "proto" }

// codecJSON is a Codec implementation with protobuf json format.
type codecJSON struct {
	protojson.MarshalOptions
	protojson.UnmarshalOptions
}

func (c codecJSON) MarshalAppend(b []byte, m proto.Message) ([]byte, error) {
	return c.MarshalOptions.MarshalAppend(b, m)
}

func (c codecJSON) Unmarshal(data []byte, m proto.Message) error {
	return c.UnmarshalOptions.Unmarshal(data, m)
}

func (codecJSON) Name() string { return "json" }

type compressor interface {
	Compress(w io.Writer) (io.WriteCloser, error)
	Decompress(r io.Reader) (io.Reader, error)
	Name() string
}

// compressorGzip implements the Compressor interface.
// Based on grpc/encoding/gzip.
type compressorGzip struct {
	Level            *int
	poolCompressor   sync.Pool
	poolDecompressor sync.Pool
}

// Name returns gzip.
func (*compressorGzip) Name() string { return "gzip" }

type gzipWriter struct {
	*gzip.Writer
	pool *sync.Pool
}

func (c *compressorGzip) Compress(w io.Writer) (io.WriteCloser, error) {
	z, ok := c.poolCompressor.Get().(*gzipWriter)
	if !ok {
		level := gzip.DefaultCompression
		if c.Level != nil {
			level = *c.Level
		}
		newZ, err := gzip.NewWriterLevel(w, level)
		if err != nil {
			return nil, err
		}
		return &gzipWriter{Writer: newZ, pool: &c.poolCompressor}, nil
	}
	z.Reset(w)
	return z, nil
}

func (z *gzipWriter) Close() error {
	defer z.pool.Put(z)
	return z.Writer.Close()
}

type gzipReader struct {
	*gzip.Reader
	pool *sync.Pool
}

func (c *compressorGzip) Decompress(r io.Reader) (io.Reader, error) {
	z, ok := c.poolDecompressor.Get().(*gzipReader)
	if !ok {
		newZ, err := gzip.NewReader(r)
		if err != nil {
			return nil, err
		}
		return &gzipReader{Reader: newZ, pool: &c.poolDecompressor}, nil
	}
	if err := z.Reset(r); err != nil {
		z.pool.Put(z)
		return nil, err
	}
	return z, nil
}

func (z *gzipReader) Read(p []byte) (n int, err error) {
	n, err = z.Reader.Read(p)
	if err == io.EOF {
		z.pool.Put(z)
	}
	return n, err
}

type compressCodec struct {
	codec      codec
	compressor compressor
	buffer     *bytes.Buffer
}

func (c compressCodec) Unmarshal(b []byte, data proto.Message) error {
	if c.compressor != nil {
		c.buffer.Reset()
		src := bytes.NewReader(b)
		rc, err := c.compressor.Decompress(src)
		if err != nil {
			return err
		}
		if _, err := c.buffer.ReadFrom(rc); err != nil {
			return err
		}
		b = c.buffer.Bytes()
	}
	return c.codec.Unmarshal(b, data)
}
func (c compressCodec) MarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	b, err := c.codec.MarshalAppend(b, msg)
	if err != nil {
		return nil, err
	}
	if c.compressor != nil {
		c.buffer.Reset()
		wc, err := c.compressor.Compress(c.buffer)
		if err != nil {
			return nil, err
		}
		if _, err := wc.Write(b); err != nil {
			return nil, err
		}
		if wc.Close(); err != nil {
			return nil, err
		}
		b = append(b, c.buffer.Bytes()...)
	}
	return b, nil
}
func (c compressCodec) Name() string {
	return c.codec.Name()
}
