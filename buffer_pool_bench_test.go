// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"crypto/rand"
	"testing"
)

//nolint:gochecknoglobals
var (
	testDataSmall []byte
	testDataLarge []byte
)

//nolint:forbidigo,gochecknoinits
func init() {
	testDataSmall = make([]byte, 256)
	_, err := rand.Read(testDataSmall)
	if err != nil {
		panic(err)
	}
	testDataLarge = make([]byte, 1024*1024)
	_, err = rand.Read(testDataLarge)
	if err != nil {
		panic(err)
	}
}

func BenchmarkBufferPool_WrapStrategy_NoopCopy(b *testing.B) {
	buffer := bytes.NewBuffer(make([]byte, 0, initialBufferSize))
	for i := 0; i < b.N; i++ {
		buffer.Reset()
		data := buffer.Bytes()
		data = append(data, testDataSmall...)
		buffer.Reset()
		buffer.Write(data)
	}
}

func BenchmarkBufferPool_WrapStrategy_NoopCopyWithoutReset(b *testing.B) {
	buffer := bytes.NewBuffer(make([]byte, 0, initialBufferSize))
	for i := 0; i < b.N; i++ {
		buffer.Reset()
		data := buffer.Bytes()
		data = append(data, testDataSmall...)
		// Note that this is riskier in practice: if we wrapped a slice
		// that were !== buffer's underlying slice, data would be corrupted
		// because we failed to copy over the prefix
		buffer.Write(data[buffer.Len():])
	}
}

func BenchmarkBufferPool_WrapStrategy_NewBuffer(b *testing.B) {
	buffer := bytes.NewBuffer(make([]byte, 0, initialBufferSize))
	for i := 0; i < b.N; i++ {
		buffer.Reset()
		data := buffer.Bytes()
		data = append(data, testDataSmall...)
		buffer = bytes.NewBuffer(data)
	}
}
