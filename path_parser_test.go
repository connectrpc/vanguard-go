// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{
			desc: &fakeMethodDescriptor{
				in: &fakeMessageDescriptor{
					fields: &fakeFieldDescriptors{
						fields: map[protoreflect.Name]protoreflect.FieldDescriptor{
							"var": &fakeFieldDescriptor{
								name: "var",
							},
							"abc": &fakeFieldDescriptor{
								name: "abc",
							},
							"def": &fakeFieldDescriptor{
								name: "def",
							},
						},
					},
				},
			},
			// no error, but lots of encoding for special/reserved characters
			path: "/my%2Fcool+blog&about%2Cstuff%5Bwat%5D/{var={abc}/{def=**}}:baz",
			segments: pathSegments{
				path: []string{"my/cool+blog&about,stuff[wat]", "*", "**"},
				verb: "baz",
			},
			variables: pathVariables{
				{fields: []protoreflect.FieldDescriptor{
					&fakeFieldDescriptor{name: "var"},
				}, start: 1, end: -1},
				{fields: []protoreflect.FieldDescriptor{
					&fakeFieldDescriptor{name: "abc"},
				}, start: 1, end: 2},
				{fields: []protoreflect.FieldDescriptor{
					&fakeFieldDescriptor{name: "def"},
				}, start: 2, end: -1},
			},
		},
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
			expectedErr: "syntax error at column 9: expected path value", // must not end in slash
		},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "/foo/bar:baz/buzz",
			expectedErr: "syntax error at column 13: unexpected '/'", // ":baz" verb can only come at the very end
		},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "/foo/{bar/baz}/buzz",
			expectedErr: "syntax error at column 10: expected '}', got '/'", // invalid field path
		},
		{
			desc: &fakeMethodDescriptor{},
			path: "/foo/bar:baz%12xyz%abcde",
			segments: pathSegments{
				path: []string{"foo", "bar"},
				verb: "baz\x12xyz\xabcde",
			},
		},
		{
			desc: &fakeMethodDescriptor{
				in: &fakeMessageDescriptor{
					fields: &fakeFieldDescriptors{
						fields: map[protoreflect.Name]protoreflect.FieldDescriptor{
							"hello": &fakeFieldDescriptor{
								name: "hello",
							},
						},
					},
				},
			},
			path: "/{hello}/world",
			segments: pathSegments{
				path: []string{"*", "world"},
			},
			variables: pathVariables{
				{fields: []protoreflect.FieldDescriptor{
					&fakeFieldDescriptor{name: "hello"},
				}, start: 0, end: 1},
			},
		},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "/foo/bar%55:baz%1",
			expectedErr: "syntax error at column 17: invalid URL escape \"%1\"",
		},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "/foo/bar*",
			expectedErr: "syntax error at column 9: unexpected '*'", // wildcard must be entire path component
		},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "/foo/bar/***",
			expectedErr: "syntax error at column 12: unexpected '*'", // no such thing as triple-wildcard
		},
		{
			desc:        &fakeMethodDescriptor{},
			path:        "/foo/**/bar",
			expectedErr: "double wildcard '**' must be the final path segment", // double-wildcard must be at end
		},
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
			t.Log(segments)
			require.NoError(t, err)
			assert.ElementsMatch(t, testCase.segments.path, segments.path, "path mismatch")
			assert.Equal(t, testCase.segments.verb, segments.verb, "verb mismatch")
			assert.ElementsMatch(t, testCase.variables, variables, "variables mismatch")
		})
	}
}
