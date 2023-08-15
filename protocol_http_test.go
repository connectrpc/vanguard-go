// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	testv1 "github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestHTTPErrorWriter(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("test error: %s", "Hello, 世界")
	cerr := connect.NewWireError(connect.CodeUnauthenticated, err)
	rec := httptest.NewRecorder()
	httpWriteError(rec, cerr)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "identity", rec.Header().Get("Content-Encoding"))
	var out bytes.Buffer
	assert.NoError(t, json.Compact(&out, rec.Body.Bytes()))
	assert.Equal(t, `{"code":16,"message":"test error: Hello, 世界","details":[]}`, out.String())

	body := bytes.NewReader(rec.Body.Bytes())
	got := httpErrorFromResponse(body)
	assert.Equal(t, cerr, got)
}

func TestHTTPEncodePathValues(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input        proto.Message
		tmpl         string
		reqFieldPath string
		wantPath     string
		wantQuery    url.Values
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
		wantQuery:    url.Values{},
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
	}, {
		// Single capture should be escaped for the URL.
		input:     &testv1.ParameterValues{StringValue: "/世界"},
		tmpl:      "/v1/{string_value=*}:get",
		wantPath:  "/v1/%2F%E4%B8%96%E7%95%8C:get",
		wantQuery: url.Values{},
	}, {
		// Single capture should be escaped for the URL.
		input:     &testv1.ParameterValues{StringValue: "%2F%2f 世界"},
		tmpl:      "/v1/{string_value=*}:get",
		wantPath:  "/v1/%252F%252f%20%E4%B8%96%E7%95%8C:get",
		wantQuery: url.Values{},
	}, {
		// Multi capture variables should be escaped for the URL.
		input:     &testv1.ParameterValues{StringValue: "books/%2F%2f 世界"},
		tmpl:      "/v1/{string_value=books/*}:get",
		wantPath:  "/v1/books/%2F%2F%20%E4%B8%96%E7%95%8C:get",
		wantQuery: url.Values{},
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

			path, query, err := httpEncodePathValues(input, target)
			if err != nil {
				assert.Equal(t, testCase.wantErr, err.Error())
				return
			}
			require.NoError(t, err)

			assert.Equal(t, testCase.wantPath, path)
			assert.Equal(t, testCase.wantQuery, query)
		})
	}
}
