// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

type pathSegment struct {
	val    string // segment value.
	isVerb bool   // may be a verb if it's the final segment.
}
type pathSegments []pathSegment

func (s pathSegments) String() string {
	var out strings.Builder
	for _, seg := range s {
		if seg.isVerb {
			out.WriteString(":")
		} else {
			out.WriteString("/")
		}
		out.WriteString(seg.val)
	}
	return out.String()
}

type pathVariable struct {
	varPath    []protoreflect.FieldDescriptor
	start, end int // start and end path segments, inclusive-exclusive, -1 for unbounded.
}

type pathVariables []pathVariable

// parsePathTemplate parsers a methods template into path segments and variables.
//
// ## Path template syntax
//
//	Template = "/" Segments [ Verb ] ;
//	Segments = Segment { "/" Segment } ;
//	Segment  = "*" | "**" | LITERAL | Variable ;
//	Variable = "{" FieldPath [ "=" Segments ] "}" ;
//	FieldPath = IDENT { "." IDENT } ;
//	Verb     = ":" LITERAL ;
func parsePathTemplate(descriptor protoreflect.MethodDescriptor, template string) (
	pathSegments, pathVariables, error,
) {
	parser := &parser{toks: lex(template), desc: descriptor, seenVars: make(map[string]bool)}
	if err := parser.consume(tokenSlash); err != nil {
		return nil, nil, err // empty path is not allowed.
	}
	if err := parser.parseSegments(); err != nil {
		return nil, nil, err
	}
	return parser.segments, parser.variables, nil
}

// parser holds the state for the recursive descent path template parser.
type parser struct {
	desc           protoreflect.MethodDescriptor // input method descriptor.
	toks           []token                       // token input for the parser.
	pos            int                           // current position in the input.
	seenVars       map[string]bool               // set of field paths.
	seenDoubleStar bool                          // true if we've seen a double star wildcard.
	segments       pathSegments                  // output segments.
	variables      pathVariables                 // output variables.
}

func (p *parser) errUnexpected() error {
	tok := p.current()
	if tok.typ == tokenError {
		return fmt.Errorf("syntax error at column %v: %s", tok.pos+1, tok.val)
	}
	return fmt.Errorf("unexpected token at column %v: '%s'", tok.pos+1, tok.val)
}

func (p *parser) next() token {
	if p.pos >= len(p.toks) {
		return token{typ: tokenEOF}
	}
	t := p.toks[p.pos]
	p.pos++
	return t
}
func (p *parser) current() token { return p.toks[p.pos-1] }
func (p *parser) assert(typ tokenType) (token, error) {
	t := p.next()
	if t.typ != typ {
		return token{}, p.errUnexpected()
	}
	return t, nil
}
func (p *parser) consume(typ tokenType) error {
	_, err := p.assert(typ)
	return err
}

func (p *parser) parseSegments() error {
	for {
		if err := p.parseSegment(); err != nil {
			return err
		}
		tok := p.next()
		//nolint:exhaustive
		switch tok.typ {
		case tokenEOF:
			return nil
		case tokenSlash:
			if p.seenDoubleStar {
				return errors.New("double wildcard '**' must be the final path segment")
			}
			continue
		case tokenVerb:
			tok, err := p.assert(tokenLiteral)
			if err != nil {
				return err
			}
			seg := pathSegment{val: tok.val, isVerb: true}
			p.segments = append(p.segments, seg)
			return p.consume(tokenEOF)
		default:
			return p.errUnexpected()
		}
	}
}
func (p *parser) parseSegment() error {
	tok := p.next()
	seg := pathSegment{val: tok.val}
	//nolint:exhaustive
	switch tok.typ {
	case tokenStarStar:
		p.seenDoubleStar = true
	case tokenStar, tokenLiteral:
	case tokenVariableStart:
		return p.parseVariable()
	default:
		return p.errUnexpected()
	}
	p.segments = append(p.segments, seg)
	return nil
}
func (p *parser) parseVariable() error {
	tok, err := p.assert(tokenFieldPath)
	if err != nil {
		return err
	}
	if p.seenVars[tok.val] {
		return fmt.Errorf("duplicate variable %q", tok.val)
	}
	varPath, err := resolvePathToDescriptors(p.desc.Input(), tok.val)
	if err != nil {
		return err
	}
	variable := pathVariable{varPath: varPath, start: len(p.segments)}

	//nolint:exhaustive
	switch tok = p.next(); tok.typ {
	case tokenVariableEnd:
		seg := pathSegment{val: "*"} // default capture.
		p.segments = append(p.segments, seg)
	case tokenEqual:
		if err := p.parseSegments(); err != nil {
			return err
		}
		if err := p.consume(tokenVariableEnd); err != nil {
			return err
		}
	default:
		return p.errUnexpected()
	}
	variable.end = len(p.segments)
	p.variables = append(p.variables, variable)
	return nil
}
