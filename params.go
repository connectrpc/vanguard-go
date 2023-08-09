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
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// param is a captured variable from the URL path or query.
type param struct {
	fields []protoreflect.FieldDescriptor
	value  protoreflect.Value
}

// parseParam parses the raw field value from a request URL into a param that
// can be used to set a field in a message.
func parseParam(fields []protoreflect.FieldDescriptor, raw []byte) (*param, error) {
	if len(fields) == 0 {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("parameter missing field descriptors"),
		)
	}
	field := fields[len(fields)-1] // last field is the type of the value
	value, err := parseFieldValue(field, raw)
	if err != nil {
		if jsonErr := (*json.UnmarshalTypeError)(nil); errors.As(err, &jsonErr) ||
			// protojson errors are not exported, check the error string.
			strings.HasPrefix(err.Error(), "proto") {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid parameter %q value for %s type: %s",
					resolveFieldDescriptorsToPath(fields), field.Kind(), raw,
				),
			)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid parameter %q %w",
				resolveFieldDescriptorsToPath(fields), err),
		)
	}
	return &param{fields: fields, value: value}, nil
}

// set the value of the param on the message.
func (p param) set(msg protoreflect.Message) {
	for i, field := range p.fields {
		if len(p.fields)-1 == i {
			switch {
			case field.IsList():
				l := msg.Mutable(field).List()
				l.Append(p.value)
			default:
				msg.Set(field, p.value)
			}
			break
		}
		msg = msg.Mutable(field).Message()
	}
}

//nolint:gocyclo
func parseFieldValue(field protoreflect.FieldDescriptor, raw []byte) (protoreflect.Value, error) {
	//nolint:exhaustive
	switch kind := field.Kind(); kind {
	case protoreflect.BoolKind:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfBool(b), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		var x int32
		if err := json.Unmarshal(raw, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt32(x), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		var x int64
		if err := json.Unmarshal(raw, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt64(x), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		var x uint32
		if err := json.Unmarshal(raw, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint32(x), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		var x uint64
		if err := json.Unmarshal(raw, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint64(x), nil
	case protoreflect.FloatKind:
		var x float32
		if err := json.Unmarshal(raw, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat32(x), nil
	case protoreflect.DoubleKind:
		var x float64
		if err := json.Unmarshal(raw, &x); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat64(x), nil
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(string(raw)), nil
	case protoreflect.BytesKind:
		enc := base64.StdEncoding
		if bytes.ContainsAny(raw, "-_") {
			enc = base64.URLEncoding
		}
		if len(raw)%4 != 0 {
			enc = enc.WithPadding(base64.NoPadding)
		}
		dst := make([]byte, enc.DecodedLen(len(raw)))
		n, err := enc.Decode(dst, raw)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfBytes(dst[:n]), nil
	case protoreflect.EnumKind:
		var x int32
		if err := json.Unmarshal(raw, &x); err == nil {
			return protoreflect.ValueOfEnum(protoreflect.EnumNumber(x)), nil
		}
		s := string(raw)
		if isNullValue(field) && s == "null" {
			return protoreflect.ValueOfEnum(0), nil
		}
		enumVal := field.Enum().Values().ByName(protoreflect.Name(s))
		if enumVal == nil {
			return protoreflect.Value{}, fmt.Errorf("unknown enum: %s", raw)
		}
		return protoreflect.ValueOfEnum(enumVal.Number()), nil
	case protoreflect.MessageKind:
		// Well known JSON scalars are decoded to message types.
		msgDesc := field.Message()
		name := string(msgDesc.FullName())
		//nolint:nestif
		if strings.HasPrefix(name, "google.protobuf.") {
			switch msgDesc.FullName()[16:] {
			case "Timestamp":
				var msg timestamppb.Timestamp
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "Duration":
				var msg durationpb.Duration
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "BoolValue":
				var msg wrapperspb.BoolValue
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "Int32Value":
				var msg wrapperspb.Int32Value
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "Int64Value":
				var msg wrapperspb.Int64Value
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "UInt32Value":
				var msg wrapperspb.UInt32Value
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "UInt64Value":
				var msg wrapperspb.UInt64Value
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "FloatValue":
				var msg wrapperspb.FloatValue
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "DoubleValue":
				var msg wrapperspb.DoubleValue
				if err := protojson.Unmarshal(raw, &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "BytesValue":
				var msg wrapperspb.BytesValue
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "StringValue":
				var msg wrapperspb.StringValue
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			case "FieldMask":
				var msg fieldmaskpb.FieldMask
				if err := protojson.Unmarshal(quote(raw), &msg); err != nil {
					return protoreflect.Value{}, err
				}
				return protoreflect.ValueOfMessage(msg.ProtoReflect()), nil
			}
		}
		if msgDesc.IsMapEntry() {
			return protoreflect.Value{}, fmt.Errorf("unsupported maps")
		}
		return protoreflect.Value{}, fmt.Errorf("unsupported message type %s", field.Message().FullName())
	default:
		return protoreflect.Value{}, fmt.Errorf("unsupported type %s", field.Kind())
	}
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
