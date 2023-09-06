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
	"reflect"
	"testing"

	testv1 "github.com/bufbuild/vanguard-go/internal/gen/buf/vanguard/test/v1"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestJSONStabilize(t *testing.T) {
	t.Parallel()
	// Verifies that technique in jsonStabilize is correct/safe with a variety of input conditions.
	testCases := []struct {
		name   string
		input  string
		output string
	}{
		{
			name:   "already compacted",
			input:  `{"foo":123,"foe":{"bar":true,"baz":[0,1,2,3,4]},"buzz":3.14159,"frob":"nitz"}`,
			output: `{"foo":123,"foe":{"bar":true,"baz":[0,1,2,3,4]},"buzz":3.14159,"frob":"nitz"}`,
		},
		{
			name: "pretty printed",
			input: `{
  "foo": 123,
  "foe": {
    "bar": true,
    "baz": [
      0,
      1,
      2,
      3,
      4
    ]
  },
  "buzz": 3.14159,
  "frob": "nitz"
}`,
			output: `{"foo":123,"foe":{"bar":true,"baz":[0,1,2,3,4]},"buzz":3.14159,"frob":"nitz"}`,
		},
		{
			name:   "just string",
			input:  `"foo bar baz\nfoo\tbar\tbaz"`,
			output: `"foo bar baz\nfoo\tbar\tbaz"`,
		},
		{
			name:   "just bool",
			input:  `           true       `,
			output: `true`,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result, err := jsonStabilize(([]byte)(testCase.input))
			require.NoError(t, err)
			require.Equal(t, testCase.output, string(result))
		})
	}
}

func TestJSONCodec_MarshalField(t *testing.T) {
	testCases := []struct {
		fieldNames     []string
		expectZeroJSON string
		value          any
		expectJSON     string

		expectZeroJSONEnumNumbers string
		expectJSONEnumNumbers     string

		expectZeroJSONProtoNames string
		expectJSONProtoNames     string
	}{
		// Singular fields (all variants: implicit and explicit presence and in oneof):
		{
			fieldNames: []string{
				"int32_value", "sint32_value", "sfixed32_value",
				"opt_int32_value", "opt_sint32_value", "opt_sfixed32_value",
				"int32_option", "sint32_option", "sfixed32_option",
			},
			expectZeroJSON: "0",
			value:          int32(-123),
			expectJSON:     "-123",
		},
		{
			fieldNames: []string{
				"uint32_value", "fixed32_value",
				"opt_uint32_value", "opt_fixed32_value",
				"uint32_option", "fixed32_option",
			},
			expectZeroJSON: "0",
			value:          uint32(123),
			expectJSON:     "123",
		},
		{
			fieldNames: []string{
				"int64_value", "sint64_value", "sfixed64_value",
				"opt_int64_value", "opt_sint64_value", "opt_sfixed64_value",
				"int64_option", "sint64_option", "sfixed64_option",
			},
			expectZeroJSON: `"0"`,
			value:          int64(-123),
			expectJSON:     `"-123"`,
		},
		{
			fieldNames: []string{
				"uint64_value", "fixed64_value",
				"opt_uint64_value", "opt_fixed64_value",
				"uint64_option", "fixed64_option",
			},
			expectZeroJSON: `"0"`,
			value:          uint64(123),
			expectJSON:     `"123"`,
		},
		{
			fieldNames: []string{
				"double_value", "opt_double_value", "double_option",
			},
			expectZeroJSON: "0",
			value:          float64(123),
			expectJSON:     "123",
		},
		{
			fieldNames: []string{
				"float_value", "opt_float_value", "float_option",
			},
			expectZeroJSON: "0",
			value:          float32(-123),
			expectJSON:     "-123",
		},
		{
			fieldNames: []string{
				"bool_value", "opt_bool_value", "bool_option",
			},
			expectZeroJSON: "false",
			value:          true,
			expectJSON:     "true",
		},
		{
			fieldNames: []string{
				"string_value", "opt_string_value", "string_option",
			},
			expectZeroJSON: `""`,
			value:          "foobar",
			expectJSON:     `"foobar"`,
		},
		{
			fieldNames: []string{
				"bytes_value", "opt_bytes_value", "bytes_option",
			},
			expectZeroJSON: `""`,
			value:          []byte{0, 1, 2, 3},
			expectJSON:     `"AAECAw=="`,
		},
		{
			fieldNames: []string{
				"enum_value", "opt_enum_value", "enum_option",
			},
			expectZeroJSON:            `"ZERO"`,
			value:                     testv1.AllTypes_ONE,
			expectJSON:                `"ONE"`,
			expectZeroJSONEnumNumbers: "0",
			expectJSONEnumNumbers:     "1",
		},
		{
			fieldNames: []string{
				"msg_value", "opt_msg_value", "msg_option",
			},
			expectZeroJSON:        "{}",
			value:                 &testv1.AllTypes{Int32Value: 123, EnumValue: testv1.AllTypes_TWO},
			expectJSON:            `{"int32Value":123,"enumValue":"TWO"}`,
			expectJSONEnumNumbers: `{"int32Value":123,"enumValue":2}`,
			expectJSONProtoNames:  `{"int32_value":123,"enum_value":"TWO"}`,
		},
		// Repeated fields:
		{
			fieldNames: []string{
				"int32_list", "sint32_list", "sfixed32_list",
			},
			expectZeroJSON: "[]",
			value:          []int32{-123, 123, 0, 5},
			expectJSON:     "[-123,123,0,5]",
		},
		{
			fieldNames: []string{
				"uint32_list", "fixed32_list",
			},
			expectZeroJSON: "[]",
			value:          []uint32{123, 321, 0, 5},
			expectJSON:     "[123,321,0,5]",
		},
		{
			fieldNames: []string{
				"int64_list", "sint64_list", "sfixed64_list",
			},
			expectZeroJSON: "[]",
			value:          []int64{-123, 123, 0, 5},
			expectJSON:     `["-123","123","0","5"]`,
		},
		{
			fieldNames: []string{
				"uint64_list", "fixed64_list",
			},
			expectZeroJSON: "[]",
			value:          []uint64{123, 321, 0, 5},
			expectJSON:     `["123","321","0","5"]`,
		},
		{
			fieldNames:     []string{"double_list"},
			expectZeroJSON: "[]",
			value:          []float64{123, 1.23, 0},
			expectJSON:     "[123,1.23,0]",
		},
		{
			fieldNames:     []string{"float_list"},
			expectZeroJSON: "[]",
			value:          []float32{123, 1.23, 0},
			expectJSON:     "[123,1.23,0]",
		},
		{
			fieldNames:     []string{"bool_list"},
			expectZeroJSON: "[]",
			value:          []bool{true, false, true},
			expectJSON:     "[true,false,true]",
		},
		{
			fieldNames:     []string{"string_list"},
			expectZeroJSON: "[]",
			value:          []string{"foo", "bar", "baz"},
			expectJSON:     `["foo","bar","baz"]`,
		},
		{
			fieldNames:     []string{"bytes_list"},
			expectZeroJSON: "[]",
			value:          [][]byte{{0, 1, 2, 3}, {4, 5, 6, 7}},
			expectJSON:     `["AAECAw==","BAUGBw=="]`,
		},
		{
			fieldNames:            []string{"enum_list"},
			expectZeroJSON:        "[]",
			value:                 []testv1.AllTypes_Enum{testv1.AllTypes_ONE, testv1.AllTypes_TWO},
			expectJSON:            `["ONE","TWO"]`,
			expectJSONEnumNumbers: "[1,2]",
		},
		{
			fieldNames:            []string{"msg_list"},
			expectZeroJSON:        "[]",
			value:                 []*testv1.AllTypes{{Int32Value: 123, EnumValue: testv1.AllTypes_TWO}, {}, {StringList: []string{"foo", "bar"}}},
			expectJSON:            `[{"int32Value":123,"enumValue":"TWO"},{},{"stringList":["foo","bar"]}]`,
			expectJSONEnumNumbers: `[{"int32Value":123,"enumValue":2},{},{"stringList":["foo","bar"]}]`,
			expectJSONProtoNames:  `[{"int32_value":123,"enum_value":"TWO"},{},{"string_list":["foo","bar"]}]`,
		},
		// Map fields (permutations on map value type):
		{
			fieldNames: []string{
				"int32_map", "sint32_map", "sfixed32_map",
			},
			expectZeroJSON: "{}",
			value:          map[string]int32{"a": -123, "b": 123, "c": 0, "d": 5},
			expectJSON:     `{"a":-123,"b":123,"c":0,"d":5}`,
		},
		{
			fieldNames: []string{
				"uint32_map", "fixed32_map",
			},
			expectZeroJSON: "{}",
			value:          map[string]uint32{"a": 123, "b": 321, "c": 0, "d": 5},
			expectJSON:     `{"a":123,"b":321,"c":0,"d":5}`,
		},
		{
			fieldNames: []string{
				"int64_map", "sint64_map", "sfixed64_map",
			},
			expectZeroJSON: "{}",
			value:          map[string]int64{"a": -123, "b": 123, "c": 0, "d": 5},
			expectJSON:     `{"a":"-123","b":"123","c":"0","d":"5"}`,
		},
		{
			fieldNames: []string{
				"uint64_map", "fixed64_map",
			},
			expectZeroJSON: "{}",
			value:          map[string]uint64{"a": 123, "b": 321, "c": 0, "d": 5},
			expectJSON:     `{"a":"123","b":"321","c":"0","d":"5"}`,
		},
		{
			fieldNames:     []string{"double_map"},
			expectZeroJSON: "{}",
			value:          map[string]float64{"a": 123, "b": 1.23, "c": 0},
			expectJSON:     `{"a":123,"b":1.23,"c":0}`,
		},
		{
			fieldNames:     []string{"float_map"},
			expectZeroJSON: "{}",
			value:          map[string]float32{"a": 123, "b": 1.23, "c": 0},
			expectJSON:     `{"a":123,"b":1.23,"c":0}`,
		},
		{
			fieldNames:     []string{"bool_map"},
			expectZeroJSON: "{}",
			value:          map[string]bool{"a": true, "b": false, "c": true},
			expectJSON:     `{"a":true,"b":false,"c":true}`,
		},
		{
			fieldNames:     []string{"string_map"},
			expectZeroJSON: "{}",
			value:          map[string]string{"a": "foo", "b": "bar", "c": "baz"},
			expectJSON:     `{"a":"foo","b":"bar","c":"baz"}`,
		},
		{
			fieldNames:     []string{"bytes_map"},
			expectZeroJSON: "{}",
			value:          map[string][]byte{"a": {0, 1, 2, 3}, "b": {4, 5, 6, 7}},
			expectJSON:     `{"a":"AAECAw==","b":"BAUGBw=="}`,
		},
		{
			fieldNames:            []string{"enum_map"},
			expectZeroJSON:        "{}",
			value:                 map[string]testv1.AllTypes_Enum{"a": testv1.AllTypes_ONE, "b": testv1.AllTypes_TWO},
			expectJSON:            `{"a":"ONE","b":"TWO"}`,
			expectJSONEnumNumbers: `{"a":1,"b":2}`,
		},
		{
			fieldNames:            []string{"msg_map"},
			expectZeroJSON:        "{}",
			value:                 map[string]*testv1.AllTypes{"a": {Int32Value: 123, EnumValue: testv1.AllTypes_TWO}, "b": {}, "c": {StringList: []string{"foo", "bar"}}},
			expectJSON:            `{"a":{"int32Value":123,"enumValue":"TWO"},"b":{},"c":{"stringList":["foo","bar"]}}`,
			expectJSONEnumNumbers: `{"a":{"int32Value":123,"enumValue":2},"b":{},"c":{"stringList":["foo","bar"]}}`,
			expectJSONProtoNames:  `{"a":{"int32_value":123,"enum_value":"TWO"},"b":{},"c":{"string_list":["foo","bar"]}}`,
		},
		// Map fields (permutations on map key type):
		{
			fieldNames: []string{
				"int32_to_string_map", "sint32_to_string_map", "sfixed32_to_string_map",
			},
			expectZeroJSON: "{}",
			value:          map[int32]string{-123: "a", 0: "b", 5: "c", 123: "d"},
			expectJSON:     `{"-123":"a","0":"b","5":"c","123":"d"}`,
		},
		{
			fieldNames: []string{
				"uint32_to_string_map", "fixed32_to_string_map",
			},
			expectZeroJSON: "{}",
			value:          map[uint32]string{0: "a", 5: "b", 123: "c"},
			expectJSON:     `{"0":"a","5":"b","123":"c"}`,
		},
		{
			fieldNames: []string{
				"int64_to_string_map", "sint64_to_string_map", "sfixed64_to_string_map",
			},
			expectZeroJSON: "{}",
			value:          map[int64]string{-123: "a", 0: "b", 5: "c", 123: "d"},
			expectJSON:     `{"-123":"a","0":"b","5":"c","123":"d"}`,
		},
		{
			fieldNames: []string{
				"uint64_to_string_map", "fixed64_to_string_map",
			},
			expectZeroJSON: "{}",
			value:          map[uint64]string{0: "a", 5: "b", 123: "c"},
			expectJSON:     `{"0":"a","5":"b","123":"c"}`,
		},
		{
			fieldNames:     []string{"bool_to_string_map"},
			expectZeroJSON: "{}",
			value:          map[bool]string{false: "a", true: "b"},
			expectJSON:     `{"false":"a","true":"b"}`,
		},
	}

	asSingularValue := func(val any) protoreflect.Value {
		switch val := val.(type) {
		case proto.Message:
			return protoreflect.ValueOfMessage(val.ProtoReflect())
		case protoreflect.Enum:
			return protoreflect.ValueOfEnum(val.Number())
		default:
			return protoreflect.ValueOf(val)
		}
	}
	asValue := func(msg protoreflect.Message, field protoreflect.FieldDescriptor, val any) protoreflect.Value {
		switch {
		case field.IsList():
			target := msg.NewField(field)
			listVal := target.List()
			source := reflect.ValueOf(val)
			for i := 0; i < source.Len(); i++ {
				listVal.Append(asSingularValue(source.Index(i).Interface()))
			}
			return target
		case field.IsMap():
			target := msg.NewField(field)
			mapVal := target.Map()
			source := reflect.ValueOf(val)
			iter := source.MapRange()
			for iter.Next() {
				k := iter.Key()
				v := iter.Value()
				mapVal.Set(asSingularValue(k.Interface()).MapKey(), asSingularValue(v.Interface()))
			}
			return target
		default:
			return asSingularValue(val)
		}
	}

	// NB: We intentionally don't have a case with the default EmitUnpopulated option
	//     because then the expected JSON values for the message cases would be huge
	//     and hard to maintain. That case shouldn't actually impact the rest of the
	//     logic so should be fine to omit.
	marshalOpts := []struct {
		name string
		opts protojson.MarshalOptions
	}{
		{
			name: "enums_as_numbers",
			opts: protojson.MarshalOptions{
				UseEnumNumbers: true,
			},
		},
		{
			name: "use_proto_names",
			opts: protojson.MarshalOptions{
				UseProtoNames: true,
			},
		},
		{
			name: "multiline",
			opts: protojson.MarshalOptions{
				Multiline: true,
				Indent:    "   ",
			},
		},
	}

	for _, marshalOpt := range marshalOpts {
		codec := &jsonCodec{m: marshalOpt.opts}
		t.Run(marshalOpt.name, func(t *testing.T) {
			t.Parallel()
			for _, testCase := range testCases {
				testCase := testCase
				for _, fieldName := range testCase.fieldNames {
					t.Run(fieldName, func(t *testing.T) {
						t.Parallel()
						msg := (&testv1.AllTypes{}).ProtoReflect()
						field := msg.Descriptor().Fields().ByName(protoreflect.Name(fieldName))

						// Marshal with value unset/zero.
						data, err := codec.MarshalAppendField(nil, msg.Interface(), field)
						require.NoError(t, err)
						data, err = jsonStabilize(data) // for deterministic JSON strings
						require.NoError(t, err)
						expected := testCase.expectZeroJSON
						if codec.m.UseEnumNumbers && testCase.expectZeroJSONEnumNumbers != "" {
							expected = testCase.expectZeroJSONEnumNumbers
						} else if codec.m.UseProtoNames && testCase.expectZeroJSONProtoNames != "" {
							expected = testCase.expectZeroJSONProtoNames
						}
						require.Equal(t, expected, string(data))

						// Do round-trip through unmarshal.
						proto.Reset(msg.Interface())
						err = codec.UnmarshalField(data, msg.Interface(), field)
						require.NoError(t, err)
						expectedMsg := (&testv1.AllTypes{}).ProtoReflect()
						if field.HasPresence() {
							// Field will be explicitly set after unmarshalling.
							if field.IsList() || field.IsMap() || field.Message() != nil {
								expectedMsg.Mutable(field)
							} else {
								expectedMsg.Set(field, expectedMsg.Get(field))
							}
						}
						diff := cmp.Diff(expectedMsg.Interface(), msg.Interface(), protocmp.Transform())
						require.Empty(t, diff)

						// Now marshal non-zero value.
						proto.Reset(msg.Interface())
						refValue := asValue(msg, field, testCase.value)
						msg.Set(field, refValue)
						data, err = codec.MarshalAppendField(nil, msg.Interface(), field)
						require.NoError(t, err)
						data, err = jsonStabilize(data)
						require.NoError(t, err)
						expected = testCase.expectJSON
						if codec.m.UseEnumNumbers && testCase.expectJSONEnumNumbers != "" {
							expected = testCase.expectJSONEnumNumbers
						} else if codec.m.UseProtoNames && testCase.expectJSONProtoNames != "" {
							expected = testCase.expectJSONProtoNames
						}
						require.Equal(t, expected, string(data))

						// And round-trip through unmarshal again.
						proto.Reset(msg.Interface())
						err = codec.UnmarshalField(data, msg.Interface(), field)
						require.NoError(t, err)
						proto.Reset(expectedMsg.Interface())
						expectedMsg.Set(field, refValue)
						diff = cmp.Diff(expectedMsg.Interface(), msg.Interface(), protocmp.Transform())
						require.Empty(t, diff)
					})
				}
			}
		})
	}
}
