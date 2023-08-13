// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	testv1 "github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1"
	"github.com/stretchr/testify/assert"
)

func TestReadAllAllocs(t *testing.T) {
	t.Parallel()
	buffers := &bufferPool{}

	str := "hello world" + strings.Repeat("b", bytes.MinRead)
	src := strings.NewReader(str)
	var buf *bytes.Buffer

	avg := testing.AllocsPerRun(100, func() {
		_, _ = src.Seek(0, 0)
		buf = buffers.Get()
		if err := readAll(buf, src, maxRecycledBufferSize); err != nil {
			t.Fatal(err)
		}
		buffers.Put(buf)
	})
	t.Log(avg)
	assert.Equal(t, str, buf.String())
	assert.LessOrEqual(t, avg, 1.0) // 1 alloc when -race is enabled
}

func TestReadEnvelopeAllocs(t *testing.T) {
	t.Parallel()
	buffers := &bufferPool{}

	str := "hello world" + strings.Repeat("b", bytes.MinRead)
	env := makeEnvelope(buffers.Get())
	env.WriteString(str)
	env.encodeSizeAndFlags(0)
	env.Write(make([]byte, 0, maxRecycledBufferSize)) // large stream, greater than maxRecycledBufferSize
	src := bytes.NewReader(env.Bytes())
	var buf *bytes.Buffer

	avg := testing.AllocsPerRun(100, func() {
		_, _ = src.Seek(0, 0)
		buf = buffers.Get()
		if err := readEnvelope(buf, src, maxRecycledBufferSize); err != nil {
			t.Fatal(err)
		}
		buffers.Put(buf)
	})
	t.Log(avg)
	assert.Equal(t, len(str)+5, buf.Len())
	assert.Equal(t, str, envelope{buf}.open().String())
	assert.LessOrEqual(t, avg, 1.0) // 1 alloc when -race is enabled
}

func TestMarshal(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	codec := &jsonCodec{}
	msg := &testv1.ParameterValues{StringValue: "hello world"}

	assert.NoError(t, marshal(buf, msg, codec))
	dst := &bytes.Buffer{}
	assert.NoError(t, json.Compact(dst, buf.Bytes()))
	assert.Equal(t, `{"stringValue":"hello world"}`, dst.String())

	out := &testv1.ParameterValues{}
	assert.NoError(t, unmarshal(buf, out, codec))
	assert.Equal(t, msg, out)
}

func TestCompress(t *testing.T) {
	t.Parallel()

	input := `{"stringValue":"` + strings.Repeat("a", bytes.MinRead) + `"}`
	src := bytes.NewBufferString(input)

	buf := &bytes.Buffer{}
	assert.NoError(t, compress(buf, src, DefaultGzipCompressor()))
	assert.Less(t, buf.Len(), len(input), "compressed data should be smaller than input")

	dst := bytes.NewBuffer(nil)
	assert.NoError(t, decompress(dst, buf, DefaultGzipDecompressor()))
	assert.Equal(t, input, dst.String())
}
