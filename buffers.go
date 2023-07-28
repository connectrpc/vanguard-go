// Copyright 2023 Buf Technologies, Inc.
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

func read(dst *bytes.Buffer, r io.Reader) (int, error) {
	dst.Grow(bytes.MinRead)
	b := dst.Bytes()
	b = b[len(b):cap(b)]
	n, err := r.Read(b)
	_, _ = dst.Write(b[:n])
	return n, err
}

func readAll(dst *bytes.Buffer, r io.Reader, maxRecvSize uint32) error {
	var total int64
	for {
		n, err := read(dst, r)
		total += int64(n)
		if total > int64(maxRecvSize) {
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

func readEnvelope(dst *bytes.Buffer, r io.Reader, maxRecvSize uint32) error {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}

	size := binary.BigEndian.Uint32(header[1:5])
	if size > maxRecvSize {
		return errRecvSize
	}
	dst.Grow(int(size) + 5)
	_, _ = dst.Write(header[:])

	b := dst.Bytes()
	b = b[len(b) : len(b)+int(size)]
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	_, _ = dst.Write(b)
	return nil
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

type chunker interface {
	Next(data buffer, endStream bool) error
}

type envelopeChunker struct {
	buffer  *bytes.Buffer
	onChunk func(src []byte) ([]byte, error)
}

func (c envelopeChunker) Next(data buffer, _ bool) error {
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

type endStreamChunker struct {
	buffer  *bytes.Buffer
	onChunk func(src []byte) ([]byte, error)
}

func (c endStreamChunker) Next(data buffer, endStream bool) error {
	_, _ = c.buffer.Write(data.Bytes())
	data.Reset()
	if !endStream {
		return nil
	}
	msg := c.buffer.Bytes()
	rsp, err := c.onChunk(msg)
	if err != nil {
		return err
	}
	if _, err := data.Write(rsp); err != nil {
		return err
	}
	c.buffer.Reset()
	return nil
}

type envelopeReader struct {
	reader      io.Reader
	buffer      *bytes.Buffer
	onChunk     func(src []byte) ([]byte, error)
	maxRecvSize uint32
}

func (c *envelopeReader) Read(p []byte) (int, error) {
	if c.buffer.Len() == 0 {
		if err := readEnvelope(c.buffer, c.reader, c.maxRecvSize); err != nil {
			return 0, err
		}
		msg := c.buffer.Bytes()
		rsp, err := c.onChunk(msg)
		if err != nil {
			return 0, err
		}
		c.buffer.Reset()
		_, _ = c.buffer.Write(rsp)
	}
	return c.buffer.Read(p)
}

type envelopeWriter struct {
	writer      io.Writer
	buffer      *bytes.Buffer
	onChunk     func(src []byte) ([]byte, error)
	maxRecvSize uint32
}

func (c *envelopeWriter) Write(p []byte) (int, error) {
	_, _ = c.buffer.Write(p)

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
			return 0, err
		}
		if len(rsp) > 0 {
			if _, err := c.writer.Write(rsp); err != nil {
				return 0, err
			}
		}
	}
	return len(p), nil
}
func (c *envelopeWriter) Flush(bool) error { return nil }

type endStreamReader struct {
	reader      io.Reader
	buffer      *bytes.Buffer
	onChunk     func(src []byte) ([]byte, error)
	maxRecvSize uint32
}

func (c endStreamReader) Read(p []byte) (int, error) {
	if c.buffer.Len() == 0 {
		if err := readAll(c.buffer, c.reader, c.maxRecvSize); err != nil {
			return 0, err
		}
		msg := c.buffer.Bytes()
		rsp, err := c.onChunk(msg)
		if err != nil {
			return 0, err
		}
		c.buffer.Reset()
		_, _ = c.buffer.Write(rsp)
	}
	return c.buffer.Read(p)
}

type endStreamWriter struct {
	writer      io.Writer
	buffer      *bytes.Buffer
	onChunk     func(src []byte) ([]byte, error)
	maxRecvSize uint32
}

type WriteFlusher interface {
	io.Writer
	Flush(bool) error
}

func (c endStreamWriter) Write(p []byte) (int, error) {
	return c.buffer.Write(p)
}
func (c endStreamWriter) Flush(isEOF bool) error {
	if !isEOF {
		return nil
	}
	b := c.buffer.Bytes()
	b, err := c.onChunk(b)
	if err != nil {
		return err
	}
	_, err = c.writer.Write(b)
	return err
}
