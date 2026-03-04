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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMultiHeader(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		input     []string
		output    []string
		actualCap int
	}{
		{
			name: "empty",
		},
		{
			name:   "one",
			input:  []string{"gzip"},
			output: []string{"gzip"},
		},
		{
			name:   "multiple in one value",
			input:  []string{"gzip,br,deflate,spdy"},
			output: []string{"gzip", "br", "deflate", "spdy"},
		},
		{
			name:   "multiple values",
			input:  []string{"gzip", "br", "deflate", "spdy"},
			output: []string{"gzip", "br", "deflate", "spdy"},
		},
		{
			name:   "multiple in multiple values",
			input:  []string{"gzip,br", "deflate,spdy", "zstd"},
			output: []string{"gzip", "br", "deflate", "spdy", "zstd"},
		},
		{
			name:      "ignore empty and errant commas",
			input:     []string{"", "deflate,,,spdy,", ",zstd,"},
			output:    []string{"deflate", "spdy", "zstd"},
			actualCap: 9, // based on errant commas
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result := parseMultiHeader(testCase.input)
			require.Equal(t, testCase.output, result)
			if testCase.actualCap > 0 {
				require.Equal(t, testCase.actualCap, cap(result))
			} else {
				require.Equal(t, len(result), cap(result))
			}
		})
	}
}
