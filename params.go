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
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

//nolint:gochecknoglobals
var isScalarWellKnownType = map[protoreflect.FullName]bool{
	"google.protobuf.Duration":    true,
	"google.protobuf.Empty":       true,
	"google.protobuf.FieldMask":   true,
	"google.protobuf.Timestamp":   true,
	"google.protobuf.NullValue":   true,
	"google.protobuf.StringValue": true,
	"google.protobuf.BytesValue":  true,
	"google.protobuf.Int32Value":  true,
	"google.protobuf.Int64Value":  true,
	"google.protobuf.UInt32Value": true,
	"google.protobuf.UInt64Value": true,
	"google.protobuf.FloatValue":  true,
	"google.protobuf.DoubleValue": true,
	"google.protobuf.BoolValue":   true,
}

func isParameterType(field protoreflect.FieldDescriptor) bool {
	kind := field.Kind()
	return kind != protoreflect.GroupKind &&
		(kind != protoreflect.MessageKind || isScalarWKT(field))
}

func isScalarWKT(field protoreflect.FieldDescriptor) bool {
	return field.Kind() == protoreflect.MessageKind &&
		isScalarWellKnownType[field.Message().FullName()]
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
	for i := 0; i < len(fields)-1; i++ {
		leaf = leaf.Mutable(fields[i]).Message()
	}
	field := fields[len(fields)-1]

	data := []byte(param)
	value, err := unmarshalFieldValue(leaf, field, data)
	if err != nil {
		if jsonErr := (*json.UnmarshalTypeError)(nil); errors.As(err, &jsonErr) ||
			// protojson errors are not exported, check the error string.
			strings.HasPrefix(err.Error(), "proto") {
			return connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid parameter %q value for type %q: %s",
					resolveFieldDescriptorsToPath(fields), field.Kind(), data,
				),
			)
		}
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid parameter %q %w",
				resolveFieldDescriptorsToPath(fields), err,
			),
		)
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

func unmarshalFieldValue(msg protoreflect.Message, field protoreflect.FieldDescriptor, data []byte) (protoreflect.Value, error) {
	switch kind := field.Kind(); kind {
	case protoreflect.BoolKind:
		var b bool
		if err := json.Unmarshal(data, &b); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfBool(b), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		var x int32
		if err := json.Unmarshal(data, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt32(x), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		var x int64
		if err := json.Unmarshal(data, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt64(x), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		var x uint32
		if err := json.Unmarshal(data, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint32(x), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		var x uint64
		if err := json.Unmarshal(data, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint64(x), nil
	case protoreflect.FloatKind:
		return unmarshalFloat(data, 32)
	case protoreflect.DoubleKind:
		return unmarshalFloat(data, 64)
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(string(data)), nil
	case protoreflect.BytesKind:
		enc := base64.StdEncoding
		if bytes.ContainsAny(data, "-_") {
			enc = base64.URLEncoding
		}
		if len(data)%4 != 0 {
			enc = enc.WithPadding(base64.NoPadding)
		}
		dst := make([]byte, enc.DecodedLen(len(data)))
		n, err := enc.Decode(dst, data)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfBytes(dst[:n]), nil
	case protoreflect.EnumKind:
		var x protoreflect.EnumNumber
		if err := json.Unmarshal(data, &x); err == nil {
			return protoreflect.ValueOfEnum(x), nil
		}
		s := string(data)
		if isNullValue(field) && s == "null" {
			return protoreflect.ValueOfEnum(0), nil
		}
		enumVal := field.Enum().Values().ByName(protoreflect.Name(s))
		if enumVal == nil {
			return protoreflect.Value{}, fmt.Errorf("unknown enum: %s", data)
		}
		return protoreflect.ValueOf(enumVal.Number()), nil
	case protoreflect.MessageKind:
		return unmarshalFieldWKT(msg, field, data)
	default:
		return protoreflect.Value{}, fmt.Errorf("unsupported type %s", field.Kind())
	}
}

// unmarshalFieldWKT unmarshals well known JSON scalars to their message types.
func unmarshalFieldWKT(msg protoreflect.Message, field protoreflect.FieldDescriptor, data []byte) (protoreflect.Value, error) {
	if !isScalarWKT(field) {
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
		data = quote(data)
	}
	return unmarshalFieldMessage(msg, field, data)
}

func unmarshalFieldMessage(msg protoreflect.Message, field protoreflect.FieldDescriptor, data []byte) (protoreflect.Value, error) {
	value := msg.NewField(field)
	if err := protojson.Unmarshal(data, value.Message().Interface()); err != nil {
		return protoreflect.Value{}, err
	}
	return value, nil
}

func unmarshalFloat(data []byte, bitSize int) (protoreflect.Value, error) {
	var value float64
	switch string(data) {
	case "NaN":
		value = math.NaN()
	case "Infinity":
		value = math.Inf(+1)
	case "-Infinity":
		value = math.Inf(-1)
	default:
		if bitSize == 32 {
			var x float32
			if err := json.Unmarshal(data, &x); err != nil {
				return protoreflect.Value{}, err
			}
			return protoreflect.ValueOfFloat32(x), nil
		}
		var x float64
		if err := json.Unmarshal(data, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat64(x), nil
	}
	if bitSize == 32 {
		return protoreflect.ValueOfFloat32(float32(value)), nil
	}
	return protoreflect.ValueOfFloat64(value), nil
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
	for i := 0; i < len(fields)-1; i++ {
		leaf = leaf.Mutable(fields[i]).Message()
	}
	field := fields[len(fields)-1]

	value := leaf.Get(field)
	if field.IsList() {
		if index > value.List().Len() {
			return "", connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("index %d out of range for field %s", index, field.Name()),
			)
		}
		value = value.List().Get(index)
	}

	param, err := marshalFieldValue(field, value)
	return string(param), err
}

func marshalFieldValue(field protoreflect.FieldDescriptor, value protoreflect.Value) ([]byte, error) {
	//nolint:exhaustive
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
	case protoreflect.MessageKind:
		return marshalFieldWKT(field, value)
	default:
		return nil, fmt.Errorf("unsupported type %s", field.Kind())
	}
}

func marshalFieldWKT(field protoreflect.FieldDescriptor, value protoreflect.Value) ([]byte, error) {
	if !isScalarWKT(field) {
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
