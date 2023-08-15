// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// encodeMessageAsHTTPRule encodes the given message as a HTTP rule.
// The path, query, and body are returned.
//
//nolint:nonamedreturns
func encodeMessageAsHTTPRule(
	input protoreflect.Message, target *routeTarget,
) (
	path string, query url.Values, body protoreflect.Message, err error,
) {
	// Copy segments to build URL
	segments := make([]string, len(target.path))
	copy(segments, target.path)

	// Count the number of times each field path is used.
	// Singular fields can be referenced multiple times.
	// Repeated fields can be referenced multiple times up to the number of elements.
	// Map fields are not supported.
	// A field count of 0 indicates that the field has not been used.
	fieldPathCounts := make(map[string]int)

	// Find path variables and set the path segments, ensuring that the path
	// matches the pattern.
	for _, variable := range target.vars {
		fieldIndex := fieldPathCounts[variable.fieldPath]
		fieldPathCounts[variable.fieldPath]++

		value, err := getParameter(input, variable.fields, fieldIndex)
		if err != nil {
			return "", nil, nil, err
		}
		variableSize := variable.size()
		var values []string
		if variableSize == 1 {
			values = []string{value}
		} else {
			values = strings.Split(value, "/")
		}

		if variableSize > 1 && len(values) != variableSize {
			return "", nil, nil, fmt.Errorf(
				"expected field %s to match pattern %q: instead got %q",
				variable.fieldPath, variable.capture(segments), value,
			)
		}

		for i, part := range values {
			segmentIndex := variable.start + i
			if segmentIndex >= len(segments) {
				//nolint:makezero
				segments = append(segments, part)
				continue
			}

			segment := segments[segmentIndex]
			if segment == "*" || segment == "**" {
				segments[segmentIndex] = part
			} else if segment != part {
				return "", nil, nil, fmt.Errorf(
					"expected field %s to match pattern %q: instead got %q",
					variable.fieldPath, variable.capture(segments), value,
				)
			}
		}
	}

	// Encode the path URL.
	var pathSize int
	for _, segment := range segments {
		pathSize += len(segment) + 1
	}
	var pathURL strings.Builder
	pathURL.Grow(pathSize)
	for _, segment := range segments {
		pathURL.WriteByte('/')
		pathURL.WriteString(url.PathEscape(segment))
	}
	if target.verb != "" {
		pathURL.WriteByte(':')
		pathURL.WriteString(target.verb)
	}
	path = pathURL.String()

	if target.requestBodyFieldPath == "*" {
		return path, nil, input, nil
	}

	// Traverse the request body, if any.
	if target.requestBodyFields != nil {
		body = input
	}
	for _, field := range target.requestBodyFields {
		body = body.Get(field).Message()
	}

	// Exclude the request body path from the query.
	fieldPathCounts[target.requestBodyFieldPath]++

	// Build the query by traversing the fields in the message.
	// Any non path or request body fields are included in the query.
	query = url.Values{}

	fields := make([]protoreflect.FieldDescriptor, 0, 3)
	var fieldRanger func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool
	var fieldError error
	fieldRanger = func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		fields = append(fields, field)
		defer func() { fields = fields[:len(fields)-1] }() // pop
		fieldPath := resolveFieldDescriptorsToPath(fields)
		fieldIndex := fieldPathCounts[fieldPath]

		switch {
		case !isParameterType(field):
			if fieldIndex > 0 {
				break
			}
			if field.IsMap() || field.IsList() ||
				field.Kind() != protoreflect.MessageKind {
				fieldError = fmt.Errorf(
					"unexpected field %s: cannot be URL encoded",
					fieldPath,
				)
				return false
			}
			// Recurse into the message fields.
			value.Message().Range(fieldRanger)
			if fieldError != nil {
				return false
			}
			fieldIndex++
		case field.IsList():
			listValue := value.List()
			for fieldIndex < listValue.Len() {
				value := listValue.Get(fieldIndex)
				encoded, err := marshalFieldValue(field, value)
				if err != nil {
					fieldError = err
					return false
				}
				query.Add(fieldPath, string(encoded))
				fieldIndex++
			}
		case fieldIndex == 0:
			encoded, err := marshalFieldValue(field, value)
			if err != nil {
				fieldError = err
				return false
			}
			query.Add(fieldPath, string(encoded))
			fieldIndex++
		}

		fieldPathCounts[fieldPath] = fieldIndex
		return true
	}
	input.Range(fieldRanger)
	if fieldError != nil {
		return "", nil, nil, fieldError
	}
	return path, query, body, nil
}
