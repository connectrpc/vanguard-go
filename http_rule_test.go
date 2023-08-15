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

	testCases := []struct {
		input        proto.Message
		tmpl         string
		reqFieldPath string
		wantPath     string
		wantQuery    url.Values
		wantBody     proto.Message
		wantErr      string
	}{{
		input:     &testv1.ParameterValues{StringValue: "books/1"},
		tmpl:      "/v1/{string_value=books/*}:get",
		wantPath:  "/v1/books/1:get",
		wantQuery: url.Values{},
	}, {
		input:     &testv1.ParameterValues{StringValue: "books/1/2/3/4"},
		tmpl:      "/v1/{string_value=books/**}:get",
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
		reqFieldPath: "recursive",
		wantPath:     "/v1/books/1:create",
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
		reqFieldPath: "recursive",
		wantPath:     "/v2/books/1/1:create",
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
		tmpl:     "/v2/{double_list=**}",
		wantPath: "/v2/1",
		wantQuery: url.Values{
			"double_list": []string{"2", "3"},
		},
	}, {
		// Map fields are not supported as URL values.
		input: &testv1.ParameterValues{
			StringMap: map[string]string{"key1": "value1"},
		},
		tmpl:    "/v2/mapfields",
		wantErr: "unexpected field string_map: cannot be URL encoded",
	}, {
		input: &testv1.ParameterValues{
			Recursive: &testv1.ParameterValues{
				StringMap: map[string]string{"key1": "value1"},
			},
		},
		tmpl:    "/v2/mapfields",
		wantErr: "unexpected field recursive.string_map: cannot be URL encoded",
	}, {
		// Repeated message fields are not supported as URL values.
		input: &testv1.ParameterValues{
			RecursiveList: []*testv1.ParameterValues{
				{StringValue: "value2"},
			},
		},
		tmpl:    "/v2/repeatedmsgs",
		wantErr: "unexpected field recursive_list: cannot be URL encoded",
	}}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.tmpl, func(t *testing.T) {
			t.Parallel()
			segments, variables, err := parsePathTemplate(testCase.tmpl)
			require.NoError(t, err)
			config := &methodConfig{
				descriptor: &fakeMethodDescriptor{
					name: testCase.tmpl,
					in:   testCase.input.ProtoReflect().Descriptor(),
				},
			}
			input := testCase.input.ProtoReflect()
			target, err := makeTarget(config, "POST", testCase.reqFieldPath, "*", segments, variables)
			require.NoError(t, err)

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
