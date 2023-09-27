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
	return &bufferPool{}
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
