// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func httpStatusCodeFromRPC(code connect.Code) int {
	var codes = [...]int{
		http.StatusOK,                  // 0 OK
		http.StatusRequestTimeout,      // 1 Canceled
		http.StatusInternalServerError, // 2 Unknown
		http.StatusBadRequest,          // 3 InvalidArgument
		http.StatusGatewayTimeout,      // 4 DeadlineExceeded
		http.StatusNotFound,            // 5 NotFound
		http.StatusConflict,            // 6 AlreadyExists
		http.StatusForbidden,           // 7 PermissionDenied
		http.StatusTooManyRequests,     // 8 ResourceExhausted
		http.StatusBadRequest,          // 9 FailedPrecondition
		http.StatusConflict,            // 10 Aborted
		http.StatusBadRequest,          // 11 OutOfRange
		http.StatusNotImplemented,      // 12 Unimplemented
		http.StatusInternalServerError, // 13 Internal
		http.StatusServiceUnavailable,  // 14 Unavailable
		http.StatusInternalServerError, // 15 DataLoss
		http.StatusUnauthorized,        // 16 Unauthenticated
	}
	if int(code) > len(codes) {
		return http.StatusInternalServerError
	}
	return codes[code]
}

func httpWriteError(rsp http.ResponseWriter, err error) {
	codec := protojson.MarshalOptions{
		EmitUnpopulated: true,
	}
	cerr := asError(err)
	statusCode := httpStatusCodeFromRPC(cerr.Code())
	status := grpcStatusFromError(err)

	hdr := rsp.Header()
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Content-Encoding", "identity")
	bin, err := codec.MarshalAppend(nil, status)
	if err != nil {
		statusCode = http.StatusInternalServerError
		hdr.Set("Content-Type", "application/json")
		bin = []byte(`{"code": 12, "message":"` + err.Error() + `"}`)
	}
	rsp.WriteHeader(statusCode)
	_, _ = rsp.Write(bin)
}

func httpErrorFromResponse(body io.Reader) *connect.Error {
	codec := protojson.UnmarshalOptions{}
	body = io.LimitReader(body, 1024)
	bin, err := io.ReadAll(body)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	var status status.Status
	if err := codec.Unmarshal(bin, &status); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	connectErr := connect.NewWireError(
		connect.Code(status.Code),
		errors.New(status.Message),
	)
	for _, msg := range status.Details {
		errDetail, _ := connect.NewErrorDetail(msg)
		connectErr.AddDetail(errDetail)
	}
	return connectErr
}

// httpEncodePathValues encodes the given message for the route target.
// The path and query are returned.
func httpEncodePathValues(input protoreflect.Message, target *routeTarget) (
	path string, query url.Values, err error,
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
			return "", nil, err
		}
		variableSize := variable.size()
		var values []string
		if variableSize == 1 {
			values = []string{value}
		} else {
			values = strings.Split(value, "/")
		}

		if variableSize > 1 && len(values) != variableSize {
			return "", nil, fmt.Errorf(
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
				return "", nil, fmt.Errorf(
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
		// No query values are included in the URL.
		return path, nil, nil
	} else if target.requestBodyFieldPath != "" {
		// Exclude the request body path from the query.
		fieldPathCounts[target.requestBodyFieldPath]++
	}

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
		return "", nil, fieldError
	}
	return path, query, nil

}
