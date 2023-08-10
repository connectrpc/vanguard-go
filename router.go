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
//
//nolint:unused
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

	segments, variables, err := parsePathTemplate(config.descriptor, template)
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
//
//nolint:unused
func (trie *routeTrie) match(req *http.Request) (*routeTarget, []routeTargetVarMatch) {
	path := strings.Split(req.URL.Path, "/")
	var verb string
	if len(path) > 0 {
		lastElement := path[len(path)-1]
		if pos := strings.IndexRune(lastElement, ':'); pos >= 0 {
			path[len(path)-1] = lastElement[:pos]
			verb = lastElement[pos+1:]
		}
	}
	target := trie.findTarget(path, verb, req.Method)
	if target == nil {
		return nil, nil
	}
	return target, computeVarValues(path, target)
}

func (trie *routeTrie) findTarget(path []string, verb, method string) *routeTarget {
	if len(path) == 0 {
		methods := trie.methods
		if verb != "" {
			methods = trie.verbs[verb]
		}
		target := methods[method]
		if target != nil {
			return target
		}
		// Could be a double-wildcard that matches zero path elements
		childDblAst := trie.children["**"]
		if childDblAst != nil {
			return childDblAst.findTarget(nil, verb, method)
		}
		return nil
	}

	current := path[0]
	path = path[1:]

	if child := trie.children[current]; child != nil {
		target := child.findTarget(path, verb, method)
		if target != nil {
			return target
		}
	}

	if childAst := trie.children["*"]; childAst != nil {
		target := childAst.findTarget(path, verb, method)
		if target != nil {
			return target
		}
	}

	// Double-asterisk must be the last element in pattern.
	// So it consumes all remaining path elements.
	childDblAst := trie.children["**"]
	if childDblAst == nil {
		return nil
	}
	return childDblAst.findTarget(nil, verb, method)
}

type routeMethods map[string]*routeTarget

type routeTarget struct {
	config *methodConfig
	//nolint:unused
	requestBodyPath []protoreflect.FieldDescriptor
	//nolint:unused
	responseBodyPath []protoreflect.FieldDescriptor
	vars             pathVariables
}

type routeTargetVarMatch struct {
	fields []protoreflect.FieldDescriptor
	value  string
}

//nolint:unused
func makeTarget(config *methodConfig, requestBody, responseBody string, variables pathVariables) (*routeTarget, error) {
	requestBodyPath, err := resolvePathToDescriptors(config.descriptor.Input(), requestBody)
	if err != nil {
		return nil, err
	}
	responseBodyPath, err := resolvePathToDescriptors(config.descriptor.Output(), responseBody)
	if err != nil {
		return nil, err
	}
	return &routeTarget{
		config:           config,
		requestBodyPath:  requestBodyPath,
		responseBodyPath: responseBodyPath,
		vars:             variables,
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

type alreadyExistsError struct {
	existing            *routeTarget
	pathPattern, method string
}

func (a alreadyExistsError) Error() string {
	return fmt.Sprintf("target for %s, method %s already exists: %s", a.pathPattern, a.method, a.existing.config.descriptor.FullName())
}
