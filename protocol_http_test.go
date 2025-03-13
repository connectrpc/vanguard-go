// Copyright 2023-2025 Buf Technologies, Inc.
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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	testv1 "connectrpc.com/vanguard/internal/gen/vanguard/test/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
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
	require.NoError(t, json.Compact(&out, rec.Body.Bytes()))
	assert.JSONEq(t, `{"code":16,"message":"test error: Hello, 世界","details":[]}`, out.String())

	body := bytes.NewBuffer(rec.Body.Bytes())
	got := httpErrorFromResponse(http.StatusUnauthorized, "application/json", body)
	assert.Equal(t, cerr, got)
}

func TestHTTPErrorFromResponse(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		var body bytes.Buffer
		got := httpErrorFromResponse(http.StatusOK, "", &body)
		assert.Nil(t, got)
	})
	t.Run("jsonStatus", func(t *testing.T) {
		t.Parallel()
		errorInfo, err := anypb.New(&errdetails.ErrorInfo{
			Reason:   "user is not authorized",
			Domain:   "vanguard.connectrpc.com",
			Metadata: map[string]string{"key1": "value1"},
		})
		require.NoError(t, err)
		stat := status.Status{
			Code:    int32(connect.CodeUnauthenticated),
			Message: "auth error",
			Details: []*anypb.Any{errorInfo},
		}
		out, err := protojson.Marshal(&stat)
		require.NoError(t, err)
		got := httpErrorFromResponse(http.StatusUnauthorized, "application/json", bytes.NewBuffer(out))
		assert.Equal(t, connect.CodeUnauthenticated, got.Code())
		assert.Equal(t, "auth error", got.Message())
	})
	t.Run("invalidStatus", func(t *testing.T) {
		t.Parallel()
		body := bytes.NewBufferString("unauthorized")
		got := httpErrorFromResponse(http.StatusUnauthorized, "application/json", body)
		assert.Equal(t, connect.CodeUnauthenticated, got.Code())
		assert.Equal(t, "Unauthorized", got.Message())
		assert.Len(t, got.Details(), 1)
		value, err := got.Details()[0].Value()
		require.NoError(t, err)
		httpBody, ok := value.(*httpbody.HttpBody)
		assert.True(t, ok)
		assert.Equal(t, "application/json", httpBody.GetContentType())
		assert.Equal(t, []byte("unauthorized"), httpBody.GetData())
	})
	t.Run("invalidAny", func(t *testing.T) {
		t.Parallel()
		stat := status.Status{
			Code:    int32(connect.CodeUnauthenticated),
			Message: "auth error",
		}
		out, err := protojson.Marshal(&stat)
		require.NoError(t, err)
		out = append(out[:len(out)-1], []byte(`,"details":{"@type":"foo","value":"bar"}`)...)
		got := httpErrorFromResponse(http.StatusUnauthorized, "application/json", bytes.NewBuffer(out))
		t.Log(got)
		assert.Equal(t, connect.CodeUnauthenticated, got.Code())
		assert.Equal(t, "Unauthorized", got.Message())
		assert.Len(t, got.Details(), 1)
		value, err := got.Details()[0].Value()
		require.NoError(t, err)
		httpBody, ok := value.(*httpbody.HttpBody)
		assert.True(t, ok)
		assert.Equal(t, "application/json", httpBody.GetContentType())
		assert.Equal(t, out, httpBody.GetData())
	})
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
		wantErr:      "unexpected path variable \"double_list\": cannot be a repeated field",
	}, {
		input: &testv1.ParameterValues{
			DoubleList: []float64{
				1.0,
				2.0,
				3.0,
			},
		},
		tmpl:    "/v2/{double_list=**}",
		wantErr: "unexpected path variable \"double_list\": cannot be a repeated field",
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
	}, {
		// Non capture variables should be left as is.
		input:     &testv1.ParameterValues{},
		tmpl:      "/v2/*",
		wantPath:  "/v2/*",
		wantQuery: url.Values{},
	}, {
		// Same with multicatpure.
		input:     &testv1.ParameterValues{},
		tmpl:      "/v2/**",
		wantPath:  "/v2/**",
		wantQuery: url.Values{},
	}, {
		input:        &testv1.ParameterValues{StringValue: "books/1"},
		reqFieldPath: "unknownQueryParam",
		tmpl:         "/v1/{string_value=books/*}:get",
		wantPath:     "/v1/books/1:get",
		wantQuery:    url.Values{},
		wantErr:      "unknown field in field path \"unknownQueryParam\": element \"unknownQueryParam\" does not correspond to any field of type vanguard.test.v1.ParameterValues",
	}}
	for _, testCase := range testCases {
		t.Run(testCase.tmpl, func(t *testing.T) {
			t.Parallel()
			segments, variables, err := parsePathTemplate(testCase.tmpl)
			if err != nil {
				assert.Equal(t, testCase.wantErr, err.Error())
				return
			}
			require.NoError(t, err)
			config := &methodConfig{
				descriptor: &fakeMethodDescriptor{
					name: testCase.tmpl,
					in:   testCase.input.ProtoReflect().Descriptor(),
				},
			}
			input := testCase.input.ProtoReflect()
			target, err := makeTarget(config, "POST", testCase.reqFieldPath, "*", segments, variables)
			if err != nil {
				assert.Equal(t, testCase.wantErr, err.Error())
				return
			}
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
