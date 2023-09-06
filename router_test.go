// Copyright 2023 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
		path         string
		expectedPath string // if blank, path is expected to NOT match
		expectedVars map[string]string
	}{
		{
			path: "/bob/lob/law",
		},
		{
			path:         "/foo/bar/baz",
			expectedPath: "/foo/bar/{name}",
			expectedVars: map[string]string{"name": "baz"},
		},
		{
			path: "/foo/bob/lob/law",
		},
		{
			path:         "/foo/bar/baz/buzz",
			expectedPath: "/foo/bar/baz/buzz",
		},
		{
			path:         "/foo/bar/baz/baz/buzz",
			expectedPath: "/foo/bar/{name}/baz/{child}",
			expectedVars: map[string]string{"name": "baz", "child": "buzz"},
		},
		{
			path:         "/foo/bar/1/baz/2/buzz/3",
			expectedPath: "/foo/bar/{name}/baz/{child.id}/buzz/{child.thing.id}",
			expectedVars: map[string]string{"name": "1", "child.id": "2", "child.thing.id": "3"},
		},
		{
			path: "/foo/bar/baz/123",
		},
		{
			path:         "/foo/bar/baz/123/buzz",
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}",
			expectedVars: map[string]string{"thing.id": "123", "cat": "buzz"},
		},
		{
			path:         "/foo/bar/baz/123/buzz/buzz",
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}",
			expectedVars: map[string]string{"thing.id": "123", "cat": "buzz/buzz"},
		},
		{
			path:         "/foo/bar/baz/123/buzz/buzz:do",
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}:do",
			expectedVars: map[string]string{"thing.id": "123", "cat": "buzz/buzz"},
		},
		{
			path:         "/foo/bar/baz/123/fizz/buzz/frob/nitz:do",
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}:do",
			expectedVars: map[string]string{"thing.id": "123", "cat": "fizz/buzz/frob/nitz"},
		},
		{
			path:         "/foo/bar/baz/123/buzz/buzz:cancel",
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}:cancel",
			expectedVars: map[string]string{"thing.id": "123", "cat": "buzz/buzz"},
		},
		{
			path: "foo/bar/baz/123/buzz/buzz:blah",
		},
		{
			path:         "/foo/bob/bar/baz/123/details",
			expectedPath: "/foo/bob/{book_id={author}/{isbn}/*}/details",
			expectedVars: map[string]string{"book_id": "bar/baz/123", "author": "bar", "isbn": "baz"},
		},
		{
			path: "/foo/bob/bar/baz/123/details:do",
		},
		{
			path:         "/foo/blah/A/B/C/foo/D/E/F/G/foo/H/I/J/K/L/M:details",
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
		{
			// No trailing slash in the path, so this should not match.
			path: "/trailing:slash",
		},
		{
			// Trailing slash in the path, so this should match.
			path:         "/trailing/:slash",
			expectedPath: "/trailing/**:slash",
		},
		{
			// Trailing verb, should not match.
			path: "/verb:",
		},
		{
			// No trailing verb, should match.
			path:         "/verb",
			expectedPath: "/verb",
		},
		{
			// Var capture use path unescaping.
			path:         "/foo/bar/baz/%2f/%2A/%2f",
			expectedPath: "/foo/bar/*/{thing.id}/{cat=**}",
			expectedVars: map[string]string{"thing.id": "/", "cat": "*/%2F"},
		},
	}

	trie := initTrie(t)

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.path, func(t *testing.T) {
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
					target, vars, _ := trie.match(testCase.path, method)
					require.NotNil(t, target)
					require.Equal(t, protoreflect.Name(fmt.Sprintf("%s %s", method, testCase.expectedPath)), target.config.descriptor.Name())
					require.Equal(t, len(testCase.expectedVars), len(vars))
					for _, varMatch := range vars {
						names := make([]string, len(varMatch.fields))
						for i, fld := range varMatch.fields {
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
					target, _, _ := trie.match(testCase.path, method)
					require.Nil(t, target)
				})
			}
		})
	}
}

func BenchmarkTrieMatch(b *testing.B) {
	trie := initTrie(b)
	path := "/foo/blah/A/B/C/foo/D/E/F/G/foo/H/I/J/K/L/M:details"
	var (
		method *routeTarget
		vars   []routeTargetVarMatch
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		method, vars, _ = trie.match(path, http.MethodPost)
		if method == nil {
			b.Fatal("method not found")
		}
	}
	b.StopTimer()
	assert.NotNil(b, method)
	assert.Len(b, vars, 10)
}

//nolint:gochecknoglobals
var routes = []string{
	"/foo/bar/baz/buzz",
	"/foo/bar/{name}",
	"/foo/bar/{name}/baz/{child}",
	"/foo/bar/{name}/baz/{child.id}/buzz/{child.thing.id}",
	"/foo/bar/*/{thing.id}/{cat=**}",
	"/foo/bar/*/{thing.id}/{cat=**}:do",
	"/foo/bar/*/{thing.id}/{cat=**}:cancel",
	"/foo/bob/{book_id={author}/{isbn}/*}/details",
	"/foo/blah/{longest_var={long_var.a={medium.a={short.aa}/*/{short.ab}/foo}/*}/{long_var.b={medium.b={short.ba}/*/{short.bb}/foo}/{last=**}}}:details",
	"/foo%2Fbar/%2A/%2A%2a/{starstar=%2A%2a/**}:%2c",
	"/trailing/**:slash",
	"/verb",
}

func initTrie(tb testing.TB) *routeTrie {
	tb.Helper()
	var trie routeTrie
	for _, route := range routes {
		segments, variables, err := parsePathTemplate(route)
		require.NoError(tb, err)

		for _, method := range []string{http.MethodGet, http.MethodPost} {
			config := &methodConfig{
				descriptor: &fakeMethodDescriptor{
					name: fmt.Sprintf("%s %s", method, route),
				},
			}
			target, err := makeTarget(config, "POST", "*", "*", segments, variables)
			require.NoError(tb, err)
			err = trie.insert(method, target, segments)
			require.NoError(tb, err)
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
	kind protoreflect.Kind
	protoreflect.FieldDescriptor
}

func (f *fakeFieldDescriptor) Name() protoreflect.Name {
	return f.name
}

func (f *fakeFieldDescriptor) Cardinality() protoreflect.Cardinality {
	return protoreflect.Optional
}

func (f *fakeFieldDescriptor) Kind() protoreflect.Kind {
	if f.kind > 0 {
		return f.kind
	}
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
func (f *fakeFieldDescriptor) IsList() bool {
	return false
}
