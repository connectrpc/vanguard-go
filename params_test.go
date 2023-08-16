// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"encoding/base64"
	"math"
	"strconv"
	"testing"
	"time"

	testv1 "github.com/bufbuild/vanguard/internal/gen/buf/vanguard/test/v1"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestIsParameter(t *testing.T) {
	t.Parallel()
	desc := (&testv1.ParameterValues{}).ProtoReflect().Descriptor()
	testCases := []struct {
		fieldPath string
		isParam   bool
	}{{
		fieldPath: "string_value",
		isParam:   true,
	}, {
		fieldPath: "recursive.string_value",
		isParam:   true,
	}, {
		fieldPath: "recursive",
		isParam:   false,
	}, {
		fieldPath: "timestamp",
		isParam:   true,
	}, {
		fieldPath: "value",
		isParam:   false,
	}}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.fieldPath, func(t *testing.T) {
			t.Parallel()
			fields, err := resolvePathToDescriptors(desc, testCase.fieldPath)
			require.NoError(t, err)
			field := fields[len(fields)-1]
			assert.Equal(t, testCase.isParam, isParameterType(field))
		})
	}
}

func TestSetParameter(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		fields  string
		value   string
		initial proto.Message // initial message state, can be nil.
		want    proto.Message // expected message state, must not be nil.
		wantErr string
	}{{
		fields: "double_value",
		value:  strconv.FormatFloat(math.MaxFloat64, 'f', -1, 64),
		want:   &testv1.ParameterValues{DoubleValue: math.MaxFloat64},
	}, {
		fields: "double_value",
		value:  "NaN",
		want:   &testv1.ParameterValues{DoubleValue: math.NaN()},
	}, {
		fields: "double_value",
		value:  "Infinity",
		want:   &testv1.ParameterValues{DoubleValue: math.Inf(1)},
	}, {
		fields: "double_value",
		value:  "-Infinity",
		want:   &testv1.ParameterValues{DoubleValue: math.Inf(-1)},
	}, {
		fields: "float_value",
		value:  strconv.FormatFloat(math.MaxFloat32, 'f', -1, 32),
		want:   &testv1.ParameterValues{FloatValue: math.MaxFloat32},
	}, {
		fields: "float_value",
		value:  "NaN",
		want:   &testv1.ParameterValues{FloatValue: float32(math.NaN())},
	}, {
		fields: "float_value",
		value:  "-Infinity",
		want:   &testv1.ParameterValues{FloatValue: float32(math.Inf(-1))},
	}, {
		fields: "float_value",
		value:  "Infinity",
		want:   &testv1.ParameterValues{FloatValue: float32(math.Inf(+1))},
	}, {
		fields: "int32_value",
		value:  strconv.FormatInt(math.MaxInt32, 10),
		want:   &testv1.ParameterValues{Int32Value: math.MaxInt32},
	}, {
		fields:  "int32_value",
		value:   strconv.FormatInt(math.MaxInt64, 10),
		want:    &testv1.ParameterValues{},
		wantErr: "invalid_argument: invalid parameter \"int32_value\" value for type \"int32\": 9223372036854775807",
	}, {
		fields: "int64_value",
		value:  strconv.FormatInt(math.MaxInt64, 10),
		want:   &testv1.ParameterValues{Int64Value: math.MaxInt64},
	}, {
		fields: "uint32_value",
		value:  strconv.FormatUint(math.MaxUint32, 10),
		want:   &testv1.ParameterValues{Uint32Value: math.MaxUint32},
	}, {
		fields: "uint64_value",
		value:  strconv.FormatUint(math.MaxUint64, 10),
		want:   &testv1.ParameterValues{Uint64Value: math.MaxUint64},
	}, {
		fields: "sint32_value",
		value:  strconv.FormatUint(math.MaxInt32, 10),
		want:   &testv1.ParameterValues{Sint32Value: math.MaxInt32},
	}, {
		fields: "sint64_value",
		value:  strconv.FormatUint(math.MaxInt64, 10),
		want:   &testv1.ParameterValues{Sint64Value: math.MaxInt64},
	}, {
		fields: "bool_value",
		value:  "true",
		want:   &testv1.ParameterValues{BoolValue: true},
	}, {
		fields: "bool_value",
		value:  "false",
		want:   &testv1.ParameterValues{BoolValue: false},
	}, {
		fields: "string_value",
		value:  "hello world",
		want:   &testv1.ParameterValues{StringValue: "hello world"},
	}, {
		fields: "bytes_value",
		value:  base64.StdEncoding.EncodeToString([]byte("abc123!?$*&()'-=@~")),
		want:   &testv1.ParameterValues{BytesValue: []byte("abc123!?$*&()'-=@~")},
	}, {
		fields: "bytes_value",
		value:  base64.URLEncoding.EncodeToString([]byte("abc123!?$*&()'-=@~")),
		want:   &testv1.ParameterValues{BytesValue: []byte("abc123!?$*&()'-=@~")},
	}, {
		fields: "bytes_value",
		value:  base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("abc123!?$*&()'-=@")),
		want:   &testv1.ParameterValues{BytesValue: []byte("abc123!?$*&()'-=@")},
	}, {
		fields: "timestamp",
		value:  "2021-01-01T00:00:00Z",
		want: &testv1.ParameterValues{
			Timestamp: timestamppb.New(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}, {
		fields: "duration",
		value:  "1s",
		want: &testv1.ParameterValues{
			Duration: &durationpb.Duration{Seconds: 1},
		},
	}, {
		fields: "duration",
		value:  "3.000000001s",
		want: &testv1.ParameterValues{
			Duration: &durationpb.Duration{Seconds: 3, Nanos: 1},
		},
	}, {
		fields: "bool_value_wrapper",
		value:  "true",
		want: &testv1.ParameterValues{
			BoolValueWrapper: &wrapperspb.BoolValue{Value: true},
		},
	}, {
		fields: "int32_value_wrapper",
		value:  strconv.FormatInt(math.MaxInt32, 10),
		want: &testv1.ParameterValues{
			Int32ValueWrapper: &wrapperspb.Int32Value{Value: math.MaxInt32},
		},
	}, {
		fields:  "int32_value_wrapper",
		value:   strconv.FormatInt(math.MaxInt64, 10),
		want:    &testv1.ParameterValues{},
		wantErr: "invalid_argument: invalid parameter \"int32_value_wrapper\" value for type \"message\": 9223372036854775807",
	}, {
		fields: "int64_value_wrapper",
		value:  strconv.FormatInt(math.MaxInt64, 10),
		want: &testv1.ParameterValues{
			Int64ValueWrapper: &wrapperspb.Int64Value{Value: math.MaxInt64},
		},
	}, {
		fields: "uint32_value_wrapper",
		value:  strconv.FormatUint(math.MaxUint32, 10),
		want: &testv1.ParameterValues{
			Uint32ValueWrapper: &wrapperspb.UInt32Value{Value: math.MaxUint32},
		},
	}, {
		fields: "uint64_value_wrapper",
		value:  strconv.FormatUint(math.MaxUint64, 10),
		want: &testv1.ParameterValues{
			Uint64ValueWrapper: &wrapperspb.UInt64Value{Value: math.MaxUint64},
		},
	}, {
		fields: "double_value_wrapper",
		value:  strconv.FormatFloat(math.MaxFloat64, 'f', -1, 64),
		want: &testv1.ParameterValues{
			DoubleValueWrapper: &wrapperspb.DoubleValue{Value: math.MaxFloat64},
		},
	}, {
		fields: "double_value_wrapper",
		value:  "NaN",
		want: &testv1.ParameterValues{
			DoubleValueWrapper: &wrapperspb.DoubleValue{Value: math.NaN()},
		},
	}, {
		fields: "double_value_wrapper",
		value:  "Infinity",
		want: &testv1.ParameterValues{
			DoubleValueWrapper: &wrapperspb.DoubleValue{Value: math.Inf(1)},
		},
	}, {
		fields: "double_value_wrapper",
		value:  "-Infinity",
		want: &testv1.ParameterValues{
			DoubleValueWrapper: &wrapperspb.DoubleValue{Value: math.Inf(-1)},
		},
	}, {
		fields: "float_value_wrapper",
		value:  strconv.FormatFloat(math.MaxFloat32, 'f', -1, 32),
		want: &testv1.ParameterValues{
			FloatValueWrapper: &wrapperspb.FloatValue{Value: math.MaxFloat32},
		},
	}, {
		fields: "float_value_wrapper",
		value:  "NaN",
		want: &testv1.ParameterValues{
			FloatValueWrapper: &wrapperspb.FloatValue{Value: float32(math.NaN())},
		},
	}, {
		fields: "float_value_wrapper",
		value:  "-Infinity",
		want: &testv1.ParameterValues{
			FloatValueWrapper: &wrapperspb.FloatValue{Value: float32(math.Inf(-1))},
		},
	}, {
		fields: "float_value_wrapper",
		value:  "Infinity",
		want: &testv1.ParameterValues{
			FloatValueWrapper: &wrapperspb.FloatValue{Value: float32(math.Inf(+1))},
		},
	}, {
		fields: "bytes_value_wrapper",
		value:  base64.StdEncoding.EncodeToString([]byte("abc123!?$*&()'-=@~")),
		want: &testv1.ParameterValues{
			BytesValueWrapper: &wrapperspb.BytesValue{Value: []byte("abc123!?$*&()'-=@~")},
		},
	}, {
		fields: "bytes_value_wrapper",
		value:  base64.URLEncoding.EncodeToString([]byte("abc123!?$*&()'-=@~")),
		want: &testv1.ParameterValues{
			BytesValueWrapper: &wrapperspb.BytesValue{Value: []byte("abc123!?$*&()'-=@~")},
		},
	}, {
		fields: "bytes_value_wrapper",
		value:  base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte("abc123!?$*&()'-=@")),
		want: &testv1.ParameterValues{
			BytesValueWrapper: &wrapperspb.BytesValue{Value: []byte("abc123!?$*&()'-=@")},
		},
	}, {
		fields: "string_value_wrapper",
		value:  "hello world",
		want: &testv1.ParameterValues{
			StringValueWrapper: &wrapperspb.StringValue{Value: "hello world"},
		},
	}, {
		fields: "string_value_wrapper",
		value:  "\"hello world\"",
		want: &testv1.ParameterValues{
			StringValueWrapper: &wrapperspb.StringValue{Value: "hello world"},
		},
	}, {
		fields: "field_mask",
		value:  "foo,bar.baz", // TODO: validate against the descriptor?
		want: &testv1.ParameterValues{
			FieldMask: &fieldmaskpb.FieldMask{Paths: []string{"foo", "bar.baz"}},
		},
	}, {
		fields: "enum_value",
		value:  "1",
		want: &testv1.ParameterValues{
			EnumValue: testv1.ParameterValues_ENUM_VALUE,
		},
	}, {
		fields: "enum_value",
		value:  "ENUM_VALUE",
		want: &testv1.ParameterValues{
			EnumValue: testv1.ParameterValues_ENUM_VALUE,
		},
	}, {
		fields: "enum_value",
		value:  "null",
		want: &testv1.ParameterValues{
			EnumValue: testv1.ParameterValues_ENUM_UNSPECIFIED,
		},
	}, {
		fields:  "enum_value",
		value:   "unknown",
		want:    &testv1.ParameterValues{},
		wantErr: "invalid_argument: invalid parameter \"enum_value\" unknown enum: unknown",
	}, {
		fields: "enum_list",
		value:  "1",
		initial: &testv1.ParameterValues{
			EnumList: []testv1.ParameterValues_Enum{
				testv1.ParameterValues_ENUM_UNSPECIFIED,
			},
		},
		want: &testv1.ParameterValues{
			EnumList: []testv1.ParameterValues_Enum{
				testv1.ParameterValues_ENUM_UNSPECIFIED,
				testv1.ParameterValues_ENUM_VALUE,
			},
		},
	}, {
		fields: "oneof_double_value",
		value:  "1.234",
		want: &testv1.ParameterValues{
			Oneof: &testv1.ParameterValues_OneofDoubleValue{
				OneofDoubleValue: 1.234,
			},
		},
	}, {
		fields: "nested.double_value",
		value:  "1.234",
		want: &testv1.ParameterValues{
			Nested: &testv1.ParameterValues_Nested{
				DoubleValue: 1.234,
			},
		},
	}, {
		fields: "recursive.double_value",
		value:  "1.234",
		want: &testv1.ParameterValues{
			Recursive: &testv1.ParameterValues{
				DoubleValue: 1.234,
			},
		},
	}, {
		fields:  "string_map",
		value:   "hello",
		want:    &testv1.ParameterValues{},
		wantErr: "invalid_argument: invalid parameter \"string_map\" unsupported message type buf.vanguard.test.v1.ParameterValues.StringMapEntry",
	}, {
		fields:  "nested_map.double_value",
		value:   "1.234",
		want:    &testv1.ParameterValues{},
		wantErr: "in field path \"nested_map.double_value\": field \"nested_map\" of type buf.vanguard.test.v1.ParameterValues should not be a list or map",
	}, {
		fields: "timestamp",
		value:  "2021-01-01T00:00:00Z",
		want: func() proto.Message {
			msg := dynamicpb.NewMessage((&testv1.ParameterValues{}).ProtoReflect().Descriptor())
			field := msg.Descriptor().Fields().ByName("timestamp")
			input := &timestamppb.Timestamp{Seconds: 1609459200}
			value := protoreflect.ValueOfMessage(input.ProtoReflect())
			msg.Set(field, value)
			return msg
		}(),
	}}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.fields+"="+testCase.value, func(t *testing.T) {
			t.Parallel()

			desc := testCase.want.ProtoReflect().Descriptor()
			fields, err := resolvePathToDescriptors(desc, testCase.fields)
			if err != nil {
				assert.Equal(t, testCase.wantErr, err.Error())
				return
			}
			require.NoError(t, err)

			value := testCase.want.ProtoReflect().New()
			if testCase.initial != nil {
				proto.Merge(value.Interface(), testCase.initial)
			}

			if err := setParameter(value, fields, testCase.value); err != nil {
				assert.Equal(t, testCase.wantErr, err.Error())
				return
			}
			require.NoError(t, err)

			got := value.Interface()
			assert.Empty(t, cmp.Diff(testCase.want, got, protocmp.Transform(), cmpopts.EquateNaNs()))
		})
	}
}

func TestGetParameter(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		fields  string
		msg     proto.Message // initial message state, must not be nil.
		want    string
		wantErr string
	}{{
		fields: "double_value",
		msg:    &testv1.ParameterValues{DoubleValue: math.MaxFloat64},
		want:   "1.7976931348623157e+308",
	}, {
		fields: "double_value",
		msg:    &testv1.ParameterValues{DoubleValue: math.NaN()},
		want:   "NaN",
	}, {
		fields: "double_value",
		msg:    &testv1.ParameterValues{DoubleValue: math.Inf(1)},
		want:   "Infinity",
	}, {
		fields: "double_value",
		msg:    &testv1.ParameterValues{DoubleValue: math.Inf(-1)},
		want:   "-Infinity",
	}, {
		fields: "float_value",
		msg:    &testv1.ParameterValues{FloatValue: math.MaxFloat32},
		want:   "3.4028235e+38",
	}, {
		fields: "float_value",
		msg:    &testv1.ParameterValues{FloatValue: float32(math.NaN())},
		want:   "NaN",
	}, {
		fields: "float_value",
		msg:    &testv1.ParameterValues{FloatValue: float32(math.Inf(1))},
		want:   "Infinity",
	}, {
		fields: "float_value",
		msg:    &testv1.ParameterValues{FloatValue: float32(math.Inf(-1))},
		want:   "-Infinity",
	}, {
		fields: "int32_value",
		msg:    &testv1.ParameterValues{Int32Value: math.MaxInt32},
		want:   strconv.FormatInt(math.MaxInt32, 10),
	}, {
		fields: "int64_value",
		msg:    &testv1.ParameterValues{Int64Value: math.MaxInt64},
		want:   strconv.FormatInt(math.MaxInt64, 10),
	}, {
		fields: "uint32_value",
		msg:    &testv1.ParameterValues{Uint32Value: math.MaxUint32},
		want:   strconv.FormatUint(math.MaxUint32, 10),
	}, {
		fields: "uint64_value",
		msg:    &testv1.ParameterValues{Uint64Value: math.MaxUint64},
		want:   strconv.FormatUint(math.MaxUint64, 10),
	}, {
		fields: "sint32_value",
		msg:    &testv1.ParameterValues{Sint32Value: math.MaxInt32},
		want:   strconv.FormatUint(math.MaxInt32, 10),
	}, {
		fields: "sint64_value",
		msg:    &testv1.ParameterValues{Sint64Value: math.MaxInt64},
		want:   strconv.FormatUint(math.MaxInt64, 10),
	}, {
		fields: "bool_value",
		msg:    &testv1.ParameterValues{BoolValue: true},
		want:   "true",
	}, {
		fields: "bool_value",
		msg:    &testv1.ParameterValues{BoolValue: false},
		want:   "false",
	}, {
		fields: "string_value",
		msg:    &testv1.ParameterValues{StringValue: "hello world"},
		want:   "hello world",
	}, {
		fields: "bytes_value",
		msg:    &testv1.ParameterValues{BytesValue: []byte("abc123!?$*&()'-=@~")},
		want:   base64.URLEncoding.EncodeToString([]byte("abc123!?$*&()'-=@~")),
	}, {
		fields: "timestamp",
		msg: &testv1.ParameterValues{
			Timestamp: timestamppb.New(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
		want: "2021-01-01T00:00:00Z",
	}, {
		fields: "duration",
		msg: &testv1.ParameterValues{
			Duration: &durationpb.Duration{Seconds: 1},
		},
		want: "1s",
	}, {
		fields: "duration",
		msg: &testv1.ParameterValues{
			Duration: &durationpb.Duration{Seconds: 3, Nanos: 1},
		},
		want: "3.000000001s",
	}, {
		fields: "bool_value_wrapper",
		msg: &testv1.ParameterValues{
			BoolValueWrapper: &wrapperspb.BoolValue{Value: true},
		},
		want: "true",
	}, {
		fields: "int32_value_wrapper",
		msg: &testv1.ParameterValues{
			Int32ValueWrapper: &wrapperspb.Int32Value{Value: math.MaxInt32},
		},
		want: strconv.FormatInt(math.MaxInt32, 10),
	}, {
		fields: "int64_value_wrapper",
		msg: &testv1.ParameterValues{
			Int64ValueWrapper: &wrapperspb.Int64Value{Value: math.MaxInt64},
		},
		want: strconv.FormatInt(math.MaxInt64, 10),
	}, {
		fields: "uint32_value_wrapper",
		msg: &testv1.ParameterValues{
			Uint32ValueWrapper: &wrapperspb.UInt32Value{Value: math.MaxUint32},
		},
		want: strconv.FormatUint(math.MaxUint32, 10),
	}, {
		fields: "uint64_value_wrapper",
		msg: &testv1.ParameterValues{
			Uint64ValueWrapper: &wrapperspb.UInt64Value{Value: math.MaxUint64},
		},
		want: strconv.FormatUint(math.MaxUint64, 10),
	}, {
		fields: "uint64_value_wrapper",
		msg: &testv1.ParameterValues{
			Uint64ValueWrapper: &wrapperspb.UInt64Value{Value: 1},
		},
		want: strconv.FormatUint(1, 10),
	}, {
		fields: "double_value_wrapper",
		msg: &testv1.ParameterValues{
			DoubleValueWrapper: &wrapperspb.DoubleValue{Value: math.MaxFloat64},
		},
		want: "1.7976931348623157e+308",
	}, {
		fields: "double_value_wrapper",
		msg: &testv1.ParameterValues{
			DoubleValueWrapper: &wrapperspb.DoubleValue{Value: math.NaN()},
		},
		want: "NaN",
	}, {
		fields: "double_value_wrapper",
		msg: &testv1.ParameterValues{
			DoubleValueWrapper: &wrapperspb.DoubleValue{Value: math.Inf(1)},
		},
		want: "Infinity",
	}, {
		fields: "double_value_wrapper",
		msg: &testv1.ParameterValues{
			DoubleValueWrapper: &wrapperspb.DoubleValue{Value: math.Inf(-1)},
		},
		want: "-Infinity",
	}, {
		fields: "float_value_wrapper",
		msg: &testv1.ParameterValues{
			FloatValueWrapper: &wrapperspb.FloatValue{Value: math.MaxFloat32},
		},
		want: "3.4028235e+38",
	}, {
		fields: "float_value_wrapper",
		msg: &testv1.ParameterValues{
			FloatValueWrapper: &wrapperspb.FloatValue{Value: float32(math.NaN())},
		},
		want: "NaN",
	}, {
		fields: "float_value_wrapper",
		msg: &testv1.ParameterValues{
			FloatValueWrapper: &wrapperspb.FloatValue{Value: float32(math.Inf(-1))},
		},
		want: "-Infinity",
	}, {
		fields: "float_value_wrapper",
		msg: &testv1.ParameterValues{
			FloatValueWrapper: &wrapperspb.FloatValue{Value: float32(math.Inf(+1))},
		},
		want: "Infinity",
	}, {
		fields: "bytes_value_wrapper",
		msg: &testv1.ParameterValues{
			BytesValueWrapper: &wrapperspb.BytesValue{Value: []byte("abc123!?$*&()'-=@~")},
		},
		want: base64.URLEncoding.EncodeToString([]byte("abc123!?$*&()'-=@~")),
	}, {
		fields: "string_value_wrapper",
		msg: &testv1.ParameterValues{
			StringValueWrapper: &wrapperspb.StringValue{Value: "hello world"},
		},
		want: "hello world",
	}, {
		fields: "string_value_wrapper",
		msg: &testv1.ParameterValues{
			StringValueWrapper: &wrapperspb.StringValue{Value: "\"hello world\""},
		},
		want: "\"hello world\"",
	}, {
		fields: "field_mask",
		msg: &testv1.ParameterValues{
			FieldMask: &fieldmaskpb.FieldMask{Paths: []string{"foo", "bar.baz"}},
		},
		want: "foo,bar.baz", // TODO: validate against the descriptor?
	}, {
		fields: "enum_value",
		msg: &testv1.ParameterValues{
			EnumValue: testv1.ParameterValues_ENUM_VALUE,
		},
		want: "ENUM_VALUE",
	}, {
		fields: "enum_value",
		msg: &testv1.ParameterValues{
			EnumValue: testv1.ParameterValues_ENUM_UNSPECIFIED,
		},
		want: "ENUM_UNSPECIFIED",
	}, {
		fields: "enum_value",
		msg: &testv1.ParameterValues{
			EnumValue: -1, // invalid value
		},
		wantErr: "unknown enum value -1",
	}, {
		fields: "enum_list",
		msg: &testv1.ParameterValues{
			EnumList: []testv1.ParameterValues_Enum{
				testv1.ParameterValues_ENUM_VALUE,
				testv1.ParameterValues_ENUM_UNSPECIFIED,
			},
		},
		want: "ENUM_VALUE",
	}, {
		fields: "oneof_double_value",
		msg: &testv1.ParameterValues{
			Oneof: &testv1.ParameterValues_OneofDoubleValue{
				OneofDoubleValue: 1.234,
			},
		},
		want: "1.234",
	}, {
		fields: "nested.double_value",
		msg: &testv1.ParameterValues{
			Nested: &testv1.ParameterValues_Nested{
				DoubleValue: 1.234,
			},
		},
		want: "1.234",
	}, {
		fields: "recursive.double_value",
		msg: &testv1.ParameterValues{
			Recursive: &testv1.ParameterValues{
				DoubleValue: 1.234,
			},
		},
		want: "1.234",
	}, {
		fields: "timestamp",
		msg: func() proto.Message {
			msg := dynamicpb.NewMessage((&testv1.ParameterValues{}).ProtoReflect().Descriptor())
			field := msg.Descriptor().Fields().ByName("timestamp")
			input := &timestamppb.Timestamp{Seconds: 1609459200}
			value := protoreflect.ValueOfMessage(input.ProtoReflect())
			msg.Set(field, value)
			return msg
		}(),
		want: "2021-01-01T00:00:00Z",
	}}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.fields+"="+testCase.want, func(t *testing.T) {
			t.Parallel()

			desc := testCase.msg.ProtoReflect().Descriptor()
			fields, err := resolvePathToDescriptors(desc, testCase.fields)
			if err != nil {
				assert.Equal(t, testCase.wantErr, err.Error())
				return
			}
			require.NoError(t, err)

			value, err := getParameter(testCase.msg.ProtoReflect(), fields, 0)
			if err != nil {
				assert.Equal(t, testCase.wantErr, err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, value)
		})
	}
}
