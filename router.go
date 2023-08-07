// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	varRefCounter atomic.Int64
)

// router is the store of supported HTTP request paths.
type router struct {
	root routeTrie
	// canonical set of markers for all variable expressions
	varMarkers map[string]varMarker
}

// addRoute adds a target to the router for the given method and the given
// HTTP rule. Only the rule itself is added. If the rule indicates additional
// bindings, they are ignored. To add routes for all bindings, callers must
// invoke this method for each rule.
func (r *router) addRoute(config *methodConfig, rule *annotations.HttpRule) error {
	var method, template string
	switch p := rule.Pattern.(type) {
	case *annotations.HttpRule_Get:
		method, template = http.MethodGet, p.Get
	case *annotations.HttpRule_Put:
		method, template = http.MethodPut, p.Put
	case *annotations.HttpRule_Post:
		method, template = http.MethodPost, p.Post
	case *annotations.HttpRule_Delete:
		method, template = http.MethodDelete, p.Delete
	case *annotations.HttpRule_Patch:
		method, template = http.MethodPatch, p.Patch
	case *annotations.HttpRule_Custom:
		method, template = p.Custom.GetKind(), p.Custom.GetPath()
	default:
		return fmt.Errorf("invalid type of pattern for HTTP rule: %T", p)
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
	varsIndex := varsIndex{}
	if err := r.indexVars(target, path, varsIndex, map[string]struct{}{}); err != nil {
		return err
	}
	existing, err := r.root.insert(newStack(path), nil, method, target, varsIndex)
	if existing != nil {
		return alreadyExistsError{existing: existing, pathPattern: path.String(), method: method}
	}
	return err
}

// indexVars populates the given varsIndex and the vars field of the given target for all
// variables referenced in the given path. This index is used to mark nodes of the routeTrie
// for where variables start and end.
func (r *router) indexVars(target *routeTarget, path routePath, varsIndex varsIndex, varsSeen map[string]struct{}) error {
	for i := range path {
		element := &path[i]
		varPath := element.variable.varPath
		if varPath == "" {
			continue // not a variable
		}
		if _, ok := varsSeen[varPath]; ok {
			return fmt.Errorf("path defines variable for field %q more than once", varPath)
		}
		varsSeen[varPath] = struct{}{}

		msg := target.config.descriptor.Input()
		expr := element.variable.String()
		marker := r.varMarkers[expr]
		if marker == 0 {
			marker = varMarker(varRefCounter.Add(1))
			if r.varMarkers == nil {
				r.varMarkers = map[string]varMarker{}
			}
			r.varMarkers[expr] = marker
		}
		fieldPath, err := computePath(msg, varPath)
		if err != nil {
			return err
		}
		// TODO: disallow composite types: make sure last element in fieldPath is neither a message nor repeated
		varsIndex[&element.variable] = marker
		if target.vars == nil {
			target.vars = map[varMarker][]protoreflect.FieldDescriptor{}
		}
		target.vars[marker] = fieldPath

		if err := r.indexVars(target, element.variable.segments, varsIndex, varsSeen); err != nil {
			return err
		}
	}
	return nil
}

// match finds a route for the given request. If a match is found, the associated target and a map
// of matched variable values is returned.
func (r *router) match(req *http.Request) (*routeTarget, map[varMarker]string) {
	path := strings.Split(req.URL.Path, "/")
	var verb string
	if len(path) > 0 {
		lastElement := path[len(path)-1]
		if pos := strings.IndexRune(lastElement, ':'); pos >= 0 {
			path[len(path)-1] = lastElement[:pos]
			verb = lastElement[pos+1:]
		}
	}
	return r.root.findTarget("", path, verb, req.Method, varAccumulator{})
}

// varMarker is a marker value for a particular variable expression in a path template.
// Unique values are provided by varRefCounter.
type varMarker int64

// varsIndex is a map of routePathVar instances inside a routePath to associated varMarker
// instances. This is used when adding a routePath to a routeTrie -- the markers identify the
// variable and aid the routeTrie in constructing variable values when  matching a URI path.
type varsIndex map[*routePathVar]varMarker

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
	// Var definitions that start with this node of the trie.
	startVars map[varMarker]struct{}
	// Var definitions that end with this node of the trie.
	endVars map[varMarker]struct{}
}

// insert inserts the given target into the trie using the given method and paths. The
// paths represent a stack. When a path element corresponds to a variable, that variable's
// path is pushed to the top of the stack. Invocations of this function always work on the
// first element of the path at the top of the stack. The function is recursive: after an
// element is processed, a sub-trie handles the next element.
func (t *routeTrie) insert(
	stack *routeStack,
	startVars []varMarker,
	method string,
	target *routeTarget,
	varsIndex varsIndex,
) (*routeTarget, error) {
	elem, endVars := stack.pop()

	if t.endVars == nil {
		t.endVars = map[varMarker]struct{}{}
	}
	for _, marker := range endVars {
		t.endVars[marker] = struct{}{}
	}

	if elem == nil {
		if t.methods == nil {
			t.methods = routeMethods{}
		}
		return t.methods.insert(method, target), nil
	}

	switch {
	case elem.segment != "":
		child := t.children[elem.segment]
		if child == nil {
			if t.children == nil {
				t.children = map[string]*routeTrie{}
			}
			child = &routeTrie{}
			t.children[elem.segment] = child
		}

		if child.startVars == nil {
			child.startVars = map[varMarker]struct{}{}
		}
		for _, marker := range startVars {
			child.startVars[marker] = struct{}{}
		}

		return child.insert(stack, nil, method, target, varsIndex)

	case elem.verb != "":
		if !stack.empty() {
			return nil, fmt.Errorf("invalid path: verb element must be last")
		}
		methods := t.verbs[elem.verb]
		if methods == nil {
			if t.verbs == nil {
				t.verbs = map[string]routeMethods{}
			}
			methods = routeMethods{}
			t.verbs[elem.verb] = methods
		}
		return methods.insert(method, target), nil

	case len(elem.variable.varPath) > 0:
		marker := varsIndex[&elem.variable]
		nextPath := elem.variable.segments
		if len(nextPath) == 0 {
			nextPath = routePath{{segment: "*"}}
		}
		stack.pushVariable(nextPath, marker)
		startVars = append(startVars, marker)
		return t.insert(stack, startVars, method, target, varsIndex)

	default:
		return nil, fmt.Errorf("invalid path element contains no values")
	}
}

func (t *routeTrie) findTarget(prev string, path []string, verb, method string, vars varAccumulator) (*routeTarget, map[varMarker]string) {
	if prev != "" {
		for v, entry := range vars {
			if entry.active {
				entry.elements = append(entry.elements, prev)
				vars[v] = entry
			}
		}
		for v := range t.startVars {
			vars[v] = varAccumEntry{active: true, elements: []string{prev}}
		}
		for v := range t.endVars {
			if entry, ok := vars[v]; ok {
				entry.active = false
				vars[v] = entry
			}
		}
	}

	if len(path) == 0 {
		methods := t.methods
		if verb != "" {
			methods = t.verbs[verb]
		}
		target := methods[method]
		if target == nil {
			return nil, nil
		}
		var varValues map[varMarker]string
		if len(target.vars) > 0 {
			varValues = make(map[varMarker]string, len(target.vars))
			for v := range target.vars {
				vals := vars[v].elements
				varValues[v] = strings.Join(vals, "/")
			}
		}
		return target, varValues
	}

	current := path[0]
	path = path[1:]
	var present int
	child := t.children[current]
	if child != nil {
		present++
	}
	childAst := t.children["*"]
	if childAst != nil {
		present++
	}
	childDblAst := t.children["**"]
	if childDblAst != nil {
		present++
	}

	var snapshotVars varAccumulator
	if present > 1 {
		// Create a snapshot if we may have to backtrack during matching (where
		// we try one of child, and then try another if that finds no match).
		// We guard this only in conditions where backtracking may occur to
		// avoid unnecessary allocations. (Otherwise, we'd have to allocate and
		// copy the varAccumulator for every single node of the trie that is
		// visited, even though it is usually not necessary.)
		snapshotVars = vars.snapshot()
	}

	if child != nil {
		target, varResults := child.findTarget(current, path, verb, method, vars)
		if target != nil {
			return target, varResults
		}
		if present > 1 {
			vars.resetTo(snapshotVars) // we need to restore vars to do a match below
		}
	}

	if childAst != nil {
		target, varResults := childAst.findTarget(current, path, verb, method, vars)
		if target != nil {
			return target, varResults
		}
		if childDblAst != nil {
			vars.resetTo(snapshotVars) // we need to restore vars to do a match below
		}
	}

	// Double-asterisk must be the last element in pattern.
	// So it consumes all remaining path elements.
	if childDblAst == nil {
		return nil, nil
	}
	if len(path) > 0 {
		current += "/" + strings.Join(path, "/")
	}
	path = nil
	return childDblAst.findTarget(current, path, verb, method, vars)
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
	config           *methodConfig
	requestBodyPath  []protoreflect.FieldDescriptor
	responseBodyPath []protoreflect.FieldDescriptor
	vars             map[varMarker][]protoreflect.FieldDescriptor
}

func makeTarget(config *methodConfig, requestBody, responseBody string) (*routeTarget, error) {
	requestBodyPath, err := computePath(config.descriptor.Input(), requestBody)
	if err != nil {
		return nil, err
	}
	responseBodyPath, err := computePath(config.descriptor.Output(), responseBody)
	if err != nil {
		return nil, err
	}
	return &routeTarget{
		config:           config,
		requestBodyPath:  requestBodyPath,
		responseBodyPath: responseBodyPath,
	}, nil
}

type routeStackEntry struct {
	path     routePath
	variable varMarker
}

type routeStack []routeStackEntry

func newStack(path routePath) *routeStack {
	return &routeStack{{path: path}}
}

// pop returns the next element in the stack or nil if there are no elements.
// The second value returned is all of the vars that are no longer active.
//
// The each entry in the stack is a path. So pop works top-to-bottom,
// left-to-right, returning the first element for the top path in the
// stack.
func (s *routeStack) pop() (*routePathElement, []varMarker) {
	if len(*s) == 0 {
		return nil, nil
	}
	topIndex := len(*s) - 1
	top := &(*s)[topIndex]
	var vars []varMarker
	for len(top.path) == 0 {
		if top.variable != 0 {
			vars = append(vars, top.variable)
		}
		*s = (*s)[:topIndex]
		topIndex--
		if topIndex < 0 {
			// stack is empty
			return nil, vars
		}
		top = &(*s)[topIndex]
	}
	// Update top of stack to remove element
	elem := &top.path[0]
	top.path = top.path[1:]
	return elem, vars
}

// pushVariable pushes the segments associated with a variable onto the stack.
func (s *routeStack) pushVariable(path routePath, variable varMarker) {
	*s = append(*s, routeStackEntry{path: path, variable: variable})
}

func (s *routeStack) empty() bool {
	for _, entry := range *s {
		if len(entry.path) > 0 {
			return false
		}
	}
	return true
}

// varAccumulator accumulates parts of var values while we are matching a URI path to
// a path through the routeTrie. Since a variable can match more than one path component,
// we accumulate those path components as we descend through the trie.
type varAccumulator map[varMarker]varAccumEntry

func (v varAccumulator) snapshot() varAccumulator {
	other := varAccumulator{}
	for marker, entry := range v {
		other[marker] = entry
	}
	return other
}

func (v varAccumulator) resetTo(snapshot varAccumulator) {
	// Set all entries to match the snapshot
	for marker, entry := range snapshot {
		v[marker] = entry
	}
	// Remove any other vars not in the snapshot
	for marker := range v {
		if _, ok := snapshot[marker]; !ok {
			delete(v, marker)
		}
	}
}

type varAccumEntry struct {
	elements []string
	active   bool // if true, we are still matching this variable and addig path components to it
}

// computePath translates the given path string, in the form of "ident.ident.ident", into a path of
// FieldDescriptors, relative to the given msg.
func computePath(msg protoreflect.MessageDescriptor, path string) ([]protoreflect.FieldDescriptor, error) {
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
