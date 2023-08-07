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
	path, err := parsePathTemplate(template)
	if err != nil {
		return err
	}
	target, err := makeTarget(config, rule.Body, rule.ResponseBody)
	if err != nil {
		return err
	}
	if _, err := indexVars(target, path, 0, map[string]struct{}{}); err != nil {
		return err
	}
	existing, err := trie.insert(newStack(path), method, target)
	if existing != nil {
		return alreadyExistsError{existing: existing, pathPattern: path.String(), method: method}
	}
	return err
}

// insert inserts the given target into the trie using the given method and paths. The
// paths represent a stack. When a path element corresponds to a variable, that variable's
// path is pushed to the top of the stack. Invocations of this function always work on the
// first element of the path at the top of the stack. The function is recursive: after an
// element is processed, a sub-trie handles the next element.
func (trie *routeTrie) insert(stack *routeStack, method string, target *routeTarget) (*routeTarget, error) {
	for elem := stack.pop(); elem != nil; elem = stack.pop() {
		switch {
		case elem.segment != "":
			child := trie.children[elem.segment]
			if child == nil {
				if trie.children == nil {
					trie.children = map[string]*routeTrie{}
				}
				child = &routeTrie{}
				trie.children[elem.segment] = child
			}
			trie = child

		case elem.verb != "":
			if !stack.empty() {
				return nil, fmt.Errorf("invalid path: verb element must be last")
			}
			methods := trie.verbs[elem.verb]
			if methods == nil {
				if trie.verbs == nil {
					trie.verbs = map[string]routeMethods{}
				}
				methods = routeMethods{}
				trie.verbs[elem.verb] = methods
			}
			return methods.insert(method, target), nil

		case len(elem.variable.varPath) > 0:
			nextPath := elem.variable.segments
			if len(nextPath) == 0 {
				nextPath = routePath{{segment: "*"}}
			}
			stack.pushVariable(nextPath)

		default:
			return nil, fmt.Errorf("invalid path element contains no values")
		}
	}
	if trie.methods == nil {
		trie.methods = routeMethods{}
	}
	return trie.methods.insert(method, target), nil
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

func (m routeMethods) insert(method string, target *routeTarget) *routeTarget {
	if existing, ok := m[method]; ok {
		return existing
	}
	m[method] = target
	return nil
}

type routeTarget struct {
	config *methodConfig
	//nolint:unused
	requestBodyPath []protoreflect.FieldDescriptor
	//nolint:unused
	responseBodyPath []protoreflect.FieldDescriptor
	vars             []routeTargetVar
}

type routeTargetVar struct {
	varPath []protoreflect.FieldDescriptor
	// start and end path components for this var. If end == -1, then this
	// var is a trailing double wildcard, in which case start may be
	// == len(path) which means that the variable's value is empty.
	// The start value is inclusive; end is exclusive (just like the start
	// and end expressions in a Go slice expression: str[start:end]).
	start, end int
}

type routeTargetVarMatch struct {
	varPath []protoreflect.FieldDescriptor
	value   string
}

//nolint:unused
func makeTarget(config *methodConfig, requestBody, responseBody string) (*routeTarget, error) {
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
	}, nil
}

type routeStack []routePath

func newStack(path routePath) *routeStack {
	return &routeStack{path}
}

// pop returns the next element in the stack or nil if there are no elements.
// The second value returned is all of the vars that are no longer active.
//
// The each entry in the stack is a path. So pop works top-to-bottom,
// left-to-right, returning the first element for the top path in the
// stack.
func (s *routeStack) pop() *routePathElement {
	if len(*s) == 0 {
		return nil
	}
	topIndex := len(*s) - 1
	top := &(*s)[topIndex]
	for len(*top) == 0 {
		*s = (*s)[:topIndex]
		topIndex--
		if topIndex < 0 {
			// stack is empty
			return nil
		}
		top = &(*s)[topIndex]
	}
	// Update top of stack to remove element
	elem := &(*top)[0]
	*top = (*top)[1:]
	return elem
}

// pushVariable pushes the segments associated with a variable onto the stack.
func (s *routeStack) pushVariable(path routePath) {
	*s = append(*s, path)
}

func (s *routeStack) empty() bool {
	for _, entry := range *s {
		if len(entry) > 0 {
			return false
		}
	}
	return true
}

// indexVars populates the given target with details about the vars in the given path.
func indexVars(target *routeTarget, path routePath, offset int, varsSeen map[string]struct{}) (int, error) {
	length := len(path)
	if (length > 0 && path[length-1].segment == "**") ||
		(length > 1 && path[length-1].verb != "" && path[length-2].segment == "**") {
		length = -1
	}
	for i := range path {
		element := &path[i]
		varPath := element.variable.varPath
		if varPath == "" {
			continue // not a variable
		}
		if _, ok := varsSeen[varPath]; ok {
			return 0, fmt.Errorf("path defines variable for field %q more than once", varPath)
		}
		varsSeen[varPath] = struct{}{}

		msg := target.config.descriptor.Input()
		fieldPath, err := resolvePathToDescriptors(msg, varPath)
		if err != nil {
			return 0, err
		}
		// TODO: disallow composite types: make sure last element in fieldPath is neither a message nor repeated
		start := i + offset
		varLen := 1
		if len(element.variable.segments) > 0 {
			varLen, err = indexVars(target, element.variable.segments, start, varsSeen)
			if err != nil {
				return 0, err
			}
		}
		end := start + varLen
		if varLen == -1 {
			length = -1
			end = -1
		} else {
			offset += varLen - 1
			if length != -1 {
				length += varLen - 1
			}
		}
		target.vars = append(target.vars, routeTargetVar{
			varPath: fieldPath,
			start:   start,
			end:     end,
		})
	}
	return length, nil
}

func computeVarValues(path []string, target *routeTarget) []routeTargetVarMatch {
	if len(target.vars) == 0 {
		return nil
	}
	vars := make([]routeTargetVarMatch, len(target.vars))
	for i, varDef := range target.vars {
		vars[i].varPath = varDef.varPath
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

//nolint:unused
type alreadyExistsError struct {
	existing            *routeTarget
	pathPattern, method string
}

//nolint:unused
func (a alreadyExistsError) Error() string {
	return fmt.Sprintf("target for %s, method %s already exists: %s", a.pathPattern, a.method, a.existing.config.descriptor.FullName())
}
