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
	"unicode"
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
	fieldPath  string // field path for the variable.
	start, end int    // start and end path segments, inclusive-exclusive, -1 for unbounded.
}

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
func parsePathTemplate(template string) (
	pathSegments, []pathVariable, error,
) {
	parser := &pathParser{scan: pathScanner{input: template}}
	if err := parser.parseTemplate(); err != nil {
		return pathSegments{}, nil, err
	}
	return parser.segments, parser.variables, nil
}

// pathParser holds the state for the recursive descent path template parser.
type pathParser struct {
	scan           pathScanner     // scanner for the input.
	seenVars       map[string]bool // set of field paths.
	seenDoubleStar bool            // true if we've seen a double star wildcard.
	segments       pathSegments    // output segments.
	variables      []pathVariable  // output variables.
}

func (p *pathParser) currentChar() string {
	if char := p.scan.current(); char != eof {
		return strconv.QuoteRune(char)
	}
	return "EOF"
}
func (p *pathParser) errSyntax(msg string) error {
	return fmt.Errorf("syntax error at column %v: %s", p.scan.pos, msg)
}
func (p *pathParser) errUnexpected() error {
	return p.errSyntax(fmt.Sprintf("unexpected %s", p.currentChar()))
}
func (p *pathParser) errExpected(expected rune) error {
	return p.errSyntax(fmt.Sprintf("expected %q, got %s", expected, p.currentChar()))
}

func (p *pathParser) parseTemplate() error {
	if !p.scan.consume('/') {
		return p.errExpected('/') // empty path is not allowed.
	}
	if err := p.parseSegments(); err != nil {
		return err
	}
	switch p.scan.next() {
	case ':':
		p.scan.discard()
		return p.parseVerb()
	case eof:
		return nil
	default:
		return p.errUnexpected()
	}
}

func (p *pathParser) parseVerb() error {
	literal, err := p.parseLiteral()
	if err != nil {
		return err
	}
	p.segments.verb = literal
	if !p.scan.consume(eof) {
		return p.errUnexpected()
	}
	return nil
}

func (p *pathParser) parseSegments() error {
	for {
		if err := p.parseSegment(); err != nil {
			return err
		}
		if p.scan.next() != '/' {
			p.scan.backup()
			return nil
		}
		p.scan.discard()
		if p.seenDoubleStar {
			return errors.New("double wildcard '**' must be the final path segment")
		}
	}
}

// parseLiteral unescapes a URL path segment.
func (p *pathParser) parseLiteral() (string, error) {
	literal := p.scan.captureRun(isLiteral)
	if literal == "" {
		p.scan.next()
		return "", p.errUnexpected()
	}
	unescaped, err := url.PathUnescape(literal)
	if err != nil {
		return "", p.errSyntax(err.Error())
	}
	return unescaped, nil
}
func (p *pathParser) parseSegment() error {
	var segment string
	switch p.scan.next() {
	case '*':
		if p.scan.next() == '*' {
			p.seenDoubleStar = true
		} else {
			p.scan.backup()
		}
		segment = p.scan.capture()
	case '{':
		p.scan.discard()
		return p.parseVariable()
	default:
		if !isLiteral(p.scan.current()) {
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

func (p *pathParser) parseFieldPath() (string, error) {
	for {
		if !unicode.IsLetter(p.scan.next()) {
			return "", p.errUnexpected()
		}
		for isIdent(p.scan.next()) {
			continue
		}
		if p.scan.current() != '.' {
			p.scan.backup()
			return p.scan.capture(), nil
		}
	}
}
func (p *pathParser) parseVariable() error {
	fieldPath, err := p.parseFieldPath()
	if err != nil {
		return err
	}
	if p.seenVars[fieldPath] {
		return fmt.Errorf("duplicate variable %q", fieldPath)
	}
	if p.seenVars == nil {
		p.seenVars = make(map[string]bool)
	}
	p.seenVars[fieldPath] = true

	variable := pathVariable{fieldPath: fieldPath, start: len(p.segments.path)}

	switch p.scan.next() {
	case '}':
		p.scan.discard()
		p.segments.path = append(p.segments.path, "*") // default capture.
	case '=':
		p.scan.discard()
		if err := p.parseSegments(); err != nil {
			return err
		}
		if !p.scan.consume('}') {
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
