// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// pathSegments holds the path segments for a method.
// The verb is the final segment, if any. Wildcards segments are annotated by
// '*' and '**' path values. Each segment is URL unescaped.
type pathSegments struct {
	path []string // segment path values.
	verb string   // final segment verb, if any.
}

// String returns the URL path representation of the segments.
func (s pathSegments) String() string {
	var out strings.Builder
	for _, value := range s.path {
		out.WriteByte('/')
		if value != "*" && value != "**" {
			value = url.PathEscape(value)
		}
		out.WriteString(value)
	}
	if s.verb != "" {
		out.WriteByte(':')
		out.WriteString(url.PathEscape(s.verb))
	}
	return out.String()
}

// pathVariable holds the path variables for a method.
// The start and end fields are the start and end path segments, inclusive-exclusive.
// If the end is -1, the variable is unbounded, representing a '**' wildcard capture.
type pathVariable struct {
	fields     []protoreflect.FieldDescriptor
	start, end int // start and end path segments, inclusive-exclusive, -1 for unbounded.
}

type pathVariables []pathVariable

// parsePathTemplate parsers a methods template into path segments and variables.
//
// The grammar for the path template is given in the protobuf definition
// in [google/api/http.proto].
//
//	Template = "/" Segments [ Verb ] ;
//	Segments = Segment { "/" Segment } ;
//	Segment  = "*" | "**" | LITERAL | Variable ;
//	Variable = "{" FieldPath [ "=" Segments ] "}" ;
//	FieldPath = IDENT { "." IDENT } ;
//	Verb     = ":" LITERAL ;
//
// [google/api/http.proto]: https://github.com/googleapis/googleapis/blob/ecb1cf0a0021267dd452289fc71c75674ae29fe3/google/api/http.proto#L227-L235
func parsePathTemplate(descriptor protoreflect.MethodDescriptor, template string) (
	pathSegments, pathVariables, error,
) {
	parser := &parser{lex: lexer{
		input: template,
	}, desc: descriptor, seenVars: make(map[string]bool)}

	if err := parser.parseTemplate(); err != nil {
		return pathSegments{}, nil, err
	}
	return parser.segments, parser.variables, nil
}

// parser holds the state for the recursive descent path template parser.
type parser struct {
	lex            lexer                         // lexer for the input.
	desc           protoreflect.MethodDescriptor // input method descriptor.
	seenVars       map[string]bool               // set of field paths.
	seenDoubleStar bool                          // true if we've seen a double star wildcard.
	segments       pathSegments                  // output segments.
	variables      pathVariables                 // output variables.
}

func (p *parser) currentChar() string {
	str := "EOF"
	if char := p.lex.current(); char != eof {
		str = strconv.QuoteRune(char)
	}
	return str
}
func (p *parser) errSyntax(msg string) error {
	return fmt.Errorf("syntax error at column %v: %s", p.lex.pos, msg)
}
func (p *parser) errUnexpected() error {
	return p.errSyntax(fmt.Sprintf("unexpected %s", p.currentChar()))
}
func (p *parser) errExpected(expected rune) error {
	return p.errSyntax(fmt.Sprintf("expected %q, got %s", expected, p.currentChar()))
}

func (p *parser) parseTemplate() error {
	if !p.lex.consume('/') {
		return p.errExpected('/') // empty path is not allowed.
	}
	if err := p.parseSegments(); err != nil {
		return err
	}
	switch p.lex.next() {
	case ':':
		p.lex.discard()
		return p.parseVerb()
	case eof:
		return nil
	default:
		return p.errUnexpected()
	}
}

func (p *parser) parseVerb() error {
	literal, err := p.parseLiteral()
	if err != nil {
		return err
	}
	p.segments.verb = literal
	if !p.lex.consume(eof) {
		return p.errUnexpected()
	}
	return nil
}

func (p *parser) parseSegments() error {
	for {
		if err := p.parseSegment(); err != nil {
			return err
		}
		if p.lex.next() != '/' {
			p.lex.backup()
			return nil
		}
		p.lex.discard()
		if p.seenDoubleStar {
			return errors.New("double wildcard '**' must be the final path segment")
		}
	}
}

// parseLiteral unescapes a URL path segment.
func (p *parser) parseLiteral() (string, error) {
	if p.lex.acceptRun(isLiteral) == 0 {
		return "", p.errUnexpected()
	}
	val, err := url.PathUnescape(p.lex.capture())
	if err != nil {
		return "", p.errSyntax(err.Error())
	}
	return val, nil
}
func (p *parser) parseSegment() error {
	var segment string
	switch p.lex.next() {
	case '*':
		if p.lex.next() == '*' {
			p.seenDoubleStar = true
		} else {
			p.lex.backup()
		}
		segment = p.lex.capture()
	case '{':
		p.lex.discard()
		return p.parseVariable()
	default:
		if !isLiteral(p.lex.current()) {
			return p.errSyntax("expected path value")
		}
		literal, err := p.parseLiteral()
		if err != nil {
			return err
		}
		segment = literal
	}
	p.segments.path = append(p.segments.path, segment)
	return nil
}
func (p *parser) parseVariable() error {
	if p.lex.acceptRun(isFieldPath) == 0 {
		return p.errUnexpected()
	}
	fieldPath := p.lex.capture()
	if p.seenVars[fieldPath] {
		return fmt.Errorf("duplicate variable %q", fieldPath)
	}
	fields, err := resolvePathToDescriptors(p.desc.Input(), fieldPath)
	if err != nil {
		return err
	}
	variable := pathVariable{fields: fields, start: len(p.segments.path)}

	switch p.lex.next() {
	case '}':
		p.lex.discard()
		p.segments.path = append(p.segments.path, "*") // default capture.
	case '=':
		p.lex.discard()
		if err := p.parseSegments(); err != nil {
			return err
		}
		if !p.lex.consume('}') {
			return p.errExpected('}')
		}
	default:
		return p.errExpected('}')
	}
	variable.end = len(p.segments.path)
	if p.seenDoubleStar {
		variable.end = -1 // double star wildcard.
	}
	p.variables = append(p.variables, variable)
	return nil
}
