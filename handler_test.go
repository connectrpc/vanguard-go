// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"io"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	testDataString           = "abc def ghi"
	testCompressedDataString = "nop qrs tuv" // rot13 of above
)

func TestHandleErrors(t *testing.T) {
	t.Parallel()
	// These tests exercise error-handling in the way the operation is initialized.
	// Non-error tests are instead covered by a separate matrix of protocol tests.
	// TODO
}

func TestMessage_AdvanceStage(t *testing.T) {
	t.Parallel()
	// Tests the state machine for message.

	type testEnviron struct {
		abcCodec, xyzCodec                               *fakeCodec
		abcCompression, xyzCompression, otherCompression *fakeCompression
		op                                               *operation
	}
	newTestEnviron := func(isRequest bool) *testEnviron {
		abcCodec := &fakeCodec{name: "abc"}
		xyzCodec := &fakeCodec{name: "xyz"}
		abcCompression := &fakeCompression{name: "abc"}
		xyzCompression := &fakeCompression{name: "xyz"}
		otherCompression := &fakeCompression{name: "other"}
		var clientCodec, serverCodec Codec
		var clientReqComp, serverReqComp, respComp *compressionPool
		if isRequest {
			clientCodec = abcCodec
			serverCodec = xyzCodec
			clientReqComp = abcCompression.newPool()
			serverReqComp = xyzCompression.newPool()
			respComp = otherCompression.newPool()
		} else {
			clientCodec = xyzCodec
			serverCodec = abcCodec
			clientReqComp = xyzCompression.newPool()
			serverReqComp = xyzCompression.newPool()
			respComp = abcCompression.newPool()
		}
		op := &operation{
			bufferPool: newBufferPool(),
			client: clientProtocolDetails{
				codec:          clientCodec,
				reqCompression: clientReqComp,
			},
			server: serverProtocolDetails{
				codec:          serverCodec,
				reqCompression: serverReqComp,
			},
			respCompression: respComp,
		}
		return &testEnviron{
			abcCodec:         abcCodec,
			xyzCodec:         xyzCodec,
			abcCompression:   abcCompression,
			xyzCompression:   xyzCompression,
			otherCompression: otherCompression,
			op:               op,
		}
	}
	resetEnv := func(env *testEnviron) {
		env.abcCodec.marshalCalls = 0
		env.abcCodec.unmarshalCalls = 0
		env.xyzCodec.marshalCalls = 0
		env.xyzCodec.unmarshalCalls = 0
		env.abcCompression.compressorCalls = 0
		env.abcCompression.decompressorCalls = 0
		env.xyzCompression.compressorCalls = 0
		env.xyzCompression.decompressorCalls = 0
		env.otherCompression.compressorCalls = 0
		env.otherCompression.decompressorCalls = 0
	}
	type expectedCounts struct {
		abcMarshalCalls    int
		abcUnmarshalCalls  int
		xyzMarshalCalls    int
		xyzUnmarshalCalls  int
		abcCompressCalls   int
		abcDecompressCalls int
		xyzCompressCalls   int
		xyzDecompressCalls int
	}
	checkCounts := func(t *testing.T, isRequest bool, env *testEnviron, counts expectedCounts) {
		t.Helper()
		if !isRequest {
			// for responses, compression for both client and server is the same (abc)
			counts.abcCompressCalls += counts.xyzCompressCalls
			counts.abcDecompressCalls += counts.xyzDecompressCalls
			counts.xyzCompressCalls = 0
			counts.xyzDecompressCalls = 0
		}
		assert.Equal(t, counts.abcMarshalCalls, env.abcCodec.marshalCalls)
		assert.Equal(t, counts.abcUnmarshalCalls, env.abcCodec.unmarshalCalls)
		assert.Equal(t, counts.xyzMarshalCalls, env.xyzCodec.marshalCalls)
		assert.Equal(t, counts.xyzUnmarshalCalls, env.xyzCodec.unmarshalCalls)
		assert.Equal(t, counts.abcCompressCalls, env.abcCompression.compressorCalls)
		assert.Equal(t, counts.abcDecompressCalls, env.abcCompression.decompressorCalls)
		assert.Equal(t, counts.xyzCompressCalls, env.xyzCompression.compressorCalls)
		assert.Equal(t, counts.xyzDecompressCalls, env.xyzCompression.decompressorCalls)
		assert.Zero(t, env.otherCompression.compressorCalls)
		assert.Zero(t, env.otherCompression.decompressorCalls)
	}

	testCases := []struct {
		name                      string
		createMessage             func() *message
		decodedToSend             expectedCounts
		decodedToSendIfCompressed *expectedCounts
		readToSend                expectedCounts
		readToSendIfCompressed    *expectedCounts
	}{
		{
			name:          "same codec, same compression",
			createMessage: func() *message { return &message{sameCodec: true, sameCompression: true} },
			// no calls necessary since client payload can be re-used
			decodedToSend: expectedCounts{},
			readToSend:    expectedCounts{},
		},
		{
			name:          "same codec, different compression",
			createMessage: func() *message { return &message{sameCodec: true} },
			// no calls necessary for uncompressed since payload can be re-used,
			// but we have to decompress/recompress for compressed payloads
			decodedToSend: expectedCounts{},
			decodedToSendIfCompressed: &expectedCounts{
				xyzCompressCalls: 1,
			},
			readToSend: expectedCounts{},
			readToSendIfCompressed: &expectedCounts{
				abcDecompressCalls: 1,
				xyzCompressCalls:   1,
			},
		},
		{
			name:          "different codec",
			createMessage: func() *message { return &message{} },
			// we must re-encode and re-compress
			decodedToSend: expectedCounts{
				xyzMarshalCalls: 1,
			},
			decodedToSendIfCompressed: &expectedCounts{
				xyzMarshalCalls:  1,
				xyzCompressCalls: 1,
			},
			readToSend: expectedCounts{
				abcUnmarshalCalls: 1,
				xyzMarshalCalls:   1,
			},
			readToSendIfCompressed: &expectedCounts{
				abcDecompressCalls: 1,
				abcUnmarshalCalls:  1,
				xyzMarshalCalls:    1,
				xyzCompressCalls:   1,
			},
		},
	}

	for _, compressed := range []bool{true, false} {
		compressed := compressed
		t.Run(fmt.Sprintf("compressed:%v", compressed), func(t *testing.T) {
			t.Parallel()
			for _, isRequest := range []bool{true, false} {
				isRequest := isRequest
				t.Run(fmt.Sprintf("request:%v", isRequest), func(t *testing.T) {
					t.Parallel()
					for _, testCase := range testCases {
						testCase := testCase
						t.Run(testCase.name, func(t *testing.T) {
							t.Parallel()

							originalData := testDataString
							if compressed {
								originalData = testCompressedDataString
							}

							env := newTestEnviron(isRequest)
							msg := testCase.createMessage()
							msg.msg = &wrapperspb.StringValue{}
							buffer := msg.reset(env.op.bufferPool, isRequest, compressed)
							checkStageEmpty(t, msg, compressed)

							buffer.WriteString(originalData)
							msg.stage = stageRead
							checkStageRead(t, msg, compressed)

							err := msg.advanceToStage(env.op, stageDecoded)
							require.NoError(t, err)
							// read -> decoded must always decode (and possibly first decompress)
							counts := expectedCounts{
								abcUnmarshalCalls: 1,
							}
							if compressed {
								counts.abcDecompressCalls = 1
							}
							checkCounts(t, isRequest, env, counts)
							checkStageDecoded(t, msg)

							resetEnv(env)
							err = msg.advanceToStage(env.op, stageSend)
							require.NoError(t, err)
							counts = testCase.decodedToSend
							if compressed && testCase.decodedToSendIfCompressed != nil {
								counts = *testCase.decodedToSendIfCompressed
							}
							checkCounts(t, isRequest, env, counts)
							checkStageSend(t, msg, compressed)

							// Re-create message and this time go directly from read to send
							msg = testCase.createMessage()
							msg.msg = &wrapperspb.StringValue{}
							buffer = msg.reset(env.op.bufferPool, isRequest, compressed)
							buffer.WriteString(originalData)
							msg.stage = stageRead

							resetEnv(env)
							err = msg.advanceToStage(env.op, stageSend)
							require.NoError(t, err)
							counts = testCase.readToSend
							if compressed && testCase.readToSendIfCompressed != nil {
								counts = *testCase.readToSendIfCompressed
							}
							checkCounts(t, isRequest, env, counts)
							checkStageSend(t, msg, compressed)
						})
					}
				})
			}
		})
	}
}

func TestIntersection(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		a, b, result []string
		resultCap    int
	}{
		{
			name:      "b is superset",
			a:         []string{"a", "b", "c"},
			b:         []string{"a", "b", "c", "d", "e", "f"},
			result:    []string{"a", "b", "c"},
			resultCap: 3,
		},
		{
			name:      "a is superset",
			a:         []string{"a", "b", "c", "d", "e", "f"},
			b:         []string{"a", "b", "c"},
			result:    []string{"a", "b", "c"},
			resultCap: 3,
		},
		{
			name:   "a is empty",
			a:      nil,
			b:      []string{"a", "b", "c", "d", "e", "f"},
			result: nil,
		},
		{
			name:   "b is empty",
			a:      []string{"a", "b", "c"},
			b:      nil,
			result: nil,
		},
		{
			name:      "result is empty",
			a:         []string{"a", "b", "c"},
			b:         []string{"d", "e", "f"},
			result:    []string{}, // only nil when one of the inputs is empty
			resultCap: 3,
		},
		{
			name:      "result is subset of both",
			a:         []string{"x", "y", "z", "a", "b", "c"},
			b:         []string{"a", "b", "c", "d", "e", "f"},
			result:    []string{"a", "b", "c"},
			resultCap: 6,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result := intersect(testCase.a, testCase.b)
			require.Equal(t, testCase.result, result)
			require.Equal(t, testCase.resultCap, cap(result))
		})
	}
}

func checkStageEmpty(t *testing.T, msg *message, compressed bool) {
	t.Helper()
	require.Equal(t, stageEmpty, msg.stage)
	if compressed {
		require.NotNil(t, msg.compressed)
		require.Zero(t, msg.compressed.Len())
		require.Nil(t, msg.data)
	} else {
		require.Nil(t, msg.compressed)
		require.NotNil(t, msg.data)
		require.Zero(t, msg.data.Len())
	}
	// Should not be possible to advance from empty.
	require.Error(t, msg.advanceToStage(nil, stageRead))
	require.Error(t, msg.advanceToStage(nil, stageDecoded))
	require.Error(t, msg.advanceToStage(nil, stageSend))
}

func checkStageRead(t *testing.T, msg *message, compressed bool) {
	t.Helper()
	require.Equal(t, stageRead, msg.stage)
	if compressed {
		require.NotNil(t, msg.compressed)
		require.Equal(t, testCompressedDataString, msg.compressed.String())
		require.Nil(t, msg.data)
	} else {
		require.Nil(t, msg.compressed)
		require.NotNil(t, msg.data)
		require.Equal(t, testDataString, msg.data.String())
	}
	// Should not be possible to go backwards.
	require.Error(t, msg.advanceToStage(nil, stageEmpty))
}

func checkStageDecoded(t *testing.T, msg *message) {
	t.Helper()
	require.Equal(t, stageDecoded, msg.stage)
	require.Equal(t, testDataString, msg.msg.(*wrapperspb.StringValue).Value) //nolint:forcetypeassert
	// Should not be possible to go backwards.
	require.Error(t, msg.advanceToStage(nil, stageRead))
	require.Error(t, msg.advanceToStage(nil, stageEmpty))
}

func checkStageSend(t *testing.T, msg *message, compressed bool) {
	t.Helper()
	if compressed {
		require.NotNil(t, msg.compressed)
		require.Equal(t, testCompressedDataString, msg.compressed.String())
		// can't assert anything about m.data: if we didn't have to do
		// anything to get to send (same codec, same compression), we
		// won't have done anything to it; but if we had to re-encode
		// and re-compress, it would get released and set to nil
	} else {
		require.Nil(t, msg.compressed)
		require.NotNil(t, msg.data)
		require.Equal(t, testDataString, msg.data.String())
	}
	require.Equal(t, stageSend, msg.stage)
	// Should not be possible to go backwards.
	require.Error(t, msg.advanceToStage(nil, stageDecoded))
	require.Error(t, msg.advanceToStage(nil, stageRead))
	require.Error(t, msg.advanceToStage(nil, stageEmpty))
}

type fakeCodec struct {
	name                         string
	marshalCalls, unmarshalCalls int
}

func (f *fakeCodec) Name() string {
	return f.name
}

func (f *fakeCodec) MarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	f.marshalCalls++
	val := msg.(*wrapperspb.StringValue).Value //nolint:forcetypeassert
	return append(b, ([]byte)(val)...), nil
}

func (f *fakeCodec) Unmarshal(b []byte, msg proto.Message) error {
	f.unmarshalCalls++
	msg.(*wrapperspb.StringValue).Value = string(b) //nolint:forcetypeassert
	return nil
}

type fakeCompression struct {
	name                               string
	compressorCalls, decompressorCalls int
	reader                             io.Reader
	writer                             io.Writer
}

func (f *fakeCompression) newPool() *compressionPool {
	return newCompressionPool(
		f.name,
		func() connect.Compressor {
			return (*fakeCompressor)(f)
		},
		func() connect.Decompressor {
			return (*fakeDecompressor)(f)
		},
	)
}

type fakeCompressor fakeCompression

func (f *fakeCompressor) Write(p []byte) (n int, err error) {
	rot13(p)
	return f.writer.Write(p)
}

func (f *fakeCompressor) Close() error {
	return nil
}

func (f *fakeCompressor) Reset(writer io.Writer) {
	(*fakeCompression)(f).compressorCalls++
	f.writer = writer
}

type fakeDecompressor fakeCompression

func (f *fakeDecompressor) Read(p []byte) (n int, err error) {
	n, err = f.reader.Read(p)
	rot13(p[:n])
	return n, err
}

func (f *fakeDecompressor) Close() error {
	return nil
}

func (f *fakeDecompressor) Reset(reader io.Reader) error {
	(*fakeCompression)(f).decompressorCalls++
	f.reader = reader
	return nil
}

func rot13(data []byte) {
	for index, char := range data {
		if char >= 'A' && char <= 'Z' {
			char += 13
			if char > 'Z' {
				char -= 26
			}
		} else if char >= 'a' && char <= 'z' {
			char += 13
			if char > 'z' {
				char -= 26
			}
		}
		data[index] = char
	}
}
