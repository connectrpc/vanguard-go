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
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestConvertBuffer(t *testing.T) {
	t.Parallel()
	buffers := &bufferPool{}
	codecJSON := NewJSONCodec(protoregistry.GlobalTypes)
	codecProto := NewProtoCodec(protoregistry.GlobalTypes)
	compGzip := newCompressionPool(CompressionGzip, defaultGzipCompressor, defaultGzipDecompressor)

	encode := func(t *testing.T, codec Codec, comp *compressionPool, msg proto.Message) string {
		t.Helper()
		data, err := codec.MarshalAppend(nil, msg)
		require.NoError(t, err)
		if comp == nil {
			return string(data)
		}
		var buf bytes.Buffer
		require.NoError(t, comp.compress(&buf, bytes.NewBuffer(data)))
		return buf.String()
	}

	testCases := []struct {
		name                string
		src, dst            string
		srcCodec, dstCodec  Codec
		srcComp, dstComp    *compressionPool
		msg                 proto.Message
		wantMsg             proto.Message
		wantMarshalCalls    int
		wantUnmarshalCalls  int
		wantCompressCalls   int
		wantDecompressCalls int
		wantErr             string
	}{{
		name: "SameCodec",
		src:  `"hello"`, dst: `"hello"`,
		srcCodec: codecJSON, dstCodec: codecJSON,
		msg:     &wrapperspb.StringValue{},
		wantMsg: &wrapperspb.StringValue{}, // Not decoded
	}, {
		name: "MustDecode",
		src:  `"hello"`, dst: `"hello"`,
		srcCodec: codecJSON, dstCodec: nil,
		msg:                &wrapperspb.StringValue{},
		wantMsg:            &wrapperspb.StringValue{Value: "hello"}, // decoded
		wantUnmarshalCalls: 1,
	}, {
		name: "DiffCodec",
		src:  `"hello"`, dst: encode(t, codecProto, nil, &wrapperspb.StringValue{Value: "hello"}),
		srcCodec: codecJSON, dstCodec: codecProto,
		msg:                &wrapperspb.StringValue{},
		wantMsg:            &wrapperspb.StringValue{Value: "hello"},
		wantUnmarshalCalls: 1,
		wantMarshalCalls:   1,
	}, {
		name: "Compress",
		src:  `"hello"`, dst: encode(t, codecProto, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		srcCodec: codecJSON, dstCodec: codecProto,
		srcComp: nil, dstComp: compGzip,
		msg:                &wrapperspb.StringValue{},
		wantMsg:            &wrapperspb.StringValue{Value: "hello"},
		wantUnmarshalCalls: 1,
		wantMarshalCalls:   1,
		wantCompressCalls:  1,
	}, {
		name: "SameCodecCompress",
		src:  `"hello"`, dst: encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		srcCodec: codecJSON, dstCodec: codecJSON,
		srcComp: nil, dstComp: compGzip,
		wantCompressCalls: 1,
	}, {
		name: "Decompress",
		src:  encode(t, codecProto, compGzip, &wrapperspb.StringValue{Value: "hello"}), dst: `"hello"`,
		srcCodec: codecProto, dstCodec: codecJSON,
		srcComp: compGzip, dstComp: nil,
		msg:                 &wrapperspb.StringValue{},
		wantMsg:             &wrapperspb.StringValue{Value: "hello"},
		wantUnmarshalCalls:  1,
		wantMarshalCalls:    1,
		wantDecompressCalls: 1,
	}, {
		name:     "SameCodecDecompress",
		src:      encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		dst:      `"hello"`,
		srcCodec: codecJSON, dstCodec: codecJSON,
		srcComp: compGzip, dstComp: nil,
		wantDecompressCalls: 1,
	}, {
		name:     "MustDecodeDecompress",
		src:      encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		dst:      encode(t, codecJSON, nil, &wrapperspb.StringValue{Value: "hello"}),
		srcCodec: codecJSON, dstCodec: nil,
		srcComp: compGzip, dstComp: nil,
		msg:                 &wrapperspb.StringValue{},
		wantMsg:             &wrapperspb.StringValue{Value: "hello"},
		wantUnmarshalCalls:  1,
		wantDecompressCalls: 1,
	}, {
		name: "ForceRecode",
		src:  `""`, dst: `"from msg"`,
		srcCodec: nil, dstCodec: codecJSON,
		srcComp: nil, dstComp: nil,
		msg:              &wrapperspb.StringValue{Value: "from msg"},
		wantMarshalCalls: 1,
	}, {
		name: "ForceRecodeRecompress",
		src:  `""`, dst: encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "from msg"}),
		srcCodec: nil, dstCodec: codecJSON,
		srcComp: nil, dstComp: compGzip,
		msg:               &wrapperspb.StringValue{Value: "from msg"},
		wantMarshalCalls:  1,
		wantCompressCalls: 1,
	}, {
		name:     "RecompressAndRecode",
		src:      encode(t, codecJSON, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		dst:      encode(t, codecProto, compGzip, &wrapperspb.StringValue{Value: "hello"}),
		srcCodec: codecJSON, dstCodec: codecProto,
		srcComp: compGzip, dstComp: compGzip,
		msg:                 &wrapperspb.StringValue{},
		wantMsg:             &wrapperspb.StringValue{Value: "hello"},
		wantUnmarshalCalls:  1,
		wantDecompressCalls: 1,
		wantMarshalCalls:    1,
		wantCompressCalls:   1,
	}}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var (
				marshalCalls, unmarshalCalls   int
				compressCalls, decompressCalls int
			)
			var srcComp compressor
			if testCase.srcComp != nil {
				srcComp = countCompressor{
					compressionPool:   testCase.srcComp,
					compressorCalls:   &compressCalls,
					decompressorCalls: &decompressCalls,
				}
			}
			var srcCodec Codec
			if testCase.srcCodec != nil {
				srcCodec = countCodec{
					Codec:          testCase.srcCodec,
					marshalCalls:   &marshalCalls,
					unmarshalCalls: &unmarshalCalls,
				}
			}
			var dstComp compressor
			if testCase.dstComp != nil {
				dstComp = countCompressor{
					compressionPool:   testCase.dstComp,
					compressorCalls:   &compressCalls,
					decompressorCalls: &decompressCalls,
				}
			}
			var dstCodec Codec
			if testCase.dstCodec != nil {
				dstCodec = countCodec{
					Codec:          testCase.dstCodec,
					marshalCalls:   &marshalCalls,
					unmarshalCalls: &unmarshalCalls,
				}
			}

			got := testCase.msg
			buf := bytes.NewBufferString(testCase.src)
			if err := convertBuffer(
				buffers,
				buf,
				srcComp,
				srcCodec,
				got,
				dstCodec,
				dstComp,
			); err != nil {
				assert.EqualError(t, err, testCase.wantErr)
				return
			}
			if testCase.wantMsg != nil {
				assert.Empty(t, cmp.Diff(testCase.wantMsg, got, protocmp.Transform()))
			}
			assert.Equal(t, testCase.dst, buf.String())
			assert.Equal(t, testCase.wantMarshalCalls, marshalCalls, "marshalCalls")
			assert.Equal(t, testCase.wantUnmarshalCalls, unmarshalCalls, "unmarshalCalls")
			assert.Equal(t, testCase.wantCompressCalls, compressCalls, "compressCalls")
			assert.Equal(t, testCase.wantDecompressCalls, decompressCalls, "decompressCalls")
		})
	}
}

type countCodec struct {
	Codec

	marshalCalls, unmarshalCalls *int
}

func (c countCodec) MarshalAppend(b []byte, msg proto.Message) ([]byte, error) {
	*c.marshalCalls++
	return c.Codec.MarshalAppend(b, msg)
}

func (c countCodec) Unmarshal(b []byte, msg proto.Message) error {
	*c.unmarshalCalls++
	return c.Codec.Unmarshal(b, msg)
}

type countCompressor struct {
	*compressionPool

	compressorCalls, decompressorCalls *int
}

func (c countCompressor) compress(dst io.Writer, src *bytes.Buffer) error {
	*c.compressorCalls++
	return c.compressionPool.compress(dst, src)
}

func (c countCompressor) decompress(dst *bytes.Buffer, src io.Reader) error {
	*c.decompressorCalls++
	return c.compressionPool.decompress(dst, src)
}
