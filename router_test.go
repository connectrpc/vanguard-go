// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestRouteTrie_Insert(t *testing.T) {
	r := initRouter(t)

	// TODO: verify properties of the constructed trie to make sure it looks correct

	// Inserting redundant rules returns existing target
	for _, route := range routes {
		target := &routeTarget{config: &methodConfig{descriptor: &fakeMethodDescriptor{name: fmt.Sprintf("%s %s", http.MethodGet, route)}}}
		varsIndex := varsIndex{}
		err := r.indexVars(target, route, varsIndex, map[string]struct{}{})
		require.NoError(t, err)
		existing, err := r.root.insert(newStack(route), nil, http.MethodGet, target, varsIndex)
		require.NoError(t, err)
		require.NotNil(t, existing)
		require.NotSame(t, existing, target)
		require.Equal(t, existing, target)
	}
}

func TestRouteTrie_FindTarget(t *testing.T) {
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
	}

	trie := &initRouter(t).root

	for _, testCase := range testCases {
		uri := "/" + strings.Join(testCase.path, "/")
		if testCase.verb != "" {
			uri += ":" + testCase.verb
		}
		t.Run(uri, func(t *testing.T) {
			var present, absent []string
			if testCase.expectedPath != "" {
				present = []string{http.MethodGet, http.MethodPost}
				absent = []string{http.MethodDelete, http.MethodPut}
			} else {
				absent = []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPut}
			}
			for _, method := range present {
				t.Run(method, func(t *testing.T) {
					target, vars := trie.findTarget("", testCase.path, testCase.verb, method, varAccumulator{})
					require.NotNil(t, target)
					require.Equal(t, protoreflect.Name(fmt.Sprintf("%s %s", method, testCase.expectedPath)), target.config.descriptor.Name())
					require.Equal(t, len(testCase.expectedVars), len(vars))
					for k, v := range vars {
						varPath := target.vars[k]
						names := make([]string, len(varPath))
						for i := range varPath {
							names[i] = string(varPath[i].Name())
						}
						name := strings.Join(names, ".")
						require.Equal(t, testCase.expectedVars[name], v)
					}
				})
			}
			for _, method := range absent {
				t.Run(method, func(t *testing.T) {
					target, vars := trie.findTarget("", testCase.path, testCase.verb, method, varAccumulator{})
					require.Nil(t, target)
					require.Nil(t, vars)
				})
			}
		})
	}
}

var routes = []routePath{
	// /foo/bar/baz/buzz
	{
		{segment: "foo"}, {segment: "bar"}, {segment: "baz"}, {segment: "buzz"},
	},
	// /foo/bar/{name}
	{
		{segment: "foo"}, {segment: "bar"},
		{variable: routePathVar{varPath: "name"}},
	},
	// /foo/bar/{name}/baz/{child}
	{
		{segment: "foo"}, {segment: "bar"},
		{variable: routePathVar{varPath: "name"}},
		{segment: "baz"},
		{variable: routePathVar{varPath: "child"}},
	},
	// /foo/bar/{name}/baz/{child.id}/buzz/{child.thing.id}
	{
		{segment: "foo"}, {segment: "bar"},
		{variable: routePathVar{varPath: "name"}},
		{segment: "baz"},
		{variable: routePathVar{varPath: "child.id"}},
		{segment: "buzz"},
		{variable: routePathVar{varPath: "child.thing.id"}},
	},
	// /foo/bar/*/{thing.id}/{cat=**}
	{
		{segment: "foo"}, {segment: "bar"}, {segment: "*"},
		{variable: routePathVar{varPath: "thing.id"}},
		{variable: routePathVar{varPath: "cat", segments: routePath{{segment: "**"}}}},
	},
	// /foo/bar/*/{thing.id}/{cat=**}:do
	{
		{segment: "foo"}, {segment: "bar"}, {segment: "*"},
		{variable: routePathVar{varPath: "thing.id"}},
		{variable: routePathVar{varPath: "cat", segments: routePath{{segment: "**"}}}},
		{verb: "do"},
	},
	// /foo/bar/*/{thing.id}/{cat=**}:cancel
	{
		{segment: "foo"}, {segment: "bar"}, {segment: "*"},
		{variable: routePathVar{varPath: "thing.id"}},
		{variable: routePathVar{varPath: "cat", segments: routePath{{segment: "**"}}}},
		{verb: "cancel"},
	},
	// /foo/bob/{book_id={author}/{isbn}/*}/details
	{
		{segment: "foo"}, {segment: "bob"},
		{variable: routePathVar{varPath: "book_id", segments: routePath{
			{variable: routePathVar{varPath: "author"}},
			{variable: routePathVar{varPath: "isbn"}},
			{segment: "*"},
		}}},
		{segment: "details"},
	},
}

func initRouter(t *testing.T) *router {
	t.Helper()

	var r router
	for _, route := range routes {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			target := &routeTarget{config: &methodConfig{descriptor: &fakeMethodDescriptor{name: fmt.Sprintf("%s %s", method, route)}}}
			varsIndex := varsIndex{}
			err := r.indexVars(target, route, varsIndex, map[string]struct{}{})
			require.NoError(t, err)
			existing, err := r.root.insert(newStack(route), nil, method, target, varsIndex)
			require.NoError(t, err)
			require.Nil(t, existing)
		}
	}
	return &r
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
