// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenType uint8

const (
	tokenError         tokenType = iota
	tokenSlash                   // /
	tokenStar                    // *
	tokenStarStar                // **
	tokenVariableStart           // {
	tokenVariableEnd             // }
	tokenEqual                   // =
	tokenLiteral                 // a-z A-Z 0-9 - _ . %
	tokenFieldPath               // a-z A-Z 0-9 - _ .
	tokenVerb                    // :
	tokenEOF
)

//nolint:gochecknoglobals
var tokenNames = [...]string{
	tokenError:         "error",
	tokenSlash:         "/",
	tokenStar:          "*",
	tokenStarStar:      "**",
	tokenVariableStart: "{",
	tokenVariableEnd:   "}",
	tokenEqual:         "=",
	tokenLiteral:       "literal",
	tokenFieldPath:     "fieldpath",
	tokenVerb:          ":",
	tokenEOF:           "eof",
}

func (t tokenType) String() string {
	if int(t) > len(tokenNames) {
		return fmt.Sprintf("unknown token type %d", t)
	}
	return tokenNames[t]
}

type token struct {
	val string
	typ tokenType
	pos int
}

func (t token) String() string {
	return fmt.Sprintf("%s(%q)", t.typ, t.val)
}

type tokens []token

func (toks tokens) String() string {
	var b strings.Builder
	for _, tok := range toks {
		b.WriteString(tok.val)
	}
	return b.String()
}

// lexer holds the state of the scanner.
type lexer struct {
	input string // the string being scanned.
	toks  tokens // the tokens collected so far.
	start int    // start position of this token.
	pos   int    // current position in the input.
	width int    // width of last rune read from input.
}

// lex the input template into tokens.
func lex(input string) tokens {
	l := &lexer{input: input}
	_ = l.lexTemplate()
	return l.toks
}

const eof = -1

func (l *lexer) next() rune {
	var char rune
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}
	if c := l.input[l.pos]; c < utf8.RuneSelf {
		l.width = 1
		char = rune(c)
	} else {
		char, l.width = utf8.DecodeRuneInString(l.input[l.pos:])
	}
	l.pos += l.width
	return char
}

func (l *lexer) current() rune {
	var char rune
	switch {
	case l.width == 0:
		return 0
	case l.pos > l.width:
		char, _ = utf8.DecodeRuneInString(l.input[l.pos-l.width:])
	default:
		char, _ = utf8.DecodeRuneInString(l.input)
	}
	return char
}

func (l *lexer) backup() {
	l.pos -= l.width
}

func (l *lexer) acceptRun(isValid func(r rune) bool) int {
	var i int
	for isValid(l.next()) {
		i++
	}
	l.backup()
	return i
}

func (l *lexer) emit(typ tokenType) bool {
	tok := token{typ: typ, val: l.input[l.start:l.pos], pos: l.start}
	l.toks = append(l.toks, tok)
	l.start = l.pos
	return typ != tokenError
}
func (l *lexer) emitError(tmpl string, args ...any) bool {
	tok := token{
		typ: tokenError, val: fmt.Sprintf(tmpl, args...), pos: l.start,
	}
	l.toks = append(l.toks, tok)
	l.start = l.pos
	return false
}
func (l *lexer) emitUnexpected() bool {
	return l.emitError("unexpected %q", l.current())
}
func (l *lexer) emitExpected(expected rune) bool {
	return l.emitError("expected %q, got %q", expected, l.current())
}
func (l *lexer) emitShort() bool {
	return l.emitError("short read %q", l.current())
}

func isIdent(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-'
}
func isFieldPath(r rune) bool {
	return isIdent(r) || r == '.'
}
func isLiteral(r rune) bool {
	if isFieldPath(r) {
		return true
	}
	// Allow all characters that are allowed in a URL path segment.
	// https://www.rfc-editor.org/rfc/rfc3986#section-3.3
	switch r {
	case '~', '!', '$', '&', '\'', '(', ')', '+', ',', ';', '=', '@':
		return true
	default:
		return false
	}
}
func isHex(r rune) bool {
	return unicode.IsNumber(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func (l *lexer) lexLiteral() bool {
	count := l.acceptRun(isLiteral)
	// lex URL-encoded characters.
	for l.next() == '%' {
		for i := 0; i < 2; i++ {
			if !isHex(l.next()) {
				l.start = l.pos - i - 1 // rewind to the start of the % escape.
				return l.emitError("malformed URL-encoded character")
			}
		}
		count += 3
		count += l.acceptRun(isLiteral)
	}
	l.backup()
	if count == 0 {
		return l.emitShort()
	}
	return l.emit(tokenLiteral)
}

func (l *lexer) lexFieldPath() bool {
	if i := l.acceptRun(isFieldPath); i == 0 {
		return l.emitShort()
	}
	return l.emit(tokenFieldPath)
}

func (l *lexer) lexVerb() bool {
	if !l.lexLiteral() {
		return false
	}
	if r := l.next(); r == eof {
		return l.emit(tokenEOF)
	}
	return l.emitError("expected EOF") // unexpected character.
}

func (l *lexer) lexVariable() bool {
	char := l.next()
	if char != '{' {
		return l.emitExpected('{')
	}
	l.emit(tokenVariableStart)
	if !l.lexFieldPath() {
		return false
	}

	char = l.next()
	if char == '=' {
		l.emit(tokenEqual)
		if !l.lexSegments() {
			return false
		}
		char = l.next()
	}

	if char != '}' {
		return l.emitExpected('}')
	}
	return l.emit(tokenVariableEnd)
}

func (l *lexer) lexSegment() bool {
	char := l.next()
	switch {
	case unicode.IsLetter(char):
		return l.lexLiteral()
	case char == '*':
		rn := l.next()
		if rn == '*' {
			return l.emit(tokenStarStar)
		}
		l.backup()
		return l.emit(tokenStar)
	case char == '{':
		l.backup()
		return l.lexVariable()
	default:
		return l.emitError("expected path value")
	}
}

func (l *lexer) lexSegments() bool {
	for {
		if !l.lexSegment() {
			return false
		}
		if r := l.next(); r != '/' {
			l.backup() // unknown
			return true
		}
		l.emit(tokenSlash)
	}
}

func (l *lexer) lexTemplate() bool {
	if r := l.next(); r != '/' {
		return l.emitExpected('/')
	}
	fmt.Println("lexTemplate", string(l.current()), l.input)
	l.emit(tokenSlash)
	if !l.lexSegments() {
		return false
	}
	switch r := l.next(); r {
	case ':':
		l.emit(tokenVerb)
		return l.lexVerb()
	case eof:
		l.emit(tokenEOF)
		return false
	default:
		return l.emitUnexpected()
	}
}
