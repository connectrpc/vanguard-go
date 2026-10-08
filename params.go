// Copyright 2023-2026 Buf Technologies, Inc.
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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// isParameterType returns true if the field is a primitive type, or a
// well-known-type that can be represented as a scalar in JSON.
// These are valid leaf fields for use in URL paths and query parameters.
func isParameterType(field protoreflect.FieldDescriptor) bool {
	kind := field.Kind()
	return kind != protoreflect.GroupKind &&
		(kind != protoreflect.MessageKind || isWKTWithScalarJSONMapping(field))
}

// isWKTWithScalarJSONMapping returns true if the field is a well-known type that
// maps to a scalar JSON type. These are also valid parameter types alongside
// primitives.
func isWKTWithScalarJSONMapping(field protoreflect.FieldDescriptor) bool {
	const wellKnownTypePrefix = "google.protobuf."
	if field.Kind() != protoreflect.MessageKind || !strings.HasPrefix(
		string(field.Message().FullName()), wellKnownTypePrefix,
	) {
		return false
	}
	switch field.Message().Name() {
	case "BoolValue", "BytesValue", "DoubleValue", "Duration", "Empty",
		"FieldMask", "FloatValue", "Int32Value", "Int64Value", "NullValue",
		"StringValue", "Timestamp", "UInt32Value", "UInt64Value":
		return true
	default:
		return false
	}
}

// setParameter sets the value of a field on a message using the ident fields.
// Leaf fields must be a primitive type, or a well-known JSON scalar type.
// Repeated fields of a primitive type are supported and will be appended to.
// Map fields and other message types are not supported.
//
// See: https://github.com/googleapis/googleapis/blob/2c28ce13ade62398e152ff3eb840f4f934812597/google/api/http.proto#L117-L122
func setParameter(msg protoreflect.Message, fields []protoreflect.FieldDescriptor, param string) error {
	// Traverse the message to the last field.
	leaf := msg
	for _, field := range fields[:len(fields)-1] {
		leaf = leaf.Mutable(field).Message()
	}
	field := fields[len(fields)-1]

	value, err := unmarshalFieldValue(leaf, field, param)
	if err != nil {
		// Resolve the field path for the error message in proto format.
		// The JSON format is not used for consistency with other errors.
		fieldPath := resolveFieldDescriptorsToPath(fields, false)
		if jsonErr := (*json.UnmarshalTypeError)(nil); errors.As(err, &jsonErr) ||
			// protojson errors are not exported, check the error string.
			strings.HasPrefix(err.Error(), "proto") {
			return connect.Errorf(connect.CodeInvalidArgument,
				"invalid parameter %q value for type %q: %s",
				fieldPath, field.Kind(), param,
			)
		}
		return connect.Errorf(connect.CodeInvalidArgument,
			"invalid parameter %q: %s", fieldPath, err,
		).WithCause(err)
	}

	// Set the value on the leaf message.
	// Cannot be a map type, only lists, primitives or messages.
	if field.IsList() {
		l := leaf.Mutable(field).List()
		l.Append(value)
	} else {
		leaf.Set(field, value)
	}
	return nil
}

func unmarshalFieldValue(msg protoreflect.Message, field protoreflect.FieldDescriptor, data string) (protoreflect.Value, error) {
	switch kind := field.Kind(); kind {
	case protoreflect.BoolKind:
		switch data {
		case "true":
			return protoreflect.ValueOfBool(true), nil
		case "false":
			return protoreflect.ValueOfBool(false), nil
		}
		var b bool
		if err := json.Unmarshal([]byte(data), &b); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfBool(b), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		x, err := unmarshalInt[int32](data, 32)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt32(x), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		x, err := unmarshalInt[int64](data, 64)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt64(x), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		x, err := unmarshalUint[uint32](data, 32)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint32(x), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		x, err := unmarshalUint[uint64](data, 64)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint64(x), nil
	case protoreflect.FloatKind:
		return unmarshalFloat(data, 32)
	case protoreflect.DoubleKind:
		return unmarshalFloat(data, 64)
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(data), nil
	case protoreflect.BytesKind:
		enc := base64.StdEncoding
		if strings.ContainsAny(data, "-_") {
			enc = base64.URLEncoding
		}
		if len(data)%4 != 0 {
			enc = enc.WithPadding(base64.NoPadding)
		}
		dst := make([]byte, enc.DecodedLen(len(data)))
		n, err := enc.Decode(dst, []byte(data))
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfBytes(dst[:n]), nil
	case protoreflect.EnumKind:
		if x, err := unmarshalInt[protoreflect.EnumNumber](data, 32); err == nil {
			return protoreflect.ValueOfEnum(x), nil
		}
		if isNullValue(field) && data == "null" {
			return protoreflect.ValueOfEnum(0), nil
		}
		enumVal := field.Enum().Values().ByName(protoreflect.Name(data))
		if enumVal == nil {
			return protoreflect.Value{}, fmt.Errorf("unknown enum: %s", data)
		}
		return protoreflect.ValueOf(enumVal.Number()), nil
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return unmarshalFieldWKT(msg, field, data)
	default:
		return protoreflect.Value{}, fmt.Errorf("unsupported type %s", field.Kind())
	}
}

// unmarshalFieldWKT unmarshals well known JSON scalars to their message types.
func unmarshalFieldWKT(msg protoreflect.Message, field protoreflect.FieldDescriptor, data string) (protoreflect.Value, error) {
	if !isWKTWithScalarJSONMapping(field) {
		return protoreflect.Value{}, fmt.Errorf("unsupported message type %s", field.Message().FullName())
	}
	switch field.Message().Name() {
	case "DoubleValue", "FloatValue":
		value := msg.NewField(field)
		subField := value.Message().Descriptor().Fields().ByName("value")
		subValue, err := unmarshalFieldValue(value.Message(), subField, data)
		if err != nil {
			return protoreflect.Value{}, err
		}
		value.Message().Set(subField, subValue)
		return value, nil
	case "Timestamp", "Duration", "BytesValue", "StringValue", "FieldMask":
		return unmarshalFieldMessage(msg, field, quote([]byte(data)))
	}
	return unmarshalFieldMessage(msg, field, []byte(data))
}

func unmarshalFieldMessage(msg protoreflect.Message, field protoreflect.FieldDescriptor, data []byte) (protoreflect.Value, error) {
	value := msg.NewField(field)
	if err := protojson.Unmarshal(data, value.Message().Interface()); err != nil {
		return protoreflect.Value{}, err
	}
	return value, nil
}

func unmarshalFloat(data string, bitSize int) (protoreflect.Value, error) {
	var value float64
	switch data {
	case "NaN":
		value = math.NaN()
	case "Infinity":
		value = math.Inf(+1)
	case "-Infinity":
		value = math.Inf(-1)
	default:
		if bitSize == 32 {
			float, err := unmarshalNumber(data, func(data string) (float32, error) {
				x, err := strconv.ParseFloat(data, 32)
				return float32(x), err
			})
			if err != nil {
				return protoreflect.Value{}, err
			}
			return protoreflect.ValueOfFloat32(float), nil
		}
		double, err := unmarshalNumber(data, func(data string) (float64, error) {
			return strconv.ParseFloat(data, 64)
		})
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat64(double), nil
	}
	if bitSize == 32 {
		return protoreflect.ValueOfFloat32(float32(value)), nil
	}
	return protoreflect.ValueOfFloat64(value), nil
}

func unmarshalInt[T ~int32 | ~int64](data string, bitSize int) (T, error) {
	return unmarshalNumber(data, func(data string) (T, error) {
		x, err := strconv.ParseInt(data, 10, bitSize)
		return T(x), err
	})
}

func unmarshalUint[T ~uint32 | ~uint64](data string, bitSize int) (T, error) {
	return unmarshalNumber(data, func(data string) (T, error) {
		x, err := strconv.ParseUint(data, 10, bitSize)
		return T(x), err
	})
}

// unmarshalNumber parses a JSON number with parse, falling back to
// [json.Unmarshal] for the errors of any input parse rejects.
func unmarshalNumber[T any](data string, parse func(string) (T, error)) (T, error) {
	if isJSONNumber(data) {
		if x, err := parse(data); err == nil {
			return x, nil
		}
	}
	var x T
	err := json.Unmarshal([]byte(data), &x)
	return x, err
}

// isJSONNumber reports whether data matches the JSON number grammar.
func isJSONNumber(data string) bool {
	digits := func(data string) (string, bool) {
		trimmed := strings.TrimLeft(data, "0123456789")
		return trimmed, len(trimmed) < len(data)
	}
	data = strings.TrimPrefix(data, "-")
	switch {
	case strings.HasPrefix(data, "0"):
		data = data[1:]
	case data != "" && data[0] >= '1' && data[0] <= '9':
		data, _ = digits(data)
	default:
		return false
	}
	if rest, ok := strings.CutPrefix(data, "."); ok {
		if data, ok = digits(rest); !ok {
			return false
		}
	}
	if len(data) > 0 && (data[0] == 'e' || data[0] == 'E') {
		rest := strings.TrimLeft(data[1:], "+-")
		if len(data)-len(rest) > 2 {
			return false
		}
		var ok bool
		if data, ok = digits(rest); !ok {
			return false
		}
	}
	return data == ""
}

func quote(raw []byte) []byte {
	if len(raw) > 0 && (raw[0] != '"' || raw[len(raw)-1] != '"') {
		raw = strconv.AppendQuote(raw[:0], string(raw))
	}
	return raw
}
func unquote(raw []byte) ([]byte, error) {
	value, err := strconv.Unquote(string(raw))
	return []byte(value), err
}

func isNullValue(field protoreflect.FieldDescriptor) bool {
	ed := field.Enum()
	return ed != nil && ed.FullName() == "google.protobuf.NullValue"
}

// getParameter gets the value of a field on a message using the ident fields.
// Optionally, an index can be provided to get the value of a repeated field.
func getParameter(msg protoreflect.Message, fields []protoreflect.FieldDescriptor, index int) (string, error) {
	// Traverse the message to the last field.
	leaf := msg
	for _, field := range fields[:len(fields)-1] {
		leaf = leaf.Mutable(field).Message()
	}
	field := fields[len(fields)-1]

	value := leaf.Get(field)
	if field.IsList() {
		if index > value.List().Len() {
			return "", connect.Errorf(connect.CodeInvalidArgument,
				"index %d out of range for field %s", index, field.Name(),
			)
		}
		value = value.List().Get(index)
	}

	param, err := marshalFieldValue(field, value)
	return string(param), err
}

func marshalFieldValue(field protoreflect.FieldDescriptor, value protoreflect.Value) ([]byte, error) {
	switch kind := field.Kind(); kind {
	case protoreflect.BoolKind,
		protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return json.Marshal(value.Interface())
	case protoreflect.FloatKind:
		return marshalFloat(value.Float(), 32)
	case protoreflect.DoubleKind:
		return marshalFloat(value.Float(), 64)
	case protoreflect.StringKind:
		return []byte(value.String()), nil
	case protoreflect.BytesKind:
		enc := base64.URLEncoding
		src := value.Bytes()
		dst := make([]byte, enc.EncodedLen(len(src)))
		enc.Encode(dst, src)
		return dst, nil
	case protoreflect.EnumKind:
		enumValue := field.Enum().Values().ByNumber(value.Enum())
		if enumValue == nil {
			return nil, fmt.Errorf("unknown enum value %d", value.Enum())
		}
		return []byte(enumValue.Name()), nil
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return marshalFieldWKT(field, value)
	default:
		return nil, fmt.Errorf("unsupported type %s", field.Kind())
	}
}

func marshalFieldWKT(field protoreflect.FieldDescriptor, value protoreflect.Value) ([]byte, error) {
	if !isWKTWithScalarJSONMapping(field) {
		return nil, fmt.Errorf("unsupported message type %s", field.Message().FullName())
	}
	msgName := field.Message().Name()
	switch msgName {
	case "BytesValue", "DoubleValue", "FloatValue":
		// Switch to base64.URLEncoding for BytesValue and handling
		// of float/double string values.
		field := field.Message().Fields().ByName("value")
		value := value.Message().Get(field)
		return marshalFieldValue(field, value)
	}
	data, err := protojson.Marshal(value.Message().Interface())
	if err != nil {
		return nil, err
	}
	switch msgName {
	case "Timestamp", "Duration", "StringValue", "FieldMask",
		"Int64Value", "UInt64Value": // Large numbers unquoted
		return unquote(data)
	}
	return data, nil
}

func marshalFloat(num float64, bitSize int) ([]byte, error) {
	switch {
	case math.IsNaN(num):
		return []byte(`NaN`), nil
	case math.IsInf(num, +1):
		return []byte(`Infinity`), nil
	case math.IsInf(num, -1):
		return []byte(`-Infinity`), nil
	}
	if bitSize == 32 {
		return json.Marshal(float32(num))
	}
	return json.Marshal(num)
}
