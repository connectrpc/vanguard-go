// Copyright 2023-2024 Buf Technologies, Inc.
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

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"
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
func (t *routeTrie) addRoute(config *methodConfig, rule *annotations.HttpRule) (*routeTarget, error) {
	var method, template string
	switch pattern := rule.GetPattern().(type) {
	case *annotations.HttpRule_Get:
		method, template = http.MethodGet, pattern.Get
	case *annotations.HttpRule_Put:
		method, template = http.MethodPut, pattern.Put
	case *annotations.HttpRule_Post:
		method, template = http.MethodPost, pattern.Post
	case *annotations.HttpRule_Delete:
		method, template = http.MethodDelete, pattern.Delete
	case *annotations.HttpRule_Patch:
		method, template = http.MethodPatch, pattern.Patch
	case *annotations.HttpRule_Custom:
		method, template = pattern.Custom.GetKind(), pattern.Custom.GetPath()
	default:
		return nil, fmt.Errorf("invalid type of pattern for HTTP rule: %T", pattern)
	}
	if method == "" {
		return nil, errors.New("invalid HTTP rule: method is blank")
	}
	if template == "" {
		return nil, errors.New("invalid HTTP rule: path template is blank")
	}
	segments, variables, err := parsePathTemplate(template)
	if err != nil {
		return nil, err
	}
	target, err := makeTarget(config, method, rule.GetBody(), rule.GetResponseBody(), segments, variables)
	if err != nil {
		return nil, err
	}
	if err := t.insert(target); err != nil {
		return nil, err
	}
	return target, nil
}

// addDefaultRoute adds a default target to the router for the given method.
// This may be overridden by a explicit rule. The default route is of the form
// POST /service/method.
func (t *routeTrie) addDefaultRoute(config *methodConfig) (*routeTarget, error) {
	target := makeDefaultTarget(config)
	if err := t.insert(target); err != nil {
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

// insert the target into the trie using the given method and segment path.
// The path is followed until the final segment is reached.
func (t *routeTrie) insert(target *routeTarget) error {
	cursor := t
	for _, segment := range target.path {
		cursor = cursor.insertChild(segment)
	}
	if existing := cursor.verbs[target.verb][target.method]; existing != nil && !existing.isImplicit {
		return alreadyExistsError{target: target, existing: existing}
	}
	cursor.insertVerb(target.verb)[target.method] = target
	return nil
}

// match finds a route for the given request. If a match is found, the associated target and a map
// of matched variable values is returned.
func (t *routeTrie) match(uriPath, httpMethod string) (*routeTarget, []routeTargetVarMatch, routeMethods) {
	if len(uriPath) == 0 || uriPath[0] != '/' || uriPath[len(uriPath)-1] == ':' {
		// Must start with "/" or if it ends with ":" it won't match
		return nil, nil, nil
	}

	path := strings.Split(uriPath[1:], "/") // skip the leading slash
	var verb string
	if len(path) > 0 {
		lastElement := path[len(path)-1]
		if pos := strings.IndexRune(lastElement, ':'); pos >= 0 {
			path[len(path)-1] = lastElement[:pos]
			verb = lastElement[pos+1:]
		}
	}
	target, methods := t.findTarget(path, verb, httpMethod)
	if target == nil {
		return nil, nil, methods
	}
	vars, err := computeVarValues(path, target)
	if err != nil {
		return nil, nil, nil
	}
	return target, vars, nil
}

// findTarget finds the target for the given path components, verb, and method.
// The method either returns a target OR the set of methods for the given path
// and verb. If the target is non-nil, the request was matched. If the target
// is nil but methods are non-nil, the path and verb matched a route, but not
// the method. This can be used to send back a well-formed "Allow" response
// header. If both are nil, the path and verb did not match.
func (t *routeTrie) findTarget(path []string, verb, method string) (*routeTarget, routeMethods) {
	if len(path) == 0 {
		return t.getTarget(verb, method)
	}
	current := path[0]
	path = path[1:]

	if child := t.children[current]; child != nil {
		target, methods := child.findTarget(path, verb, method)
		if target != nil || methods != nil {
			return target, methods
		}
	}

	if childAst := t.children["*"]; childAst != nil {
		target, methods := childAst.findTarget(path, verb, method)
		if target != nil || methods != nil {
			return target, methods
		}
	}

	// Double-asterisk must be the last element in pattern.
	// So it consumes all remaining path elements.
	if childDblAst := t.children["**"]; childDblAst != nil {
		return childDblAst.findTarget(nil, verb, method)
	}
	return nil, nil
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
	config                *methodConfig
	method                string // HTTP method
	path                  []string
	verb                  string
	requestBodyFieldPath  string
	requestBodyFields     []protoreflect.FieldDescriptor
	responseBodyFieldPath string
	responseBodyFields    []protoreflect.FieldDescriptor
	vars                  []routeTargetVar

	// isImplicit is true if the target was created implicitly, such as for a
	// default method. This is used to prevent overwriting an explicit target.
	isImplicit bool
}

func (t *routeTarget) String() string {
	return t.method + " " + pathSegments{path: t.path, verb: t.verb}.String()
}

func makeTarget(
	config *methodConfig,
	method, requestBody, responseBody string,
	segments pathSegments,
	variables []pathVariable,
) (*routeTarget, error) {
	requestBodyFields, err := resolvePathToDescriptors(config.descriptor.Input(), requestBody)
	if err != nil {
		return nil, err
	}
	if len(requestBodyFields) > 1 {
		return nil, fmt.Errorf(
			"unexpected request body path %q: must be a single field",
			requestBody,
		)
	}
	responseBodyFields, err := resolvePathToDescriptors(config.descriptor.Output(), responseBody)
	if err != nil {
		return nil, err
	}
	if len(responseBodyFields) > 1 {
		return nil, fmt.Errorf(
			"unexpected response body path %q: must be a single field",
			requestBody,
		)
	}
	routeTargetVars := make([]routeTargetVar, len(variables))
	for i, variable := range variables {
		fields, err := resolvePathToDescriptors(config.descriptor.Input(), variable.fieldPath)
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
	return &routeTarget{
		config:                config,
		method:                method,
		path:                  segments.path,
		verb:                  segments.verb,
		requestBodyFieldPath:  requestBody,
		requestBodyFields:     requestBodyFields,
		responseBodyFieldPath: responseBody,
		responseBodyFields:    responseBodyFields,
		vars:                  routeTargetVars,
	}, nil
}

// makeDefaultTarget of the form POST /service/method.
func makeDefaultTarget(
	config *methodConfig,
) *routeTarget {
	return &routeTarget{
		config: config,
		method: http.MethodPost,
		path: []string{
			string(config.descriptor.Parent().FullName()),
			string(config.descriptor.Name()),
		},
		requestBodyFieldPath:  "*",
		requestBodyFields:     []protoreflect.FieldDescriptor{},
		responseBodyFieldPath: "*",
		responseBodyFields:    []protoreflect.FieldDescriptor{},
		isImplicit:            true,
	}
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
func (v routeTargetVar) capture(segments []string) (string, error) {
	parts := v.index(segments)
	mode := pathEncodeSingle
	if v.end == -1 || v.start-v.end > 1 {
		mode = pathEncodeMulti
	}
	var sb strings.Builder
	for i, part := range parts {
		val, err := pathUnescape(part, mode)
		if err != nil {
			return "", err
		}
		if i > 0 {
			sb.WriteByte('/')
		}
		sb.WriteString(val)
	}
	return sb.String(), nil
}

type routeTargetVarMatch struct {
	fields []protoreflect.FieldDescriptor
	value  string
}

func computeVarValues(path []string, target *routeTarget) ([]routeTargetVarMatch, error) {
	if len(target.vars) == 0 {
		return nil, nil
	}
	vars := make([]routeTargetVarMatch, len(target.vars))
	for i, varDef := range target.vars {
		val, err := varDef.capture(path)
		if err != nil {
			return nil, err
		}
		vars[i].fields = varDef.fields
		vars[i].value = val
	}
	return vars, nil
}

// resolvePathToDescriptors translates the given path string, in the form of "ident.ident.ident",
// into a path of FieldDescriptors, relative to the given msg.
func resolvePathToDescriptors(msg protoreflect.MessageDescriptor, path string) ([]protoreflect.FieldDescriptor, error) {
	if path == "" {
		return nil, nil
	}
	if path == "*" {
		// non-nil, empty slice means use the whole thing
		return []protoreflect.FieldDescriptor{}, nil
	}
	fields := msg.Fields()
	parts := strings.Split(path, ".")
	result := make([]protoreflect.FieldDescriptor, len(parts))
	for i, part := range parts {
		field := fields.ByName(protoreflect.Name(part))
		if field == nil {
			return nil, fmt.Errorf("in field path %q: element %q does not correspond to any field of type %s",
				path, part, msg.FullName())
		}
		result[i] = field
		if i == len(parts)-1 {
			break
		}
		if field.Cardinality() == protoreflect.Repeated {
			return nil, fmt.Errorf("in field path %q: field %q of type %s should not be a list or map",
				path, part, msg.FullName())
		}
		msg = field.Message()
		if msg == nil {
			return nil, fmt.Errorf("in field path %q: field %q of type %s should be a message but is instead %s",
				path, part, protoreflect.MessageKind, field.Kind())
		}
		fields = msg.Fields()
	}
	return result, nil
}

// resolveFieldDescriptorsToPath translates the given path of FieldDescriptors into a string
// of the form "ident.ident.ident".
func resolveFieldDescriptorsToPath(fields []protoreflect.FieldDescriptor) string {
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = string(field.Name())
	}
	return strings.Join(parts, ".")
}

type alreadyExistsError struct {
	target, existing *routeTarget
}

func (a alreadyExistsError) Error() string {
	return fmt.Sprintf(
		"target for %s already exists: %s",
		a.target.String(), a.existing.config.descriptor.FullName(),
	)
}
