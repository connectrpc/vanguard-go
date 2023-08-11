// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"strings"
)

// parsePathTemplate parses the given path template into a routePath.
func parsePathTemplate(path string) (routePath, error) {
	// TODO
	_ = path
	return nil, errors.New("TODO")
}

// routePath represents a path template found in an HTTP annotation.
type routePath []routePathElement

func (p routePath) String() string {
	var sb strings.Builder
	for _, element := range p {
		element.toStringBuilder(&sb, true)
	}
	return sb.String()
}

func (p routePath) PatternString() string {
	var sb strings.Builder
	for _, element := range p {
		element.toPatternStringBuilder(&sb)
	}
	return sb.String()
}

type routePathElement struct {
	// Only one of the following will be set, depending on whether
	// this path represents a normal segment, a variable definition,
	// or a verb suffix.
	segment  string
	variable routePathVar
	verb     string
}

func (e *routePathElement) toStringBuilder(sb *strings.Builder, leadingSlash bool) {
	switch {
	case e.segment != "":
		if leadingSlash {
			sb.WriteRune('/')
		}
		sb.WriteString(e.segment)
	case e.verb != "":
		sb.WriteRune(':')
		sb.WriteString(e.verb)
	case len(e.variable.varPath) > 0:
		if leadingSlash {
			sb.WriteRune('/')
		}
		e.variable.toStringBuilder(sb)
	default:
		sb.WriteString("<invalid element>")
	}
}

// toPatternStringBuilder adds a pattern for this element to sb. This differs from
// toStringBuilder as it only adds segment patterns and not variable declarations.
// So a variable declaration like "{foo=bar/baz/**}" is represented in the pattern
// as simply "bar/baz/**".
func (e *routePathElement) toPatternStringBuilder(sb *strings.Builder) {
	switch {
	case e.segment != "":
		sb.WriteRune('/')
		sb.WriteString(e.segment)
	case e.verb != "":
		sb.WriteRune(':')
		sb.WriteString(e.verb)
	case len(e.variable.varPath) > 0:
		if len(e.variable.segments) == 0 {
			sb.WriteString("/*")
		} else {
			for _, element := range e.variable.segments {
				element.toPatternStringBuilder(sb)
			}
		}
	default:
		sb.WriteString("<invalid element>")
	}
}

type routePathVar struct {
	varPath string
	// When a variable definition does not indicate a path, it
	// defaults to "*": []routePathElement{{segment:"*"}}
	segments routePath
}

func (v *routePathVar) String() string {
	var sb strings.Builder
	v.toStringBuilder(&sb)
	return sb.String()
}

func (v *routePathVar) toStringBuilder(sb *strings.Builder) {
	sb.WriteRune('{')
	sb.WriteString(v.varPath)
	if len(v.segments) > 0 {
		sb.WriteRune('=')
		for i, element := range v.segments {
			element.toStringBuilder(sb, i > 0)
		}
	}
	sb.WriteRune('}')
}
