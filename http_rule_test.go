// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"net/url"
	"testing"

	testv1 "github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestHTTPRule_EncodeMessage(t *testing.T) {
	t.Parallel()

	type pathVar struct {
		fieldPath  string
		start, end int
	}

	testCases := []struct {
		input        proto.Message
		tmpl         string
		path         []string
		verb         string
		reqFieldPath string
		vars         []pathVar
		wantPath     string
		wantQuery    url.Values
		wantBody     proto.Message
		wantErr      string
	}{{
		input: &testv1.ParameterValues{StringValue: "books/1"},
		tmpl:  "/v1/{string_value=books/*}:get",
		path:  []string{"v1", "books", "*"},
		verb:  "get",
		vars: []pathVar{
			{fieldPath: "string_value", start: 1, end: 3},
		},
		wantPath:  "/v1/books/1:get",
		wantQuery: url.Values{},
	}, {
		input: &testv1.ParameterValues{StringValue: "books/1/2/3/4"},
		tmpl:  "/v1/{string_value=books/**}:get",
		path:  []string{"v1", "books", "**"},
		verb:  "get",
		vars: []pathVar{
			{fieldPath: "string_value", start: 1, end: -1},
		},
		wantPath:  "/v1/books/1/2/3/4:get",
		wantQuery: url.Values{},
	}, {
		input: &testv1.ParameterValues{
			StringValue: "books/1",
			Recursive: &testv1.ParameterValues{
				StringValue: "Title",
			},
		},
		tmpl:         "/v1/{string_value=books/*}:create",
		path:         []string{"v1", "books", "*"},
		verb:         "create",
		reqFieldPath: "recursive",
		vars: []pathVar{
			{fieldPath: "string_value", start: 1, end: -1},
		},
		wantPath: "/v1/books/1:create",
		wantBody: &testv1.ParameterValues{
			StringValue: "Title",
		},
		wantQuery: url.Values{},
	}, {
		input: &testv1.ParameterValues{
			StringValue: "books/1",
			Recursive: &testv1.ParameterValues{
				StringValue: "Title",
			},
			Nested: &testv1.ParameterValues_Nested{
				EnumValue: testv1.ParameterValues_Nested_ENUM_VALUE,
			},
			BoolValue: true,
			DoubleList: []float64{
				1.0,
				2.0,
				3.0,
			},
		},
		tmpl:         "/v2/{string_value=books/*}/{double_list}:create",
		path:         []string{"v2", "books", "*", "*"},
		verb:         "query",
		reqFieldPath: "recursive",
		vars: []pathVar{
			{fieldPath: "string_value", start: 1, end: 3},
			{fieldPath: "double_list", start: 3, end: 4},
		},
		wantPath: "/v2/books/1/1:query",
		wantBody: &testv1.ParameterValues{
			StringValue: "Title",
		},
		wantQuery: url.Values{
			"bool_value":        []string{"true"},
			"nested.enum_value": []string{"ENUM_VALUE"},
			"double_list":       []string{"2", "3"},
		},
	}, {
		// TODO: should we support list values as path segments?
		input: &testv1.ParameterValues{
			DoubleList: []float64{
				1.0,
				2.0,
				3.0,
			},
		},
		tmpl: "/v2/{double_list=**}",
		path: []string{"v2", "**"},
		vars: []pathVar{
			{fieldPath: "double_list", start: 1, end: -1},
		},
		wantPath: "/v2/1",
		wantQuery: url.Values{
			"double_list": []string{"2", "3"},
		},
	}}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.tmpl, func(t *testing.T) {
			t.Parallel()
			// TODO(emcfarlane): fix this
			// routePath, err := parsePathTemplate(testCase.tmpl)
			// require.NoError(t, err)

			input := testCase.input.ProtoReflect()
			fields, err := resolvePathToDescriptors(input.Descriptor(), testCase.reqFieldPath)
			require.NoError(t, err)

			var vars []routeTargetVar
			for _, variable := range testCase.vars {
				fields, err := resolvePathToDescriptors(input.Descriptor(), variable.fieldPath)
				require.NoError(t, err)
				vars = append(vars, routeTargetVar{
					varPath: fields,
					start:   variable.start,
					end:     variable.end,
				})
			}

			target := &routeTarget{
				path:            testCase.path,
				verb:            testCase.verb,
				requestBodyPath: fields,
				vars:            vars,
			}

			path, query, body, err := encodeMessageAsHTTPRule(input, target)
			if err != nil {
				assert.Equal(t, testCase.wantErr, err.Error())
				return
			}
			require.NoError(t, err)

			assert.Equal(t, testCase.wantPath, path)
			assert.Equal(t, testCase.wantQuery, query)
			if testCase.wantBody != nil {
				require.NotNil(t, body)
				assert.Empty(t, cmp.Diff(testCase.wantBody, body.Interface(), protocmp.Transform()))
			}
		})
	}
}
