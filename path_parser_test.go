// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutePath_ParseRoundTripToString(t *testing.T) {
	t.Parallel()
	var testCases = []struct {
		pathStr    string
		patternStr string
		path       routePath
	}{
		{
			pathStr:    "/foo/bar/baz/buzz",
			patternStr: "/foo/bar/baz/buzz",
			path: routePath{
				{segment: "foo"}, {segment: "bar"}, {segment: "baz"}, {segment: "buzz"},
			},
		},
		{
			pathStr:    "/foo/bar/{name}",
			patternStr: "/foo/bar/*",
			path: routePath{
				{segment: "foo"}, {segment: "bar"},
				{variable: routePathVar{varPath: "name"}},
			},
		},
		{
			pathStr:    "/foo/bar/{name}/baz/{child}",
			patternStr: "/foo/bar/*/baz/*",
			path: routePath{
				{segment: "foo"}, {segment: "bar"},
				{variable: routePathVar{varPath: "name"}},
				{segment: "baz"},
				{variable: routePathVar{varPath: "child"}},
			},
		},
		{
			pathStr:    "/foo/bar/{name}/baz/{child.id}/buzz/{child.thing.id}",
			patternStr: "/foo/bar/*/baz/*/buzz/*",
			path: routePath{
				{segment: "foo"}, {segment: "bar"},
				{variable: routePathVar{varPath: "name"}},
				{segment: "baz"},
				{variable: routePathVar{varPath: "child.id"}},
				{segment: "buzz"},
				{variable: routePathVar{varPath: "child.thing.id"}},
			},
		},
		{
			pathStr:    "/foo/bar/*/{thing.id}/{cat=**}",
			patternStr: "/foo/bar/*/*/**",
			path: routePath{
				{segment: "foo"}, {segment: "bar"}, {segment: "*"},
				{variable: routePathVar{varPath: "thing.id"}},
				{variable: routePathVar{varPath: "cat", segments: routePath{{segment: "**"}}}},
			},
		},
		{
			pathStr:    "/foo/bar/*/{thing.id}/{cat=**}:do",
			patternStr: "/foo/bar/*/*/**:do",
			path: routePath{
				{segment: "foo"}, {segment: "bar"}, {segment: "*"},
				{variable: routePathVar{varPath: "thing.id"}},
				{variable: routePathVar{varPath: "cat", segments: routePath{{segment: "**"}}}},
				{verb: "do"},
			},
		},
		{
			pathStr:    "/foo/bar/*/{thing.id}/{cat=**}:cancel",
			patternStr: "/foo/bar/*/*/**:cancel",
			path: routePath{
				{segment: "foo"}, {segment: "bar"}, {segment: "*"},
				{variable: routePathVar{varPath: "thing.id"}},
				{variable: routePathVar{varPath: "cat", segments: routePath{{segment: "**"}}}},
				{verb: "cancel"},
			},
		},
		{
			pathStr:    "/foo/bob/{book_id={author}/{isbn}/*}/details",
			patternStr: "/foo/bob/*/*/*/details",
			path: routePath{
				{segment: "foo"}, {segment: "bob"},
				{variable: routePathVar{varPath: "book_id", segments: routePath{
					{variable: routePathVar{varPath: "author"}},
					{variable: routePathVar{varPath: "isbn"}},
					{segment: "*"},
				}}},
				{segment: "details"},
			},
		},
		{
			pathStr:    "/foo/blah/{longest_var={long_var.a={medium.a={short.aa}/*/{short.ab}/foo}/*}/{long_var.b={medium.b={short.ba}/*/{short.bb}/foo}/{last=**}}}:details",
			patternStr: "/foo/blah/*/*/*/foo/*/*/*/*/foo/**:details",
			path: routePath{
				{segment: "foo"}, {segment: "blah"},
				{variable: routePathVar{varPath: "longest_var", segments: routePath{
					{variable: routePathVar{varPath: "long_var.a", segments: routePath{
						{variable: routePathVar{varPath: "medium.a", segments: routePath{
							{variable: routePathVar{varPath: "short.aa"}},
							{segment: "*"},
							{variable: routePathVar{varPath: "short.ab"}},
							{segment: "foo"},
						}}},
						{segment: "*"},
					}}},
					{variable: routePathVar{varPath: "long_var.b", segments: routePath{
						{variable: routePathVar{varPath: "medium.b", segments: routePath{
							{variable: routePathVar{varPath: "short.ba"}},
							{segment: "*"},
							{variable: routePathVar{varPath: "short.bb"}},
							{segment: "foo"},
						}}},
						{variable: routePathVar{varPath: "last", segments: routePath{
							{segment: "**"},
						}}},
					}}},
				}}},
				{verb: "details"},
			},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.pathStr, func(t *testing.T) {
			t.Parallel()
			parsedPath, err := parsePathTemplate(testCase.pathStr)
			require.NoError(t, err)
			assert.Equal(t, testCase.path, parsedPath)
			assert.Equal(t, testCase.pathStr, testCase.path.String())
			assert.Equal(t, testCase.patternStr, testCase.path.PatternString())
		})
	}
}

func TestRoutePath_ParsePathTemplate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		path        string
		resultPath  string
		expectedErr string
	}{
		{
			path:       "/foo=~bar~/a_b_c[xyz]&123\\456/%7c_%3f_%3e/`a|b|c`/\"d,e,f\"/<'abc'>/r.t.f/j#j/k$k/l@l/a!a/s^s/(x-y-z)/{var={abc}/{def=**}}:baz",
			resultPath: "/foo%3D~bar~/a_b_c%5Bxyz%5D%26123%5C456/%7C_%3F_%3E/%60a%7Cb%7Cc%60/%22d%2Ce%2Cf%22/%3C%27abc%27%3E/r.t.f/j%23j/k%24k/l%40l/a%21a/s%5Es/%28x-y-z%29/{var={abc}/{def=**}}:baz",
			// no error, but lots of encoding for special/reserved characters
		},
		{
			path:       "/%66%6f%6f/%30%39%41%5a%61%7a/%5f%2D%2e%7e%25",
			resultPath: "/foo/09AZaz/_-.~%25",
			// no error, but we normalize unnecessary percent-encoded characters
		},
		{
			path:        "/foo/bar/baz?abc=def",
			expectedErr: "syntax error at column 13: expecting EOF", // no query string allowed
		},
		{
			path:        "/foo/bar/baz buzz",
			expectedErr: "syntax error at column 13: expecting EOF", // no whitespace allowed
		},
		{
			path:        "foo/bar/baz",
			expectedErr: "syntax error at column 1: expecting '/'", // must start with slash
		},
		{
			path:        "/foo/bar/",
			expectedErr: "syntax error at column 10: expecting path component literal", // must not end in slash
		},
		{
			path:        "/foo/bar:baz/buzz",
			expectedErr: "syntax error at column 13: expecting EOF", // ":baz" verb can only come at the very end
		},
		{
			path:        "/foo/{bar/baz}/buzz",
			expectedErr: "syntax error at column 10: expecting '}'", // invalid field path
		},
		{
			path:       "/foo/bar:baz%12xyz%abcde",
			resultPath: "/foo/bar:baz%12xyz%ABcde",
			// no error, but we capitalize URL-encoded chars
		},
		{
			path:        "/foo/bar%55:baz%1",
			expectedErr: "syntax error at column 16: malformed URL-encoded character",
		},
		{
			path:        "/foo/bar*",
			expectedErr: "syntax error at column 9: expecting EOF", // wildcard must be entire path component
		},
		{
			path:        "/foo/bar/***",
			expectedErr: "syntax error at column 12: expecting EOF", // no such thing as triple-wildcard
		},
		{
			path:        "/foo/**/bar",
			expectedErr: "double wildcard '**' must be final path component", // double-wildcard must be at end
		},
		{
			path:        "/foo/bar/*:*",
			expectedErr: "syntax error at column 12: expecting path component literal", // wildcard not allowed as verb
		},
		{
			path:       "/foo%2fbar%2F*",
			resultPath: "/foo/bar/*", // url-encoded slashes interpreted as normal slashes
		},
		{
			path:        "/foo/bar/baz\003\002\177",
			expectedErr: "syntax error at column 13: expecting EOF", // control chars not allowed
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()
			parsed, err := parsePathTemplate(testCase.path)
			if testCase.expectedErr != "" {
				require.ErrorContains(t, err, testCase.expectedErr)
				t.Log(err)
			} else {
				require.NoError(t, err)
				expectString := testCase.resultPath
				if expectString == "" {
					expectString = testCase.path
				}
				require.Equal(t, expectString, parsed.String())
			}
		})
	}
}
