// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"strings"
	"unicode"
)

// parsePathTemplate parses the given path template into a routePath.
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
func parsePathTemplate(path string) (routePath, error) {
	lex := &lexer{input: path}
	if _, ok := lex.consume(tokenSlash); !ok {
		return nil, lex.syntaxError("expecting '/'")
	}
	result, err := parsePathSegments(lex)
	if err != nil {
		return nil, err
	}
	if lex.lookingAt(tokenColon) {
		verb, err := parsePathVerb(lex)
		if err != nil {
			return nil, err
		}
		result = append(result, routePathElement{verb: verb})
	}
	if _, ok := lex.consume(tokenEOF); !ok {
		return nil, lex.syntaxError("expecting EOF")
	}
	if err := result.normalize(stateInitial); err != nil {
		return nil, err
	}
	return result, nil
}

func parsePathSegments(lex *lexer) (routePath, error) {
	elem, err := parsePathSegment(lex)
	if err != nil {
		return nil, err
	}
	path := routePath{elem}
	for {
		if _, ok := lex.consume(tokenSlash); !ok {
			return path, nil
		}
		elem, err = parsePathSegment(lex)
		if err != nil {
			return nil, err
		}
		path = append(path, elem)
	}
}

func parsePathSegment(lex *lexer) (routePathElement, error) {
	//nolint:exhaustive // no need for exhaustive switch, most things fall into default case
	switch t, _ := lex.peek(); t {
	case tokenWildcard:
		lex.next() // consume wildcard
		if lex.lookingAt(tokenWildcard) {
			lex.next() // consume second wildcard
			return routePathElement{segment: "**"}, nil
		}
		return routePathElement{segment: "*"}, nil
	case tokenOpenBrace:
		variable, err := parsePathVar(lex)
		if err != nil {
			return routePathElement{}, err
		}
		return routePathElement{variable: variable}, nil
	default:
		str, err := parsePathLiteral(lex)
		if err != nil {
			return routePathElement{}, err
		}
		return routePathElement{segment: str}, nil
	}
}

func parsePathVar(lex *lexer) (routePathVar, error) {
	if _, ok := lex.consume(tokenOpenBrace); !ok {
		return routePathVar{}, lex.syntaxError("expecting '{'")
	}
	fieldPath, err := parsePathVarFields(lex)
	if err != nil {
		return routePathVar{}, err
	}
	var segments routePath
	if lex.lookingAt(tokenEquals) {
		lex.next() // consume peeked equals
		segments, err = parsePathSegments(lex)
		if err != nil {
			return routePathVar{}, err
		}
	}
	if _, ok := lex.consume(tokenCloseBrace); !ok {
		return routePathVar{}, lex.syntaxError("expecting '}'")
	}
	return routePathVar{
		varPath:  fieldPath,
		segments: segments,
	}, nil
}

func parsePathVarFields(lex *lexer) (string, error) {
	ident, ok := lex.consume(tokenIdent)
	if !ok {
		return "", lex.syntaxError("expecting identifier")
	}
	var sb strings.Builder
	sb.WriteString(ident)
	for {
		if _, ok := lex.consume(tokenDot); !ok {
			return sb.String(), nil
		}
		sb.WriteRune('.')
		ident, ok := lex.consume(tokenIdent)
		if !ok {
			return "", lex.syntaxError("expecting identifier")
		}
		sb.WriteString(ident)
	}
}

func parsePathVerb(lex *lexer) (string, error) {
	if _, ok := lex.consume(tokenColon); !ok {
		return "", lex.syntaxError("expecting ':'")
	}
	return parsePathLiteral(lex)
}

func parsePathLiteral(lex *lexer) (string, error) {
	str, count := lex.consumeAll(tokenIdent, tokenNonIdent, tokenDot, tokenEquals, tokenSpecialChar)
	if count == 0 {
		return "", lex.syntaxError("expecting path component literal")
	}
	_, peeked := lex.peek()
	if peeked == "%" {
		return "", lex.syntaxError("malformed URL-encoded character")
	}
	return str, nil
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

type pathValidationState int

const (
	stateInitial = pathValidationState(iota)
	stateSeenDoubleWildcard
	stateSeenVerb
)

// normalize validates and normalizes the path. Normalization includes
// re-writing path component literals with reserved characters to use
// URL-encoding. The state is used to make sure that verbs can only
// appear at the end, and double-wildcards only appear in the last path
// element (at the end, other than optional verb).
func (p routePath) normalize(state pathValidationState) error {
	for i := range p {
		elem := &p[i]
		switch {
		case elem.segment != "":
			switch state { //nolint:exhaustive
			case stateSeenDoubleWildcard:
				return fmt.Errorf("double wildcard '**' must be final path component")
			case stateSeenVerb:
				// This should not actually be possible due to how the parser works
				return fmt.Errorf("colon-delimited verb must be on final path component")
			}
			if elem.segment == "**" {
				state = stateSeenDoubleWildcard
			} else if elem.segment != "*" {
				elem.segment = sanitizePathElement(elem.segment)
			}
		case elem.verb != "":
			if state == stateSeenVerb {
				return fmt.Errorf("colon-delimited verb must appear only once and be on final path component")
			}
			state = stateSeenVerb
			elem.verb = sanitizePathElement(elem.verb)
		case elem.variable.varPath != "":
			subPath := elem.variable.segments
			if len(subPath) == 0 {
				subPath = routePath{{segment: "*"}}
			}
			if err := subPath.normalize(state); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected routePath element: %+v", elem)
		}
	}
	return nil
}

const hexDigits = "0123456789ABCDEF"

func sanitizePathElement(elem string) string {
	var sb strings.Builder
	// used to capitalize URL-encoded characters
	var specialCharRemaining int
	// we don't use range because we want to iterate bytes, not runes
	for i := 0; i < len(elem); i++ {
		char := elem[i]
		// "%" is a reserved char, but we don't need to replace it because we already
		// validate when parsing the URL that "%" is an already-URL-encoded char
		replace := !isIdentifier(rune(char)) && !strings.ContainsRune("-.~%", rune(char))
		capitalize := specialCharRemaining > 0 && unicode.IsLower(rune(char))
		if (replace || capitalize) && sb.Len() == 0 {
			// initialize sb up to this point of the string
			sb.WriteString(elem[:i])
		}
		if replace || capitalize || sb.Len() > 0 {
			switch {
			case replace:
				sb.WriteByte('%')
				sb.WriteByte(hexDigits[(char>>4)&0xf])
				sb.WriteByte(hexDigits[char&0xf])
			case capitalize:
				sb.WriteRune(unicode.ToUpper(rune(char)))
			default:
				sb.WriteByte(char)
			}
		}
		if specialCharRemaining > 0 {
			specialCharRemaining--
		}
		if char == '%' {
			specialCharRemaining = 2
		}
	}
	if sb.Len() > 0 {
		return sb.String()
	}
	return elem // nothing to replace
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
