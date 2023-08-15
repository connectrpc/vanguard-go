// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	return kind < protoreflect.GroupKind ||
		(kind > protoreflect.MessageKind && kind <= protoreflect.Sint64Kind) ||
		(kind == protoreflect.MessageKind && isScalarWKT(field))
}

func isScalarWKT(field protoreflect.FieldDescriptor) bool {
	if field.Kind() != protoreflect.MessageKind {
		return false
	}
	msgDesc := field.Message()
	return isScalarWellKnownType[msgDesc.FullName()]
}

// setParameter sets the value of a field on a message using the ident fields.
// Leaf fields must be a primitive type, or a well-known JSON scalar type.
// Repeated fields of a primitive type are supported and will be appended to.
// Map fields and other message types are not supported.
//
// See: https://github.com/googleapis/googleapis/blob/2c28ce13ade62398e152ff3eb840f4f934812597/google/api/http.proto#L117-L122
func setParameter(msg protoreflect.Message, fields []protoreflect.FieldDescriptor, param string) error {
	if len(fields) == 0 {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("parameter missing field descriptors"),
		)
	}

	// Traverse the message to the last field.
	leaf, field := msg, fields[0]
	for i := 0; i < len(fields)-1; i++ {
		leaf, field = leaf.Mutable(field).Message(), fields[i+1]
	}

	data := []byte(param)
	value, err := unmarshalFieldValue(leaf, field, data)
	if err != nil {
		if jsonErr := (*json.UnmarshalTypeError)(nil); errors.As(err, &jsonErr) ||
			// protojson errors are not exported, check the error string.
			strings.HasPrefix(err.Error(), "proto") {
			return connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid parameter %q value for %s type: %s",
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
		var x float32
		if err := json.Unmarshal(data, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat32(x), nil
	case protoreflect.DoubleKind:
		var x float64
		if err := json.Unmarshal(data, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat64(x), nil
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

func quote(raw []byte) []byte {
	if len(raw) > 0 && (raw[0] != '"' || raw[len(raw)-1] != '"') {
		raw = strconv.AppendQuote(raw[:0], string(raw))
	}
	return raw
}
func unquote(raw []byte) ([]byte, error) {
	val, err := strconv.Unquote(string(raw))
	return []byte(val), err
}

func isNullValue(field protoreflect.FieldDescriptor) bool {
	ed := field.Enum()
	return ed != nil && ed.FullName() == "google.protobuf.NullValue"
}

// getParameter gets the value of a field on a message using the ident fields.
// Optionally, an index can be provided to get the value of a repeated field.
func getParameter(msg protoreflect.Message, fields []protoreflect.FieldDescriptor, index int) (string, error) {
	if len(fields) == 0 {
		return "", connect.NewError(connect.CodeInternal,
			fmt.Errorf("parameter missing field descriptors"),
		)
	}

	// Traverse the message to the last field.
	leaf, field := msg, fields[0]
	for i := 0; i < len(fields)-1; i++ {
		leaf, field = leaf.Mutable(field).Message(), fields[i+1]
	}

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
	case protoreflect.BoolKind:
		return json.Marshal(value.Bool())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return json.Marshal(value.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return json.Marshal(value.Uint())
	case protoreflect.FloatKind:
		return json.Marshal(float32(value.Float()))
	case protoreflect.DoubleKind:
		return json.Marshal(value.Float())
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
	msgName := string(field.Message().Name())
	if msgName == "BytesValue" {
		// Switch to base64.URLEncoding
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
		"Int64Value", "UInt64Value": // Large ints unquoted
		return unquote(data)
	}
	return data, nil
}
