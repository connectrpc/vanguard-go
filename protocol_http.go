// Copyright 2023-2025 Buf Technologies, Inc.
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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/api/annotations"
	httpbody "google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

//nolint:gochecknoglobals
var httpStatusCodeFromRPCIndex = [...]int{
	http.StatusOK,                  // 0 OK
	499,                            // 1 Canceled
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

func httpStatusCodeFromRPC(code connect.Code) int {
	if int(code) > len(httpStatusCodeFromRPCIndex) {
		return http.StatusInternalServerError
	}
	return httpStatusCodeFromRPCIndex[code]
}

func httpStatusCodeToRPC(code int) connect.Code {
	switch code {
	case http.StatusOK:
		return 0 // OK
	case http.StatusBadRequest:
		return connect.CodeInternal // Internal
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated // Unauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied // PermissionDenied
	case http.StatusNotFound:
		return connect.CodeUnimplemented // Unimplemented
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return connect.CodeUnavailable // Unavailable
	default:
		return connect.CodeUnknown // Unknown
	}
}

func httpWriteError(rsp http.ResponseWriter, err error) {
	codec := protojson.MarshalOptions{
		EmitUnpopulated: true,
	}
	cerr := asConnectError(err)
	statusCode := httpStatusCodeFromRPC(cerr.Code())
	stat := grpcStatusFromError(cerr)

	hdr := rsp.Header()
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Content-Encoding", "identity")
	bin, err := codec.MarshalAppend(nil, stat)
	if err != nil {
		statusCode = http.StatusInternalServerError
		hdr.Set("Content-Type", "application/json")
		bin = []byte(`{"code": 12, "message":"` + err.Error() + `"}`)
	}
	rsp.WriteHeader(statusCode)
	_, _ = rsp.Write(bin)
}

func httpErrorFromResponse(statusCode int, contentType string, src *bytes.Buffer) *connect.Error {
	if statusCode == http.StatusOK {
		return nil
	}
	codec := protojson.UnmarshalOptions{}
	var stat status.Status
	if err := codec.Unmarshal(src.Bytes(), &stat); err != nil {
		body, err := anypb.New(&httpbody.HttpBody{
			ContentType: contentType,
			Data:        src.Bytes(),
		})
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
		stat.Details = append(stat.Details, body)
		stat.Code = int32(httpStatusCodeToRPC(statusCode))
		stat.Message = http.StatusText(statusCode)
	}

	connectErr := connect.NewWireError(
		connect.Code(stat.GetCode()),
		errors.New(stat.GetMessage()),
	)
	for _, msg := range stat.GetDetails() {
		errDetail, _ := connect.NewErrorDetail(msg)
		connectErr.AddDetail(errDetail)
	}
	return connectErr
}

func httpSplitVar(variable string, multi bool) []string {
	if !multi {
		return []string{pathEscape(variable, pathEncodeSingle)}
	}
	values := strings.Split(variable, "/")
	for i, value := range values {
		values[i] = pathEscape(value, pathEncodeMulti)
	}
	return values
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

		values := httpSplitVar(value, variableSize != 1)

		if variableSize > 1 && len(values) != variableSize {
			return "", nil, fmt.Errorf(
				"expected field %s to match pattern %q: instead got %q",
				variable.fieldPath, strings.Join(variable.index(segments), "/"), value,
			)
		}

		for i, part := range values {
			segmentIndex := variable.start + i
			if segmentIndex >= len(segments) {
				segments = append(segments, part) // nozero
				continue
			}

			segment := segments[segmentIndex]
			if segment == "*" || segment == "**" {
				segments[segmentIndex] = part
			} else if segment != part {
				return "", nil, fmt.Errorf(
					"expected field %s to match pattern %q: instead got %q",
					variable.fieldPath, strings.Join(variable.index(segments), "/"), value,
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
		pathURL.WriteString(segment)
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
		// A count of negative one means *all* values (even for repeated fields) are already used.
		fieldPathCounts[target.requestBodyFieldPath] = -1
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
		// The field path is resolved to the proto format for lookups,
		// but the query values are encoded in their JSON representation.
		fieldPath := resolveFieldDescriptorsToPath(fields, false)
		fieldIndex := fieldPathCounts[fieldPath]

		switch {
		case !isParameterType(field):
			if fieldIndex != 0 {
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
			if fieldIndex < 0 {
				break
			}
			listValue := value.List()
			for fieldIndex < listValue.Len() {
				value := listValue.Get(fieldIndex)
				encoded, err := marshalFieldValue(field, value)
				if err != nil {
					fieldError = err
					return false
				}
				query.Add(
					// Query values are encoded in their JSON representation.
					resolveFieldDescriptorsToPath(fields, true),
					string(encoded),
				)
				fieldIndex++
			}
		case fieldIndex == 0:
			encoded, err := marshalFieldValue(field, value)
			if err != nil {
				fieldError = err
				return false
			}
			query.Add(
				// Query values are encoded in their JSON representation.
				resolveFieldDescriptorsToPath(fields, true),
				string(encoded),
			)
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

func httpExtractTrailers(headers http.Header, knownTrailerKeys headerKeys) http.Header {
	trailers := make(http.Header, len(knownTrailerKeys))
	for key, vals := range headers {
		if strings.HasPrefix(key, http.TrailerPrefix) {
			trailers[strings.TrimPrefix(key, http.TrailerPrefix)] = vals
			delete(headers, key)
			continue
		}
		if _, expected := knownTrailerKeys[key]; expected {
			trailers[key] = vals
			delete(headers, key)
			continue
		}
	}
	return trailers
}

func httpMergeTrailers(header http.Header, trailer http.Header) {
	for key, vals := range trailer {
		if !strings.HasPrefix(key, http.TrailerPrefix) {
			key = http.TrailerPrefix + key
		}
		for _, val := range vals {
			header.Add(key, val)
		}
	}
}

func httpExtractContentLength(headers http.Header) (int, error) {
	contentLenStr := headers.Get("Content-Length")
	if contentLenStr == "" {
		return -1, nil
	}
	i, err := strconv.Atoi(contentLenStr)
	if err != nil {
		return 0, fmt.Errorf("could not parse content-length %q: %w", contentLenStr, err)
	}
	if i < 0 {
		return 0, fmt.Errorf("content-length %d should not be negative", i)
	}
	headers.Del("Content-Length")
	return i, nil
}

func getHTTPRuleExtension(desc protoreflect.MethodDescriptor) (*annotations.HttpRule, bool) {
	opts := desc.Options()
	if !proto.HasExtension(opts, annotations.E_Http) {
		return nil, false
	}
	rule, ok := proto.GetExtension(opts, annotations.E_Http).(*annotations.HttpRule)
	return rule, ok
}
