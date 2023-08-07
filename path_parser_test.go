// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoutePath_String(t *testing.T) {
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
			assert.Equal(t, testCase.pathStr, testCase.path.String())
			assert.Equal(t, testCase.patternStr, testCase.path.PatternString())
		})
	}
}
