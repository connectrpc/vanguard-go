// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
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
	// If the path to this node represents a complete path, this
	// will be non-nil and represent the method table  for the path.
	methods routeMethods
	// Child nodes, keyed by the next segment in the path.
	children map[string]*routeTrie
	// If this is the final path element, any matches for this path
	// that contain ":verb" suffixes are in this map, keyed by verb.
	verbs map[string]routeMethods
}

// addRoute adds a target to the router for the given method and the given
// HTTP rule. Only the rule itself is added. If the rule indicates additional
// bindings, they are ignored. To add routes for all bindings, callers must
// invoke this method for each rule.
func (trie *routeTrie) addRoute(config *methodConfig, rule *annotations.HttpRule) error {
	var method, template string
	switch pattern := rule.Pattern.(type) {
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
		return fmt.Errorf("invalid type of pattern for HTTP rule: %T", pattern)
	}
	if method == "" {
		return fmt.Errorf("invalid HTTP rule: method is blank")
	}
	if template == "" {
		return fmt.Errorf("invalid HTTP rule: path template is blank")
	}

	segments, variables, err := parsePathTemplate(template)
	if err != nil {
		return err
	}
	target, err := makeTarget(config, method, rule.Body, rule.ResponseBody, segments, variables)
	if err != nil {
		return err
	}
	return trie.insert(method, target, segments)
}

func (trie *routeTrie) insertChild(segment string) *routeTrie {
	child := trie.children[segment]
	if child == nil {
		if trie.children == nil {
			trie.children = make(map[string]*routeTrie, 1)
		}
		child = &routeTrie{}
		trie.children[segment] = child
	}
	return child
}
func (trie *routeTrie) insertVerb(verb string) routeMethods {
	methods := trie.verbs[verb]
	if methods == nil {
		if trie.verbs == nil {
			trie.verbs = make(map[string]routeMethods, 1)
		}
		methods = make(routeMethods, 1)
		trie.verbs[verb] = methods
	}
	return methods
}

// insert the target into the trie using the given method and segment path.
// The path is followed until the final segment is reached.
func (trie *routeTrie) insert(method string, target *routeTarget, segments pathSegments) error {
	cursor, methods := trie, trie.methods
	for _, segment := range segments.path {
		cursor = cursor.insertChild(segment)
		methods = cursor.methods // may be nil.
	}
	if segments.verb != "" {
		methods = cursor.insertVerb(segments.verb) // cannot be nil.
		cursor = nil
	}
	if existing := methods[method]; existing != nil {
		return alreadyExistsError{
			existing: existing, pathPattern: segments.String(), method: method,
		}
	}
	// Lazily allocate the method map for a trie node.
	if methods == nil {
		methods = make(routeMethods, 1)
		cursor.methods = methods
	}
	methods[method] = target
	return nil
}

// match finds a route for the given request. If a match is found, the associated target and a map
// of matched variable values is returned.
func (trie *routeTrie) match(uriPath, httpMethod string) (*routeTarget, []routeTargetVarMatch, routeMethods) {
	// TODO: Not checking if path ends with "/" means we accept missing final segment
	//       for both * and **. Is that right? Makes sense for **, but maybe not for *.
	if len(uriPath) == 0 || uriPath[0] != '/' || uriPath[len(uriPath)-1] == ':' {
		// TODO: is this how grpc-gateway works? Is it lenient and forgives trailing slash
		//       or absence of leading slash?
		// if it doesn't start with "/" or if it ends with "/" it won't match
		return nil, nil, nil
	}
	uriPath = uriPath[1:] // skip the leading slash

	// TODO: we may want a custom Split so that we can pool the resulting slices
	//       and reduce allocations here; in fact, we could even skip the Split
	//       and just pass the uriPath string to findTarget, which must find
	//       the next slash and split into car/cdr (no allocation required).
	path := strings.Split(uriPath, "/")
	var verb string
	if len(path) > 0 {
		lastElement := path[len(path)-1]
		if pos := strings.IndexRune(lastElement, ':'); pos >= 0 {
			path[len(path)-1] = lastElement[:pos]
			verb = lastElement[pos+1:]
		}
	}
	target, methods := trie.findTarget(path, verb, httpMethod)
	if target == nil {
		return nil, nil, methods
	}
	// TODO: instead of []routeTargetMatch, we may want a different data structure
	//       that doesn't need to allocate slices but instead retrieves substrings
	//       of uriPath on demand.
	return target, computeVarValues(path, target), nil
}

// findTarget finds the target for the given path components, verb, and method.
// The method either returns a target OR the set of methods for the given path
// and verb. If the target is non-nil, the request was matched. If the target
// is nil but methods are non-nil, the path and verb matched a route, but not
// the method. This can be used to send back a well-formed "Allow" response
// header. If both are nil, the path and verb did not match.
func (trie *routeTrie) findTarget(path []string, verb, method string) (*routeTarget, routeMethods) {
	if len(path) == 0 {
		return trie.getTarget(verb, method)
	}

	current := path[0]
	path = path[1:]

	if child := trie.children[current]; child != nil {
		target, methods := child.findTarget(path, verb, method)
		if target != nil || methods != nil {
			return target, methods
		}
	}

	if childAst := trie.children["*"]; childAst != nil {
		target, methods := childAst.findTarget(path, verb, method)
		if target != nil || methods != nil {
			return target, methods
		}
	}

	// Double-asterisk must be the last element in pattern.
	// So it consumes all remaining path elements.
	childDblAst := trie.children["**"]
	if childDblAst == nil {
		return nil, nil
	}
	return childDblAst.findTarget(nil, verb, method)
}

// getTarget gets the target for the given verb and method from the
// node trie. It is like findTarget, except that it does not use a
// path to first descend into a sub-trie.
func (trie *routeTrie) getTarget(verb, method string) (*routeTarget, routeMethods) {
	methods := trie.methods
	if verb != "" {
		methods = trie.verbs[verb]
	}
	target := methods[method]
	if target != nil {
		return target, nil
	}
	// See if a wildcard method was used
	target = methods["*"]
	if target != nil {
		return target, nil
	}
	// TODO: If final segment is ** and nothing is provided that matches, the request path should
	//       have trailing slash. For example, if the pattern is "foo/bar/**", an empty match
	//       should look like "foo/bar/". However, *should* it support having no trailing slash?
	//       For example, should pattern "foo/bar/**" allow "foo/bar"? If so, we'd need to do a
	//       little more work here, to see if trie has a ** child that has a matching method.
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
	responseBodyFields, err := resolvePathToDescriptors(config.descriptor.Output(), responseBody)
	if err != nil {
		return nil, err
	}
	routeTargetVars := make([]routeTargetVar, len(variables))
	for i, variable := range variables {
		fields, err := resolvePathToDescriptors(config.descriptor.Input(), variable.fieldPath)
		if err != nil {
			return nil, err
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
func (v routeTargetVar) capture(segments []string) string {
	start, end := v.start, v.end
	if v.end == -1 {
		if start >= len(segments) {
			return ""
		}
		end = len(segments)
	}
	return strings.Join(segments[start:end], "/")
}

type routeTargetVarMatch struct {
	fields []protoreflect.FieldDescriptor
	value  string
}

func computeVarValues(path []string, target *routeTarget) []routeTargetVarMatch {
	if len(target.vars) == 0 {
		return nil
	}
	vars := make([]routeTargetVarMatch, len(target.vars))
	for i, varDef := range target.vars {
		vars[i].fields = varDef.fields
		vars[i].value = varDef.capture(path)
	}
	return vars
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
				path, part, msg.FullName(), field.Kind())
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
	existing            *routeTarget
	pathPattern, method string
}

func (a alreadyExistsError) Error() string {
	return fmt.Sprintf("target for %s, method %s already exists: %s", a.pathPattern, a.method, a.existing.config.descriptor.FullName())
}
