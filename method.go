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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

// method is the per-procedure metadata vanguard needs to bridge between
// REST and a [connect.Server] / [connect.Transport]. One method per
// registered RPC that has at least one HTTP rule (either an embedded
// google.api.http annotation or one supplied via WithRules).
//
// Dispatch flows through [connect.Server.Call], which applies the
// registered interceptor chain; vanguard keeps just the Spec and the
// resolved descriptor / message types here.
type method struct {
	spec       connect.Spec
	descriptor protoreflect.MethodDescriptor
	requestT   protoreflect.MessageType
	responseT  protoreflect.MessageType
	// httpRule is the primary route target (the rule's top-level
	// pattern). Additional bindings register their own routeTargets in
	// the routeTrie and reference the same *method via routeTarget.method.
	httpRule *routeTarget
}

// methodFromSpec converts a [connect.Spec] to a *method when its
// Schema carries a protoreflect.MethodDescriptor, and silently returns
// nil for everything else. A *connect.Server may carry methods from
// multiple transports with different schema conventions; vanguard
// contributes routes only for those it can interpret.
func methodFromSpec(spec connect.Spec, resolver connectproto.TypeResolver) *method {
	desc, ok := spec.Schema.(protoreflect.MethodDescriptor)
	if !ok {
		return nil // the Spec isn't vanguard's to handle
	}
	if resolver == nil {
		if svc, ok := desc.Parent().(protoreflect.ServiceDescriptor); ok {
			resolver = resolverForService(svc)
		} else {
			resolver = protoregistry.GlobalTypes
		}
	}
	return &method{
		spec:       spec,
		descriptor: desc,
		requestT:   messageType(resolver, desc.Input()),
		responseT:  messageType(resolver, desc.Output()),
	}
}

func messageType(resolver connectproto.TypeResolver, desc protoreflect.MessageDescriptor) protoreflect.MessageType {
	if mt, err := resolver.FindMessageByName(desc.FullName()); err == nil {
		return mt
	}
	return dynamicpb.NewMessageType(desc)
}

// resolveMethods walks the server's registered Specs, attaches HTTP
// rules (from annotations and from opts.rules), and returns:
//   - methodsByProcedure: lookup table keyed by procedure path
//   - routes: prefix trie of REST URI templates
//
// A registered procedure with no HTTP rule is silently skipped — vanguard
// only handles methods that opted into REST.
func resolveMethods(
	server *connect.Server,
	opts options,
) (map[string]*method, *routeTrie, error) {
	methodsByProcedure := make(map[string]*method)
	for spec := range server.Specs() {
		method := methodFromSpec(spec, opts.resolver)
		if method == nil {
			// Spec.Schema isn't a proto MethodDescriptor; skip silently
			// so a *Server shared across transports registers cleanly.
			continue
		}
		methodsByProcedure[method.spec.Procedure] = method
	}

	routes := &routeTrie{}

	// 1. Embedded rules on each method descriptor.
	for _, method := range methodsByProcedure {
		rule, ok := getHTTPRuleExtension(method.descriptor)
		if !ok {
			continue
		}
		target, err := routes.addRoute(method, rule)
		if err != nil {
			return nil, nil, fmt.Errorf("attach rule for %s: %w", method.spec.Procedure, err)
		}
		method.httpRule = target
		// Additional bindings register more targets, but they all
		// reference the same method; the trie owns them.
		for _, extra := range rule.GetAdditionalBindings() {
			if _, err := routes.addRoute(method, extra); err != nil {
				return nil, nil, fmt.Errorf("attach additional binding for %s: %w", method.spec.Procedure, err)
			}
		}
	}

	// 2. Rules supplied via WithRules. Each rule's selector picks a
	// procedure; an unmatched selector is a configuration error.
	for _, rule := range opts.rules {
		selector := rule.GetSelector()
		if selector == "" {
			return nil, nil, errors.New("WithRules: rule has no selector")
		}
		procedure := procedureFromSelector(selector)
		method, ok := methodsByProcedure[procedure]
		if !ok {
			return nil, nil, fmt.Errorf("WithRules: selector %q does not match a registered procedure", selector)
		}
		target, err := routes.addRoute(method, rule)
		if err != nil {
			return nil, nil, fmt.Errorf("attach external rule for %s: %w", selector, err)
		}
		if method.httpRule == nil {
			method.httpRule = target
		}
		for _, extra := range rule.GetAdditionalBindings() {
			if _, err := routes.addRoute(method, extra); err != nil {
				return nil, nil, fmt.Errorf("attach additional binding for %s: %w", selector, err)
			}
		}
	}

	return methodsByProcedure, routes, nil
}

// procedureFromSelector converts a google.api.http selector
// ("foo.bar.Service.Method") to a connect procedure
// ("/foo.bar.Service/Method"). The selector is the fully-qualified
// method name; the last dot separates service from method.
func procedureFromSelector(selector string) string {
	last := -1
	for i := len(selector) - 1; i >= 0; i-- {
		if selector[i] == '.' {
			last = i
			break
		}
	}
	if last < 0 {
		return "/" + selector
	}
	return "/" + selector[:last] + "/" + selector[last+1:]
}
