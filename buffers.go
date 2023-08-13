// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

const maxRecycledBufferSize = 8 << 20 // 8 MiB

type bufferPool struct{ sync.Pool }

func (p *bufferPool) Get() *bytes.Buffer {
	if buffer, ok := p.Pool.Get().(*bytes.Buffer); ok {
		buffer.Reset()
		return buffer
	}
	return bytes.NewBuffer(make([]byte, 0, bytes.MinRead))
}
func (p *bufferPool) Put(buffer *bytes.Buffer) {
	if buffer.Cap() < maxRecycledBufferSize {
		p.Pool.Put(buffer)
	}
}

func read(dst *bytes.Buffer, src io.Reader) (int, error) {
	dst.Grow(bytes.MinRead)
	b := dst.Bytes()
	b = b[len(b):cap(b)]
	n, err := src.Read(b)
	_, _ = dst.Write(b[:n])
	return n, err
}

func errRecvSize(limit uint32) *connect.Error {
	return connect.NewError(connect.CodeInternal,
		fmt.Errorf("received message exceeds limit: %d", limit),
	)
}

func readAll(dst *bytes.Buffer, src io.Reader, maxRecvSize uint32) error {
	var total int64
	for {
		n, err := read(dst, src)
		total += int64(n)
		if total > int64(maxRecvSize) {
			return errRecvSize(maxRecvSize)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return err
		}
	}
}

//nolint:unused
func writeAll(dst io.Writer, src *bytes.Buffer) error {
	if _, err := src.WriteTo(dst); err != nil {
		return err
	}
	return nil
}

func readEnvelope(dst *bytes.Buffer, src io.Reader, maxRecvSize uint32) error {
	dst.Grow(5)
	header := dst.Bytes()[dst.Len() : dst.Len()+5]
	if _, err := io.ReadFull(src, header); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[1:5])
	if size > maxRecvSize {
		return errRecvSize(maxRecvSize)
	}
	_, _ = dst.Write(header)
	dst.Grow(int(size))
	body := dst.Bytes()[dst.Len() : dst.Len()+int(size)]
	if _, err := io.ReadFull(src, body); err != nil {
		return err
	}
	_, _ = dst.Write(body)
	return nil
}

type envelope struct{ *bytes.Buffer }

// makeEnvelope creates an envelope with a 5-byte prefix.
func makeEnvelope(buffer *bytes.Buffer) envelope {
	var head [5]byte
	buffer.Write(head[:])
	return envelope{Buffer: buffer}
}

func (e envelope) size() int { return e.Len() - 5 }

func (e envelope) encodeSizeAndFlags(flags uint8) {
	e.Bytes()[0] = flags
	binary.BigEndian.PutUint32(e.Bytes()[1:5], uint32(e.size()))
}

// open strips the envelope prefix, returning the underlying buffer.
func (e envelope) open() *bytes.Buffer {
	e.Next(5) // skip the prefix
	return e.Buffer
}

func marshal(dst *bytes.Buffer, msg proto.Message, codec Codec) error {
	raw, err := codec.MarshalAppend(dst.Bytes(), msg)
	if err != nil {
		return err
	}
	if cap(raw) > dst.Cap() {
		// Dst buffer was too small, so MarshalAppend grew the slice.
		// Replace the buffer with the larger, newly-allocated slice.
		*dst = *bytes.NewBuffer(raw)
	} else {
		// The buffer from the pool was large enough, MarshalAppend didn't allocate.
		// Copy to the same byte slice is a nop.
		dst.Write(raw[dst.Len():])
	}
	return nil
}

func unmarshal(src *bytes.Buffer, msg proto.Message, codec Codec) error {
	return codec.Unmarshal(src.Bytes(), msg)
}

func compress(dst *bytes.Buffer, src *bytes.Buffer, compressor connect.Compressor) error {
	compressor.Reset(dst)
	defer compressor.Close()
	_, err := src.WriteTo(compressor)
	return err
}

func decompress(dst *bytes.Buffer, src *bytes.Buffer, decompressor connect.Decompressor) error {
	if err := decompressor.Reset(src); err != nil {
		return err
	}
	defer decompressor.Close()
	_, err := dst.ReadFrom(decompressor)
	return err
}
