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
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// pathVariable holds the path variables for a method.
// The start and end fields are the start and end path segments, inclusive-exclusive.
// If the end is -1, the variable is unbounded, representing a '**' wildcard capture.
type pathVariable struct {
	fieldPath  string // field path for the variable.
	start, end int    // start and end path segments, inclusive-exclusive, -1 for unbounded.
}

// parsePathTemplate parsers a methods template into its canonical form and
// variables. The canonical template escapes each literal, so '/', ':' and '*'
// only appear as separators and wildcards.
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
	string, []pathVariable, error,
) {
	parser := &pathParser{input: template}
	if err := parser.parseTemplate(); err != nil {
		return "", nil, err
	}
	for i, variable := range parser.variables {
		for _, seen := range parser.variables[:i] {
			if seen.fieldPath == variable.fieldPath {
				return "", nil, fmt.Errorf("duplicate variable %q", variable.fieldPath)
			}
		}
	}
	return parser.output.String(), parser.variables, nil
}

// pathParser holds the state for the recursive descent path template parser.
// The grammar is ASCII, so the input is scanned byte by byte.
type pathParser struct {
	input          string          // the template being parsed.
	pos            int             // offset of the next unread byte.
	seenDoubleStar bool            // true if we've seen a double star wildcard.
	segmentCount   int             // number of segments written to output.
	output         strings.Builder // output canonical template.
	variables      []pathVariable  // output variables.
}

func (p *pathParser) writeSegment(segment string) {
	p.output.WriteByte('/')
	p.output.WriteString(segment)
	p.segmentCount++
}

func (p *pathParser) peek() byte {
	if p.pos < len(p.input) {
		return p.input[p.pos]
	}
	return 0
}
func (p *pathParser) consume(expected byte) bool {
	if p.pos < len(p.input) && p.input[p.pos] == expected {
		p.pos++
		return true
	}
	return false
}
func (p *pathParser) consumeRun(isValid func(byte) bool) string {
	start := p.pos
	for p.pos < len(p.input) && isValid(p.input[p.pos]) {
		p.pos++
	}
	return p.input[start:p.pos]
}

func (p *pathParser) currentChar() string {
	if p.pos < len(p.input) {
		char, _ := utf8.DecodeRuneInString(p.input[p.pos:])
		return strconv.QuoteRune(char)
	}
	return "EOF"
}
func (p *pathParser) errSyntax(msg string) error {
	return fmt.Errorf("syntax error at column %v: %s", p.pos+1, msg)
}
func (p *pathParser) errUnexpected() error {
	return p.errSyntax("unexpected " + p.currentChar())
}
func (p *pathParser) errExpected(expected byte) error {
	return p.errSyntax("expected " + strconv.QuoteRune(rune(expected)) + ", got " + p.currentChar())
}

func (p *pathParser) parseTemplate() error {
	if !p.consume('/') {
		return p.errExpected('/') // empty path is not allowed.
	}
	if err := p.parseSegments(); err != nil {
		return err
	}
	if p.consume(':') {
		verb, err := p.parseLiteral()
		if err != nil {
			return err
		}
		p.output.WriteByte(':')
		p.output.WriteString(verb)
	}
	if p.pos != len(p.input) {
		return p.errUnexpected()
	}
	return nil
}

func (p *pathParser) parseSegments() error {
	for {
		if err := p.parseSegment(); err != nil {
			return err
		}
		if !p.consume('/') {
			return nil
		}
		if p.seenDoubleStar {
			return errors.New("double wildcard '**' must be the final path segment")
		}
	}
}

func (p *pathParser) parseSegment() error {
	switch {
	case p.consume('*'):
		segment := "*"
		if p.consume('*') {
			segment = "**"
			p.seenDoubleStar = true
		}
		p.writeSegment(segment)
		return nil
	case p.consume('{'):
		return p.parseVariable()
	case !isLiteral(p.peek()):
		return p.errSyntax("expected path value")
	}
	literal, err := p.parseLiteral()
	if err != nil {
		return err
	}
	p.writeSegment(literal)
	return nil
}

// parseLiteral parses a URL path segment in URL path escaped form.
func (p *pathParser) parseLiteral() (string, error) {
	start := p.pos
	literal := p.consumeRun(isLiteral)
	if literal == "" {
		return "", p.errUnexpected()
	}
	unescaped, err := pathUnescape(literal, pathEncodeSingle)
	if err != nil {
		p.pos = start
		return "", p.errSyntax(err.Error())
	}
	return pathEscape(unescaped, pathEncodeSingle), nil
}

func (p *pathParser) parseFieldPath() (string, error) {
	start := p.pos
	for {
		if !isIdentStart(p.peek()) {
			return "", p.errSyntax("expected identifier")
		}
		p.consumeRun(isIdent)
		if !p.consume('.') {
			return p.input[start:p.pos], nil
		}
	}
}

func (p *pathParser) parseVariable() error {
	fieldPath, err := p.parseFieldPath()
	if err != nil {
		return err
	}
	variable := pathVariable{fieldPath: fieldPath, start: p.segmentCount}
	if p.consume('=') {
		if err := p.parseSegments(); err != nil {
			return err
		}
	} else {
		p.writeSegment("*") // default capture.
	}
	if !p.consume('}') {
		return p.errExpected('}')
	}
	variable.end = p.segmentCount
	if p.seenDoubleStar {
		variable.end = -1 // double star wildcard.
	}
	p.variables = append(p.variables, variable)
	return nil
}

func isIdentStart(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_'
}
func isIdent(char byte) bool {
	return isIdentStart(char) || (char >= '0' && char <= '9')
}
func isLiteral(char byte) bool {
	// Allow [-_.~0-9a-zA-Z] and % for escaped characters.
	return isVariable(char) || char == '%'
}

// isVariable is used to determine if a character is allowed in a single variable segment.
//
// See: https://github.com/googleapis/googleapis/blob/master/google/api/http.proto#L251C34-L251C38
func isVariable(char byte) bool {
	// Allow [-_.~0-9a-zA-Z].
	return isIdent(char) || char == '.' || char == '-' || char == '~'
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

func pathIsHexSlash(input string) bool {
	if len(input) < 3 {
		return false
	}
	return input[0] == '%' && input[1] == '2' && (input[2] == 'f' || input[2] == 'F')
}

func pathEscape(input string, mode pathEncoding) string {
	// Count the number of characters that possibly escaping.
	hexCount := 0
	for i := range len(input) {
		if !isVariable(input[i]) {
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
		case !isVariable(char):
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
