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
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

const (
	initialBufferSize    = bytes.MinRead
	maxRecycleBufferSize = 8 * 1024 * 1024 // if >8MiB, don't hold onto a buffer
	// chunkMessageSize for google.api.HttpBody messages will be chunked into
	// multiple messages of this size. It should be large enough to avoid
	// excessive overhead, but small enough to avoid holding onto large buffers.
	chunkMessageSize = 4 * 1024 * 1024 // 4MiB
)

type bufferPool struct {
	sync.Pool
}

func (b *bufferPool) Get() *bytes.Buffer {
	if buffer, ok := b.Pool.Get().(*bytes.Buffer); ok {
		buffer.Reset()
		return buffer
	}
	return bytes.NewBuffer(make([]byte, 0, initialBufferSize))
}

func (b *bufferPool) Put(buffer *bytes.Buffer) {
	if buffer.Cap() > maxRecycleBufferSize {
		return
	}
	b.Pool.Put(buffer)
}

type readMode int

const (
	readModeSize = readMode(iota)
	readModeEOF
	readModeChunk
)

type srcParams struct {
	Size         uint32   // Size or message
	ReadMode     readMode // Read size, unil EOF, or chunked
	IsEOF        bool     // Last bytes of stream
	IsTrailer    bool     // Trailer message, call DecodeTrailer
	IsCompressed bool     // Compressed message, call Decompress
}

func (s srcParams) String() string {
	return fmt.Sprintf("srcParams{Size: %d, IsEOF: %t, IsTrailer: %t, IsCompressed: %t}", s.Size, s.IsEOF, s.IsTrailer, s.IsCompressed)
}

type dstParams struct {
	Flags          uint8 // Envelope flags
	IsEnvelope     bool  // Set envelope prefix on messages
	IsCompressed   bool  // Compress message, call Compress
	WaitForTrailer bool  // Wait for trailers, buffering messages
}

func (d dstParams) String() string {
	return fmt.Sprintf("dstParams{Flags: %d, IsEnvelope: %t, IsCompressed: %t, WaitForTrailer: %t}", d.Flags, d.IsEnvelope, d.IsCompressed, d.WaitForTrailer)
}

type messageStage int

const (
	stageEmpty    = messageStage(iota)
	stageRead     // TODO: docs
	stageBuffered // TODO: docs
	stageEOF      // TODO: docs
)

type messageBuffer struct {
	Buf   *bytes.Buffer
	Index int // Index of message in the stream
	Src   srcParams
	Dst   dstParams

	offset    int
	envOffset int
	size      int
	stage     messageStage
}

func (m *messageBuffer) Read(data []byte) (n int, err error) {
	if m.stage != stageBuffered {
		return 0, errorf(connect.CodeInternal, "message not buffered")
	}
	if m.Dst.IsEnvelope && m.envOffset < 5 {
		env := makeEnvelope(m.Dst.Flags, m.size)
		envN := copy(data, env[m.envOffset:])
		data = data[envN:]
		n += envN
		m.envOffset += envN
		if m.envOffset < 5 {
			return n, nil
		}
	}
	src := m.Buf.Bytes()[m.offset:m.size]
	wroteN := copy(data, src)
	m.offset += wroteN
	n += wroteN
	if n == 0 && len(data) > 0 {
		err = io.EOF
	}
	return n, err
}

func (m *messageBuffer) WriteTo(dst io.Writer) (n int64, err error) {
	if m.stage != stageBuffered {
		return 0, errorf(connect.CodeInternal, "message not buffered")
	}
	if m.Dst.IsEnvelope && m.envOffset < 5 {
		env := makeEnvelope(m.Dst.Flags, m.size)
		envN, err := dst.Write(env[m.envOffset:])
		n += int64(envN)
		m.envOffset += envN
		if err != nil {
			return n, err
		}
		if m.envOffset < 5 {
			return n, io.ErrShortWrite
		}
	}
	src := m.Buf.Bytes()[m.offset:m.size]
	wroteN, err := dst.Write(src)
	m.offset += wroteN
	n += int64(wroteN)
	return n, err
}

// Flush the first message from the buffer and reclaim size by shifting any
// excess data to the front of the buffer.
func (m *messageBuffer) Flush() {
	// Shift any excess data to the front of the buffer.
	excess := m.Buf.Bytes()[m.size:]
	m.Buf.Reset()
	_, _ = m.Buf.Write(excess)

	m.Src = srcParams{}
	m.Dst = dstParams{}
	m.Index++
	m.offset = 0
	m.envOffset = 0
	m.size = 0
	m.stage = stageEmpty
}

func (m *messageBuffer) Convert(buffers *bufferPool, msg proto.Message, src, dst encoding) error {
	srcCompressor := src.Compressor
	if !m.Src.IsCompressed {
		srcCompressor = nil
	}
	dstCompressor := dst.Compressor
	if !m.Dst.IsCompressed {
		dstCompressor = nil
	}
	if err := convertBuffer(
		buffers,
		m.Buf,
		srcCompressor,
		src.Codec,
		msg,
		dst.Codec,
		dstCompressor,
	); err != nil {
		return err
	}
	m.size = m.Buf.Len()
	m.stage = stageBuffered
	return nil
}

// encode the message into the buffer, compressing and encoding as needed.
func encodeBuffer(buffers *bufferPool, buf *bytes.Buffer, msg proto.Message, codec Codec, comp compressor) error { //nolint:unused
	// Force re-encoding.
	// Force re-compression, if needed.
	return convertBuffer(buffers, buf, nil, nil, msg, codec, comp)
}

// decode the message from the buffer, decompressing and unmarshalling as needed.
func decodeBuffer(buffers *bufferPool, buf *bytes.Buffer, msg proto.Message, codec Codec, comp compressor) error {
	// Force decompression, if needed.
	// Force decoding.
	return convertBuffer(buffers, buf, comp, codec, msg, nil, nil)
}

// convert the message in the buffer to the new compression and encoding.
// The message will only be used if required to convert the encoding.
func convertBuffer(
	buffers *bufferPool,
	buf *bytes.Buffer,
	srcCompressor compressor,
	srcCodec Codec,
	msg proto.Message,
	dstCodec Codec,
	dstCompressor compressor,
) error {
	var tmp *bytes.Buffer
	defer func() {
		if tmp != nil {
			buffers.Put(tmp)
		}
	}()
	needsRecoding := getName(srcCodec) != getName(dstCodec)
	needsRecompressing := getName(srcCompressor) != getName(dstCompressor) || needsRecoding
	if srcCompressor != nil && needsRecompressing {
		// Decompress
		tmp = buffers.Get()
		// Read from m, don't mutate m.buf
		if err := srcCompressor.decompress(tmp, buf); err != nil {
			return err
		}
		*buf, *tmp = *tmp, *buf // swap buffers
	}
	if srcCodec != nil && needsRecoding {
		// Decode
		if err := srcCodec.Unmarshal(buf.Bytes(), msg); err != nil {
			return err
		}
	}
	if dstCodec != nil && needsRecoding {
		// Encode
		buf.Reset()
		if err := marshal(buf, msg, dstCodec); err != nil {
			return err
		}
	}
	if dstCompressor != nil && needsRecompressing {
		// Compress
		if tmp == nil {
			tmp = buffers.Get()
		} else {
			tmp.Reset()
		}
		if err := dstCompressor.compress(tmp, buf); err != nil {
			return err
		}
		*buf, *tmp = *tmp, *buf // swap buffers
	}
	return nil
}

func getName(thing interface{ Name() string }) string {
	if thing == nil {
		return ""
	}
	return thing.Name()
}

func readEnvelope(src io.Reader) (uint8, uint32, error) {
	var env envelopeBytes
	if _, err := io.ReadFull(src, env[:]); err != nil {
		return 0, 0, errorf(connect.CodeInternal, "read envelope: %w", err)
	}
	flags := env[0]
	size := binary.BigEndian.Uint32(env[1:])
	return flags, size, nil
}

// read a bit from the src into the dst, growing the dst if needed.
// This is used to check for EOF when reading messages.
func read(dst *bytes.Buffer, src io.Reader) (int, error) {
	dst.Grow(bytes.MinRead)
	b := dst.Bytes()[dst.Len() : dst.Len()+bytes.MinRead]
	n, err := src.Read(b)
	_, _ = dst.Write(b[:n]) // noop
	return n, err
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

// makeEnvelope returns a byte array representing an encoded envelope.
func makeEnvelope(flags uint8, size int) [5]byte {
	prefix := [5]byte{}
	prefix[0] = flags
	binary.BigEndian.PutUint32(prefix[1:5], uint32(size))
	return prefix
}
