// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestRoutePath_ParsePathTemplate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		desc        protoreflect.MethodDescriptor
		path        string
		segments    pathSegments
		variables   pathVariables
		expectedErr string
	}{
		//{
		//	desc: &fakeMethodDescriptor{
		//		in: &fakeMessageDescriptor{
		//			fields: map[string]protoreflect.FieldDescriptor{
		//				"foo": &fakeFieldDescriptor{},
		//			},
		//		},
		//	},
		//	path:       "/foo=~bar~/a_b_c[xyz]&123\\456/%7c_%3f_%3e/`a|b|c`/\"d,e,f\"/<'abc'>/r.t.f/j#j/k$k/l@l/a!a/s^s/(x-y-z)/{var={abc}/{def=**}}:baz",
		//	resultPath: "/foo%3D~bar~/a_b_c%5Bxyz%5D%26123%5C456/%7C_%3F_%3E/%60a%7Cb%7Cc%60/%22d%2Ce%2Cf%22/%3C%27abc%27%3E/r.t.f/j%23j/k%24k/l%40l/a%21a/s%5Es/%28x-y-z%29/{var={abc}/{def=**}}:baz",
		//	// no error, but lots of encoding for special/reserved characters
		//},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "/foo/bar/baz?abc=def",
			expectedErr: "syntax error at column 13: unexpected '?'", // no query string allowed
		},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "/foo/bar/baz buzz",
			expectedErr: "syntax error at column 13: unexpected ' '", // no whitespace allowed
		},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "foo/bar/baz",
			expectedErr: "syntax error at column 1: expected '/', got 'f'", // must start with slash
		},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "/foo/bar/",
			expectedErr: "syntax error at column 10: expected path component literal", // must not end in slash
		},
		//{
		//	desc:        &fakeMethodDescriptor{},
		//	path:        "/foo/bar:baz/buzz",
		//	expectedErr: "syntax error at column 13: expecting EOF", // ":baz" verb can only come at the very end
		//},
		//{
		//	desc:        &fakeMethodDescriptor{},
		//	path:        "/foo/{bar/baz}/buzz",
		//	expectedErr: "syntax error at column 10: expecting '}'", // invalid field path
		//},
		//{
		//	desc: &fakeMethodDescriptor{},
		//	path: "/foo/bar:baz%12xyz%abcde",
		//	//resultPath: "/foo/bar:baz%12xyz%ABcde",
		//	// no error
		//},
		//{
		//	desc:        &fakeMethodDescriptor{},
		//	path:        "/foo/bar%55:baz%1",
		//	expectedErr: "syntax error at column 16: malformed URL-encoded character",
		//},
		//{
		//	desc:        &fakeMethodDescriptor{},
		//	path:        "/foo/bar*",
		//	expectedErr: "syntax error at column 9: expecting EOF", // wildcard must be entire path component
		//},
		//{
		//	desc:        &fakeMethodDescriptor{},
		//	path:        "/foo/bar/***",
		//	expectedErr: "syntax error at column 12: expecting EOF", // no such thing as triple-wildcard
		//},
		//{
		//	desc:        &fakeMethodDescriptor{},
		//	path:        "/foo/**/bar",
		//	expectedErr: "double wildcard '**' must be final path component", // double-wildcard must be at end
		//},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()
			segments, variables, err := parsePathTemplate(testCase.desc, testCase.path)
			if testCase.expectedErr != "" {
				assert.ErrorContains(t, err, testCase.expectedErr)
				return
			}
			assert.NoError(t, err)
			assert.ElementsMatch(t, testCase.segments, segments)
			assert.ElementsMatch(t, testCase.variables, variables)
		})
	}
}
