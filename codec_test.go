// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

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
