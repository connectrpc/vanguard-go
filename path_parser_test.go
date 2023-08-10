// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutePath_ParsePathTemplate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		tmpl        string
		wantPath    []string
		wantVerb    string
		wantVars    []pathVariable
		expectedErr string
	}{{
		// no error, but lots of encoding for special/reserved characters
		tmpl:     "/my%2Fcool+blog&about%2Cstuff%5Bwat%5D/{var={abc}/{def=**}}:baz",
		wantPath: []string{"my/cool+blog&about,stuff[wat]", "*", "**"},
		wantVerb: "baz",
		wantVars: []pathVariable{
			{fieldPath: "var", start: 1, end: -1},
			{fieldPath: "abc", start: 1, end: 2},
			{fieldPath: "def", start: 2, end: -1},
		},
	}, {
		tmpl:        "/foo/bar/baz?abc=def",
		expectedErr: "syntax error at column 13: unexpected '?'", // no query string allowed
	}, {
		tmpl:        "/foo/bar/baz buzz",
		expectedErr: "syntax error at column 13: unexpected ' '", // no whitespace allowed
	}, {
		tmpl:        "foo/bar/baz",
		expectedErr: "syntax error at column 1: expected '/', got 'f'", // must start with slash
	}, {
		tmpl:        "/foo/bar/",
		expectedErr: "syntax error at column 9: expected path value", // must not end in slash
	}, {
		tmpl:        "/foo/bar:baz/buzz",
		expectedErr: "syntax error at column 13: unexpected '/'", // ":baz" verb can only come at the very end
	}, {
		tmpl:        "/foo/{bar/baz}/buzz",
		expectedErr: "syntax error at column 10: expected '}', got '/'", // invalid field path
	}, {
		tmpl:     "/foo/bar:baz%12xyz%abcde",
		wantPath: []string{"foo", "bar"},
		wantVerb: "baz\x12xyz\xabcde",
	}, {
		tmpl:     "/{hello}/world",
		wantPath: []string{"*", "world"},
		wantVars: []pathVariable{
			{fieldPath: "hello", start: 0, end: 1},
		},
	}, {
		tmpl:        "/foo/bar%55:baz%1",
		expectedErr: "syntax error at column 17: invalid URL escape \"%1\"",
	}, {
		tmpl:        "/foo/bar*",
		expectedErr: "syntax error at column 9: unexpected '*'", // wildcard must be entire path component
	}, {
		tmpl:        "/foo/bar/***",
		expectedErr: "syntax error at column 12: unexpected '*'", // no such thing as triple-wildcard
	}, {
		tmpl:        "/foo/**/bar",
		expectedErr: "double wildcard '**' must be the final path segment", // double-wildcard must be at end
	}, {
		tmpl:        "/{a}/{a}", // TODO: allow this?
		expectedErr: "duplicate variable \"a\"",
	}, {
		tmpl:     "/f/bar",
		wantPath: []string{"f", "bar"},
	}, {
		tmpl:     "/v1/{name=shelves/*/books/*}",
		wantPath: []string{"v1", "shelves", "*", "books", "*"},
		wantVars: []pathVariable{
			{fieldPath: "name", start: 1, end: 5},
		},
	}, {
		tmpl:     "/v1/{parent=shelves/*}/books",
		wantPath: []string{"v1", "shelves", "*", "books"},
		wantVars: []pathVariable{
			{fieldPath: "parent", start: 1, end: 3},
		},
	}, {
		tmpl:     "/v1/{book.name=shelves/*/books/*}",
		wantPath: []string{"v1", "shelves", "*", "books", "*"},
		wantVars: []pathVariable{
			{fieldPath: "book.name", start: 1, end: 5},
		},
	}, {
		tmpl:     "/v1:watch",
		wantPath: []string{"v1"},
		wantVerb: "watch",
	}, {
		tmpl:     "/v3/events:clear",
		wantPath: []string{"v3", "events"},
		wantVerb: "clear",
	}, {
		tmpl:     "/v3/{name=events/*}:cancel",
		wantPath: []string{"v3", "events", "*"},
		wantVerb: "cancel",
		wantVars: []pathVariable{
			{fieldPath: "name", start: 1, end: 3},
		},
	}}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.tmpl, func(t *testing.T) {
			t.Parallel()
			segments, variables, err := parsePathTemplate(testCase.tmpl)
			if testCase.expectedErr != "" {
				assert.ErrorContains(t, err, testCase.expectedErr)
				return
			}
			t.Log(segments)
			require.NoError(t, err)
			assert.ElementsMatch(t, testCase.wantPath, segments.path, "path mismatch")
			assert.Equal(t, testCase.wantVerb, segments.verb, "verb mismatch")
			assert.ElementsMatch(t, testCase.wantVars, variables, "variables mismatch")
		})
	}
}
