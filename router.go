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
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	errUnknownField = errors.New("unknown field")
)

// routeTrie is a prefix trie of valid REST URI paths to route targets.
// It supports evaluation of variables as the path is matched, for
// interpolating parts of the URI path into an RPC request field. The
// map is keyed by the path component that corresponds to a given node.
type routeTrie struct {
	// Child nodes, keyed by the next segment in the path.
	children map[string]*routeTrie
	// Final node in the path has a map of verbs to methods.
	// Verbs are either an empty string or a single literal.
	verbs map[string]routeMethods
}

// addRoute adds a target to the router for the given method and the given
// HTTP rule. Only the rule itself is added. If the rule indicates additional
// bindings, they are ignored. To add routes for all bindings, callers must
// invoke this method for each rule.
func (t *routeTrie) addRoute(method *method, rule *annotations.HttpRule) (*routeTarget, error) {
	var httpMethod, template string
	switch pattern := rule.GetPattern().(type) {
	case *annotations.HttpRule_Get:
		httpMethod, template = http.MethodGet, pattern.Get
	case *annotations.HttpRule_Put:
		httpMethod, template = http.MethodPut, pattern.Put
	case *annotations.HttpRule_Post:
		httpMethod, template = http.MethodPost, pattern.Post
	case *annotations.HttpRule_Delete:
		httpMethod, template = http.MethodDelete, pattern.Delete
	case *annotations.HttpRule_Patch:
		httpMethod, template = http.MethodPatch, pattern.Patch
	case *annotations.HttpRule_Custom:
		httpMethod, template = pattern.Custom.GetKind(), pattern.Custom.GetPath()
	default:
		return nil, fmt.Errorf("invalid type of pattern for HTTP rule: %T", pattern)
	}
	if httpMethod == "" {
		return nil, errors.New("invalid HTTP rule: method is blank")
	}
	if template == "" {
		return nil, errors.New("invalid HTTP rule: path template is blank")
	}
	template, variables, err := parsePathTemplate(template)
	if err != nil {
		return nil, err
	}
	target, err := makeTarget(method, httpMethod, rule.GetBody(), rule.GetResponseBody(), template, variables)
	if err != nil {
		return nil, err
	}
	if err := t.insert(httpMethod, target); err != nil {
		return nil, err
	}
	return target, nil
}

func (t *routeTrie) insertChild(segment string) *routeTrie {
	child := t.children[segment]
	if child == nil {
		if t.children == nil {
			t.children = make(map[string]*routeTrie, 1)
		}
		child = &routeTrie{}
		t.children[segment] = child
	}
	return child
}
func (t *routeTrie) insertVerb(verb string) routeMethods {
	methods := t.verbs[verb]
	if methods == nil {
		if t.verbs == nil {
			t.verbs = make(map[string]routeMethods, 1)
		}
		methods = make(routeMethods, 1)
		t.verbs[verb] = methods
	}
	return methods
}

// insert the target into the trie using the given method and the target's
// template. The path is followed until the final segment is reached.
func (t *routeTrie) insert(method string, target *routeTarget) error {
	path, verb := splitVerb(target.template)
	cursor := t
	for segment := range strings.SplitSeq(path[1:], "/") {
		cursor = cursor.insertChild(segment)
	}
	if existing := cursor.verbs[verb][method]; existing != nil {
		return alreadyExistsError{
			existing: existing, pathPattern: target.template, method: method,
		}
	}
	cursor.insertVerb(verb)[method] = target
	return nil
}

// match finds a route for the given request. If no target matches, the
// methods for a matching path and verb are returned, if any.
func (t *routeTrie) match(uriPath, httpMethod string) (*routeTarget, routeMethods) {
	if !strings.HasPrefix(uriPath, "/") || strings.HasSuffix(uriPath, ":") {
		// Must start with "/" or if it ends with ":" it won't match
		return nil, nil
	}
	path, verb := splitVerb(uriPath)
	return t.findTarget(path, verb, httpMethod)
}

// splitVerb splits the ":verb" suffix from the final segment of the URI path.
func splitVerb(uriPath string) (path, verb string) {
	last := strings.LastIndexByte(uriPath, '/') + 1
	if colon := strings.IndexByte(uriPath[last:], ':'); colon >= 0 {
		return uriPath[:last+colon], uriPath[last+colon+1:]
	}
	return uriPath, ""
}

// findTarget finds the target for the given escaped path, verb, and method.
// Each segment of the path is preceded by a "/".
// The method either returns a target OR the set of methods for the given path
// and verb. If the target is non-nil, the request was matched. If the target
// is nil but methods are non-nil, the path and verb matched a route, but not
// the method. This can be used to send back a well-formed "Allow" response
// header. If both are nil, the path and verb did not match.
func (t *routeTrie) findTarget(path, verb, method string) (*routeTarget, routeMethods) {
	if path == "" {
		return t.getTarget(verb, method)
	}
	current, rest := path[1:], ""
	if next := strings.IndexByte(current, '/'); next >= 0 {
		current, rest = current[:next], current[next:]
	}

	if literal, ok := canonicalSegment(current); ok {
		if child := t.children[literal]; child != nil {
			target, methods := child.findTarget(rest, verb, method)
			if target != nil || methods != nil {
				return target, methods
			}
		}
	}

	if childAst := t.children["*"]; childAst != nil {
		target, methods := childAst.findTarget(rest, verb, method)
		if target != nil || methods != nil {
			return target, methods
		}
	}

	// Double-asterisk must be the last element in pattern.
	// So it consumes all remaining path elements.
	if childDblAst := t.children["**"]; childDblAst != nil {
		return childDblAst.findTarget("", verb, method)
	}
	return nil, nil
}

// canonicalSegment returns the escaped segment in the canonical form of
// template literals, or false if it holds an invalid escape.
func canonicalSegment(segment string) (string, bool) {
	unescaped, err := pathUnescape(segment, pathEncodeSingle)
	if err != nil {
		return "", false
	}
	return pathEscape(unescaped, pathEncodeSingle), true
}

// getTarget gets the target for the given verb and method from the
// node trie. It is like findTarget, except that it does not use a
// path to first descend into a sub-trie.
func (t *routeTrie) getTarget(verb, method string) (*routeTarget, routeMethods) {
	methods := t.verbs[verb]
	if target := methods[method]; target != nil {
		return target, methods
	}
	// See if a wildcard method was used
	if target := methods["*"]; target != nil {
		return target, methods
	}
	return nil, methods
}

type routeMethods map[string]*routeTarget

type routeTarget struct {
	method                *method
	httpMethod            string // HTTP method
	template              string // canonical path template
	requestBodyFieldPath  string
	requestBodyFields     []protoreflect.FieldDescriptor
	responseBodyFieldPath string
	responseBodyFields    []protoreflect.FieldDescriptor
	vars                  []routeTargetVar
}

func makeTarget(
	method *method,
	httpMethod, requestBody, responseBody string,
	template string,
	variables []pathVariable,
) (*routeTarget, error) {
	var requestBodyFields []protoreflect.FieldDescriptor
	if requestBody == "*" {
		// non-nil, empty slice means use the whole thing
		requestBodyFields = []protoreflect.FieldDescriptor{}
	} else if requestBody != "" {
		var err error
		requestBodyFields, err = resolvePathToFieldDescriptors(
			method.descriptor.Input(), requestBody, false,
		)
		if err != nil {
			return nil, err
		}
		if len(requestBodyFields) > 1 {
			return nil, fmt.Errorf(
				"unexpected request body path %q: must be a single field",
				requestBody,
			)
		}
	}
	var responseBodyFields []protoreflect.FieldDescriptor
	if responseBody == "*" {
		// non-nil, empty slice means use the whole thing
		responseBodyFields = []protoreflect.FieldDescriptor{}
	} else if responseBody != "" {
		var err error
		responseBodyFields, err = resolvePathToFieldDescriptors(
			method.descriptor.Output(), responseBody, false,
		)
		if err != nil {
			return nil, err
		}
		if len(responseBodyFields) > 1 {
			return nil, fmt.Errorf(
				"unexpected response body path %q: must be a single field",
				requestBody,
			)
		}
	}
	routeTargetVars := make([]routeTargetVar, len(variables))
	for i, variable := range variables {
		fields, err := resolvePathToFieldDescriptors(
			method.descriptor.Input(), variable.fieldPath, false,
		)
		if err != nil {
			return nil, err
		}
		if last := fields[len(fields)-1]; last.IsList() {
			return nil, fmt.Errorf(
				"unexpected path variable %q: cannot be a repeated field",
				variable.fieldPath,
			)
		}
		routeTargetVars[i] = routeTargetVar{
			pathVariable: variable,
			fields:       fields,
		}
	}
	target := &routeTarget{
		method:                method,
		httpMethod:            httpMethod,
		template:              template,
		requestBodyFieldPath:  requestBody,
		requestBodyFields:     requestBodyFields,
		responseBodyFieldPath: responseBody,
		responseBodyFields:    responseBodyFields,
		vars:                  routeTargetVars,
	}
	if err := checkStreamType(target); err != nil {
		return nil, err
	}
	return target, nil
}

// checkStreamType rejects streams REST cannot carry: an HTTP body has no
// message framing, so the streamed side must be a google.api.HttpBody.
func checkStreamType(target *routeTarget) error {
	method := target.method
	streamType := method.spec.StreamType
	switch streamType {
	case connect.StreamTypeUnary:
		return nil
	case connect.StreamTypeClient:
		if isHTTPBodyRequest(target, method.descriptor.Input()) {
			return nil
		}
		return fmt.Errorf("stream type %s requires a google.api.HttpBody request body", streamType)
	case connect.StreamTypeServer:
		if isHTTPBodyResponse(target, method.descriptor.Output()) {
			return nil
		}
		return fmt.Errorf("stream type %s requires a google.api.HttpBody response body", streamType)
	case connect.StreamTypeBidi:
	}
	return fmt.Errorf("stream type %s not supported", streamType)
}

type routeTargetVar struct {
	pathVariable

	fields []protoreflect.FieldDescriptor
}

func (v routeTargetVar) size() int {
	if v.end == -1 {
		return -1
	}
	return v.end - v.start
}
func (v routeTargetVar) index(segments []string) []string {
	start, end := v.start, v.end
	if end == -1 {
		if start >= len(segments) {
			return nil
		}
		return segments[start:]
	}
	return segments[start:end]
}

// capture returns the unescaped value of the variable from the matched URI
// path, with any verb removed.
func (v routeTargetVar) capture(path string) (string, error) {
	start, end := segmentOffset(path, v.start), len(path)
	mode := pathEncodeMulti
	if v.end != -1 {
		end = start + segmentOffset(path[start:], v.end-v.start)
		if v.end-v.start == 1 {
			mode = pathEncodeSingle
		}
	}
	return pathUnescape(path[start+1:end], mode)
}

// segmentOffset returns the offset of the "/" preceding segment index of the
// path, or the length of the path if it has fewer segments.
func segmentOffset(path string, index int) int {
	offset := 0
	for range index {
		next := strings.IndexByte(path[offset+1:], '/')
		if next < 0 {
			return len(path)
		}
		offset += next + 1
	}
	return offset
}

// resolvePathToFieldDescriptors translates the given path string, in the form of
// "ident.ident.ident", into a path of FieldDescriptors, relative to the given msg.
// If fromJSON is true, the JSON name of the field is used first, falling back to
// the proto name.
func resolvePathToFieldDescriptors(
	msg protoreflect.MessageDescriptor, path string, fromJSON bool,
) ([]protoreflect.FieldDescriptor, error) {
	if path == "" {
		return nil, errors.New("empty field path")
	}
	fields := msg.Fields()
	result := make([]protoreflect.FieldDescriptor, strings.Count(path, ".")+1)
	for i, remaining := 0, path; remaining != ""; i++ {
		part := remaining
		if i := strings.IndexByte(remaining, '.'); i >= 0 {
			part, remaining = remaining[:i], remaining[i+1:]
		} else {
			remaining = ""
		}
		var field protoreflect.FieldDescriptor
		if fromJSON {
			field = fields.ByJSONName(part)
		}
		if field == nil {
			field = fields.ByName(protoreflect.Name(part))
			if field == nil {
				return nil, fmt.Errorf("%w in field path %q: element %q does not correspond to any field of type %s",
					errUnknownField, path, part, msg.FullName())
			}
		}
		result[i] = field
		if remaining == "" {
			break
		}
		if field.Cardinality() == protoreflect.Repeated {
			return nil, fmt.Errorf("in field path %q: field %q of type %s should not be a list or map",
				path, part, msg.FullName())
		}
		childMsg := field.Message()
		if childMsg == nil {
			return nil, fmt.Errorf("in field path %q: field %q of type %s should be a message but is instead %s",
				path, part, msg.FullName(), field.Kind())
		}
		msg, fields = childMsg, childMsg.Fields()
	}
	return result, nil
}

// resolveFieldDescriptorsToPath translates the given path of FieldDescriptors into a string
// of the form "ident.ident.ident". If toJSON is true, the JSON name of the field is used.
func resolveFieldDescriptorsToPath(fields []protoreflect.FieldDescriptor, toJSON bool) string {
	if len(fields) == 0 {
		return ""
	}
	sb := strings.Builder{}
	for i, field := range fields {
		if i > 0 {
			sb.WriteByte('.')
		}
		var name string
		if toJSON {
			name = field.JSONName()
		} else {
			name = string(field.Name())
		}
		_, _ = sb.WriteString(name)
	}
	return sb.String()
}

type alreadyExistsError struct {
	existing            *routeTarget
	pathPattern, method string
}

func (a alreadyExistsError) Error() string {
	return fmt.Sprintf("target for %s, method %s already exists: %s", a.pathPattern, a.method, a.existing.method.descriptor.FullName())
}
