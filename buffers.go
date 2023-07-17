// Copyright 2021-2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

var (
	errRecvSize = fmt.Errorf("received message exceeds maximum size")
)

type bufferPool struct{ sync.Pool }

func (p *bufferPool) Get() *bytes.Buffer {
	if buffer, ok := p.Pool.Get().(*bytes.Buffer); ok {
		buffer.Reset()
		return buffer
	}
	return bytes.NewBuffer(make([]byte, 0, bytes.MinRead))
}
func (p *bufferPool) Put(buffer *bytes.Buffer) {
	p.Pool.Put(buffer)
}

type bufferReader struct {
	data []byte
	err  error
	next func() ([]byte, error)
	//msg proto.Message
	//rCodec codec
	//wCodec codec

}

func (r *bufferReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.data != nil {
		r.data, r.err = r.next()
		if r.err != nil {
			return 0, r.err
		}
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		r.data = nil
	}
	return n, nil

	//if err := r.rCodec.Unmarshal(data, r.msg); err != nil {
	//	r.rerr = err
	//	return 0, r.err
	//}

	//if err := r.wCodec.MarshalAppend(p, r.msg); err != nil {
	// read next
	// unmarshal
	// apply filter
	// marshal to protocol
	// return
	//return 0, nil
}

func readAll(dst *bytes.Buffer, r io.Reader, maxRecvSize uint32) error {
	for {
		dst.Grow(bytes.MinRead)
		b := dst.Bytes()
		b = b[len(b):cap(b)]
		n, err := r.Read(b)
		_, _ = dst.Write(b[:n])
		if uint32(n) > maxRecvSize {
			return errRecvSize
		}
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return err
		}
	}
}

func readEnvelope(dst *bytes.Buffer, r io.Reader, maxRecvSize uint32) (uint8, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, err
	}

	size := binary.BigEndian.Uint32(header[1:5])
	if size > maxRecvSize {
		return 0, errRecvSize
	}
	dst.Grow(int(size))
	b := dst.Bytes()
	b = b[len(b) : len(b)+int(size)]
	if _, err := io.ReadFull(r, b); err != nil {
		return 0, err
	}
	_, _ = dst.Write(b)
	return header[0], nil
}

func putUint32(buffer buffer, p uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], p)
	_, _ = buffer.Write(buf[:])
}

type buffer interface {
	Write(p []byte) (n int, err error)
	WriteString(s string) (n int, err error)
	WriteByte(p byte) error
	Len() int
	Bytes() []byte
	Reset()
}

type envelopeChunker struct {
	buffer  *bytes.Buffer
	onChunk func(src []byte) ([]byte, error)
}

func (c *envelopeChunker) Next(data buffer, _ bool) error {
	_, _ = c.buffer.Write(data.Bytes())
	data.Reset()

	b := c.buffer.Bytes()
	for len(b) >= 5 {
		size := binary.BigEndian.Uint32(b[1:5])
		if len(b) < int(size)+5 {
			break
		}
		msg := b[:size+5]
		b = b[size+5:]

		rsp, err := c.onChunk(msg)
		if err != nil {
			return err
		}
		if _, err := data.Write(rsp); err != nil {
			return err
		}
	}
	c.buffer.Reset()
	c.buffer.Write(b)
	return nil
}

type chunker interface {
	Next(data buffer, isEnd bool) error
}
