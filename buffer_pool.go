// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"sync"
)

const (
	initialBufferSize    = 512
	maxRecycleBufferSize = 8 * 1024 * 1024 // if >8MiB, don't hold onto a buffer
)

type bufferPool struct {
	sync.Pool
}

func newBufferPool() *bufferPool {
	return &bufferPool{
		Pool: sync.Pool{
			New: func() any {
				return bytes.NewBuffer(make([]byte, 0, initialBufferSize))
			},
		},
	}
}

func (b *bufferPool) Get() *bytes.Buffer {
	return b.Pool.Get().(*bytes.Buffer) //nolint:forcetypeassert
}

func (b *bufferPool) Put(buffer *bytes.Buffer) {
	if buffer.Cap() > maxRecycleBufferSize {
		return
	}
	buffer.Reset()
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
