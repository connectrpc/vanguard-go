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
