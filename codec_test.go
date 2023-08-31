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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONStabilize(t *testing.T) {
	t.Parallel()
	// Verifies that technique in jsonStabilize is correct/safe with a variety of input conditions.
	testCases := []struct {
		name   string
		input  string
		output string
	}{
		{
			name:   "already compacted",
			input:  `{"foo":123,"foe":{"bar":true,"baz":[0,1,2,3,4]},"buzz":3.14159,"frob":"nitz"}`,
			output: `{"foo":123,"foe":{"bar":true,"baz":[0,1,2,3,4]},"buzz":3.14159,"frob":"nitz"}`,
		},
		{
			name: "pretty printed",
			input: `{
  "foo": 123,
  "foe": {
    "bar": true,
    "baz": [
      0,
      1,
      2,
      3,
      4
    ]
  },
  "buzz": 3.14159,
  "frob": "nitz"
}`,
			output: `{"foo":123,"foe":{"bar":true,"baz":[0,1,2,3,4]},"buzz":3.14159,"frob":"nitz"}`,
		},
		{
			name:   "just string",
			input:  `"foo bar baz\nfoo\tbar\tbaz"`,
			output: `"foo bar baz\nfoo\tbar\tbaz"`,
		},
		{
			name:   "just bool",
			input:  `           true       `,
			output: `true`,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result, err := jsonStabilize(([]byte)(testCase.input))
			require.NoError(t, err)
			require.Equal(t, testCase.output, string(result))
		})
	}
}
