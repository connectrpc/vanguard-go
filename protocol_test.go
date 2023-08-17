// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

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
		testCase := testCase
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
