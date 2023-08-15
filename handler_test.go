// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TODO: other handler tests

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
