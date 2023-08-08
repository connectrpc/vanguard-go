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

// ### Path template syntax
//
//     Template = "/" Segments [ Verb ] ;
//     Segments = Segment { "/" Segment } ;
//     Segment  = "*" | "**" | LITERAL | Variable ;
//     Variable = "{" FieldPath [ "=" Segments ] "}" ;
//     FieldPath = IDENT { "." IDENT } ;
//     Verb     = ":" LITERAL ;

type tokenType uint8

const (
	tokenError         tokenType = iota
	tokenSlash                   // /
	tokenStar                    // *
	tokenStarStar                // **
	tokenVariableStart           // {
	tokenVariableEnd             // }
	tokenEqual                   // =
	tokenLiteral                 // a-z A-Z 0-9 - _ .
	tokenFieldPath               // a-z A-Z 0-9 - _ .
	tokenVerb                    // :
	tokenPath                    // a-z A-Z 0-9 - _ . ~ ! $ & ' ( ) * + , ; = @
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
	tokenPath:          "path",
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
func lex(input string) (tokens, error) {
	l := &lexer{input: input}
	if err := l.lexTemplate(); err != nil {
		return nil, err
	}
	return l.toks, nil
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

func (l *lexer) emit(typ tokenType) error {
	tok := token{typ: typ, val: l.input[l.start:l.pos]}
	l.toks = append(l.toks, tok)
	l.start = l.pos
	return nil
}

func (l *lexer) errf(tmpl string, args ...any) error {
	if err := l.emit(tokenError); err != nil {
		return err
	}
	return fmt.Errorf("syntax error at column %v: %s", l.pos-l.width+1, fmt.Sprintf(tmpl, args...))
}
func (l *lexer) errUnexpected() error {
	return l.errf("unexpected %q", l.current())
}
func (l *lexer) errExpected(expected rune) error {
	return l.errf("expected %q, got %q", expected, l.current())
}
func (l *lexer) errShort() error {
	return l.errf("short read %q", l.current())
}
func isIdent(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-'
}

func isLiteral(r rune) bool {
	return isIdent(r) || r == '.'
}

func (l *lexer) lexLiteral() error {
	if i := l.acceptRun(isLiteral); i == 0 {
		return l.errShort()
	}
	return l.emit(tokenLiteral)
}

func (l *lexer) lexFieldPath() error {
	if i := l.acceptRun(isLiteral); i == 0 {
		return l.errShort()
	}
	return l.emit(tokenFieldPath)
}

func (l *lexer) lexVerb() error {
	if err := l.lexLiteral(); err != nil {
		return err
	}
	if r := l.next(); r == eof {
		return l.emit(tokenEOF)
	}
	return l.errUnexpected()
}

func (l *lexer) lexVariable() error {
	char := l.next()
	if char != '{' {
		return l.errUnexpected()
	}
	if err := l.emit(tokenVariableStart); err != nil {
		return err
	}
	if err := l.lexFieldPath(); err != nil {
		return err
	}

	char = l.next()
	if char == '=' {
		if err := l.emit(tokenEqual); err != nil {
			return err
		}

		if err := l.lexSegments(); err != nil {
			return err
		}
		char = l.next()
	}

	if char != '}' {
		return l.errUnexpected()
	}
	return l.emit(tokenVariableEnd)
}

func (l *lexer) lexSegment() error {
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
		return l.errf("expected path component literal")
	}
}

func (l *lexer) lexSegments() error {
	for {
		if err := l.lexSegment(); err != nil {
			return err
		}
		if r := l.next(); r != '/' {
			l.backup() // unknown
			return nil
		}
		if err := l.emit(tokenSlash); err != nil {
			return err
		}
	}
}

func (l *lexer) lexTemplate() error {
	if r := l.next(); r != '/' {
		return l.errExpected('/')
	}
	if err := l.emit(tokenSlash); err != nil {
		return err
	}
	if err := l.lexSegments(); err != nil {
		return err
	}

	switch r := l.next(); r {
	case ':':
		if err := l.emit(tokenVerb); err != nil {
			return err
		}
		return l.lexVerb()
	case eof:
		if err := l.emit(tokenEOF); err != nil {
			return err
		}
		return nil
	default:
		return l.errUnexpected()
	}
}
