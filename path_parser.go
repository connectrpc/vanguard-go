// Copyright 2023-2024 Buf Technologies, Inc.
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
	"net/url"
	"strconv"
	"strings"
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
	return p.errSyntax("unexpected " + p.currentChar())
}
func (p *pathParser) errExpected(expected rune) error {
	return p.errSyntax("expected " + strconv.QuoteRune(expected) + ", got " + p.currentChar())
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

// parseLiteral parses a URL path segment in URL path escaped form.
func (p *pathParser) parseLiteral() (string, error) {
	literal := p.scan.captureRun(isLiteral)
	if literal == "" {
		p.scan.next()
		return "", p.errUnexpected()
	}
	unescaped, err := pathUnescape(literal, pathEncodeSingle)
	if err != nil {
		return "", p.errSyntax(err.Error())
	}
	return pathEscape(unescaped, pathEncodeSingle), nil
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
		if !isIdentStart(p.scan.next()) {
			return "", p.errSyntax("expected identifier")
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

const upperhex = "0123456789ABCDEF"

func ishex(char byte) bool {
	switch {
	case '0' <= char && char <= '9':
		return true
	case 'a' <= char && char <= 'f':
		return true
	case 'A' <= char && char <= 'F':
		return true
	}
	return false
}
func unhex(char byte) byte {
	switch {
	case '0' <= char && char <= '9':
		return char - '0'
	case 'a' <= char && char <= 'f':
		return char - 'a' + 10
	case 'A' <= char && char <= 'F':
		return char - 'A' + 10
	}
	return 0
}

// pathEncoding is the encoding used for path variables.
// Single encoding is used for single segment capture variables,
// while multi encoding is used for multi segment capture variables.
// On multi encoding variables, '/' is not escaped and is preserved
// as '%2F' if encoded in the path.
//
// See: https://github.com/googleapis/googleapis/blob/1769846666fbeb0f9ece6ad929ddc0d563cccd8d/google/api/http.proto#L249-L264
type pathEncoding int

const (
	pathEncodeSingle pathEncoding = iota
	pathEncodeMulti
)

func pathShouldEscape(char byte, _ pathEncoding) bool {
	return !isVariable(rune(char))
}
func pathIsHexSlash(input string) bool {
	if len(input) < 3 {
		return false
	}
	return input[0] == '%' && input[1] == '2' && (input[2] == 'f' || input[2] == 'F')
}

func pathEscape(input string, mode pathEncoding) string {
	// Count the number of characters that possibly escaping.
	hexCount := 0
	for i := 0; i < len(input); i++ {
		if pathShouldEscape(input[i], mode) {
			hexCount++
		}
	}
	if hexCount == 0 {
		return input
	}

	var sb strings.Builder
	sb.Grow(len(input) + 2*hexCount)
	for i := 0; i < len(input); i++ {
		switch char := input[i]; {
		case char == '%' && mode == pathEncodeMulti && pathIsHexSlash(input[i:]):
			sb.WriteString("%2F")
			i += 2
		case pathShouldEscape(char, mode):
			sb.WriteByte('%')
			sb.WriteByte(upperhex[char>>4])
			sb.WriteByte(upperhex[char&15])
		default:
			sb.WriteByte(char)
		}
	}
	return sb.String()
}
func validateHex(input string) error {
	if len(input) < 3 || input[0] != '%' || !ishex(input[1]) || !ishex(input[2]) {
		if len(input) > 3 {
			input = input[:3]
		}
		return url.EscapeError(input)
	}
	return nil
}
func pathUnescape(input string, mode pathEncoding) (string, error) {
	// Count %, check that they're well-formed.
	percentCount := 0
	for i := 0; i < len(input); {
		switch input[i] {
		case '%':
			percentCount++
			if err := validateHex(input[i:]); err != nil {
				return "", err
			}
			i += 3
		default:
			i++
		}
	}
	if percentCount == 0 {
		return input, nil
	}

	var sb strings.Builder
	sb.Grow(len(input) - 2*percentCount)
	for i := 0; i < len(input); i++ {
		switch input[i] {
		case '%':
			if mode == pathEncodeMulti && pathIsHexSlash(input[i:]) {
				// Multi doesn't escape /, so we don't escape.
				sb.WriteString("%2F")
			} else {
				sb.WriteByte(unhex(input[i+1])<<4 | unhex(input[i+2]))
			}
			i += 2
		default:
			sb.WriteByte(input[i])
		}
	}
	return sb.String(), nil
}
