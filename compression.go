// Copyright 2023-2026 Buf Technologies, Inc.
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
	"errors"
	"io"
	"strings"

	"connectrpc.com/connect/v2"
)

// compressors indexes [connect.Compressor]s by Content-Encoding token.
type compressors struct {
	byName map[string]connect.Compressor
	names  string // comma-separated, for Accept-Encoding
}

func newCompressors(list []connect.Compressor) *compressors {
	byName := make(map[string]connect.Compressor, len(list))
	names := make([]string, 0, len(list))
	for _, compressor := range list {
		name := compressor.Name()
		if _, dup := byName[name]; dup || name == "" || name == connect.CompressionNameIdentity {
			continue
		}
		byName[name] = compressor
		names = append(names, name)
	}
	return &compressors{byName: byName, names: strings.Join(names, ", ")}
}

// encodingName returns the Content-Encoding token for compressor, identity for nil.
func encodingName(compressor connect.Compressor) string {
	if compressor == nil {
		return connect.CompressionNameIdentity
	}
	return compressor.Name()
}

// get returns the compressor for name, or nil for identity and unknown names.
func (c *compressors) get(name string) connect.Compressor {
	return c.byName[name]
}

// negotiate mirrors connecthttp: the request encoding must be known, and the
// response reuses it or else the first accepted encoding that is supported.
func (c *compressors) negotiate(sent, accept string) (request, response connect.Compressor, err error) {
	if sent != "" && sent != connect.CompressionNameIdentity {
		request = c.byName[sent]
		if request == nil {
			return nil, nil, connect.Errorf(connect.CodeUnimplemented,
				"unknown compression %q: supported encodings are %v", sent, c.names)
		}
	}
	response = request
	if response == nil && accept != "" {
		for name := range strings.FieldsFuncSeq(accept, isCommaOrSpace) {
			name, _, _ = strings.Cut(name, ";") // drop any q-weight
			if response = c.byName[name]; response != nil {
				break
			}
		}
	}
	return request, response, nil
}

func isCommaOrSpace(r rune) bool { return r == ',' || r == ' ' }

// decompressBody wraps body so reads are decompressed. Close releases both.
func decompressBody(compressor connect.Compressor, body io.ReadCloser) (io.ReadCloser, error) {
	reader, err := compressor.Decompress(body)
	if err != nil {
		return nil, err
	}
	return &decompressedBody{Reader: reader, decompressor: reader, body: body}, nil
}

type decompressedBody struct {
	io.Reader

	decompressor io.Closer
	body         io.Closer
}

func (d *decompressedBody) Close() error {
	return errors.Join(d.decompressor.Close(), d.body.Close())
}
