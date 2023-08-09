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

	fieldPathCounts := make(map[string]int)

	// Find variable and set the path segments.
	for _, variable := range target.vars {
		fieldPath := resolveFieldDescriptorsToPath(variable.varPath)
		fieldIndex := fieldPathCounts[fieldPath]
		fieldPathCounts[fieldPath]++

		value, err := getParameter(input, variable.varPath, fieldIndex)
		if err != nil {
			return "", nil, nil, err
		}
		if variable.start-variable.end == 1 {
			// simple insert
			segments[variable.start] = value
			continue
		}

		values := strings.Split(value, "/")
		if variable.end != -1 && len(values) != variable.end-variable.start {
			return "", nil, nil, fmt.Errorf(
				"value %q expected to have %d segments, but has %d",
				value, variable.end-variable.start, len(values),
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
				continue
			}

			if segment != part {
				return "", nil, nil, fmt.Errorf(
					"value %q part %q does not match path segment %q",
					value, part, segment,
				)
			}
		}
	}

	// Encode the path URL.
	var pathURL strings.Builder
	for _, segment := range segments {
		pathURL.WriteByte('/')
		pathURL.WriteString(url.PathEscape(segment))
	}
	if target.verb != "" {
		pathURL.WriteByte(':')
		pathURL.WriteString(url.PathEscape(target.verb))
	}

	path = pathURL.String()
	if target.requestBodyPath != nil {
		body = input
	}
	for _, field := range target.requestBodyPath {
		body = body.Get(field).Message()
	}

	// Exclude the request body path from the query.
	fieldPathCounts[resolveFieldDescriptorsToPath(target.requestBodyPath)]++
	query = url.Values{}

	fields := make([]protoreflect.FieldDescriptor, 0, 3)
	var fieldRanger func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool
	var fieldError error
	fieldRanger = func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		fields = append(fields, field)
		defer func() { fields = fields[:len(fields)-1] }() // pop
		fieldPath := resolveFieldDescriptorsToPath(fields)

		fieldIndex := fieldPathCounts[fieldPath]
		isParameter := isParameterType(field)
		if !isParameter {
			if field.Kind() == protoreflect.MessageKind && fieldIndex == 0 {
				value.Message().Range(fieldRanger)
			}
			return true
		}

		if field.IsList() {
			listValue := value.List()
			for i := fieldIndex; i < listValue.Len(); i++ {
				value := listValue.Get(i)
				encoded, err := marshalFieldValue(field, value)
				if err != nil {
					fieldError = err
					return false
				}
				query.Add(fieldPath, string(encoded))
				fieldIndex++
			}
		} else if fieldIndex == 0 {
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
