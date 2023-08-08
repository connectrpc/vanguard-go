// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestRouteTrie_Insert(t *testing.T) {
	t.Parallel()
	_ = initTrie(t)
}

func TestRouteTrie_FindTarget(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		path         []string
		verb         string
		expectedPath string // if blank, path is expected to NOT match
		expectedVars map[string]string
	}{
		{
			path: []string{"bob", "lob", "law"},
		},
		{
			path:         []string{"foo", "bar", "baz"},
			expectedPath: "/foo/bar/{name}",
			expectedVars: map[string]string{"name": "baz"},
		},
		{
			path: []string{"foo", "bob", "lob", "law"},
		},
		{
			path:         []string{"foo", "bar", "baz", "buzz"},
			expectedPath: "/foo/bar/baz/buzz",
		},
		{
			path:         []string{"foo", "bar", "baz", "baz", "buzz"},
			expectedPath: "/foo/bar/{name}/baz/{child}",
			expectedVars: map[string]string{"name": "baz", "child": "buzz"},
		},
		{
			path:         []string{"foo", "bar", "1", "baz", "2", "buzz", "3"},
			expectedPath: "/foo/bar/{name}/baz/{child.id}/buzz/{child.thing.id}",
			expectedVars: map[string]string{"name": "1", "child.id": "2", "child.thing.id": "3"},
		},
		{
			path:         []string{"foo", "bar", "baz", "123"},
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}",
			expectedVars: map[string]string{"thing.id": "123", "cat": ""},
		},
		{
			path:         []string{"foo", "bar", "baz", "123", "buzz"},
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}",
			expectedVars: map[string]string{"thing.id": "123", "cat": "buzz"},
		},
		{
			path:         []string{"foo", "bar", "baz", "123", "buzz", "buzz"},
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}",
			expectedVars: map[string]string{"thing.id": "123", "cat": "buzz/buzz"},
		},
		{
			path:         []string{"foo", "bar", "baz", "123", "buzz", "buzz"},
			verb:         "do",
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}:do",
			expectedVars: map[string]string{"thing.id": "123", "cat": "buzz/buzz"},
		},
		{
			path:         []string{"foo", "bar", "baz", "123", "fizz", "buzz", "frob", "nitz"},
			verb:         "do",
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}:do",
			expectedVars: map[string]string{"thing.id": "123", "cat": "fizz/buzz/frob/nitz"},
		},
		{
			path:         []string{"foo", "bar", "baz", "123", "buzz", "buzz"},
			verb:         "cancel",
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}:cancel",
			expectedVars: map[string]string{"thing.id": "123", "cat": "buzz/buzz"},
		},
		{
			path: []string{"foo", "bar", "baz", "123", "buzz", "buzz"},
			verb: "blah",
		},
		{
			path:         []string{"foo", "bob", "bar", "baz", "123", "details"},
			expectedPath: "/foo/bob/{book_id={author}/{isbn}/*}/details",
			expectedVars: map[string]string{"book_id": "bar/baz/123", "author": "bar", "isbn": "baz"},
		},
		{
			path: []string{"foo", "bob", "bar", "baz", "123", "details"},
			verb: "do",
		},
		{
			path:         []string{"foo", "blah", "A", "B", "C", "foo", "D", "E", "F", "G", "foo", "H", "I", "J", "K", "L", "M"},
			verb:         "details",
			expectedPath: "/foo/blah/{longest_var={long_var.a={medium.a={short.aa}/*/{short.ab}/foo}/*}/{long_var.b={medium.b={short.ba}/*/{short.bb}/foo}/{last=**}}}:details",
			expectedVars: map[string]string{
				"longest_var": "A/B/C/foo/D/E/F/G/foo/H/I/J/K/L/M",
				"long_var.a":  "A/B/C/foo/D",
				"medium.a":    "A/B/C/foo",
				"short.aa":    "A",
				"short.ab":    "C",
				"long_var.b":  "E/F/G/foo/H/I/J/K/L/M",
				"medium.b":    "E/F/G/foo",
				"short.ba":    "E",
				"short.bb":    "G",
				"last":        "H/I/J/K/L/M",
			},
		},
	}

	trie := initTrie(t)

	for _, testCase := range testCases {
		testCase := testCase
		uri := "/" + strings.Join(testCase.path, "/")
		if testCase.verb != "" {
			uri += ":" + testCase.verb
		}
		t.Run(uri, func(t *testing.T) {
			t.Parallel()
			var present, absent []string
			if testCase.expectedPath != "" {
				present = []string{http.MethodGet, http.MethodPost}
				absent = []string{http.MethodDelete, http.MethodPut}
			} else {
				absent = []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPut}
			}
			for _, method := range present {
				method := method
				t.Run(method, func(t *testing.T) {
					t.Parallel()
					target := trie.findTarget(testCase.path, testCase.verb, method)
					require.NotNil(t, target)
					require.Equal(t, protoreflect.Name(fmt.Sprintf("%s %s", method, testCase.expectedPath)), target.config.descriptor.Name())
					vars := computeVarValues(testCase.path, target)
					require.Equal(t, len(testCase.expectedVars), len(vars))
					for _, varMatch := range vars {
						names := make([]string, len(varMatch.varPath))
						for i, fld := range varMatch.varPath {
							names[i] = string(fld.Name())
						}
						name := strings.Join(names, ".")
						expectedValue, ok := testCase.expectedVars[name]
						assert.True(t, ok, name)
						require.Equal(t, expectedValue, varMatch.value, name)
					}
				})
			}
			for _, method := range absent {
				method := method
				t.Run(method, func(t *testing.T) {
					t.Parallel()
					target := trie.findTarget(testCase.path, testCase.verb, method)
					require.Nil(t, target)
				})
			}
		})
	}
}

type testRoute struct {
	path      string // source path
	segments  pathSegments
	variables pathVariables
}

//nolint:gochecknoglobals
var routes = []testRoute{
	{
		path:     "/foo/bar/baz/buzz",
		segments: pathSegments{{val: "foo"}, {val: "bar"}, {val: "baz"}, {val: "buzz"}},
	},
	{
		path:     "/foo/bar/{name}",
		segments: pathSegments{{val: "foo"}, {val: "bar"}, {val: "*"}},
		variables: pathVariables{
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "name"}}, start: 2, end: 3},
		},
	},
	{
		path:     "/foo/bar/{name}/baz/{child}",
		segments: pathSegments{{val: "foo"}, {val: "bar"}, {val: "*"}, {val: "baz"}, {val: "*"}},
		variables: pathVariables{
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "name"}}, start: 2, end: 3},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "child"}}, start: 4, end: 5},
		},
	},
	{
		path:     "/foo/bar/{name}/baz/{child.id}/buzz/{child.thing.id}",
		segments: pathSegments{{val: "foo"}, {val: "bar"}, {val: "*"}, {val: "baz"}, {val: "*"}, {val: "buzz"}, {val: "*"}},
		variables: pathVariables{
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "name"}}, start: 2, end: 3},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "child.id"}}, start: 4, end: 5},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "child.thing.id"}}, start: 6, end: 7},
		},
	},
	{
		path:     "/foo/bar/*/{thing.id}/{cat=**}",
		segments: pathSegments{{val: "foo"}, {val: "bar"}, {val: "*"}, {val: "*"}, {val: "**"}},
		variables: pathVariables{
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "thing.id"}}, start: 3, end: 4},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "cat"}}, start: 4, end: -1},
		},
	},
	{
		path:     "/foo/bar/*/{thing.id}/{cat=**}:do",
		segments: pathSegments{{val: "foo"}, {val: "bar"}, {val: "*"}, {val: "*"}, {val: "**"}, {val: "do", isVerb: true}},
		variables: pathVariables{
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "thing.id"}}, start: 3, end: 4},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "cat"}}, start: 4, end: -1},
		},
	},
	{
		path:     "/foo/bar/*/{thing.id}/{cat=**}:cancel",
		segments: pathSegments{{val: "foo"}, {val: "bar"}, {val: "*"}, {val: "*"}, {val: "**"}, {val: "cancel", isVerb: true}},
		variables: pathVariables{
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "thing.id"}}, start: 3, end: 4},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "cat"}}, start: 4, end: -1},
		},
	},
	{
		path:     "/foo/bob/{book_id={author}/{isbn}/*}/details",
		segments: pathSegments{{val: "foo"}, {val: "bob"}, {val: "*"}, {val: "*"}, {val: "*"}, {val: "details"}},
		variables: pathVariables{
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "book_id"}}, start: 2, end: 5},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "author"}}, start: 2, end: 3},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "isbn"}}, start: 3, end: 4},
		},
	},
	{
		path: "/foo/blah/{longest_var={long_var.a={medium.a={short.aa}/*/{short.ab}/foo}/*}/{long_var.b={medium.b={short.ba}/*/{short.bb}/foo}/{last=**}}}:details",
		segments: pathSegments{{val: "foo"}, {val: "blah"},
			{val: "*"},  // 2 logest_var
			{val: "*"},  // 3 long_var.a
			{val: "*"},  // 4 medium.a
			{val: "*"},  // 5 short.aa
			{val: "*"},  // 6
			{val: "*"},  // 7 short.ba
			{val: "*"},  // 8
			{val: "*"},  // 9 short.bb
			{val: "**"}, // 10
			{val: "details", isVerb: true}},
		variables: pathVariables{
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "longest_var"}}, start: 2, end: -1},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "long_var.a"}}, start: 2, end: 7},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "medium.a"}}, start: 2, end: 6},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "short.aa"}}, start: 2, end: 3},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "short.ab"}}, start: 4, end: 5},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "long_var.b"}}, start: 7, end: -1},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "medium.b"}}, start: 7, end: 11},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "short.ba"}}, start: 7, end: 8},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "short.bb"}}, start: 9, end: 10},
			{varPath: []protoreflect.FieldDescriptor{&fakeFieldDescriptor{name: "last"}}, start: 11, end: -1},
		},
	},
}

func initTrie(t *testing.T) *routeTrie {
	t.Helper()
	var trie routeTrie
	for _, route := range routes {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			target := &routeTarget{
				config: &methodConfig{
					descriptor: &fakeMethodDescriptor{
						name: fmt.Sprintf("%s %s", method, route.path),
					},
				},
				vars: route.variables,
			}
			err := trie.insert(method, target, route.segments)
			require.NoError(t, err)
		}
	}
	return &trie
}

type fakeMethodDescriptor struct {
	protoreflect.MethodDescriptor
	name    string
	in, out protoreflect.MessageDescriptor
}

func (f *fakeMethodDescriptor) Name() protoreflect.Name {
	return protoreflect.Name(f.name)
}

func (f *fakeMethodDescriptor) Input() protoreflect.MessageDescriptor {
	if f.in == nil {
		f.in = &fakeMessageDescriptor{}
	}
	return f.in
}

func (f *fakeMethodDescriptor) Output() protoreflect.MessageDescriptor {
	if f.out == nil {
		f.out = &fakeMessageDescriptor{}
	}
	return f.out
}

type fakeMessageDescriptor struct {
	protoreflect.MessageDescriptor
	fields protoreflect.FieldDescriptors
}

func (f *fakeMessageDescriptor) Fields() protoreflect.FieldDescriptors {
	if f.fields == nil {
		f.fields = &fakeFieldDescriptors{}
	}
	return f.fields
}

type fakeFieldDescriptors struct {
	protoreflect.FieldDescriptors
	fields map[protoreflect.Name]protoreflect.FieldDescriptor
}

func (f *fakeFieldDescriptors) ByName(name protoreflect.Name) protoreflect.FieldDescriptor {
	fld := f.fields[name]
	if fld == nil {
		if f.fields == nil {
			f.fields = map[protoreflect.Name]protoreflect.FieldDescriptor{}
		}
		fld = &fakeFieldDescriptor{name: name}
		f.fields[name] = fld
	}
	return fld
}

type fakeFieldDescriptor struct {
	name protoreflect.Name
	msg  protoreflect.MessageDescriptor
	protoreflect.FieldDescriptor
}

func (f *fakeFieldDescriptor) Name() protoreflect.Name {
	return f.name
}

func (f *fakeFieldDescriptor) Cardinality() protoreflect.Cardinality {
	return protoreflect.Optional
}

func (f *fakeFieldDescriptor) Kind() protoreflect.Kind {
	if f.msg != nil {
		return protoreflect.MessageKind
	}
	return protoreflect.StringKind
}

func (f *fakeFieldDescriptor) Message() protoreflect.MessageDescriptor {
	if f.msg == nil {
		f.msg = &fakeMessageDescriptor{}
	}
	return f.msg
}
