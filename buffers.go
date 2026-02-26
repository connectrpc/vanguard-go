// Copyright 2023-2025 Buf Technologies, Inc.
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
	"sync"

	"google.golang.org/protobuf/proto"
)

const (
	initialBufferSize    = bytes.MinRead
	maxRecycleBufferSize = 8 * 1024 * 1024 // if >8MiB, don't hold onto a buffer
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

func (b *bufferPool) Wrap(data []byte, orig *bytes.Buffer) *bytes.Buffer {
	if cap(data) > orig.Cap() {
		// Original buffer was too small, so we had to grow its slice to
		// compute data.  Replace the buffer with the larger,
		// newly-allocated slice.
		return bytes.NewBuffer(data)
	}
	// The buffer from the pool was large enough so no growing was necessary.
	// That means this should be a no-op since the buffer, under the hood, will
	// copy the given data to its internal slice, which should be the exact
	// same slice.
	orig.Reset()
	orig.Write(data)
	return orig
}

// convertBuffer converts the message in the buffer between compression and
// encoding formats. The message will only be used if required to convert the
// encoding (i.e. when srcCodec and dstCodec differ).
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

// encodeBuffer encodes the message into the buffer, compressing as needed.
func encodeBuffer(buffers *bufferPool, buf *bytes.Buffer, msg proto.Message, codec Codec, comp compressor) error { //nolint:unused
	return convertBuffer(buffers, buf, nil, nil, msg, codec, comp)
}

// decodeBuffer decodes the message from the buffer, decompressing as needed.
func decodeBuffer(buffers *bufferPool, buf *bytes.Buffer, msg proto.Message, codec Codec, comp compressor) error {
	return convertBuffer(buffers, buf, comp, codec, msg, nil, nil)
}

// marshal encodes the message into the buffer using the given codec,
// reusing the buffer's capacity when possible.
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
		// The buffer from the pool was large enough, MarshalAppend didn't
		// allocate. Copy to the same byte slice is a nop.
		dst.Write(raw[dst.Len():]) //nolint:gosec
	}
	return nil
}

func getName(thing interface{ Name() string }) string {
	if thing == nil {
		return ""
	}
	return thing.Name()
}
