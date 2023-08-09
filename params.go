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

// setParameter sets the value of a field on a message using the ident fields.
// Leaf fields must be a primitive type, or a well-known JSON scalar type.
// Repeated fields of a primitive type are supported and will be appended to.
// Map fields and other message types are not supported.
//
// See: https://github.com/googleapis/googleapis/blob/2c28ce13ade62398e152ff3eb840f4f934812597/google/api/http.proto#L117-L122
func setParameter(msg protoreflect.Message, fields []protoreflect.FieldDescriptor, data []byte) error {
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
	switch {
	case field.IsList():
		l := leaf.Mutable(field).List()
		l.Append(value)
	default:
		leaf.Set(field, value)
	}
	return nil
}

func unmarshalFieldValue(msg protoreflect.Message, field protoreflect.FieldDescriptor, data []byte) (protoreflect.Value, error) {
	//nolint:exhaustive
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
	msgDesc := field.Message()
	if msgDesc.IsMapEntry() {
		return protoreflect.Value{}, fmt.Errorf("unsupported maps")
	}
	name := string(msgDesc.FullName())
	if strings.HasPrefix(name, "google.protobuf.") {
		switch name[16:] {
		case "Timestamp", "Duration", "BytesValue", "StringValue", "FieldMask":
			data = quote(data)
			fallthrough
		case "BoolValue", "Int32Value", "Int64Value", "UInt32Value",
			"UInt64Value", "FloatValue", "DoubleValue":
			return unmarshalFieldMessage(msg, field, data)
		}
	}
	return protoreflect.Value{}, fmt.Errorf("unsupported message type %s", field.Message().FullName())
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

func isNullValue(field protoreflect.FieldDescriptor) bool {
	ed := field.Enum()
	return ed != nil && ed.FullName() == "google.protobuf.NullValue"
}
