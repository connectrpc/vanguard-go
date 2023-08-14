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
	target, err := makeTarget(config, rule.Body, rule.ResponseBody, variables)
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
	cursor := trie
	for _, segment := range segments.path {
		cursor = cursor.insertChild(segment)
	}
	if existing := cursor.verbs[segments.verb][method]; existing != nil {
		return alreadyExistsError{
			existing: existing, pathPattern: segments.String(), method: method,
		}
	}
	cursor.insertVerb(segments.verb)[method] = target
	return nil
}

// match finds a route for the given request. If a match is found, the associated target and a map
// of matched variable values is returned.
func (trie *routeTrie) match(path string, method string) (*routeTarget, []routeTargetVarMatch) {
	if !strings.HasPrefix(path, "/") || strings.HasSuffix(path, ":") {
		return nil, nil
	}
	segments := strings.Split(path[1:], "/")
	var verb string
	if len(path) > 0 {
		lastElement := segments[len(segments)-1]
		if pos := strings.IndexRune(lastElement, ':'); pos >= 0 {
			segments[len(segments)-1] = lastElement[:pos]
			verb = lastElement[pos+1:]
		}
	}
	target, _ := trie.findTarget(segments, verb, method)
	if target == nil {
		return nil, nil
	}
	return target, computeVarValues(segments, target)
}

func (trie *routeTrie) findTarget(path []string, verb, method string) (*routeTarget, routeMethods) {
	if trie == nil {
		return nil, nil
	}
	if len(path) == 0 {
		methods := trie.verbs[verb]
		if target := methods[method]; target != nil {
			return target, methods
		}
		if target := methods["*"]; target != nil {
			return target, methods
		}
		return nil, nil
	}

	current := path[0]
	path = path[1:]

	if target, methods := trie.children[current].findTarget(path, verb, method); target != nil {
		return target, methods
	}
	if target, methods := trie.children["*"].findTarget(path, verb, method); target != nil {
		return target, methods
	}
	// Double-asterisk must be the last element in pattern.
	// So it consumes all remaining path elements.
	return trie.children["**"].findTarget(nil, verb, method)
}

type routeMethods map[string]*routeTarget

type routeTarget struct {
	config           *methodConfig
	requestBodyPath  []protoreflect.FieldDescriptor
	responseBodyPath []protoreflect.FieldDescriptor
	vars             []routeTargetVar
}

type routeTargetVar struct {
	pathVariable

	fields []protoreflect.FieldDescriptor
}

type routeTargetVarMatch struct {
	fields []protoreflect.FieldDescriptor
	value  string
}

func makeTarget(config *methodConfig, requestBody, responseBody string, variables []pathVariable) (*routeTarget, error) {
	requestBodyPath, err := resolvePathToDescriptors(config.descriptor.Input(), requestBody)
	if err != nil {
		return nil, err
	}
	responseBodyPath, err := resolvePathToDescriptors(config.descriptor.Output(), responseBody)
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
		config:           config,
		requestBodyPath:  requestBodyPath,
		responseBodyPath: responseBodyPath,
		vars:             routeTargetVars,
	}, nil
}

func computeVarValues(path []string, target *routeTarget) []routeTargetVarMatch {
	if len(target.vars) == 0 {
		return nil
	}
	vars := make([]routeTargetVarMatch, len(target.vars))
	for i, varDef := range target.vars {
		vars[i].fields = varDef.fields
		var pathElements []string
		if varDef.end == -1 {
			if varDef.start < len(path) {
				pathElements = path[varDef.start:]
			} else {
				// leave value blank; it was double-asterisk that matched zero path elements
				continue
			}
		} else {
			pathElements = path[varDef.start:varDef.end]
		}
		vars[i].value = strings.Join(pathElements, "/")
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
